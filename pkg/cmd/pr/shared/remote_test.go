package shared

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceRepo(t *testing.T) {
	ctx := context.Background()
	repo := adocontext.Repo{Host: "dev.azure.com", Org: "srcorg", Project: "proj", Name: "web"}
	id := 7
	reg := &httpmock.Registry{}
	reg.StubLocations("srcorg", httpmock.GitPRLocations...)
	t.Cleanup(func() { reg.Verify(t) })
	c := api.NewPRClient(api.NewHTTPClient(api.ClientOptions{Transport: reg}), "https://dev.azure.com/srcorg", "proj", "web")

	head, u, fork, err := SourceRepo(ctx, c, repo, &git.GitPullRequest{PullRequestId: &id})
	require.NoError(t, err)
	assert.Equal(t, repo, head)
	assert.Equal(t, "https://dev.azure.com/srcorg/proj/_git/web", u)
	assert.False(t, fork)

	forkURL := "https://dev.azure.com/srcorg/other/_git/webfork"
	withFork := func(r git.GitRepository) *git.GitPullRequest {
		return &git.GitPullRequest{PullRequestId: &id, ForkSource: &git.GitForkRef{Repository: &r}}
	}
	head, u, fork, err = SourceRepo(ctx, c, repo, withFork(git.GitRepository{RemoteUrl: &forkURL}))
	require.NoError(t, err)
	assert.Equal(t, "webfork", head.Name)
	assert.Equal(t, forkURL, u)
	assert.True(t, fork)

	bad := "-x"
	_, _, _, err = SourceRepo(ctx, c, repo, withFork(git.GitRepository{RemoteUrl: &bad}))
	assert.EqualError(t, err, "pull request !7 has an unsupported fork URL")

	forkID := uuid.MustParse("8b6f0a8e-0000-0000-0000-000000000009")
	reg.Register(httpmock.REST(http.MethodGet, "srcorg/_apis/git/repositories/"+forkID.String()),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401019: not found"}))
	_, _, _, err = SourceRepo(ctx, c, repo, withFork(git.GitRepository{Id: &forkID}))
	assert.EqualError(t, err, "cannot read the fork of pull request !7; you need read access to the fork repository: TF401019: not found (HTTP 404)")
}
