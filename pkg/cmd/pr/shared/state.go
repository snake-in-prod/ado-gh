package shared

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// SetState applies update, a Status or IsDraft change, to pr. A completed pull request, or a
// draft change on one that isn't active, is an error. When pr is already in the target state
// it prints a warning to ios.ErrOut and returns false. Otherwise it posts comment as a new
// thread first (if not empty), then patches the pull request and returns true.
func SetState(ctx context.Context, ios *iostreams.IOStreams, c *api.PRClient, pr, update *git.GitPullRequest, comment string) (bool, error) {
	id, title, status := Deref(pr.PullRequestId), Deref(pr.Title), Deref(pr.Status)
	if status == git.PullRequestStatusValues.Completed || update.IsDraft != nil && status != git.PullRequestStatusValues.Active {
		return false, fmt.Errorf("pull request !%d (%s) is %s", id, title, status)
	}
	already := ""
	switch {
	case update.Status != nil && *update.Status == status:
		already = string(status)
	case update.IsDraft != nil && *update.IsDraft == Deref(pr.IsDraft):
		already = "ready for review"
		if *update.IsDraft {
			already = "a draft"
		}
	}
	if already != "" {
		fmt.Fprintf(ios.ErrOut, "%s Pull request !%d (%s) is already %s\n", ios.ColorScheme().Yellow("!"), id, title, already)
		return false, nil
	}
	if comment != "" {
		if _, err := c.CreateThread(ctx, id, comment, nil, nil); err != nil {
			return false, err
		}
	}
	if _, err := c.UpdatePullRequest(ctx, id, update); err != nil {
		return false, err
	}
	return true, nil
}

// Deref returns *p, or the zero value when p is nil.
func Deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
