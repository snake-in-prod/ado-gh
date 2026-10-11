// Package reopen implements `ado pr reopen`.
package reopen

import (
	"context"
	"fmt"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	ref, comment string
}

// NewCmdReopen returns the `pr reopen` command.
func NewCmdReopen(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "reopen [<number> | <url> | <branch>]",
		Short: "Reactivate an abandoned pull request",
		Long: `Set an abandoned pull request back to active. Without an argument, the pull request of
the current branch is used. Its source branch must still exist: push or restore it first.`,
		Example: `  ado pr reopen 123
  ado pr reopen 123 --comment "Picking this up again"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				o.ref = args[0]
			}
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().StringVarP(&o.comment, "comment", "c", "", "Add a comment before reactivating")
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	pr, c, _, err := shared.Find(ctx, f, o.ref)
	if err != nil {
		return err
	}
	id := *pr.PullRequestId
	// The server's error for a deleted source branch is undocumented, so check the branch
	// first; other states are left to SetState's guards.
	if shared.Deref(pr.Status) == git.PullRequestStatusValues.Abandoned {
		head := *c
		if fs := pr.ForkSource; fs != nil && fs.Repository != nil && fs.Repository.Id != nil {
			head.Repo = fs.Repository.Id.String()
			if p := fs.Repository.Project; p != nil && p.Name != nil {
				head.Project = *p.Name
			}
		}
		branch := strings.TrimPrefix(shared.Deref(pr.SourceRefName), "refs/heads/")
		ref, err := head.Ref(ctx, branch)
		if err != nil {
			return err
		}
		switch {
		case ref == nil && head.Repo != c.Repo:
			return fmt.Errorf("source branch %s of pull request !%d was deleted from its fork; push it to the fork or restore it there, then retry", branch, id)
		case ref == nil:
			return fmt.Errorf("source branch %s of pull request !%d was deleted; push it (git push origin %s) or restore it, then retry", branch, id, branch)
		}
	}
	changed, err := shared.SetState(ctx, f.IOStreams, c, pr, &git.GitPullRequest{Status: &git.PullRequestStatusValues.Active}, o.comment)
	if err != nil || !changed {
		return err
	}
	if ios := f.IOStreams; ios.IsStdoutTTY() {
		fmt.Fprintf(ios.ErrOut, "%s Reopened pull request !%d (%s)\n", ios.ColorScheme().Green("✓"), id, shared.Deref(pr.Title))
	}
	return nil
}
