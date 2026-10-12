// Package release implements the `ado release` command group for Classic Release pipelines.
package release

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmd/release/approve"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/release/create"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/release/deploy"
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
		approve.NewCmdApprove(f),
		deploy.NewCmdCancel(f),
		create.NewCmdCreate(f),
		deploy.NewCmdDeploy(f),
		list.NewCmdList(f),
		approve.NewCmdReject(f),
		logs.NewCmdLogs(f),
		view.NewCmdView(f),
		watch.NewCmdWatch(f),
	)
	return cmd
}
