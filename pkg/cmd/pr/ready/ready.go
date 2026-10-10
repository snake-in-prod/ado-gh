// Package ready implements `ado pr ready`.
package ready

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	ref       string
	undo, yes bool
}

// NewCmdReady returns the `pr ready` command.
func NewCmdReady(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "ready [<number> | <url> | <branch>]",
		Short: "Mark a pull request as ready for review",
		Long: `Publish a draft pull request, which assigns required reviewers and starts policies.
Without an argument, the pull request of the current branch is used.

With --undo, convert it back to a draft. This removes all existing votes, so it asks for
confirmation when the pull request has votes (skip it with --yes).`,
		Example: `  ado pr ready 123
  ado pr ready --undo --yes`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				o.ref = args[0]
			}
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().BoolVar(&o.undo, "undo", false, "Convert a pull request back to a draft")
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, "Skip the confirmation prompt of --undo")
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	pr, c, _, err := shared.Find(ctx, f, o.ref)
	if err != nil {
		return err
	}
	ios := f.IOStreams
	id, title := shared.Deref(pr.PullRequestId), shared.Deref(pr.Title)

	// Marking as draft removes all votes: confirm when it would actually happen.
	if votes := countVotes(pr); o.undo && !o.yes && votes > 0 &&
		shared.Deref(pr.Status) == git.PullRequestStatusValues.Active && !shared.Deref(pr.IsDraft) {
		if !ios.CanPrompt() {
			return cmdutil.FlagErrorf("--yes required when not running interactively")
		}
		ok, err := f.Prompter.Confirm(fmt.Sprintf("Converting pull request !%d (%s) to draft removes its %d votes. Continue?", id, title, votes), false)
		if err != nil {
			return err
		}
		if !ok {
			return cmdutil.CancelError
		}
	}

	draft := o.undo
	changed, err := shared.SetState(ctx, ios, c, pr, &git.GitPullRequest{IsDraft: &draft}, "")
	if err != nil || !changed || !ios.IsStdoutTTY() {
		return err
	}
	state := "marked as ready for review"
	if draft {
		state = "converted to draft"
	}
	fmt.Fprintf(ios.ErrOut, "%s Pull request !%d (%s) is %s\n", ios.ColorScheme().Green("✓"), id, title, state)
	return nil
}

func countVotes(pr *git.GitPullRequest) int {
	n := 0
	for _, r := range shared.Deref(pr.Reviewers) {
		if shared.Deref(r.Vote) != 0 {
			n++
		}
	}
	return n
}
