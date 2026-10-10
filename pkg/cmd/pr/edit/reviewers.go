package edit

import (
	"context"
	"fmt"
	"slices"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// identity is a resolved reviewer argument: what the user typed and its identity id.
type identity struct{ name, id string }

// reviewerEdits are the resolved --add-reviewer, --add-required-reviewer and --remove-reviewer values.
type reviewerEdits struct{ add, required, remove []identity }

// resolveReviewers resolves every reviewer flag before anything is changed, so an unknown
// identity fails the whole edit.
func resolveReviewers(ctx context.Context, r *api.IdentityResolver, o *options) (e reviewerEdits, err error) {
	if e.add, err = resolveIdentities(ctx, r, o.addReviewers); err != nil {
		return e, err
	}
	if e.required, err = resolveIdentities(ctx, r, o.requiredReviewers); err != nil {
		return e, err
	}
	e.remove, err = resolveIdentities(ctx, r, o.removeReviewers)
	return e, err
}

func resolveIdentities(ctx context.Context, r *api.IdentityResolver, values []string) ([]identity, error) {
	var out []identity
	for _, v := range values {
		ids, err := r.Resolve(ctx, []string{v})
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if !slices.ContainsFunc(out, func(i identity) bool { return i.id == id.String() }) {
				out = append(out, identity{v, id.String()})
			}
		}
	}
	return out, nil
}

// applyReviewers adds, promotes and removes reviewers one call each, so one failure doesn't
// stop the others. Warnings go to stderr; it returns the applied and failed edits.
func applyReviewers(ctx context.Context, ios *iostreams.IOStreams, c *api.PRClient, pr *git.GitPullRequest, e reviewerEdits) (applied, failed []string) {
	id := shared.Deref(pr.PullRequestId)
	current := map[string]git.IdentityRefWithVote{}
	for _, r := range shared.Deref(pr.Reviewers) {
		current[shared.Deref(r.Id)] = r
	}
	warn := ios.ColorScheme().Yellow("!")

	for _, r := range e.add {
		if _, ok := current[r.id]; ok {
			continue
		}
		if _, err := c.AddReviewer(ctx, id, r.id, false); err != nil {
			failed = append(failed, fmt.Sprintf("failed to add reviewer %s: %v", r.name, err))
		} else {
			applied = append(applied, "added reviewer "+r.name)
		}
	}
	for _, r := range e.required {
		old, existing := current[r.id]
		if shared.Deref(old.IsRequired) {
			continue
		}
		out, err := c.AddReviewer(ctx, id, r.id, true)
		switch {
		case err != nil:
			failed = append(failed, fmt.Sprintf("failed to add required reviewer %s: %v", r.name, err))
		case existing:
			// Whether the server keeps the vote on promotion is undocumented.
			if shared.Deref(old.Vote) != 0 && shared.Deref(out.Vote) == 0 {
				fmt.Fprintf(ios.ErrOut, "%s Promoting %s to required reset their vote\n", warn, r.name)
			}
			applied = append(applied, "promoted "+r.name+" to required")
		default:
			applied = append(applied, "added required reviewer "+r.name)
		}
	}
	for _, r := range e.remove {
		if _, ok := current[r.id]; !ok {
			fmt.Fprintf(ios.ErrOut, "%s %s is not a reviewer of pull request !%d\n", warn, r.name, id)
			continue
		}
		// Reviewers required by a branch policy can't be removed; the server refuses.
		if err := c.RemoveReviewer(ctx, id, r.id); err != nil {
			failed = append(failed, fmt.Sprintf("failed to remove reviewer %s: %v", r.name, err))
		} else {
			applied = append(applied, "removed reviewer "+r.name)
		}
	}
	return applied, failed
}
