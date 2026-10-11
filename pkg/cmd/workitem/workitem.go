// Package workitem implements the `ado workitem` command group.
package workitem

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/create"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/list"
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
		create.NewCmdCreate(f),
		list.NewCmdList(f),
		view.NewCmdView(f),
	)
	return cmd
}
