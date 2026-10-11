// Package close implements `ado pr close`.
package close

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	gitcmd "github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	ref, comment      string
	deleteBranch, yes bool
}

// NewCmdClose returns the `pr close` command.
func NewCmdClose(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "close [<number> | <url> | <branch>]",
		Short: "Abandon a pull request",
		Long: `Abandon a pull request. Without an argument, the pull request of the current branch is used.

With --delete-branch, the source branch is deleted from the remote (unless it is in a fork),
together with every local branch tracking it. When one of them is checked out, the default
branch is checked out first.`,
		Example: `  ado pr close 123 --comment "Superseded by !124"
  ado pr close 123 --delete-branch --yes`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				o.ref = args[0]
			}
			if !o.yes && !f.IOStreams.CanPrompt() {
				return cmdutil.FlagErrorf("--yes required when not running interactively")
			}
			return run(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&o.comment, "comment", "c", "", "Leave an abandoning comment")
	fl.BoolVarP(&o.deleteBranch, "delete-branch", "d", false, "Delete the local and remote source branch after abandoning")
	fl.BoolVarP(&o.yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	pr, c, _, err := shared.Find(ctx, f, o.ref)
	if err != nil {
		return err
	}
	id, title := shared.Deref(pr.PullRequestId), shared.Deref(pr.Title)
	// Only an active pull request gets abandoned; SetState reports the other states.
	if !o.yes && shared.Deref(pr.Status) == git.PullRequestStatusValues.Active {
		ok, err := f.Prompter.Confirm(fmt.Sprintf("Abandon pull request !%d %q?", id, title), true)
		if err != nil {
			return err
		}
		if !ok {
			return cmdutil.CancelError
		}
	}
	changed, err := shared.SetState(ctx, f.IOStreams, c, pr, &git.GitPullRequest{Status: &git.PullRequestStatusValues.Abandoned}, o.comment)
	if err != nil || !changed {
		return err
	}
	ios := f.IOStreams
	cs := ios.ColorScheme()
	if ios.IsStdoutTTY() {
		fmt.Fprintf(ios.ErrOut, "%s Abandoned pull request !%d (%s)\n", cs.Green("✓"), id, title)
	}
	if !o.deleteBranch {
		return nil
	}

	branch := strings.TrimPrefix(shared.Deref(pr.SourceRefName), "refs/heads/")
	var errs []error
	// With -R the current directory may be another repository: leave local branches alone (gh).
	if f.RepoOverride == "" {
		errs = append(errs, deleteLocal(ctx, f, c, branch))
	}
	if pr.ForkSource != nil && pr.ForkSource.Repository != nil {
		fmt.Fprintf(ios.ErrOut, "%s Skipped deleting remote branch %s: it is in a fork\n", cs.Yellow("!"), branch)
	} else {
		errs = append(errs, deleteRemote(ctx, f, c, branch))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("pull request !%d was abandoned, but %w", id, err)
	}
	return nil
}

// deleteLocal deletes every local branch whose upstream is the remote branch, plus a local branch
// of the same name (gh deletes that one), checking out the repository's default branch first
// when one of them is checked out.
func deleteLocal(ctx context.Context, f *cmdutil.Factory, c *api.PRClient, branch string) error {
	out, err := f.GitClient.Exec(ctx, "config", "--get-regexp", `^branch\..*\.merge$`)
	var gitErr *gitcmd.Error
	if errors.As(err, &gitErr) && gitErr.ExitCode == 1 {
		out, err = nil, nil // no branch has an upstream
	}
	if err != nil {
		return err
	}
	var locals []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, merge, _ := strings.Cut(line, " ")
		if merge == "refs/heads/"+branch {
			locals = append(locals, strings.TrimSuffix(strings.TrimPrefix(key, "branch."), ".merge"))
		}
	}
	if !slices.Contains(locals, branch) {
		if _, err := f.GitClient.Exec(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
			locals = append([]string{branch}, locals...)
		}
	}
	if len(locals) == 0 {
		return nil
	}
	current, err := f.GitClient.CurrentBranch(ctx)
	if err != nil && !errors.Is(err, gitcmd.ErrNotOnAnyBranch) {
		return err
	}
	ios := f.IOStreams
	for _, local := range locals {
		switched := ""
		if local == current {
			repo, err := c.Repository(ctx)
			if err != nil {
				return err
			}
			def := strings.TrimPrefix(shared.Deref(repo.DefaultBranch), "refs/heads/")
			if _, err := f.GitClient.Exec(ctx, "checkout", def); err != nil {
				return fmt.Errorf("cannot check out %s to delete local branch %s: %w", def, local, err)
			}
			switched = " and switched to branch " + def
		}
		if _, err := f.GitClient.Exec(ctx, "branch", "-D", local); err != nil {
			return fmt.Errorf("cannot delete local branch %s: %w", local, err)
		}
		if ios.IsStdoutTTY() {
			fmt.Fprintf(ios.ErrOut, "%s Deleted local branch %s%s\n", ios.ColorScheme().Green("✓"), local, switched)
		}
	}
	return nil
}

// deleteRemote deletes the source branch at its current tip; a branch already gone is reported.
func deleteRemote(ctx context.Context, f *cmdutil.Factory, c *api.PRClient, branch string) error {
	ref, err := c.Ref(ctx, branch)
	if err != nil {
		return fmt.Errorf("cannot delete branch %s: %w", branch, err)
	}
	if ref != nil {
		err = c.DeleteRef(ctx, branch, shared.Deref(ref.ObjectId))
	}
	ios := f.IOStreams
	switch {
	case ref == nil && err == nil, errors.Is(err, api.ErrRefNotFound):
		fmt.Fprintf(ios.ErrOut, "%s Remote branch %s was already deleted\n", ios.ColorScheme().Yellow("!"), branch)
		return nil
	case err != nil:
		return err
	}
	if ios.IsStdoutTTY() {
		fmt.Fprintf(ios.ErrOut, "%s Deleted remote branch %s\n", ios.ColorScheme().Green("✓"), branch)
	}
	return nil
}
