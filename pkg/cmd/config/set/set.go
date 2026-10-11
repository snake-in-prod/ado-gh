// Package set implements `ado config set`.
package set

import (
	"fmt"

	"github.com/snake-in-prod/ado-gh/pkg/cmd/config/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdSet returns the `config set` command.
func NewCmdSet(f *cmdutil.Factory) *cobra.Command {
	var host, project string
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Update configuration with a value for the given key",
		Example: `  $ ado config set editor "code --wait"
  $ ado config set prompt disabled --host dev.azure.com
  $ ado config set team "Web Team" --project contoso/fabrikam`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			opt, err := shared.Find(args[0])
			if err != nil {
				return err
			}
			scope, err := opt.Scope(host, project)
			if err != nil {
				return err
			}
			if err := opt.Validate(args[1]); err != nil {
				return err
			}
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			cfg.Set(scope, opt.Key, args[1])
			if err := cfg.Write(); err != nil {
				return fmt.Errorf("failed to write config: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Set per-host setting")
	cmd.Flags().StringVar(&project, "project", "", "Set per-project setting for ORG/PROJECT (team only)")
	return cmd
}
