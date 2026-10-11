// Package reopen implements `ado workitem reopen`.
package reopen

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdReopen returns the `workitem reopen` command.
func NewCmdReopen(f *cmdutil.Factory) *cobra.Command {
	o := &shared.StateChange{Category: shared.CategoryProposed}
	cmd := &cobra.Command{
		Use:   "reopen <id>...",
		Short: "Reopen work items",
		Long: `Reopen work items: move each one to the first Proposed state of its type (e.g. New,
To Do), or the first In Progress state when the type has none. --state names the target
state instead.

An item already in a Proposed or In Progress state is left alone with a warning.`,
		Example: `  ado workitem reopen 123 -c "Still failing on Windows"
  ado wi reopen 123 --state Active -r Reactivated`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Refs = args
			return o.Run(cmd.Context(), f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&o.State, "state", "", "Move to this state")
	fl.StringVarP(&o.Reason, "reason", "r", "", "Set the reason for the state change")
	fl.StringVarP(&o.Comment, "comment", "c", "", "Add a reopening comment (Markdown)")
	fl.StringArrayVarP(&o.Fields, "field", "f", nil, "Set a field: Name=Value (repeatable)")
	return cmd
}
