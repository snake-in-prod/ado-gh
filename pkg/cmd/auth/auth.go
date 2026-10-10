// Package auth implements the `ado auth` command group.
package auth

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmd/auth/login"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/auth/logout"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/auth/status"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/auth/token"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdAuth returns the `auth` command group.
func NewCmdAuth(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "auth <command>",
		Short:   "Authenticate ado with Azure DevOps",
		GroupID: "core",
	}
	cmd.AddCommand(login.NewCmdLogin(f), logout.NewCmdLogout(f), status.NewCmdStatus(f), token.NewCmdToken(f))
	return cmd
}
