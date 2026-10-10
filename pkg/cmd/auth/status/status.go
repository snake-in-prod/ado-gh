// Package status implements `ado auth status`.
package status

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdStatus returns the `auth status` command.
func NewCmdStatus(f *cmdutil.Factory) *cobra.Command {
	var org string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the authentication status for an organization",
		Long: `Show which user, method (pat or entra) and token source ado uses for an
organization, and check the credentials against its connectionData API. The
token is masked.

The organization comes from --org, else the current git remote, else ADO_ORG.
Exits with status 4 when not logged in or when the credentials are rejected.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statusRun(cmd.Context(), f, org)
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "Azure DevOps organization to check")
	return cmd
}

func statusRun(ctx context.Context, f *cmdutil.Factory, orgFlag string) error {
	ios := f.IOStreams
	host, org := shared.OrgFromContext(orgFlag, f.BaseRepo)
	if org == "" {
		return errors.New(shared.NoOrg)
	}
	cred, err := shared.Lookup(ctx, f, host, org)
	if err != nil {
		return err
	}
	base, err := f.HttpClient()
	if err != nil {
		return err
	}
	clientOpts := api.ClientOptions{
		AppVersion: f.AppVersion,
		Token:      func(*http.Request) (api.Token, error) { return cred.Token, nil },
		Transport:  base.Transport,
	}
	if f.Debug {
		clientOpts.Log = ios.ErrOut
	}
	user, err := shared.Validate(ctx, api.NewHTTPClient(clientOpts), host, org)
	var authErr *cmdutil.AuthError
	if err != nil && !errors.As(err, &authErr) {
		return err
	}

	cs := ios.ColorScheme()
	fmt.Fprintf(ios.Out, "%s/%s\n", host, org)
	if err != nil {
		fmt.Fprintf(ios.Out, "  %s Token invalid\n", cs.Red("X"))
	} else {
		fmt.Fprintf(ios.Out, "  %s Logged in as %s\n", cs.Green("✓"), user.DisplayName)
	}
	fmt.Fprintf(ios.Out, "  - Method: %s\n  - Token source: %s\n  - Token: %s\n", cred.Method, cred.Source, mask(cred.Token.Value))
	return err
}

// mask keeps the first 4 characters of a token, padded to a fixed length so it isn't revealed.
func mask(token string) string {
	if len(token) <= 8 {
		return strings.Repeat("*", len(token))
	}
	return token[:4] + strings.Repeat("*", 16)
}
