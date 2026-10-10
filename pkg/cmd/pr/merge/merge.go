// Package merge implements `ado pr merge`.
package merge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// strategyFlags maps each strategy flag to the merge strategy it sends.
var strategyFlags = []struct {
	name, usage string
	strategy    git.GitPullRequestMergeStrategy
}{
	{"merge", "Merge with a merge commit (no fast-forward)", git.GitPullRequestMergeStrategyValues.NoFastForward},
	{"squash", "Squash the commits into one commit", git.GitPullRequestMergeStrategyValues.Squash},
	{"rebase", "Rebase the commits onto the target branch and fast-forward", git.GitPullRequestMergeStrategyValues.Rebase},
	{"rebase-merge", "Rebase the commits, then create a merge commit", git.GitPullRequestMergeStrategyValues.RebaseMerge},
}

type options struct {
	ref                 string
	strategy            *git.GitPullRequestMergeStrategy
	deleteBranch, auto  bool
	subject, body       string
	bypass, yes         bool
	bypassReason        string
	subjectSet, bodySet bool
}

// NewCmdMerge returns the `pr merge` command.
func NewCmdMerge(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	chosen := make([]bool, len(strategyFlags))
	cmd := &cobra.Command{
		Use:     "merge [<number> | <url> | <branch>]",
		Aliases: []string{"complete"},
		Short:   "Complete a pull request",
		Long: `Complete (merge) a pull request. Without an argument, the pull request of the current
branch is used. Without a strategy flag, the strategy already set on the pull request is
kept, else a merge commit is created.

When branch policies block completion, use --auto to complete the pull request once they
pass, or --bypass-policy with --bypass-reason if you are allowed to override them.`,
		Example: `  ado pr merge 123 --squash --delete-branch --yes
  ado pr merge --auto --rebase
  ado pr complete 123 --bypass-policy --bypass-reason "hotfix"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				o.ref = args[0]
			}
			for i, c := range chosen {
				if c {
					o.strategy = &strategyFlags[i].strategy
				}
			}
			if o.bypass && o.bypassReason == "" {
				return cmdutil.FlagErrorf("--bypass-reason is required with --bypass-policy")
			}
			if !o.bypass && o.bypassReason != "" {
				return cmdutil.FlagErrorf("--bypass-reason requires --bypass-policy")
			}
			if !o.yes && !f.IOStreams.CanPrompt() {
				return cmdutil.FlagErrorf("--yes required when not running interactively")
			}
			o.subjectSet, o.bodySet = cmd.Flags().Changed("subject"), cmd.Flags().Changed("body")
			return run(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	names := make([]string, len(strategyFlags))
	for i, s := range strategyFlags {
		fl.BoolVar(&chosen[i], s.name, false, s.usage)
		names[i] = s.name
	}
	cmd.MarkFlagsMutuallyExclusive(names...)
	fl.BoolVarP(&o.deleteBranch, "delete-branch", "d", false, "Delete the source branch after completing")
	fl.BoolVar(&o.auto, "auto", false, "Set auto-complete: complete once all required policies pass")
	fl.StringVarP(&o.subject, "subject", "t", "", "Subject of the merge commit")
	fl.StringVarP(&o.body, "body", "b", "", "Body of the merge commit")
	fl.BoolVar(&o.bypass, "bypass-policy", false, "Bypass branch policies (requires --bypass-reason)")
	fl.StringVar(&o.bypassReason, "bypass-reason", "", "Reason for bypassing branch policies")
	fl.BoolVarP(&o.yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	pr, c, repo, err := shared.Find(ctx, f, o.ref)
	if err != nil {
		return err
	}
	id, title := *pr.PullRequestId, shared.Deref(pr.Title)
	if s := shared.Deref(pr.Status); s != git.PullRequestStatusValues.Active {
		return fmt.Errorf("pull request !%d (%s) is %s", id, title, s)
	}

	// Carry over the completion options already on the pull request; SquashMerge is
	// deprecated, so always send an explicit MergeStrategy instead.
	opts := git.GitPullRequestCompletionOptions{}
	if pr.CompletionOptions != nil {
		opts = *pr.CompletionOptions
	}
	opts.SquashMerge = nil
	switch {
	case o.strategy != nil:
		opts.MergeStrategy = o.strategy
	case opts.MergeStrategy == nil:
		opts.MergeStrategy = &git.GitPullRequestMergeStrategyValues.NoFastForward
	}
	if o.deleteBranch {
		opts.DeleteSourceBranch = &o.deleteBranch
	}
	if o.subjectSet || o.bodySet {
		msg := o.subject
		if !o.subjectSet {
			msg = fmt.Sprintf("Merged PR %d: %s", id, title)
		}
		if o.body != "" {
			msg += "\n\n" + o.body
		}
		opts.MergeCommitMessage = &msg
	}
	if o.bypass {
		opts.BypassPolicy, opts.BypassReason = &o.bypass, &o.bypassReason
	}

	if !o.yes {
		q := fmt.Sprintf("Complete pull request !%d (%s) into %s with %s?",
			id, title, strings.TrimPrefix(shared.Deref(pr.TargetRefName), "refs/heads/"), *opts.MergeStrategy)
		if o.auto {
			q = fmt.Sprintf("Set auto-complete on pull request !%d (%s) with %s?", id, title, *opts.MergeStrategy)
		}
		ok, err := f.Prompter.Confirm(q, true)
		if err != nil {
			return err
		}
		if !ok {
			return cmdutil.CancelError
		}
	}

	update := &git.GitPullRequest{CompletionOptions: &opts}
	if o.auto {
		hc, err := authshared.HTTPClient(ctx, f, repo.Host, repo.Org)
		if err != nil {
			return err
		}
		r := &api.IdentityResolver{HTTP: hc, OrgURL: authshared.OrgURL(repo.Host, repo.Org)}
		me, err := r.Resolve(ctx, []string{"@me"})
		if err != nil {
			return err
		}
		meID := me[0].String()
		update.AutoCompleteSetBy = &webapi.IdentityRef{Id: &meID}
	} else {
		update.Status = &git.PullRequestStatusValues.Completed
		update.LastMergeSourceCommit = pr.LastMergeSourceCommit
	}

	if _, err := c.UpdatePullRequest(ctx, id, update); err != nil {
		var he *api.HTTPError
		// ponytail: the policy rejection is matched by its message; switch to the error's
		// typeKey if wording differs across server versions.
		if !o.auto && errors.As(err, &he) && strings.Contains(strings.ToLower(he.Message), "polic") {
			return fmt.Errorf("%w\nuse --auto to complete it when policies pass, or --bypass-policy --bypass-reason to override them", err)
		}
		return err
	}

	ios := f.IOStreams
	if ios.IsStdoutTTY() {
		check := ios.ColorScheme().Green("✓")
		if o.auto {
			fmt.Fprintf(ios.ErrOut, "%s Pull request !%d will be completed automatically when all policies pass\n", check, id)
		} else {
			fmt.Fprintf(ios.ErrOut, "%s Completed pull request !%d (%s)\n", check, id, title)
		}
	}
	return nil
}
