// Package repo implements the `ado repo` command group.
package repo

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/clone"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/create"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/list"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/view"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdRepo returns the `repo` command group.
func NewCmdRepo(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "repo <command>",
		Short:   "Manage repositories",
		GroupID: "core",
	}
	cmd.AddCommand(
		clone.NewCmdClone(f),
		create.NewCmdCreate(f),
		list.NewCmdList(f),
		view.NewCmdView(f),
	)
	return cmd
}
