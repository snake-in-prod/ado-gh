// Package config implements `ado config`.
package config

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmd/config/get"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/config/list"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/config/set"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/config/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdConfig returns the `config` command group.
func NewCmdConfig(f *cmdutil.Factory) *cobra.Command {
	long := "Display or change configuration settings for ado.\n\nCurrent respected settings:\n"
	for _, o := range shared.Options {
		long += "- " + o.Key + ": " + o.Description
		if o.Default != "" {
			long += ` (default "` + o.Default + `")`
		}
		long += "\n"
	}
	cmd := &cobra.Command{
		Use:     "config <command>",
		Short:   "Manage configuration for ado",
		Long:    long,
		GroupID: "additional",
	}
	cmd.AddCommand(get.NewCmdGet(f), set.NewCmdSet(f), list.NewCmdList(f))
	return cmd
}
