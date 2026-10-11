// Package close implements `ado workitem close`.
package close

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdClose returns the `workitem close` command.
func NewCmdClose(f *cmdutil.Factory) *cobra.Command {
	o := &shared.StateChange{}
	var resolve, removed bool
	cmd := &cobra.Command{
		Use:   "close <id>...",
		Short: "Close work items",
		Long: `Close work items: move each one to the Completed state of its type in the project's
process, e.g. Closed (Agile, CMMI) or Done (Scrum, Basic).

--resolve moves it to the Resolved state instead (the Resolved category, else a state
named "Resolved"), --removed to the Removed state (closed as not planned). --state names
the target state and overrides both.

--reason sets the Reason field; --comment adds a Markdown comment with the change. An
item already in a target state is left alone with a warning. When the process requires
another field to close, set it with --field.`,
		Example: `  ado workitem close 123 -c "Fixed in !45"
  ado wi close 123 124 --resolve -f "Resolved Reason=Fixed"
  ado wi close 123 --removed -r Cut`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Refs, o.Category = args, shared.CategoryCompleted
			if resolve {
				o.Category = shared.CategoryResolved
			} else if removed {
				o.Category = shared.CategoryRemoved
			}
			return o.Run(cmd.Context(), f)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&resolve, "resolve", false, "Move to the Resolved state instead")
	fl.BoolVar(&removed, "removed", false, "Move to the Removed state (not planned)")
	fl.StringVar(&o.State, "state", "", "Move to this state, overriding --resolve and --removed")
	fl.StringVarP(&o.Reason, "reason", "r", "", "Set the reason for the state change")
	fl.StringVarP(&o.Comment, "comment", "c", "", "Add a closing comment (Markdown)")
	fl.StringArrayVarP(&o.Fields, "field", "f", nil, "Set a field: Name=Value (repeatable)")
	cmd.MarkFlagsMutuallyExclusive("resolve", "removed")
	return cmd
}
