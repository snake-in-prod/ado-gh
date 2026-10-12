// Package token implements `ado auth token`.
package token

import (
	"errors"
	"fmt"
	"os"

	"github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/extension"
	"github.com/spf13/cobra"
)

// NewCmdToken returns the `auth token` command.
func NewCmdToken(f *cmdutil.Factory) *cobra.Command {
	var orgFlag string
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Print the authentication token ado uses for an organization",
		Long: `Print the PAT or Microsoft Entra ID access token ado uses for an organization,
and nothing else. Entra ID tokens are refreshed when needed.

The organization comes from --org, else the current git remote, else ADO_ORG.
Exits with status 4 when not logged in.

Inside an extension (ADO_EXTENSION_NAME set) it refuses unless the user granted that
extension token consent. This is a convention, not a security boundary: extensions run
with your privileges.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name := os.Getenv("ADO_EXTENSION_NAME"); name != "" {
				// A missing record or an invalid name means no consent.
				if rec, err := extension.ReadRecord(extension.Dir(), name); err != nil || !rec.TokenConsent {
					return fmt.Errorf("extension %q has no token consent; reinstall it and allow token access to use `ado auth token`", name)
				}
			}
			host, org := shared.OrgFromContext(orgFlag, f.BaseRepo)
			if org == "" {
				return errors.New(shared.NoOrg)
			}
			cred, err := shared.Lookup(cmd.Context(), f, host, org)
			if err != nil {
				return err
			}
			fmt.Fprintln(f.IOStreams.Out, cred.Token.Value)
			return nil
		},
	}
	cmd.Flags().StringVar(&orgFlag, "org", "", "Azure DevOps organization of the token")
	return cmd
}
