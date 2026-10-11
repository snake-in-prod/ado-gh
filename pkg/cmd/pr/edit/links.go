package edit

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// artifactURL is the URL work items link to for pr; it fails when the repository or project
// ID is missing from pr.
func artifactURL(pr *git.GitPullRequest) (string, error) {
	r := pr.Repository
	if r == nil || r.Id == nil || r.Project == nil || r.Project.Id == nil {
		return "", fmt.Errorf("cannot link work items: the repository and project IDs of pull request !%d are unknown", shared.Deref(pr.PullRequestId))
	}
	return api.PullRequestArtifactURL(r.Project.Id.String(), r.Id.String(), shared.Deref(pr.PullRequestId)), nil
}

// loadLabels fills pr.Labels from the labels endpoint unless it is already set: the pull
// request GET leaves labels out.
func loadLabels(ctx context.Context, c *api.PRClient, pr *git.GitPullRequest) error {
	if pr.Labels != nil {
		return nil
	}
	ls, err := c.ListLabels(ctx, shared.Deref(pr.PullRequestId))
	if err != nil {
		return err
	}
	pr.Labels = &ls
	return nil
}

// applyLinks links and unlinks work items (url is the PR's artifact URL) and adds and removes
// labels, one call each. Warnings go to stderr; it returns the applied and failed edits.
func applyLinks(ctx context.Context, ios *iostreams.IOStreams, c *api.PRClient, pr *git.GitPullRequest, o *options, url string) (applied, failed []string) {
	id := shared.Deref(pr.PullRequestId)
	warn := ios.ColorScheme().Yellow("!")

	for _, wi := range o.addWorkItems {
		if err := c.Wit.AddRelation(ctx, wi, "ArtifactLink", url, map[string]any{"name": "Pull Request"}); err != nil {
			failed = append(failed, fmt.Sprintf("failed to link work item #%d: %v", wi, err))
		} else {
			applied = append(applied, fmt.Sprintf("linked work item #%d", wi))
		}
	}
	for _, wi := range o.removeWorkItems {
		found, err := c.Wit.RemoveRelation(ctx, wi, "ArtifactLink", url)
		switch {
		case err != nil:
			failed = append(failed, fmt.Sprintf("failed to unlink work item #%d: %v", wi, err))
		case !found:
			fmt.Fprintf(ios.ErrOut, "%s work item #%d is not linked to pull request !%d\n", warn, wi, id)
		default:
			applied = append(applied, fmt.Sprintf("unlinked work item #%d", wi))
		}
	}

	// label returns the ID (else the name) of the PR's label name, or "" when it has none.
	// Tags are case-insensitive.
	label := func(name string) string {
		for _, l := range shared.Deref(pr.Labels) {
			if strings.EqualFold(shared.Deref(l.Name), name) {
				if l.Id != nil {
					return l.Id.String()
				}
				return *l.Name
			}
		}
		return ""
	}
	for _, name := range uniqueFold(o.addLabels) {
		if label(name) != "" {
			continue
		}
		if err := c.AddLabel(ctx, id, name); err != nil {
			failed = append(failed, fmt.Sprintf("failed to add label %s: %v", name, err))
		} else {
			applied = append(applied, "added label "+name)
		}
	}
	for _, name := range uniqueFold(o.removeLabels) {
		lid := label(name)
		if lid == "" {
			fmt.Fprintf(ios.ErrOut, "%s label %s not found on pull request !%d\n", warn, name, id)
			continue
		}
		if err := c.RemoveLabel(ctx, id, lid); err != nil {
			failed = append(failed, fmt.Sprintf("failed to remove label %s: %v", name, err))
		} else {
			applied = append(applied, "removed label "+name)
		}
	}
	return applied, failed
}

// uniqueFold returns names without case-insensitive repeats, keeping the first spelling:
// tags are case-insensitive, so a repeat would be a second call for the same label.
func uniqueFold(names []string) []string {
	var out []string
	for _, n := range names {
		if !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, n) }) {
			out = append(out, n)
		}
	}
	return out
}
