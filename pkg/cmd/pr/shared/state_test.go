package shared

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetState(t *testing.T) {
	const prPath = "/proj/_apis/git/repositories/web/pullRequests/7"
	abandoned, active, completed := git.PullRequestStatusValues.Abandoned, git.PullRequestStatusValues.Active, git.PullRequestStatusValues.Completed
	yes, no := true, false
	tests := []struct {
		name        string
		status      git.PullRequestStatus
		draft       bool
		update      git.GitPullRequest
		comment     string
		wantPatch   map[string]any
		wantThread  bool
		wantChanged bool
		wantWarn    string
		wantErr     string
	}{
		{name: "abandon", status: active, update: git.GitPullRequest{Status: &abandoned},
			wantPatch: map[string]any{"status": "abandoned"}, wantChanged: true},
		{name: "abandon with comment", status: active, update: git.GitPullRequest{Status: &abandoned}, comment: "dup",
			wantThread: true, wantPatch: map[string]any{"status": "abandoned"}, wantChanged: true},
		{name: "reopen", status: abandoned, update: git.GitPullRequest{Status: &active},
			wantPatch: map[string]any{"status": "active"}, wantChanged: true},
		{name: "already abandoned", status: abandoned, update: git.GitPullRequest{Status: &abandoned}, comment: "dup",
			wantWarn: "! Pull request !7 (Fix) is already abandoned\n"},
		{name: "already active", status: active, update: git.GitPullRequest{Status: &active},
			wantWarn: "! Pull request !7 (Fix) is already active\n"},
		{name: "completed", status: completed, update: git.GitPullRequest{Status: &active},
			wantErr: "pull request !7 (Fix) is completed"},
		{name: "ready", status: active, draft: true, update: git.GitPullRequest{IsDraft: &no},
			wantPatch: map[string]any{"isDraft": false}, wantChanged: true},
		{name: "undo ready", status: active, update: git.GitPullRequest{IsDraft: &yes},
			wantPatch: map[string]any{"isDraft": true}, wantChanged: true},
		{name: "already ready", status: active, update: git.GitPullRequest{IsDraft: &no},
			wantWarn: "! Pull request !7 (Fix) is already ready for review\n"},
		{name: "already draft", status: active, draft: true, update: git.GitPullRequest{IsDraft: &yes},
			wantWarn: "! Pull request !7 (Fix) is already a draft\n"},
		{name: "ready on abandoned", status: abandoned, draft: true, update: git.GitPullRequest{IsDraft: &no},
			wantErr: "pull request !7 (Fix) is abandoned"},
		{name: "ready on completed", status: completed, update: git.GitPullRequest{IsDraft: &yes},
			wantErr: "pull request !7 (Fix) is completed"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org := "stateorg" + strconv.Itoa(i)
			reg := &httpmock.Registry{}
			if tt.wantPatch != nil {
				reg.StubLocations(org, httpmock.GitPRLocations...)
			}
			t.Cleanup(func() { reg.Verify(t) })
			var calls []string
			var patch, thread map[string]any
			readBody := func(r *http.Request, dst *map[string]any) {
				b, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(b, dst))
			}
			if tt.wantThread {
				reg.Register(httpmock.REST(http.MethodPost, org+prPath+"/threads"), func(r *http.Request) (*http.Response, error) {
					calls = append(calls, "thread")
					readBody(r, &thread)
					return httpmock.JSONResponse(map[string]any{"id": 1})(r)
				})
			}
			if tt.wantPatch != nil {
				reg.Register(httpmock.REST(http.MethodPatch, org+prPath), func(r *http.Request) (*http.Response, error) {
					calls = append(calls, "patch")
					readBody(r, &patch)
					return httpmock.JSONResponse(map[string]any{"pullRequestId": 7})(r)
				})
			}
			c := api.NewPRClient(&http.Client{Transport: reg}, "https://dev.azure.com/"+org, "proj", "web")
			ios, _, _, stderr := iostreams.Test()
			id, title := 7, "Fix"
			pr := &git.GitPullRequest{PullRequestId: &id, Title: &title, Status: &tt.status, IsDraft: &tt.draft}

			changed, err := SetState(context.Background(), ios, c, pr, &tt.update, tt.comment)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				assert.Empty(t, reg.Requests)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantChanged, changed)
			assert.Equal(t, tt.wantWarn, stderr.String())
			assert.Equal(t, tt.wantPatch, patch)
			if tt.wantThread {
				assert.Equal(t, []string{"thread", "patch"}, calls, "the comment is posted before the state change")
				assert.Equal(t, "dup", thread["comments"].([]any)[0].(map[string]any)["content"])
			}
		})
	}
}

func TestSetStateCommentError(t *testing.T) {
	org := "stateerrorg"
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.GitPRLocations...)
	reg.Register(httpmock.REST(http.MethodPost, org+"/proj/_apis/git/repositories/web/pullRequests/7/threads"),
		httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))
	c := api.NewPRClient(&http.Client{Transport: reg}, "https://dev.azure.com/"+org, "proj", "web")
	ios, _, _, _ := iostreams.Test()
	id, active, abandoned := 7, git.PullRequestStatusValues.Active, git.PullRequestStatusValues.Abandoned
	pr := &git.GitPullRequest{PullRequestId: &id, Status: &active}
	changed, err := SetState(context.Background(), ios, c, pr, &git.GitPullRequest{Status: &abandoned}, "dup")
	require.EqualError(t, err, "denied (HTTP 403)")
	assert.False(t, changed)
	reg.Verify(t)
}

func TestDeref(t *testing.T) {
	s := "x"
	assert.Equal(t, "x", Deref(&s))
	assert.Equal(t, "", Deref[string](nil))
}
