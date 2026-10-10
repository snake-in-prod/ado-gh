// Package list implements `ado config list`.
package list

import (
	"fmt"

	"github.com/snake-in-prod/ado-gh/pkg/cmd/config/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdList returns the `config list` command.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	var host string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Print a list of configuration keys and values",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			ios := f.IOStreams
			for _, opt := range shared.Options {
				v := opt.Value(cfg, host)
				if v == "" && ios.IsStdoutTTY() {
					v = ios.ColorScheme().Gray("(not set)")
				}
				if _, err := fmt.Fprintf(ios.Out, "%s=%s\n", opt.Key, v); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "Get per-host configuration")
	return cmd
}
