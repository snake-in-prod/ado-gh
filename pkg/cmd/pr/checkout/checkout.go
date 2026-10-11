// Package checkout implements `ado pr checkout`.
package checkout

import (
	"context"
	"fmt"
	"strings"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type options struct {
	branch string
	force  bool
}

// NewCmdCheckout returns the `pr checkout` command.
func NewCmdCheckout(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "checkout {<number> | <url> | <branch>}",
		Short: "Check out a pull request in git",
		Long: `Check out the source branch of a pull request as a local branch.

Azure DevOps has no pull request head ref, so the source branch itself is fetched: from the
local remote of the source repository, or by URL for a fork without one.`,
		Example: `  ado pr checkout 123
  ado pr checkout 123 --branch review/123
  ado pr checkout 123 --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.HasPrefix(o.branch, "-") {
				return cmdutil.FlagErrorf("invalid branch name %q", o.branch)
			}
			return run(cmd.Context(), f, args[0], o)
		},
	}
	cmd.Flags().StringVarP(&o.branch, "branch", "b", "", "Local branch name to use (default: the source branch name)")
	cmd.Flags().BoolVarP(&o.force, "force", "f", false, "Reset the existing local branch to the latest state of the pull request")
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, ref string, o *options) error {
	pr, c, repo, err := shared.Find(ctx, f, ref)
	if err != nil {
		return err
	}
	id := *pr.PullRequestId
	var source string
	if pr.SourceRefName != nil {
		source = *pr.SourceRefName
	}
	branch := strings.TrimPrefix(source, "refs/heads/")
	if branch == "" || strings.HasPrefix(branch, "-") {
		return fmt.Errorf("invalid source branch %q in pull request !%d", branch, id)
	}
	source = "refs/heads/" + branch

	// head is the repository holding the source branch.
	head, headURL, fork, err := shared.SourceRepo(ctx, c, repo, pr)
	if err != nil {
		return err
	}
	remotes, err := f.GitClient.Remotes(ctx)
	if err != nil {
		return err
	}
	remote := adocontext.MatchRemote(remotes, head)

	local := o.branch
	if local == "" {
		local = branch
		if fork && remote == "" {
			// Avoid naming the fork's branch like the local default branch (gh does the same).
			base, err := c.Repository(ctx)
			if err != nil {
				return err
			}
			if base.DefaultBranch != nil && *base.DefaultBranch == source {
				local = head.Name + "/" + branch
			}
		}
	}

	git := func(args ...string) bool {
		_, err := f.GitClient.Exec(ctx, args...)
		return err == nil
	}
	var cmds [][]string
	if remote != "" {
		tracking := "refs/remotes/" + remote + "/" + branch
		cmds = append(cmds, []string{"fetch", remote, "+" + source + ":" + tracking, "--no-tags"})
		switch {
		case !git("rev-parse", "--verify", "--quiet", "refs/heads/"+local):
			cmds = append(cmds, []string{"checkout", "-b", local, "--track", remote + "/" + branch})
		case o.force:
			cmds = append(cmds, []string{"checkout", local}, []string{"reset", "--hard", tracking})
		default:
			cmds = append(cmds, []string{"checkout", local}, []string{"merge", "--ff-only", tracking})
		}
	} else {
		// git refuses to fetch into the checked-out branch, so update it from FETCH_HEAD.
		current, _ := f.GitClient.CurrentBranch(ctx)
		switch {
		case local == current && o.force:
			cmds = append(cmds, []string{"fetch", headURL, source, "--no-tags"}, []string{"reset", "--hard", "FETCH_HEAD"})
		case local == current:
			cmds = append(cmds, []string{"fetch", headURL, source, "--no-tags"}, []string{"merge", "--ff-only", "FETCH_HEAD"})
		case o.force:
			cmds = append(cmds, []string{"fetch", headURL, source + ":refs/heads/" + local, "--no-tags", "--force"}, []string{"checkout", local})
		default:
			cmds = append(cmds, []string{"fetch", headURL, source + ":refs/heads/" + local, "--no-tags"}, []string{"checkout", local})
		}
		// .remote makes `git pull` work, .pushRemote `git push` even with remote.pushDefault set.
		if !git("config", "--get", "branch."+local+".merge") {
			cmds = append(cmds,
				[]string{"config", "branch." + local + ".remote", headURL},
				[]string{"config", "branch." + local + ".pushRemote", headURL},
				[]string{"config", "branch." + local + ".merge", source})
		}
	}

	for i, args := range cmds {
		if _, err := f.GitClient.Exec(ctx, args...); err != nil {
			if i == 0 && fork && remote == "" {
				return shared.ForkAccessError(id, err)
			}
			return err
		}
	}
	if f.IOStreams.IsStdoutTTY() {
		fmt.Fprintf(f.IOStreams.ErrOut, "Checked out branch %s for pull request !%d\n", local, id)
	}
	return nil
}
