package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/policy"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/workitemtracking"
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
	Git    git.Client
	Policy policy.Client
	// Wit links work items, which have no pull request side write API.
	Wit     workitemtracking.Client
	Project string
	// Repo is the repository name or ID.
	Repo string
}

// NewPRClient returns a PRClient for project/repo in the organization at orgURL, sending
// requests through hc.
func NewPRClient(hc *http.Client, orgURL, project, repo string) *PRClient {
	sdk := NewSDKClient(hc, orgURL)
	return &PRClient{Git: &git.ClientImpl{Client: *sdk}, Policy: &policy.ClientImpl{Client: *sdk}, Wit: &workitemtracking.ClientImpl{Client: *sdk}, Project: project, Repo: repo}
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

// AddReviewer adds reviewerID to pull request id, as required when required is set; an
// existing optional reviewer is promoted. No vote is sent, so an existing vote isn't overwritten.
func (c *PRClient) AddReviewer(ctx context.Context, id int, reviewerID string, required bool) (*git.IdentityRefWithVote, error) {
	r := &git.IdentityRefWithVote{Id: &reviewerID}
	if required {
		r.IsRequired = &required
	}
	out, err := c.Git.CreatePullRequestReviewer(ctx, git.CreatePullRequestReviewerArgs{
		Reviewer: r, RepositoryId: &c.Repo, PullRequestId: &id, ReviewerId: &reviewerID, Project: &c.Project,
	})
	return out, mapErr(err)
}

// RemoveReviewer removes reviewerID (a user or team) from pull request id. Reviewers required
// by a branch policy can't be removed.
func (c *PRClient) RemoveReviewer(ctx context.Context, id int, reviewerID string) error {
	return mapErr(c.Git.DeletePullRequestReviewer(ctx, git.DeletePullRequestReviewerArgs{
		RepositoryId: &c.Repo, PullRequestId: &id, ReviewerId: &reviewerID, Project: &c.Project,
	}))
}

// CreateThread adds an active comment thread with one text comment to pull request id. tc and
// prc anchor it to lines of a file; with both nil it is a pull request level thread.
func (c *PRClient) CreateThread(ctx context.Context, id int, content string, tc *git.CommentThreadContext, prc *git.GitPullRequestCommentThreadContext) (*git.GitPullRequestCommentThread, error) {
	thread := &git.GitPullRequestCommentThread{
		Comments:                 &[]git.Comment{{Content: &content, CommentType: &git.CommentTypeValues.Text}},
		Status:                   &git.CommentThreadStatusValues.Active,
		ThreadContext:            tc,
		PullRequestThreadContext: prc,
	}
	out, err := c.Git.CreateThread(ctx, git.CreateThreadArgs{CommentThread: thread, RepositoryId: &c.Repo, PullRequestId: &id, Project: &c.Project})
	return out, mapErr(err)
}

// Thread fetches comment thread threadID of pull request id.
func (c *PRClient) Thread(ctx context.Context, id, threadID int) (*git.GitPullRequestCommentThread, error) {
	out, err := c.Git.GetPullRequestThread(ctx, git.GetPullRequestThreadArgs{RepositoryId: &c.Repo, PullRequestId: &id, ThreadId: &threadID, Project: &c.Project})
	return out, mapErr(err)
}

// UpdateThread sets the status of thread threadID of pull request id.
func (c *PRClient) UpdateThread(ctx context.Context, id, threadID int, status git.CommentThreadStatus) (*git.GitPullRequestCommentThread, error) {
	out, err := c.Git.UpdateThread(ctx, git.UpdateThreadArgs{
		CommentThread: &git.GitPullRequestCommentThread{Status: &status}, RepositoryId: &c.Repo, PullRequestId: &id, ThreadId: &threadID, Project: &c.Project,
	})
	return out, mapErr(err)
}

// CreateComment adds a text comment to thread threadID of pull request id, replying to comment
// parentID (0 = none).
func (c *PRClient) CreateComment(ctx context.Context, id, threadID, parentID int, content string) (*git.Comment, error) {
	comment := &git.Comment{Content: &content, CommentType: &git.CommentTypeValues.Text}
	if parentID != 0 {
		comment.ParentCommentId = &parentID
	}
	out, err := c.Git.CreateComment(ctx, git.CreateCommentArgs{Comment: comment, RepositoryId: &c.Repo, PullRequestId: &id, ThreadId: &threadID, Project: &c.Project})
	return out, mapErr(err)
}

// UpdateComment replaces the content of comment commentID in thread threadID of pull request id.
func (c *PRClient) UpdateComment(ctx context.Context, id, threadID, commentID int, content string) (*git.Comment, error) {
	out, err := c.Git.UpdateComment(ctx, git.UpdateCommentArgs{
		Comment: &git.Comment{Content: &content}, RepositoryId: &c.Repo, PullRequestId: &id, ThreadId: &threadID, CommentId: &commentID, Project: &c.Project,
	})
	return out, mapErr(err)
}

// DeleteComment deletes comment commentID in thread threadID of pull request id. Deletion is
// soft: the comment stays with isDeleted set and no content.
func (c *PRClient) DeleteComment(ctx context.Context, id, threadID, commentID int) error {
	return mapErr(c.Git.DeleteComment(ctx, git.DeleteCommentArgs{
		RepositoryId: &c.Repo, PullRequestId: &id, ThreadId: &threadID, CommentId: &commentID, Project: &c.Project,
	}))
}

// Threads lists the comment threads of pull request id.
func (c *PRClient) Threads(ctx context.Context, id int) ([]git.GitPullRequestCommentThread, error) {
	out, err := c.Git.GetThreads(ctx, git.GetThreadsArgs{RepositoryId: &c.Repo, PullRequestId: &id, Project: &c.Project})
	if err != nil {
		return nil, mapErr(err)
	}
	return *out, nil
}

// iterationChangesPage is the $top sent per iteration changes page (the API maximum).
const iterationChangesPage = 2000

// Iterations lists the iterations of pull request id; each push to the source branch adds one.
func (c *PRClient) Iterations(ctx context.Context, id int) ([]git.GitPullRequestIteration, error) {
	out, err := c.Git.GetPullRequestIterations(ctx, git.GetPullRequestIterationsArgs{RepositoryId: &c.Repo, PullRequestId: &id, Project: &c.Project})
	if err != nil {
		return nil, mapErr(err)
	}
	return *out, nil
}

// IterationChanges returns every file change of iteration iter of pull request id against the
// merge base (the web Files tab), following nextSkip until it is 0 or absent.
func (c *PRClient) IterationChanges(ctx context.Context, id, iter int) ([]git.GitPullRequestChange, error) {
	var all []git.GitPullRequestChange
	top := iterationChangesPage
	for skip := 0; ; {
		out, err := c.Git.GetPullRequestIterationChanges(ctx, git.GetPullRequestIterationChangesArgs{
			RepositoryId: &c.Repo, PullRequestId: &id, IterationId: &iter, Project: &c.Project, Top: &top, Skip: &skip,
		})
		if err != nil {
			return nil, mapErr(err)
		}
		if out.ChangeEntries != nil {
			all = append(all, *out.ChangeEntries...)
		}
		if out.NextSkip == nil || *out.NextSkip == 0 {
			return all, nil
		}
		skip = *out.NextSkip
	}
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

// ErrRefNotFound is returned by DeleteRef when the branch no longer exists.
var ErrRefNotFound = errors.New("branch not found")

// zeroObjectID is the nil commit a ref is updated to when it is deleted.
const zeroObjectID = "0000000000000000000000000000000000000000"

// refUpdateErrors are the messages of the failed ref update statuses DeleteRef maps.
var refUpdateErrors = map[git.GitRefUpdateStatus]string{
	git.GitRefUpdateStatusValues.WritePermissionRequired: "you need the Force push permission on it",
	git.GitRefUpdateStatusValues.ForcePushRequired:       "you need the Force push permission on it",
	git.GitRefUpdateStatusValues.Locked:                  "it is locked",
	git.GitRefUpdateStatusValues.RejectedByPolicy:        "rejected by a branch policy",
	git.GitRefUpdateStatusValues.StaleOldObjectId:        "it was updated since it was read; retry",
	git.GitRefUpdateStatusValues.InvalidRefName:          "invalid branch name",
}

// Ref returns branch (with or without "refs/heads/") in the client's repository, or nil when
// it doesn't exist.
func (c *PRClient) Ref(ctx context.Context, branch string) (*git.GitRef, error) {
	name := "refs/heads/" + strings.TrimPrefix(branch, "refs/heads/")
	// The filter is a starts-with match on the name without "refs/"; refs come back sorted, so
	// the exact name, the shortest match, is on the first page.
	filter := strings.TrimPrefix(name, "refs/")
	out, err := c.Git.GetRefs(ctx, git.GetRefsArgs{RepositoryId: &c.Repo, Project: &c.Project, Filter: &filter})
	if err != nil {
		return nil, mapErr(err)
	}
	for i := range out.Value {
		if r := &out.Value[i]; r.Name != nil && *r.Name == name {
			return r, nil
		}
	}
	return nil, nil
}

// DeleteRef deletes branch (with or without "refs/heads/") if it still points at oldObjectID.
// It returns ErrRefNotFound when the branch is already gone.
func (c *PRClient) DeleteRef(ctx context.Context, branch, oldObjectID string) error {
	branch = strings.TrimPrefix(branch, "refs/heads/")
	name, zero := "refs/heads/"+branch, zeroObjectID
	out, err := c.Git.UpdateRefs(ctx, git.UpdateRefsArgs{
		RefUpdates:   &[]git.GitRefUpdate{{Name: &name, OldObjectId: &oldObjectID, NewObjectId: &zero}},
		RepositoryId: &c.Repo, Project: &c.Project,
	})
	if err != nil {
		return fmt.Errorf("cannot delete branch %s: %w", branch, mapErr(err))
	}
	if len(*out) == 0 {
		return fmt.Errorf("cannot delete branch %s: empty response", branch)
	}
	res := (*out)[0]
	var status git.GitRefUpdateStatus
	if res.UpdateStatus != nil {
		status = *res.UpdateStatus
	}
	switch {
	case status == git.GitRefUpdateStatusValues.SucceededNonExistentRef:
		return ErrRefNotFound
	case res.Success != nil && *res.Success:
		return nil
	case refUpdateErrors[status] != "":
		return fmt.Errorf("cannot delete branch %s: %s", branch, refUpdateErrors[status])
	}
	msg := string(status)
	if status == git.GitRefUpdateStatusValues.RejectedByPlugin {
		msg = "rejected by plugin"
	}
	if res.CustomMessage != nil && *res.CustomMessage != "" {
		msg += ": " + *res.CustomMessage
	}
	return fmt.Errorf("cannot delete branch %s: %s", branch, msg)
}

// AddLabel adds the label name to pull request id, creating the tag when it doesn't exist.
func (c *PRClient) AddLabel(ctx context.Context, id int, name string) error {
	_, err := c.Git.CreatePullRequestLabel(ctx, git.CreatePullRequestLabelArgs{
		Label: &core.WebApiCreateTagRequestData{Name: &name}, RepositoryId: &c.Repo, PullRequestId: &id, Project: &c.Project,
	})
	return mapErr(err)
}

// RemoveLabel removes label (a name or ID) from pull request id; the tag itself is kept.
func (c *PRClient) RemoveLabel(ctx context.Context, id int, label string) error {
	return mapErr(c.Git.DeletePullRequestLabels(ctx, git.DeletePullRequestLabelsArgs{
		RepositoryId: &c.Repo, PullRequestId: &id, LabelIdOrName: &label, Project: &c.Project,
	}))
}

// WorkItems returns the IDs of the work items linked to pull request id.
func (c *PRClient) WorkItems(ctx context.Context, id int) ([]int, error) {
	refs, err := c.Git.GetPullRequestWorkItemRefs(ctx, git.GetPullRequestWorkItemRefsArgs{RepositoryId: &c.Repo, PullRequestId: &id, Project: &c.Project})
	if err != nil {
		return nil, mapErr(err)
	}
	var ids []int
	for _, r := range *refs {
		if r.Id == nil {
			continue
		}
		if n, err := strconv.Atoi(*r.Id); err == nil {
			ids = append(ids, n)
		}
	}
	return ids, nil
}

// PullRequestArtifactURL is the artifact URL work items link to for pull request id of the
// repository repoID in the project projectID (both GUIDs).
func PullRequestArtifactURL(projectID, repoID string, id int) string {
	return fmt.Sprintf("vstfs:///Git/PullRequestId/%s%%2F%s%%2F%d", projectID, repoID, id)
}

// LinkWorkItem adds an ArtifactLink from work item wi to artifactURL. An existing link is
// success.
func (c *PRClient) LinkWorkItem(ctx context.Context, wi int, artifactURL string) error {
	op, path := webapi.OperationValues.Add, "/relations/-"
	_, err := c.Wit.UpdateWorkItem(ctx, workitemtracking.UpdateWorkItemArgs{Id: &wi, Document: &[]webapi.JsonPatchOperation{{
		Op: &op, Path: &path,
		Value: map[string]any{"rel": "ArtifactLink", "url": artifactURL, "attributes": map[string]any{"name": "Pull Request"}},
	}}})
	if err = mapErr(err); err != nil && strings.Contains(err.Error(), "Relation already exists.") {
		return nil
	}
	return err
}

// UnlinkWorkItem removes the first link of work item wi whose URL matches artifactURL (case
// insensitively); its other links are kept. It returns false when there is no such link.
func (c *PRClient) UnlinkWorkItem(ctx context.Context, wi int, artifactURL string) (bool, error) {
	item, err := c.Wit.GetWorkItem(ctx, workitemtracking.GetWorkItemArgs{Id: &wi, Expand: &workitemtracking.WorkItemExpandValues.Relations})
	if err != nil {
		return false, mapErr(err)
	}
	var rels []workitemtracking.WorkItemRelation
	if item.Relations != nil {
		rels = *item.Relations
	}
	i := slices.IndexFunc(rels, func(r workitemtracking.WorkItemRelation) bool {
		return r.Url != nil && strings.EqualFold(*r.Url, artifactURL)
	})
	if i < 0 {
		return false, nil
	}
	// The rev test fails the patch if the work item changed since it was read.
	test, remove, revPath, relPath := webapi.OperationValues.Test, webapi.OperationValues.Remove, "/rev", fmt.Sprintf("/relations/%d", i)
	_, err = c.Wit.UpdateWorkItem(ctx, workitemtracking.UpdateWorkItemArgs{Id: &wi, Document: &[]webapi.JsonPatchOperation{
		{Op: &test, Path: &revPath, Value: item.Rev},
		{Op: &remove, Path: &relPath},
	}})
	if err != nil {
		return false, mapErr(err)
	}
	return true, nil
}
