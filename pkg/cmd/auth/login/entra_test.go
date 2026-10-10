package login

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/snake-in-prod/ado-gh/internal/auth"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tid     = "72f988bf-86f1-41af-91ab-2d7cd011db47"
	homeTID = "33333333-3333-3333-3333-333333333333"
	zeroTID = "00000000-0000-0000-0000-000000000000"
)

type fakeBrowser struct {
	urls []string
	err  error
}

func (b *fakeBrowser) Browse(u string) error {
	b.urls = append(b.urls, u)
	return b.err
}

type call struct{ method, tenant string }

// fakeMSAL stands in for MSAL; Interactive opens the browser like MSAL does.
type fakeMSAL struct {
	calls          []call
	clientID       string
	interactiveErr error
	blockOnCtx     bool
	deviceErr      error
}

func (m *fakeMSAL) result(tenant string) public.AuthResult {
	return public.AuthResult{AccessToken: "at-" + tenant, Account: public.Account{HomeAccountID: "uid." + homeTID}}
}

func (m *fakeMSAL) Accounts(context.Context) ([]public.Account, error) { return nil, nil }

func (m *fakeMSAL) Silent(_ context.Context, _ public.Account, tenant string) (public.AuthResult, error) {
	m.calls = append(m.calls, call{"silent", tenant})
	return m.result(tenant), nil
}

func (m *fakeMSAL) Interactive(ctx context.Context, tenant string, openURL func(string) error) (public.AuthResult, error) {
	m.calls = append(m.calls, call{"interactive", tenant})
	if err := openURL("https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/authorize"); err != nil {
		return public.AuthResult{}, err
	}
	if m.blockOnCtx {
		<-ctx.Done()
		return public.AuthResult{}, ctx.Err()
	}
	return m.result(tenant), m.interactiveErr
}

func (m *fakeMSAL) RemoveAccount(context.Context, public.Account) error { return nil }

func (m *fakeMSAL) DeviceCode(_ context.Context, tenant string, show func(string)) (public.AuthResult, error) {
	m.calls = append(m.calls, call{"device", tenant})
	if m.deviceErr != nil {
		return public.AuthResult{}, m.deviceErr
	}
	show("To sign in, use a web browser to open https://microsoft.com/devicelogin and enter the code ABCD")
	return m.result(tenant), nil
}

// setupEntra prepares a login env with a fake MSAL, a client ID and a desktop session
// (interactive browser by default).
func setupEntra(t *testing.T) (*env, *fakeMSAL) {
	t.Helper()
	e := setup(t)
	m := &fakeMSAL{}
	old := newMSAL
	newMSAL = func(clientID string, c cache.ExportReplace) (auth.MSAL, error) {
		m.clientID = clientID
		if mc, ok := c.(*auth.MSALCache); !ok || mc.Dir != e.configDir {
			t.Errorf("MSAL cache = %#v; want an MSALCache in the config dir", c)
		}
		return m, nil
	}
	t.Cleanup(func() { newMSAL = old })
	t.Setenv("ADO_ENTRA_CLIENT_ID", "client-id")
	for k, v := range map[string]string{"SSH_CONNECTION": "", "SSH_TTY": "", "DISPLAY": ":0", "WAYLAND_DISPLAY": "", "WSL_DISTRO_NAME": "", "WSL_INTEROP": ""} {
		t.Setenv(k, v)
	}
	return e, m
}

func tenantHead(tenantHeader string) httpmock.Responder {
	return func(req *http.Request) (*http.Response, error) {
		h := http.Header{}
		if tenantHeader != "" {
			h.Set("X-VSS-ResourceTenant", tenantHeader)
		}
		return &http.Response{StatusCode: 401, Header: h, Body: http.NoBody, Request: req}, nil
	}
}

func (e *env) stubDiscovery(org, tenantHeader string) {
	e.reg.Register(httpmock.REST("HEAD", org+"/"), tenantHead(tenantHeader))
}

// selectEntra answers the login menu with Entra ID, the first option.
func (e *env) selectEntra(more string) {
	e.tty()
	e.accessible = true
	e.in.WriteString("1\n" + more)
}

func TestEntraLoginBrowser(t *testing.T) {
	e, m := setupEntra(t)
	e.selectEntra("")
	e.stubDiscovery("contoso", tid)
	e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), connectionData("1234"))

	require.NoError(t, e.run("--org", "contoso"))
	e.reg.Verify(t)

	out := e.out.String()
	entra, pat := strings.Index(out, "1. Microsoft Entra ID"), strings.Index(out, "2. Personal access token")
	assert.True(t, entra >= 0 && pat > entra, "menu offers Entra ID first, PAT second: %q", out)

	assert.Equal(t, "client-id", m.clientID)
	assert.Equal(t, []call{{"interactive", tid}}, m.calls)
	assert.Equal(t, []string{"https://login.microsoftonline.com/" + tid + "/oauth2/v2.0/authorize"}, e.browser.urls,
		"MSAL's openURL is the ado browser")
	assert.Empty(t, e.reg.Requests[0].Header.Get("Authorization"), "tenant discovery is unauthenticated")
	assert.Equal(t, "Bearer at-"+tid, e.reg.Requests[1].Header.Get("Authorization"))

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "uid."+homeTID, cfg.Get("dev.azure.com", auth.HomeAccountIDKey))
	assert.Equal(t, tid, cfg.Get("dev.azure.com", auth.TenantKey("contoso")))
	assert.Contains(t, e.errOut.String(), "Logged in to dev.azure.com/contoso as Mona Lisa (Microsoft Entra ID)")
	assert.NotContains(t, e.out.String()+e.errOut.String(), "at-"+tid, "token not printed")
}

func TestEntraLoginDevice(t *testing.T) {
	e, m := setupEntra(t)
	e.stubDiscovery("contoso", tid)
	e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), connectionData("1234"))

	// Non-interactive: --device needs no prompt.
	require.NoError(t, e.run("--org", "contoso", "--device"))
	assert.Equal(t, []call{{"device", tid}}, m.calls)
	assert.Contains(t, e.errOut.String(), "enter the code ABCD", "device code message on stderr")
	assert.Empty(t, e.out.String())
	assert.Empty(t, e.browser.urls)
}

func TestEntraLoginBrowserFallsBackToDeviceCode(t *testing.T) {
	e, m := setupEntra(t)
	e.selectEntra("")
	e.browser.err = errors.New("xdg-open not found")
	e.stubDiscovery("contoso", tid)
	e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), connectionData("1234"))

	require.NoError(t, e.run("--org", "contoso"))
	assert.Equal(t, []call{{"interactive", tid}, {"device", tid}}, m.calls)
	assert.Contains(t, e.errOut.String(), "Could not open a browser (xdg-open not found): using device code sign-in")
	assert.Contains(t, e.errOut.String(), "enter the code ABCD")
}

func TestEntraLoginSSHUsesDeviceCode(t *testing.T) {
	e, m := setupEntra(t)
	t.Setenv("SSH_CONNECTION", "10.0.0.1 22 10.0.0.2 22")
	e.selectEntra("")
	e.stubDiscovery("contoso", tid)
	e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), connectionData("1234"))

	require.NoError(t, e.run("--org", "contoso"))
	assert.Equal(t, []call{{"device", tid}}, m.calls)
}

func TestUseDeviceCode(t *testing.T) {
	tests := []struct {
		name   string
		device bool
		goos   string
		env    map[string]string
		want   bool
	}{
		{name: "--device", device: true, goos: "windows", want: true},
		{name: "windows desktop", goos: "windows", want: false},
		{name: "macOS", goos: "darwin", want: false},
		{name: "SSH_CONNECTION", goos: "darwin", env: map[string]string{"SSH_CONNECTION": "x"}, want: true},
		{name: "SSH_TTY", goos: "windows", env: map[string]string{"SSH_TTY": "/dev/pts/0"}, want: true},
		{name: "linux without display", goos: "linux", want: true},
		{name: "linux with DISPLAY", goos: "linux", env: map[string]string{"DISPLAY": ":0"}, want: false},
		{name: "linux with WAYLAND_DISPLAY", goos: "linux", env: map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, want: false},
		{name: "WSL without display", goos: "linux", env: map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, want: false},
		{name: "WSL interop", goos: "linux", env: map[string]string{"WSL_INTEROP": "/run/WSL/1_interop"}, want: false},
		{name: "WSL over SSH", goos: "linux", env: map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "SSH_TTY": "x"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			assert.Equal(t, tt.want, useDeviceCode(tt.device, tt.goos, getenv))
		})
	}
}

func TestEntraLoginTimeout(t *testing.T) {
	old := loginTimeout
	loginTimeout = 10 * time.Millisecond
	t.Cleanup(func() { loginTimeout = old })
	m := &fakeMSAL{blockOnCtx: true}
	_, err := acquireToken(context.Background(), m, tid, false, func(string) error { return nil }, &strings.Builder{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
	assert.Contains(t, err.Error(), "--device")
}

func TestEntraLoginCancelled(t *testing.T) {
	e, m := setupEntra(t)
	m.interactiveErr = context.Canceled
	e.selectEntra("")
	e.stubDiscovery("contoso", tid)
	assert.ErrorIs(t, e.run("--org", "contoso"), cmdutil.CancelError)
}

func TestEntraLoginErrors(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		clientID string
		discover httpmock.Responder
		deviceEr error
		validate httpmock.Responder
		want     []string
	}{
		{
			name: "no client ID", args: []string{"--org", "contoso", "--device"},
			want: []string{"ADO_ENTRA_CLIENT_ID"},
		},
		{
			name: "MSA-backed org", args: []string{"--org", "contoso", "--device"}, clientID: "c",
			discover: tenantHead(zeroTID), want: []string{"personal Microsoft accounts", "PAT", "--with-token"},
		},
		{
			name: "org not found", args: []string{"--org", "contoso", "--device"}, clientID: "c",
			discover: httpmock.StatusStringResponse(404, ""), want: []string{`organization "contoso" not found`},
		},
		{
			name: "device code blocked by Conditional Access", args: []string{"--org", "contoso", "--device"}, clientID: "c",
			discover: tenantHead(tid),
			deviceEr: errors.New("AADSTS53003: access has been blocked by Conditional Access policies"),
			want:     []string{"AADSTS53003", "Conditional Access", "browser", "PAT"},
		},
		{
			name: "device code failure", args: []string{"--org", "contoso", "--device"}, clientID: "c",
			discover: tenantHead(tid), deviceEr: errors.New("invalid_client"),
			want: []string{"sign-in failed: invalid_client"},
		},
		{
			name: "not a member", args: []string{"--org", "contoso", "--device"}, clientID: "c",
			discover: tenantHead(tid), validate: httpmock.StatusStringResponse(401, ""),
			want: []string{"rejected", "member of the organization"},
		},
		{
			name: "validation error", args: []string{"--org", "contoso", "--device"}, clientID: "c",
			discover: tenantHead(tid), validate: httpmock.StatusStringResponse(500, ""),
			want: []string{"HTTP 500"},
		},
		{
			name: "non-TTY without org", args: []string{"--device"}, clientID: "c",
			want: []string{"no organization"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, m := setupEntra(t)
			t.Setenv("ADO_ENTRA_CLIENT_ID", tt.clientID)
			m.deviceErr = tt.deviceEr
			if tt.discover != nil {
				e.reg.Register(httpmock.REST("HEAD", "contoso/"), tt.discover)
			}
			if tt.validate != nil {
				e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), tt.validate)
			}
			err := e.run(tt.args...)
			require.Error(t, err)
			for _, w := range tt.want {
				assert.Contains(t, err.Error(), w)
			}
			var authErr *cmdutil.AuthError
			assert.False(t, errors.As(err, &authErr), "login must not ask to run `ado auth login`")
			cfg, _ := config.Load()
			assert.Empty(t, cfg.Get("dev.azure.com", auth.HomeAccountIDKey), "nothing saved on failure")
		})
	}
}

func TestEntraLoginNewMSALError(t *testing.T) {
	e, _ := setupEntra(t)
	newMSAL = func(string, cache.ExportReplace) (auth.MSAL, error) { return nil, errors.New("bad authority") }
	e.stubDiscovery("contoso", tid)
	assert.ErrorContains(t, e.run("--org", "contoso", "--device"), "bad authority")
}

func stubOrgs(reg *httpmock.Registry, names ...string) {
	reg.Register(httpmock.REST("GET", "_apis/profile/profiles/me"), httpmock.JSONResponse(map[string]any{"id": "member-1"}))
	value := make([]map[string]any, len(names))
	for i, n := range names {
		value[i] = map[string]any{"accountName": n, "accountUri": "https://vssps.dev.azure.com/" + n + "/"}
	}
	reg.Register(httpmock.REST("GET", "_apis/accounts"), httpmock.JSONResponse(map[string]any{"count": len(names), "value": value}))
}

func TestEntraLoginSelectsOrg(t *testing.T) {
	e, m := setupEntra(t)
	e.selectEntra("2\n")
	stubOrgs(e.reg, "fabrikam", "contoso")
	e.stubDiscovery("contoso", tid)
	e.reg.Register(httpmock.REST("GET", "contoso/_apis/connectionData"), connectionData("1234"))

	require.NoError(t, e.run())
	e.reg.Verify(t)
	assert.Equal(t, []call{{"interactive", auth.OrganizationsTenant}, {"silent", tid}}, m.calls,
		"sign in with organizations, then get a token from the chosen org's tenant")
	assert.Contains(t, e.out.String(), "https://dev.azure.com/fabrikam")
	assert.Contains(t, e.out.String(), "https://dev.azure.com/contoso")

	accounts := e.reg.Requests[1]
	assert.Equal(t, "app.vssps.visualstudio.com", accounts.URL.Host)
	assert.Equal(t, "member-1", accounts.URL.Query().Get("memberId"))
	assert.Equal(t, "Bearer at-"+auth.OrganizationsTenant, accounts.Header.Get("Authorization"))
	validate := e.reg.Requests[3]
	assert.Equal(t, "dev.azure.com", validate.URL.Host)
	assert.Equal(t, "Bearer at-"+tid, validate.Header.Get("Authorization"), "validated with the org-tenant token")

	cfg, _ := config.Load()
	assert.Equal(t, tid, cfg.Get("dev.azure.com", auth.TenantKey("contoso")))
}

func TestSelectOrg(t *testing.T) {
	tests := []struct {
		name    string
		stub    func(*httpmock.Registry)
		input   string
		want    string
		wantErr string
		warn    string
	}{
		{name: "pick from list", stub: func(r *httpmock.Registry) { stubOrgs(r, "fabrikam", "contoso") }, input: "1\n", want: "fabrikam"},
		{name: "empty list: manual entry", stub: func(r *httpmock.Registry) { stubOrgs(r) }, input: " typed \n", want: "typed"},
		{
			name: "profile error: manual entry", input: "typed\n", want: "typed", warn: "Could not list your organizations",
			stub: func(r *httpmock.Registry) {
				r.Register(httpmock.REST("GET", "_apis/profile/profiles/me"), httpmock.StatusStringResponse(500, ""))
			},
		},
		{
			name: "accounts error: manual entry", input: "typed\n", want: "typed", warn: "HTTP 400",
			stub: func(r *httpmock.Registry) {
				r.Register(httpmock.REST("GET", "_apis/profile/profiles/me"), httpmock.JSONResponse(map[string]any{"id": "m"}))
				r.Register(httpmock.REST("GET", "_apis/accounts"), httpmock.StatusStringResponse(400, ""))
			},
		},
		{name: "empty manual entry", stub: func(r *httpmock.Registry) { stubOrgs(r) }, input: "\n", wantErr: "no organization"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setup(t)
			e.tty()
			e.accessible = true
			e.in.WriteString(tt.input)
			tt.stub(e.reg)
			f := e.factory()
			got, err := selectOrg(context.Background(), &http.Client{Transport: e.reg}, f.Prompter, e.errOut)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Contains(t, e.errOut.String(), tt.warn)
		})
	}
}

func TestSelectOrgPromptErrors(t *testing.T) {
	for _, n := range [][]string{{"a"}, nil} {
		e := setup(t)
		stubOrgs(e.reg, n...)
		// Not a TTY: prompts fail.
		_, err := selectOrg(context.Background(), &http.Client{Transport: e.reg}, &cmdutil.Prompter{IO: e.ios}, e.errOut)
		assert.ErrorIs(t, err, cmdutil.ErrNoPrompt)
	}
}
