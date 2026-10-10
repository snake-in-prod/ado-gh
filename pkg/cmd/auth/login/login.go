// Package login implements `ado auth login`.
package login

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/auth"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// baseTransport sends the validation request; nil means the network. Tests stub it.
var baseTransport http.RoundTripper

const globalPATHint = "Global PATs stop working on 2026-12-01: use a PAT scoped to this organization, or sign in with Microsoft Entra ID."

type options struct {
	f               *cmdutil.Factory
	org             string
	withToken       bool
	device          bool
	insecureStorage bool
}

// NewCmdLogin returns the `auth login` command.
func NewCmdLogin(f *cmdutil.Factory) *cobra.Command {
	opts := &options{f: f}
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to an Azure DevOps organization",
		Long: `Authenticate with an Azure DevOps organization using Microsoft Entra ID or a
personal access token (PAT).

Run interactively to choose between Microsoft Entra ID (recommended) and a PAT.

Microsoft Entra ID sign-in opens your browser. Over SSH, on Linux without a
display, when no browser can be opened, or with --device, it uses the device
code flow instead: open the printed URL on any device and enter the code. The
Entra ID tokens are kept in an encrypted cache file whose key is stored in the
system credential store (in plain text when no credential store is available).
Organizations backed by personal Microsoft accounts need a PAT.

Pass a PAT on standard input with --with-token, or choose PAT interactively to
be prompted for it. Create an organization-scoped PAT with the scopes
"Code (Read & write)" and "Identity (Read)". Global PATs stop working on
2026-12-01: use an organization-scoped PAT or Microsoft Entra ID.

Credentials are checked against the organization's connectionData API before
they are saved. The organization comes from --org, else the current git remote,
else ADO_ORG; otherwise Microsoft Entra ID lists your organizations and the PAT
flow prompts for one.

A PAT is stored per organization in the system credential store. With
--insecure-storage, or when no credential store is available, it is written in
plain text to the config file.`,
		Example: `  # Interactive
  $ ado auth login

  # Microsoft Entra ID with the device code flow
  $ ado auth login --org contoso --device

  # Read the PAT from a file
  $ ado auth login --org contoso --with-token < pat.txt`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return loginRun(cmd, opts)
		},
	}
	cmd.Flags().StringVar(&opts.org, "org", "", "Azure DevOps organization to log in to")
	cmd.Flags().BoolVar(&opts.withToken, "with-token", false, "Read the PAT from standard input")
	cmd.Flags().BoolVar(&opts.device, "device", false, "Sign in to Microsoft Entra ID with the device code flow")
	cmd.Flags().BoolVar(&opts.insecureStorage, "insecure-storage", false, "Save the PAT in plain text instead of the credential store")
	cmd.MarkFlagsMutuallyExclusive("with-token", "device")
	return cmd
}

func loginRun(cmd *cobra.Command, opts *options) error {
	f, ios := opts.f, opts.f.IOStreams
	if !opts.withToken && !opts.device && !ios.CanPrompt() {
		return cmdutil.FlagErrorf("--with-token required when not running interactively (or --device to sign in with Microsoft Entra ID)")
	}
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	store, ok := cfg.(auth.Config)
	if !ok {
		return errors.New("config does not support storing credentials")
	}
	if opts.device {
		return entraLogin(cmd.Context(), opts, store)
	}
	if !opts.withToken {
		method, err := f.Prompter.Select("How would you like to authenticate?", []string{
			"Microsoft Entra ID (sign in with your browser)",
			"Personal access token (PAT)",
		})
		if err != nil {
			return err
		}
		if method == 0 {
			return entraLogin(cmd.Context(), opts, store)
		}
	}
	return patLogin(cmd, opts, store)
}

func patLogin(cmd *cobra.Command, opts *options, store auth.Config) error {
	f, ios := opts.f, opts.f.IOStreams
	host, org, err := resolveOrg(opts)
	if err != nil {
		return err
	}

	var token string
	if opts.withToken {
		b, err := io.ReadAll(ios.In)
		if err != nil {
			return fmt.Errorf("read token from standard input: %w", err)
		}
		token = string(b)
	} else if token, err = f.Prompter.Password("Paste your personal access token for " + org); err != nil {
		return err
	}
	if token = strings.TrimSpace(token); token == "" {
		return errors.New("no token provided")
	}

	clientOpts := api.ClientOptions{
		AppVersion: f.AppVersion,
		Token:      func(*http.Request) (api.Token, error) { return api.Token{Value: token}, nil },
		Transport:  baseTransport,
	}
	if f.Debug {
		clientOpts.Log = ios.ErrOut
	}
	user, err := shared.Validate(cmd.Context(), api.NewHTTPClient(clientOpts), host, org)
	var authErr *cmdutil.AuthError
	if errors.As(err, &authErr) {
		// A plain error: AuthError would tell the user to run this very command.
		return fmt.Errorf("%w\n%s", authErr.Err, globalPATHint)
	}
	if err != nil {
		return err
	}

	if err := (&auth.Store{Config: store, ErrOut: ios.ErrOut}).Set(host, org, token, opts.insecureStorage); err != nil {
		return fmt.Errorf("save token: %w", err)
	}
	fmt.Fprintf(ios.ErrOut, "%s Logged in to %s/%s as %s\n", ios.ColorScheme().Green("✓"), host, org, user.DisplayName)
	return nil
}

const noOrg = shared.NoOrg

func orgFromContext(opts *options) (host, org string) {
	return shared.OrgFromContext(opts.org, opts.f.BaseRepo)
}

// resolveOrg is orgFromContext, prompting when no org is set.
func resolveOrg(opts *options) (host, org string, err error) {
	if host, org = orgFromContext(opts); org != "" {
		return host, org, nil
	}
	if !opts.f.IOStreams.CanPrompt() {
		return "", "", errors.New(noOrg)
	}
	v, err := opts.f.Prompter.Input("Azure DevOps organization (dev.azure.com/<org>)", "")
	if err != nil {
		return "", "", err
	}
	if v = strings.TrimSpace(v); v == "" {
		return "", "", errors.New(noOrg)
	}
	return "dev.azure.com", v, nil
}
