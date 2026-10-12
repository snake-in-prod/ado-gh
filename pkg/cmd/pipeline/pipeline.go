// Package pipeline implements the `ado pipeline` command group.
package pipeline

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/cancel"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/list"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/logs"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/rerun"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/runcmd"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/view"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/watch"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdPipeline returns the `pipeline` command group, also reachable as `run`.
func NewCmdPipeline(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pipeline <command>",
		Short:   "Manage pipelines and runs",
		Aliases: []string{"run"},
		GroupID: "core",
	}
	cmd.AddCommand(
		cancel.NewCmdCancel(f),
		list.NewCmdList(f),
		logs.NewCmdLogs(f),
		rerun.NewCmdRerun(f),
		runcmd.NewCmdRun(f),
		view.NewCmdView(f),
		watch.NewCmdWatch(f),
	)
	return cmd
}
