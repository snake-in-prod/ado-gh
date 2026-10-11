// Package workitem implements the `ado workitem` command group.
package workitem

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/assign"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/close"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/comment"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/create"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/edit"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/list"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/reopen"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/view"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdWorkItem returns the `workitem` command group, aliased as `wi` and `issue`.
func NewCmdWorkItem(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workitem <command>",
		Short:   "Manage work items",
		Aliases: []string{"wi", "issue"},
		GroupID: "core",
	}
	cmd.AddCommand(
		assign.NewCmdAssign(f),
		close.NewCmdClose(f),
		comment.NewCmdComment(f),
		create.NewCmdCreate(f),
		edit.NewCmdEdit(f),
		list.NewCmdList(f),
		reopen.NewCmdReopen(f),
		view.NewCmdView(f),
	)
	return cmd
}
