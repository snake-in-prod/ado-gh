// Package get implements `ado config get`.
package get

import (
	"fmt"

	"github.com/snake-in-prod/ado-gh/pkg/cmd/config/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdGet returns the `config get` command.
func NewCmdGet(f *cmdutil.Factory) *cobra.Command {
	var host, project string
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Print the value of a given configuration key",
		Example: `  $ ado config get editor
  $ ado config get prompt --host dev.azure.com
  $ ado config get team --project contoso/fabrikam`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			opt, err := shared.Find(args[0])
			if err != nil {
				return err
			}
			scope, err := opt.Scope(host, project)
			if err != nil {
				return err
			}
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(f.IOStreams.Out, opt.Value(cfg, scope))
			return err
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Get per-host setting")
	cmd.Flags().StringVar(&project, "project", "", "Get per-project setting for ORG/PROJECT (team only)")
	return cmd
}
