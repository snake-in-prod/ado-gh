// Package sprint implements the `ado sprint` command group.
package sprint

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmd/sprint/board"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/sprint/current"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/sprint/list"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/sprint/view"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdSprint returns the `sprint` command group.
func NewCmdSprint(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sprint <command>",
		Short:   "View sprints (team iterations)",
		GroupID: "core",
	}
	cmd.AddCommand(
		board.NewCmdBoard(f),
		current.NewCmdCurrent(f),
		list.NewCmdList(f),
		view.NewCmdView(f),
	)
	return cmd
}
