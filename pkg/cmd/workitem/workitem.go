// Package workitem implements the `ado workitem` command group.
package workitem

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/assign"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/close"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/comment"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/create"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/delete"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/edit"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/link"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/list"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/query"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/querylist"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/reopen"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/restore"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/unlink"
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
	q := query.NewCmdQuery(f)
	q.AddCommand(querylist.NewCmdList(f))
	cmd.AddCommand(
		assign.NewCmdAssign(f),
		close.NewCmdClose(f),
		comment.NewCmdComment(f),
		create.NewCmdCreate(f),
		delete.NewCmdDelete(f),
		edit.NewCmdEdit(f),
		link.NewCmdLink(f),
		list.NewCmdList(f),
		q,
		reopen.NewCmdReopen(f),
		restore.NewCmdRestore(f),
		unlink.NewCmdUnlink(f),
		view.NewCmdView(f),
	)
	return cmd
}
