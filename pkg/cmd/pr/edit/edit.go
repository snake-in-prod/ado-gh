// Package edit implements `ado pr edit`.
package edit

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// maxDescription is the longest description, in UTF-16 code units, Azure DevOps accepts.
const maxDescription = 4000

type options struct {
	ref                         string
	title, body, bodyFile, base string
	titleSet, bodySet, baseSet  bool

	addReviewers, requiredReviewers, removeReviewers []string
	addWorkItems, removeWorkItems                    []int
	addLabels, removeLabels                          []string

	// interactive is set when no edit flag is given on a terminal.
	interactive bool
	// pickedRemovals are the reviewers unselected in the interactive mode.
	pickedRemovals []identity
}

// hasListEdits reports whether any reviewer, work item or label flag is set.
func (o *options) hasListEdits() bool {
	return len(o.addReviewers)+len(o.requiredReviewers)+len(o.removeReviewers)+
		len(o.addWorkItems)+len(o.removeWorkItems)+len(o.addLabels)+len(o.removeLabels) > 0
}

// NewCmdEdit returns the `pr edit` command.
func NewCmdEdit(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "edit [<number> | <url> | <branch>]",
		Short: "Edit a pull request",
		Long: `Edit the title, description, target branch, reviewers, linked work items or labels
of a pull request. Without an argument, the pull request of the current branch is used.

Only fields that differ from the current values are sent. Changing the target branch
(--base) needs the pull request retargeting feature of Azure DevOps.

Reviewers are users (email, name or GUID) or teams ([Project]\Team). --add-required-reviewer
also promotes an existing optional reviewer. Reviewers required by a branch policy can't be
removed. --remove-work-item removes only the pull request link of the work item; its branch
and commit links stay. --add-label creates the tag when it doesn't exist yet.
When some edits fail, the others are still applied and the command exits 1.

Without edit flags on a terminal, you pick the fields to edit and change each one starting
from its current value.`,
		Example: `  ado pr edit 123 --title "Add login" --body-file notes.md
  ado pr edit --base develop
  ado pr edit 123 --add-reviewer alice@contoso.com,"[Web]\Reviewers" --remove-reviewer bob
  ado pr edit 123 --add-work-item 42,43 --add-label "needs docs" --remove-label wip`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				o.ref = args[0]
			}
			fl := cmd.Flags()
			o.titleSet, o.baseSet = fl.Changed("title"), fl.Changed("base")
			o.bodySet = fl.Changed("body") || fl.Changed("body-file")
			if fl.Changed("body-file") {
				var err error
				if o.body, err = shared.ReadBody(f.IOStreams.In, o.bodyFile); err != nil {
					return err
				}
			}
			if !o.titleSet && !o.bodySet && !o.baseSet && !o.hasListEdits() {
				if !f.IOStreams.CanPrompt() {
					return cmdutil.FlagErrorf("specify at least one of --title, --body, --body-file, --base, --add-reviewer, --add-required-reviewer, " +
						"--remove-reviewer, --add-work-item, --remove-work-item, --add-label or --remove-label")
				}
				o.interactive = true
			}
			return run(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&o.title, "title", "t", "", "Set the new title")
	fl.StringVarP(&o.body, "body", "b", "", "Set the new description")
	fl.StringVarP(&o.bodyFile, "body-file", "F", "", `Read the description from file (use "-" for stdin)`)
	fl.StringVarP(&o.base, "base", "B", "", "Change the target branch")
	fl.StringSliceVar(&o.addReviewers, "add-reviewer", nil, "Add optional reviewers (email, name, [Project]\\Team, GUID or @me)")
	fl.StringSliceVar(&o.requiredReviewers, "add-required-reviewer", nil, "Add required reviewers, or promote optional ones")
	fl.StringSliceVar(&o.removeReviewers, "remove-reviewer", nil, "Remove reviewers")
	fl.IntSliceVar(&o.addWorkItems, "add-work-item", nil, "Link work items by ID")
	fl.IntSliceVar(&o.removeWorkItems, "remove-work-item", nil, "Unlink work items by ID")
	fl.StringSliceVar(&o.addLabels, "add-label", nil, "Add labels")
	fl.StringSliceVar(&o.removeLabels, "remove-label", nil, "Remove labels")
	cmd.MarkFlagsMutuallyExclusive("body", "body-file")
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	if err := checkBody(o); err != nil {
		return err
	}
	pr, c, repo, err := shared.Find(ctx, f, o.ref)
	if err != nil {
		return err
	}
	if o.interactive {
		if err := promptEdits(ctx, f.Prompter, c, pr, o); err != nil {
			return err
		}
		if err := checkBody(o); err != nil {
			return err
		}
	}

	var reviewers reviewerEdits
	if len(o.addReviewers)+len(o.requiredReviewers)+len(o.removeReviewers)+len(o.pickedRemovals) > 0 {
		hc, err := authshared.HTTPClient(ctx, f, repo.Host, repo.Org)
		if err != nil {
			return err
		}
		resolver := &api.IdentityResolver{HTTP: hc, OrgURL: authshared.OrgURL(repo.Host, repo.Org), Prompter: f.Prompter}
		if reviewers, err = resolveReviewers(ctx, resolver, o); err != nil {
			return err
		}
		reviewers.remove = append(reviewers.remove, o.pickedRemovals...)
	}
	if len(o.addLabels)+len(o.removeLabels) > 0 {
		if err := loadLabels(ctx, c, pr); err != nil {
			return err
		}
	}
	var url string
	if len(o.addWorkItems)+len(o.removeWorkItems) > 0 {
		if url, err = artifactURL(pr); err != nil {
			return err
		}
	}

	update := &git.GitPullRequest{}
	var applied []string
	if o.titleSet && o.title != shared.Deref(pr.Title) {
		update.Title, applied = &o.title, append(applied, "title")
	}
	if o.bodySet && o.body != shared.Deref(pr.Description) {
		update.Description, applied = &o.body, append(applied, "description")
	}
	base := strings.TrimPrefix(o.base, "refs/heads/")
	target := "refs/heads/" + base
	if o.baseSet && target != shared.Deref(pr.TargetRefName) {
		// A fork's source branch lives in another repository, so the same name is a different branch.
		if pr.ForkSource == nil && target == shared.Deref(pr.SourceRefName) {
			return fmt.Errorf("the base branch cannot be the source branch %s", base)
		}
		ref, err := c.Ref(ctx, base)
		if err != nil {
			return err
		}
		if ref == nil {
			return fmt.Errorf("branch %s not found in %s/%s", base, repo.Project, repo.Name)
		}
		update.TargetRefName, applied = &target, append(applied, "base")
	}

	// A failed field update doesn't stop the reviewer, work item and label edits.
	var failed []string
	if len(applied) > 0 {
		out, err := c.UpdatePullRequest(ctx, shared.Deref(pr.PullRequestId), update)
		switch {
		case err != nil:
			failed, applied = []string{fmt.Sprintf("failed to update %s: %v", strings.Join(applied, ", "), err)}, nil
		// Without the retargeting feature the server may ignore targetRefName silently.
		case update.TargetRefName != nil && shared.Deref(out.TargetRefName) != target:
			failed = []string{fmt.Sprintf("base branch not changed to %s: pull request retargeting may be disabled in this organization", base)}
			applied = slices.DeleteFunc(applied, func(s string) bool { return s == "base" })
		}
	}
	done, revFailed := applyReviewers(ctx, f.IOStreams, c, pr, reviewers)
	applied, failed = append(applied, done...), append(failed, revFailed...)
	done, linkFailed := applyLinks(ctx, f.IOStreams, c, pr, o, url)
	applied, failed = append(applied, done...), append(failed, linkFailed...)
	fmt.Fprintln(f.IOStreams.Out, shared.PRURL(shared.RepoURL(repo), *pr))
	if len(failed) == 0 {
		return nil
	}
	msg := strings.Join(failed, "\n")
	if len(applied) > 0 {
		msg += "\napplied: " + strings.Join(applied, ", ")
	}
	return errors.New(msg)
}

// checkBody rejects a new description longer than Azure DevOps accepts.
func checkBody(o *options) error {
	if n := len(utf16.Encode([]rune(o.body))); o.bodySet && n > maxDescription {
		return fmt.Errorf("the description is %d characters long; the maximum is %d", n, maxDescription)
	}
	return nil
}
