// Package pr implements the `ado pr` command group.
package pr

import (
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/checkout"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/create"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/list"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/merge"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/review"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/view"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdPR returns the `pr` command group.
func NewCmdPR(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pr <command>",
		Short:   "Manage pull requests",
		GroupID: "core",
	}
	cmd.AddCommand(
		checkout.NewCmdCheckout(f),
		create.NewCmdCreate(f),
		list.NewCmdList(f),
		merge.NewCmdMerge(f),
		review.NewCmdReview(f),
		view.NewCmdView(f),
	)
	return cmd
}
