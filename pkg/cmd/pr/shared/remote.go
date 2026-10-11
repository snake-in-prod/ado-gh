package shared

import (
	"context"
	"fmt"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	gitcmd "github.com/snake-in-prod/ado-gh/internal/git"
)

// ForkAccessError wraps a failure to read the fork that pull request id comes from.
func ForkAccessError(id int, err error) error {
	return fmt.Errorf("cannot read the fork of pull request !%d; you need read access to the fork repository: %w", id, err)
}

// SourceRepo returns the repository holding pr's source branch and its clone URL: repo
// itself, or the fork pr comes from (fork true), looked up by ID when pr lacks its URL.
func SourceRepo(ctx context.Context, c *api.PRClient, repo adocontext.Repo, pr *git.GitPullRequest) (head adocontext.Repo, headURL string, fork bool, err error) {
	headURL = RepoURL(repo)
	if pr.ForkSource == nil || pr.ForkSource.Repository == nil {
		return repo, headURL, false, nil
	}
	fr := pr.ForkSource.Repository
	if fr.RemoteUrl == nil && fr.Id != nil {
		if fr, err = c.RepositoryByID(ctx, fr.Id.String()); err != nil {
			return head, "", true, ForkAccessError(*pr.PullRequestId, err)
		}
	}
	if fr.RemoteUrl != nil {
		headURL = *fr.RemoteUrl
	}
	// The URL is never echoed: it may carry userinfo. Parsing also rejects a "-" prefix.
	if head, err = adocontext.ParseRemoteURL(headURL); err != nil {
		return head, "", true, fmt.Errorf("pull request !%d has an unsupported fork URL", *pr.PullRequestId)
	}
	return head, headURL, true, nil
}

// MatchRemote returns the name of the first remote whose fetch URL points at r, or "".
func MatchRemote(remotes []gitcmd.Remote, r adocontext.Repo) string {
	for _, rm := range remotes {
		if rr, err := adocontext.ParseRemoteURL(rm.FetchURL); err == nil && strings.EqualFold(rr.Org, r.Org) &&
			strings.EqualFold(rr.Project, r.Project) && strings.EqualFold(rr.Name, r.Name) {
			return rm.Name
		}
	}
	return ""
}
