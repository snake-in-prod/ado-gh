// Package shared holds code used by several `ado pr` commands.
package shared

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	gitcmd "github.com/snake-in-prod/ado-gh/internal/git"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// branchLookupLimit caps the pull requests fetched when resolving a branch name.
const branchLookupLimit = 30

// NewClient returns a pull request client for repo, authenticated for its organization.
func NewClient(ctx context.Context, f *cmdutil.Factory, repo adocontext.Repo) (*api.PRClient, error) {
	hc, err := authshared.HTTPClient(ctx, f, repo.Host, repo.Org)
	if err != nil {
		return nil, err
	}
	return api.NewPRClient(hc, authshared.OrgURL(repo.Host, repo.Org), repo.Project, repo.Name), nil
}

// Find resolves ref to a pull request: a number, "!123", a pull request URL, a branch name,
// or "" for the current branch. Numbers and branches are looked up in the base repository,
// a URL in the repository it names. It returns the client and repository used.
func Find(ctx context.Context, f *cmdutil.Factory, ref string) (*git.GitPullRequest, *api.PRClient, adocontext.Repo, error) {
	id, repo, err := parseRef(ref)
	if err != nil {
		return nil, nil, repo, err
	}
	if repo.Name == "" {
		if repo, err = f.BaseRepo(); err != nil {
			return nil, nil, repo, err
		}
	}
	c, err := NewClient(ctx, f, repo)
	if err != nil {
		return nil, nil, repo, err
	}
	if id > 0 {
		pr, err := c.PullRequest(ctx, id)
		// Pull request IDs are unique per organization; a PR of another repository is "not found" here.
		if api.IsNotFound(err) || err == nil && pr.Repository != nil && pr.Repository.Name != nil && !strings.EqualFold(*pr.Repository.Name, repo.Name) {
			return nil, nil, repo, fmt.Errorf("pull request !%d not found in %s/%s", id, repo.Project, repo.Name)
		}
		return pr, c, repo, err
	}

	branch := ref
	if branch == "" {
		if branch, err = f.GitClient.CurrentBranch(ctx); errors.Is(err, gitcmd.ErrNotOnAnyBranch) {
			return nil, nil, repo, errors.New("not on any branch; pass a pull request number, URL or branch name")
		} else if err != nil {
			return nil, nil, repo, err
		}
	}
	pr, err := findForBranch(ctx, c, branch)
	return pr, c, repo, err
}

// findForBranch returns the newest active pull request from branch, else its newest one.
func findForBranch(ctx context.Context, c *api.PRClient, branch string) (*git.GitPullRequest, error) {
	all, src := git.PullRequestStatusValues.All, "refs/heads/"+branch
	prs, err := c.ListPullRequests(ctx, git.GitPullRequestSearchCriteria{Status: &all, SourceRefName: &src}, branchLookupLimit, false)
	if err != nil {
		return nil, err
	}
	if len(prs) == 0 {
		return nil, fmt.Errorf("no pull request found for branch %q in %s/%s", branch, c.Project, c.Repo)
	}
	for i := range prs {
		if prs[i].Status != nil && *prs[i].Status == git.PullRequestStatusValues.Active {
			return &prs[i], nil
		}
	}
	return &prs[0], nil
}

// parseRef returns the pull request number of ref, and the repository for a URL ref.
// A ref that is neither (id 0, empty repo) is a branch name.
func parseRef(ref string) (int, adocontext.Repo, error) {
	if strings.Contains(ref, "://") {
		return parseURL(ref)
	}
	s, bang := strings.CutPrefix(ref, "!")
	n, err := strconv.Atoi(s)
	switch {
	case err == nil && n > 0:
		return n, adocontext.Repo{}, nil
	case bang || err == nil:
		return 0, adocontext.Repo{}, fmt.Errorf("invalid pull request number %q", ref)
	}
	return 0, adocontext.Repo{}, nil
}

// parseURL parses https://{host}/{org}/{project}/_git/{repo}/pullrequest/{id} and the
// other repository URL forms ParseRemoteURL accepts; query and fragment are ignored.
func parseURL(ref string) (int, adocontext.Repo, error) {
	// ref is never echoed: its userinfo may hold a PAT.
	invalid := errors.New("invalid pull request URL: expected https://dev.azure.com/ORG/PROJECT/_git/REPO/pullrequest/ID")
	u, err := url.Parse(ref)
	if err != nil || u.Scheme != "https" {
		return 0, adocontext.Repo{}, invalid
	}
	segs := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	i := len(segs) - 2
	if i < 1 || !strings.EqualFold(segs[i], "pullrequest") {
		return 0, adocontext.Repo{}, invalid
	}
	n, err := strconv.Atoi(segs[i+1])
	if err != nil || n <= 0 {
		return 0, adocontext.Repo{}, invalid
	}
	repo, err := adocontext.ParseRemoteURL("https://" + u.Host + "/" + strings.Join(segs[:i], "/"))
	if err != nil {
		return 0, adocontext.Repo{}, invalid
	}
	return n, repo, nil
}
