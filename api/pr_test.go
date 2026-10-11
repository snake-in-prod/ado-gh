package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const prBase = "/proj/_apis/git/repositories/repo/pullRequests"

// newTestPRClient returns a PRClient for proj/repo in org; org must be unique per test
// because the SDK caches OPTIONS _apis per base URL for the whole process.
func newTestPRClient(t *testing.T, org string) (*PRClient, *httpmock.Registry) {
	t.Helper()
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.GitPRLocations...)
	t.Cleanup(func() { reg.Verify(t) })
	hc := NewHTTPClient(ClientOptions{Transport: reg})
	return NewPRClient(hc, "https://dev.azure.com/"+org, "proj", "repo"), reg
}

func prs(from, n int, draft bool) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = map[string]any{"pullRequestId": from + i, "isDraft": draft}
	}
	return out
}

func page(v []map[string]any) httpmock.Responder {
	return httpmock.JSONResponse(map[string]any{"count": len(v), "value": v})
}

func prIDs(list []git.GitPullRequest) []int {
	out := make([]int, len(list))
	for i, pr := range list {
		out[i] = *pr.PullRequestId
	}
	return out
}

func queries(reg *httpmock.Registry, key string) []string {
	var out []string
	for _, r := range reg.Requests {
		if r.Method == http.MethodGet {
			out = append(out, r.URL.Query().Get(key))
		}
	}
	return out
}

func TestListPullRequestsPaging(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		draftOnly bool
		pages     [][]map[string]any
		wantIDs   int
		wantTop   []string
		wantSkip  []string
	}{
		{
			name:    "stops at limit",
			limit:   30,
			pages:   [][]map[string]any{prs(1, 30, false)},
			wantIDs: 30, wantTop: []string{"30"}, wantSkip: []string{"0"},
		},
		{
			name:    "loops skip by returned count until limit",
			limit:   150,
			pages:   [][]map[string]any{prs(1, 100, false), prs(101, 50, false)},
			wantIDs: 150, wantTop: []string{"100", "50"}, wantSkip: []string{"0", "100"},
		},
		{
			name:    "short page does not end, empty page does",
			limit:   150,
			pages:   [][]map[string]any{prs(1, 60, false), prs(61, 10, false), nil},
			wantIDs: 70, wantTop: []string{"100", "90", "80"}, wantSkip: []string{"0", "60", "70"},
		},
		{
			name:      "draft filter is client-side and pages full",
			limit:     2,
			draftOnly: true,
			pages:     [][]map[string]any{append(prs(1, 2, false), prs(3, 1, true)...), append(prs(4, 1, false), prs(5, 2, true)...)},
			wantIDs:   2, wantTop: []string{"100", "100"}, wantSkip: []string{"0", "3"},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org := "listorg" + strconv.Itoa(i)
			c, reg := newTestPRClient(t, org)
			for _, p := range tt.pages {
				reg.Register(httpmock.REST(http.MethodGet, org+prBase), page(p))
			}
			status := git.PullRequestStatusValues.All
			ref := "refs/heads/main"
			got, err := c.ListPullRequests(context.Background(), git.GitPullRequestSearchCriteria{Status: &status, TargetRefName: &ref}, tt.limit, tt.draftOnly)
			require.NoError(t, err)
			assert.Len(t, got, tt.wantIDs)
			assert.Equal(t, tt.wantTop, queries(reg, "$top"))
			assert.Equal(t, tt.wantSkip, queries(reg, "$skip"))
			assert.Equal(t, []string{"all"}, queries(reg, "searchCriteria.status")[:1])
			assert.Equal(t, "refs/heads/main", queries(reg, "searchCriteria.targetRefName")[0])
			if tt.draftOnly {
				assert.Equal(t, []int{3, 5}, prIDs(got))
			}
		})
	}
}

func TestListPullRequestsError(t *testing.T) {
	org := "listerrorg"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+prBase),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401019: repo not found"}))
	_, err := c.ListPullRequests(context.Background(), git.GitPullRequestSearchCriteria{}, 10, false)
	var he *HTTPError
	require.ErrorAs(t, err, &he)
	assert.Equal(t, http.StatusNotFound, he.StatusCode)
	assert.Equal(t, "TF401019: repo not found (HTTP 404)", err.Error())
	assert.True(t, IsNotFound(err))
}

func TestPullRequestByID(t *testing.T) {
	org := "getorg"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/pullRequests/42"), httpmock.JSONResponse(map[string]any{
		"pullRequestId":         42,
		"description":           "full description, not truncated",
		"sourceRefName":         "refs/heads/feature",
		"lastMergeSourceCommit": map[string]any{"commitId": "abc123"},
		"forkSource":            map[string]any{"name": "refs/heads/feature", "repository": map[string]any{"id": "8b6f0a8e-0000-0000-0000-000000000002", "remoteUrl": "https://dev.azure.com/getorg/proj/_git/fork"}},
	}))
	pr, err := c.PullRequest(context.Background(), 42)
	require.NoError(t, err)
	assert.Equal(t, "refs/heads/feature", *pr.SourceRefName)
	assert.Equal(t, "abc123", *pr.LastMergeSourceCommit.CommitId)
	assert.Equal(t, "https://dev.azure.com/getorg/proj/_git/fork", *pr.ForkSource.Repository.RemoteUrl)
	assert.Equal(t, "8b6f0a8e-0000-0000-0000-000000000002", pr.ForkSource.Repository.Id.String())
}

func TestPullRequestNotFound(t *testing.T) {
	org := "get404org"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/pullRequests/7"), httpmock.StatusStringResponse(http.StatusNotFound, ""))
	_, err := c.PullRequest(context.Background(), 7)
	assert.True(t, IsNotFound(err), "err = %v (%T)", err, err)
}

func TestMapErr(t *testing.T) {
	status, msg := 403, "denied"
	plain := errors.New("boom")
	tests := []struct {
		name string
		in   error
		want error
	}{
		{"nil", nil, nil},
		{"value WrappedError", azuredevops.WrappedError{StatusCode: &status, Message: &msg}, &HTTPError{403, "denied"}},
		{"pointer WrappedError", &azuredevops.WrappedError{StatusCode: &status}, &HTTPError{StatusCode: 403}},
		{"nil pointer WrappedError", (*azuredevops.WrappedError)(nil), (*azuredevops.WrappedError)(nil)},
		{"other error", plain, plain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mapErr(tt.in))
		})
	}
	assert.Equal(t, "HTTP 403", (&HTTPError{StatusCode: 403}).Error())
	assert.False(t, IsNotFound(plain))
}

func jsonBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	var v map[string]any
	require.NoError(t, json.Unmarshal(b, &v))
	return v
}

func TestPRMutations(t *testing.T) {
	org := "mutorg"
	c, reg := newTestPRClient(t, org)
	var created, updated, voted, thread map[string]any
	var reviewers []map[string]any
	capture := func(dst *map[string]any, resp any) httpmock.Responder {
		return func(r *http.Request) (*http.Response, error) {
			*dst = jsonBody(t, r)
			return httpmock.JSONResponse(resp)(r)
		}
	}
	reg.Register(httpmock.REST(http.MethodPost, org+prBase), capture(&created, map[string]any{"pullRequestId": 9}))
	reg.Register(httpmock.REST(http.MethodPatch, org+prBase+"/9"), capture(&updated, map[string]any{"pullRequestId": 9, "status": "completed"}))
	reg.Register(httpmock.REST(http.MethodPut, org+prBase+"/9/reviewers/me-id"), capture(&voted, map[string]any{"id": "me-id", "vote": 0}))
	reg.Register(httpmock.REST(http.MethodPost, org+prBase+"/9/reviewers"), func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &reviewers))
		return httpmock.JSONResponse(map[string]any{"count": 2, "value": []map[string]any{{"id": "a"}, {"id": "b"}}})(r)
	})
	reg.Register(httpmock.REST(http.MethodPost, org+prBase+"/9/threads"), capture(&thread, map[string]any{"id": 1}))
	reg.Register(httpmock.REST(http.MethodGet, org+prBase+"/9/threads"), page([]map[string]any{{"id": 1}, {"id": 2}}))
	ctx := context.Background()

	title, src := "Add feature", "refs/heads/feature"
	pr, err := c.CreatePullRequest(ctx, &git.GitPullRequest{Title: &title, SourceRefName: &src})
	require.NoError(t, err)
	assert.Equal(t, 9, *pr.PullRequestId)
	assert.Equal(t, map[string]any{"title": "Add feature", "sourceRefName": "refs/heads/feature"}, created)

	completed := git.PullRequestStatusValues.Completed
	pr, err = c.UpdatePullRequest(ctx, 9, &git.GitPullRequest{Status: &completed})
	require.NoError(t, err)
	assert.Equal(t, completed, *pr.Status)
	assert.Equal(t, map[string]any{"status": "completed"}, updated)

	_, err = c.Vote(ctx, 9, "me-id", 0)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"id": "me-id", "vote": float64(0)}, voted, "a zero vote (reset) must be sent")

	added, err := c.AddReviewers(ctx, 9, []string{"a", "b"})
	require.NoError(t, err)
	assert.Len(t, added, 2)
	assert.Equal(t, []map[string]any{{"id": "a"}, {"id": "b"}}, reviewers)

	_, err = c.CreateThread(ctx, 9, "LGTM", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"comments": []any{map[string]any{"content": "LGTM", "commentType": "text"}},
		"status":   "active",
	}, thread)

	threads, err := c.Threads(ctx, 9)
	require.NoError(t, err)
	assert.Len(t, threads, 2)
}

func TestPRThreadsAndComments(t *testing.T) {
	org := "commentsorg"
	c, reg := newTestPRClient(t, org)
	bodies := map[string]map[string]any{}
	capture := func(key string, resp any) httpmock.Responder {
		return func(r *http.Request) (*http.Response, error) {
			bodies[key] = jsonBody(t, r)
			return httpmock.JSONResponse(resp)(r)
		}
	}
	reg.Register(httpmock.REST(http.MethodPost, org+prBase+"/9/threads"), capture("thread", map[string]any{"id": 4}))
	reg.Register(httpmock.REST(http.MethodGet, org+prBase+"/9/threads/4"), httpmock.JSONResponse(map[string]any{"id": 4, "status": "active"}))
	reg.Register(httpmock.REST(http.MethodPatch, org+prBase+"/9/threads/4"), capture("status", map[string]any{"id": 4, "status": "fixed"}))
	reg.Register(httpmock.REST(http.MethodPost, org+prBase+"/9/threads/4/comments"), capture("reply", map[string]any{"id": 2}))
	reg.Register(httpmock.REST(http.MethodPost, org+prBase+"/9/threads/4/comments"), capture("root", map[string]any{"id": 3}))
	reg.Register(httpmock.REST(http.MethodPatch, org+prBase+"/9/threads/4/comments/2"), capture("edit", map[string]any{"id": 2}))
	reg.Register(httpmock.REST(http.MethodDelete, org+prBase+"/9/threads/4/comments/2"), httpmock.StringResponse(""))
	reg.Register(httpmock.REST(http.MethodDelete, org+prBase+"/9/threads/4/comments/3"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "gone"}))
	ctx := context.Background()

	path := "/a.go"
	thread, err := c.CreateThread(ctx, 9, "nit", &git.CommentThreadContext{FilePath: &path}, &git.GitPullRequestCommentThreadContext{})
	require.NoError(t, err)
	assert.Equal(t, 4, *thread.Id)
	assert.Equal(t, map[string]any{"filePath": "/a.go"}, bodies["thread"]["threadContext"])
	assert.Equal(t, map[string]any{}, bodies["thread"]["pullRequestThreadContext"])

	thread, err = c.Thread(ctx, 9, 4)
	require.NoError(t, err)
	assert.Equal(t, 4, *thread.Id)

	thread, err = c.UpdateThread(ctx, 9, 4, git.CommentThreadStatusValues.Fixed)
	require.NoError(t, err)
	assert.Equal(t, git.CommentThreadStatusValues.Fixed, *thread.Status)
	assert.Equal(t, map[string]any{"status": "fixed"}, bodies["status"], "only the status is sent")

	comment, err := c.CreateComment(ctx, 9, 4, 1, "reply")
	require.NoError(t, err)
	assert.Equal(t, 2, *comment.Id)
	assert.Equal(t, map[string]any{"content": "reply", "commentType": "text", "parentCommentId": float64(1)}, bodies["reply"])
	_, err = c.CreateComment(ctx, 9, 4, 0, "root")
	require.NoError(t, err)
	assert.NotContains(t, bodies["root"], "parentCommentId")

	_, err = c.UpdateComment(ctx, 9, 4, 2, "edited")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"content": "edited"}, bodies["edit"])

	require.NoError(t, c.DeleteComment(ctx, 9, 4, 2))
	assert.True(t, IsNotFound(c.DeleteComment(ctx, 9, 4, 3)))
}

func TestPRWorkItems(t *testing.T) {
	org := "wiorg"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+prBase+"/9/workitems"),
		page([]map[string]any{{"id": "42"}, {"id": "x"}, {"id": "43"}}))
	reg.Register(httpmock.REST(http.MethodGet, org+prBase+"/8/workitems"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "gone"}))

	ids, err := c.WorkItems(context.Background(), 9)
	require.NoError(t, err)
	assert.Equal(t, []int{42, 43}, ids)
	_, err = c.WorkItems(context.Background(), 8)
	assert.Error(t, err)
}

func TestPRMutationErrors(t *testing.T) {
	org := "muterrorg"
	c, reg := newTestPRClient(t, org)
	for range 2 {
		reg.Register(httpmock.MatchAny, httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "bad"}))
	}
	ctx := context.Background()
	_, err := c.AddReviewers(ctx, 1, []string{"x"})
	assert.Equal(t, "bad (HTTP 400)", err.Error())
	_, err = c.Threads(ctx, 1)
	assert.Equal(t, "bad (HTTP 400)", err.Error())
}

func TestRepository(t *testing.T) {
	org := "repoorg"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/repo"),
		httpmock.JSONResponse(map[string]any{"name": "repo", "defaultBranch": "refs/heads/main"}))
	// By ID: no project segment, so forks in other projects resolve.
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/git/repositories/8b6f0a8e-0000-0000-0000-000000000002"),
		httpmock.JSONResponse(map[string]any{"name": "fork", "remoteUrl": "https://dev.azure.com/repoorg/other/_git/fork"}))
	ctx := context.Background()

	repo, err := c.Repository(ctx)
	require.NoError(t, err)
	assert.Equal(t, "refs/heads/main", *repo.DefaultBranch)

	fork, err := c.RepositoryByID(ctx, "8b6f0a8e-0000-0000-0000-000000000002")
	require.NoError(t, err)
	assert.Equal(t, "fork", *fork.Name)
}
