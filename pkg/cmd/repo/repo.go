// Package repo implements the `ado repo` command group.
package repo

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/clone"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/create"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/delete"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/list"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/restore"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/repo/setdefault"
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
		delete.NewCmdDelete(f),
		list.NewCmdList(f),
		restore.NewCmdRestore(f),
		setdefault.NewCmdSetDefault(f),
		view.NewCmdView(f),
	)
	return cmd
}
