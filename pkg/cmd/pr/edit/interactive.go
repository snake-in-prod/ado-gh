package edit

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// editFields are the fields the interactive mode offers, in prompt order.
var editFields = []string{"Title", "Description", "Base branch", "Reviewers", "Work items", "Labels"}

// promptEdits asks which fields to edit, then for each one its new value prefilled with the
// current one, and records the answers in o like the matching flags.
func promptEdits(ctx context.Context, p *cmdutil.Prompter, c *api.PRClient, pr *git.GitPullRequest, o *options) error {
	picked, err := p.MultiSelect("What would you like to edit?", nil, editFields)
	if err != nil {
		return err
	}
	if len(picked) == 0 {
		return errors.New("nothing to edit")
	}
	for _, i := range picked {
		switch editFields[i] {
		case "Title":
			o.titleSet = true
			o.title, err = p.Input("Title", shared.Deref(pr.Title))
		case "Description":
			o.bodySet = true
			o.body, err = p.Text("Description", shared.Deref(pr.Description))
		case "Base branch":
			o.baseSet = true
			o.base, err = p.Input("Base branch", strings.TrimPrefix(shared.Deref(pr.TargetRefName), "refs/heads/"))
		case "Reviewers":
			err = promptReviewers(p, pr, o)
		case "Work items":
			err = promptWorkItems(ctx, p, c, pr, o)
		case "Labels":
			err = promptLabels(ctx, p, c, pr, o)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func promptReviewers(p *cmdutil.Prompter, pr *git.GitPullRequest, o *options) error {
	revs := shared.Deref(pr.Reviewers)
	names := make([]string, len(revs))
	for i, r := range revs {
		names[i] = cmp.Or(shared.Deref(r.DisplayName), shared.Deref(r.UniqueName), shared.Deref(r.Id))
	}
	removed, added, err := keepAndAdd(p, "reviewers", names)
	for _, i := range removed {
		o.pickedRemovals = append(o.pickedRemovals, identity{names[i], shared.Deref(revs[i].Id)})
	}
	o.addReviewers = append(o.addReviewers, added...)
	return err
}

func promptWorkItems(ctx context.Context, p *cmdutil.Prompter, c *api.PRClient, pr *git.GitPullRequest, o *options) error {
	ids, err := c.WorkItems(ctx, shared.Deref(pr.PullRequestId))
	if err != nil {
		return err
	}
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = fmt.Sprintf("#%d", id)
	}
	removed, added, err := keepAndAdd(p, "work items", names)
	if err != nil {
		return err
	}
	for _, i := range removed {
		o.removeWorkItems = append(o.removeWorkItems, ids[i])
	}
	for _, v := range added {
		id, err := strconv.Atoi(strings.TrimPrefix(v, "#"))
		if err != nil {
			return fmt.Errorf("invalid work item ID %q", v)
		}
		o.addWorkItems = append(o.addWorkItems, id)
	}
	return nil
}

func promptLabels(ctx context.Context, p *cmdutil.Prompter, c *api.PRClient, pr *git.GitPullRequest, o *options) error {
	if err := loadLabels(ctx, c, pr); err != nil {
		return err
	}
	var names []string
	for _, l := range shared.Deref(pr.Labels) {
		names = append(names, shared.Deref(l.Name))
	}
	removed, added, err := keepAndAdd(p, "labels", names)
	for _, i := range removed {
		o.removeLabels = append(o.removeLabels, names[i])
	}
	o.addLabels = append(o.addLabels, added...)
	return err
}

// keepAndAdd lists the current values, all selected, and asks for comma-separated values to
// add. It returns the indexes of the unselected values and the values to add.
func keepAndAdd(p *cmdutil.Prompter, what string, current []string) (removed []int, added []string, err error) {
	if len(current) > 0 {
		kept, err := p.MultiSelect("Current "+what+" (unselect to remove)", current, current)
		if err != nil {
			return nil, nil, err
		}
		for i := range current {
			if !slices.Contains(kept, i) {
				removed = append(removed, i)
			}
		}
	}
	in, err := p.Input("Add "+what+" (comma-separated)", "")
	if err != nil {
		return nil, nil, err
	}
	for v := range strings.SplitSeq(in, ",") {
		if v = strings.TrimSpace(v); v != "" {
			added = append(added, v)
		}
	}
	return removed, added, nil
}
