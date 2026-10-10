package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/policy"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
)

// prPageSize is the $top sent per pull request list page.
const prPageSize = 100

// HTTPError is an Azure DevOps API error returned by the PRClient methods.
type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.StatusCode)
}

// IsNotFound reports whether err is a 404 HTTPError.
func IsNotFound(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.StatusCode == http.StatusNotFound
}

// mapErr turns the SDK's error, which UnwrapError returns as WrappedError or *WrappedError,
// into *HTTPError; other errors pass through.
func mapErr(err error) error {
	var w *azuredevops.WrappedError
	if v := (azuredevops.WrappedError{}); errors.As(err, &v) {
		w = &v
	} else if !errors.As(err, &w) || w == nil {
		return err
	}
	he := &HTTPError{}
	if w.StatusCode != nil {
		he.StatusCode = *w.StatusCode
	}
	if w.Message != nil {
		he.Message = *w.Message
	}
	return he
}

// PRClient calls the Git pull request APIs of one repository.
type PRClient struct {
	Git     git.Client
	Policy  policy.Client
	Project string
	// Repo is the repository name or ID.
	Repo string
}

// NewPRClient returns a PRClient for project/repo in the organization at orgURL, sending
// requests through hc.
func NewPRClient(hc *http.Client, orgURL, project, repo string) *PRClient {
	sdk := NewSDKClient(hc, orgURL)
	return &PRClient{Git: &git.ClientImpl{Client: *sdk}, Policy: &policy.ClientImpl{Client: *sdk}, Project: project, Repo: repo}
}

// PullRequest fetches one pull request with its full description.
func (c *PRClient) PullRequest(ctx context.Context, id int) (*git.GitPullRequest, error) {
	pr, err := c.Git.GetPullRequestById(ctx, git.GetPullRequestByIdArgs{PullRequestId: &id, Project: &c.Project})
	return pr, mapErr(err)
}

// ListPullRequests returns up to limit pull requests matching criteria, paging with $top and
// $skip until limit or an empty page. draftOnly keeps only drafts (no server-side filter).
func (c *PRClient) ListPullRequests(ctx context.Context, criteria git.GitPullRequestSearchCriteria, limit int, draftOnly bool) ([]git.GitPullRequest, error) {
	var out []git.GitPullRequest
	for skip := 0; len(out) < limit; {
		top := prPageSize
		if !draftOnly {
			top = min(top, limit-len(out))
		}
		page, err := c.Git.GetPullRequests(ctx, git.GetPullRequestsArgs{
			RepositoryId: &c.Repo, Project: &c.Project, SearchCriteria: &criteria, Top: &top, Skip: &skip,
		})
		if err != nil {
			return nil, mapErr(err)
		}
		if page == nil || len(*page) == 0 {
			break
		}
		// $top is not a guaranteed page size, so step by what came back.
		skip += len(*page)
		for _, pr := range *page {
			if len(out) < limit && (!draftOnly || pr.IsDraft != nil && *pr.IsDraft) {
				out = append(out, pr)
			}
		}
	}
	return out, nil
}

// CreatePullRequest creates pr in the repository.
func (c *PRClient) CreatePullRequest(ctx context.Context, pr *git.GitPullRequest) (*git.GitPullRequest, error) {
	out, err := c.Git.CreatePullRequest(ctx, git.CreatePullRequestArgs{GitPullRequestToCreate: pr, RepositoryId: &c.Repo, Project: &c.Project})
	return out, mapErr(err)
}

// UpdatePullRequest patches pull request id with the non-nil fields of pr.
func (c *PRClient) UpdatePullRequest(ctx context.Context, id int, pr *git.GitPullRequest) (*git.GitPullRequest, error) {
	out, err := c.Git.UpdatePullRequest(ctx, git.UpdatePullRequestArgs{GitPullRequestToUpdate: pr, RepositoryId: &c.Repo, PullRequestId: &id, Project: &c.Project})
	return out, mapErr(err)
}

// Vote sets reviewerID's vote (10, 5, 0, -5, -10) on pull request id, adding them as a reviewer.
func (c *PRClient) Vote(ctx context.Context, id int, reviewerID string, vote int) (*git.IdentityRefWithVote, error) {
	out, err := c.Git.CreatePullRequestReviewer(ctx, git.CreatePullRequestReviewerArgs{
		Reviewer: &git.IdentityRefWithVote{Id: &reviewerID, Vote: &vote}, RepositoryId: &c.Repo, PullRequestId: &id, ReviewerId: &reviewerID, Project: &c.Project,
	})
	return out, mapErr(err)
}

// AddReviewers adds the identities ids as optional reviewers of pull request id.
func (c *PRClient) AddReviewers(ctx context.Context, id int, ids []string) ([]git.IdentityRefWithVote, error) {
	refs := make([]webapi.IdentityRef, len(ids))
	for i := range ids {
		refs[i].Id = &ids[i]
	}
	out, err := c.Git.CreatePullRequestReviewers(ctx, git.CreatePullRequestReviewersArgs{Reviewers: &refs, RepositoryId: &c.Repo, PullRequestId: &id, Project: &c.Project})
	if err != nil {
		return nil, mapErr(err)
	}
	return *out, nil
}

// CreateThread adds an active comment thread with one text comment to pull request id.
func (c *PRClient) CreateThread(ctx context.Context, id int, content string) (*git.GitPullRequestCommentThread, error) {
	thread := &git.GitPullRequestCommentThread{
		Comments: &[]git.Comment{{Content: &content, CommentType: &git.CommentTypeValues.Text}},
		Status:   &git.CommentThreadStatusValues.Active,
	}
	out, err := c.Git.CreateThread(ctx, git.CreateThreadArgs{CommentThread: thread, RepositoryId: &c.Repo, PullRequestId: &id, Project: &c.Project})
	return out, mapErr(err)
}

// Threads lists the comment threads of pull request id.
func (c *PRClient) Threads(ctx context.Context, id int) ([]git.GitPullRequestCommentThread, error) {
	out, err := c.Git.GetThreads(ctx, git.GetThreadsArgs{RepositoryId: &c.Repo, PullRequestId: &id, Project: &c.Project})
	if err != nil {
		return nil, mapErr(err)
	}
	return *out, nil
}

// Repository fetches the client's repository (e.g. for its default branch).
func (c *PRClient) Repository(ctx context.Context) (*git.GitRepository, error) {
	out, err := c.Git.GetRepository(ctx, git.GetRepositoryArgs{RepositoryId: &c.Repo, Project: &c.Project})
	return out, mapErr(err)
}

// RepositoryByID fetches any repository in the organization by ID, such as a PR's fork source.
func (c *PRClient) RepositoryByID(ctx context.Context, id string) (*git.GitRepository, error) {
	out, err := c.Git.GetRepository(ctx, git.GetRepositoryArgs{RepositoryId: &id})
	return out, mapErr(err)
}
