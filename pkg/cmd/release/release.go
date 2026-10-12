// Package release implements the `ado release` command group for Classic Release pipelines.
package release

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmd/release/list"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/release/logs"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/release/view"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/release/watch"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdRelease returns the `release` command group.
func NewCmdRelease(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "release <command>",
		Short:   "Manage Classic Release pipelines",
		GroupID: "core",
	}
	cmd.AddCommand(
		list.NewCmdList(f),
		logs.NewCmdLogs(f),
		view.NewCmdView(f),
		watch.NewCmdWatch(f),
	)
	return cmd
}
