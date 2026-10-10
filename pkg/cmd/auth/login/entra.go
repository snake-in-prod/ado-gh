package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/auth"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// newMSAL builds the MSAL client; tests replace it.
var newMSAL = auth.NewMSAL

// loginTimeout bounds the wait for the browser sign-in.
var loginTimeout = 5 * time.Minute

const caHint = "Device code sign-in may be blocked by your organization's Conditional Access policy: " +
	"sign in with a browser (`ado auth login` in a desktop session, without --device) or use a PAT (`ado auth login --with-token`)."

// entraLogin signs in with Microsoft Entra ID and records the MSAL account and the org's tenant
// in the per-host config; the tokens live in the MSAL cache.
func entraLogin(ctx context.Context, opts *options, cfg auth.Config) error {
	f, ios := opts.f, opts.f.IOStreams
	clientID, err := auth.EntraClientID(cfg)
	if err != nil {
		return err
	}
	host, org := orgFromContext(opts)
	if org == "" && !ios.CanPrompt() {
		return errors.New(noOrg)
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	// Discovery is unauthenticated: the api transport would turn its 401 into an AuthError.
	plain := &http.Client{Transport: baseTransport}
	tenant := auth.OrganizationsTenant
	if org != "" {
		if tenant, err = auth.DiscoverTenant(ctx, plain, shared.OrgURL(host, org), org); err != nil {
			return err
		}
	}
	client, err := newMSAL(clientID, &auth.MSALCache{Dir: config.Dir(), ErrOut: ios.ErrOut})
	if err != nil {
		return err
	}
	res, err := acquireToken(ctx, client, tenant, useDeviceCode(opts.device, runtime.GOOS, os.Getenv), f.Browser.Browse, ios.ErrOut)
	if errors.Is(err, context.Canceled) {
		return cmdutil.CancelError
	}
	if err != nil {
		return fmt.Errorf("Microsoft Entra ID sign-in failed: %w", err) //nolint:revive,staticcheck // proper noun
	}

	token := res.AccessToken
	clientOpts := api.ClientOptions{
		AppVersion: f.AppVersion,
		Token:      func(*http.Request) (api.Token, error) { return api.Token{Value: token, Bearer: true}, nil },
		Transport:  baseTransport,
	}
	if f.Debug {
		clientOpts.Log = ios.ErrOut
	}
	hc := api.NewHTTPClient(clientOpts)
	if org == "" {
		host = "dev.azure.com"
		if org, err = selectOrg(ctx, hc, f.Prompter, ios.ErrOut); err != nil {
			return err
		}
		if tenant, err = auth.DiscoverTenant(ctx, plain, shared.OrgURL(host, org), org); err != nil {
			return err
		}
		// The first token came from the user's home tenant; a guest needs one from the org's.
		if tenant != auth.OrganizationsTenant {
			if res, err = client.Silent(ctx, res.Account, tenant); err != nil {
				return fmt.Errorf("get a token for the tenant of %s: %w", org, err)
			}
			token = res.AccessToken
		}
	}

	user, err := shared.Validate(ctx, hc, host, org)
	var authErr *cmdutil.AuthError
	if errors.As(err, &authErr) {
		return fmt.Errorf("%w: is your account a member of the organization?", authErr.Err)
	}
	if err != nil {
		return err
	}
	cfg.Set(host, auth.HomeAccountIDKey, res.Account.HomeAccountID)
	cfg.Set(host, auth.TenantKey(org), tenant)
	if err := cfg.Write(); err != nil {
		return err
	}
	fmt.Fprintf(ios.ErrOut, "%s Logged in to %s/%s as %s (Microsoft Entra ID)\n", ios.ColorScheme().Green("✓"), host, org, user.DisplayName)
	return nil
}

// useDeviceCode reports whether to skip the browser: --device, an SSH session, or Linux
// outside WSL without a display. getenv is os.Getenv outside tests.
func useDeviceCode(device bool, goos string, getenv func(string) string) bool {
	if device || getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return true
	}
	// ponytail: WSL detected from its env vars only; add /proc/sys/fs/binfmt_misc/WSLInterop if they prove unreliable.
	wsl := getenv("WSL_DISTRO_NAME") != "" || getenv("WSL_INTEROP") != ""
	return goos == "linux" && !wsl && getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == ""
}

// acquireToken runs the browser sign-in, or the device code flow when device is set or no
// browser can be opened. Device code instructions go to errOut.
func acquireToken(ctx context.Context, client auth.MSAL, tenant string, device bool, browse func(string) error, errOut io.Writer) (public.AuthResult, error) {
	if !device {
		bctx, cancel := context.WithTimeout(ctx, loginTimeout)
		defer cancel()
		var openErr error
		res, err := client.Interactive(bctx, tenant, func(u string) error {
			fmt.Fprintln(errOut, "Opening your browser to sign in to Microsoft Entra ID...")
			openErr = browse(u)
			return openErr
		})
		if openErr == nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return res, fmt.Errorf("timed out after %s waiting for the browser sign-in; try again or use --device", loginTimeout)
			}
			return res, err
		}
		fmt.Fprintf(errOut, "! Could not open a browser (%v): using device code sign-in\n", openErr)
	}
	res, err := client.DeviceCode(ctx, tenant, func(msg string) { fmt.Fprintln(errOut, msg) })
	if err != nil && (strings.Contains(err.Error(), "AADSTS53003") || strings.Contains(strings.ToLower(err.Error()), "conditional access")) {
		return res, fmt.Errorf("%w\n%s", err, caHint)
	}
	return res, err
}

const vssps = "https://app.vssps.visualstudio.com/_apis/"

// listOrgs returns the names of the organizations the signed-in Entra user belongs to.
func listOrgs(ctx context.Context, hc *http.Client) ([]string, error) {
	var me struct {
		ID string `json:"id"`
	}
	if err := getJSON(ctx, hc, vssps+"profile/profiles/me?api-version=7.1", &me); err != nil {
		return nil, err
	}
	var accounts struct {
		Value []struct {
			AccountName string `json:"accountName"`
		} `json:"value"`
	}
	if err := getJSON(ctx, hc, vssps+"accounts?memberId="+url.QueryEscape(me.ID)+"&api-version=7.1", &accounts); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(accounts.Value))
	for _, a := range accounts.Value {
		names = append(names, a.AccountName)
	}
	return names, nil
}

func getJSON(ctx context.Context, hc *http.Client, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", req.URL.Path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// selectOrg lets the user pick one of their organizations, or type one in when they can't be listed.
func selectOrg(ctx context.Context, hc *http.Client, p *cmdutil.Prompter, errOut io.Writer) (string, error) {
	orgs, err := listOrgs(ctx, hc)
	if err != nil {
		fmt.Fprintf(errOut, "! Could not list your organizations: %v\n", err)
	}
	if len(orgs) == 0 {
		v, err := p.Input("Azure DevOps organization (dev.azure.com/<org>)", "")
		if err != nil {
			return "", err
		}
		if v = strings.TrimSpace(v); v == "" {
			return "", errors.New(noOrg)
		}
		return v, nil
	}
	urls := make([]string, len(orgs))
	for i, o := range orgs {
		urls[i] = "https://dev.azure.com/" + o
	}
	i, err := p.Select("Which organization?", urls)
	if err != nil {
		return "", err
	}
	return orgs[i], nil
}
