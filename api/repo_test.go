package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	repoTestID       = "8b6f0a8e-0000-0000-0000-000000000001"
	repoTestParentID = "8b6f0a8e-0000-0000-0000-000000000002"
)

func newTestRepoClient(t *testing.T, org, project string) (*RepoClient, *httpmock.Registry) {
	t.Helper()
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.GitRepoLocations...)
	t.Cleanup(func() { reg.Verify(t) })
	return NewRepoClient(NewHTTPClient(ClientOptions{Transport: reg}), "https://dev.azure.com/"+org, project), reg
}

// repoCapture responds with resp after appending the request's JSON body to bodies.
func repoCapture(t *testing.T, bodies *[]map[string]any, resp httpmock.Responder) httpmock.Responder {
	return func(r *http.Request) (*http.Response, error) {
		*bodies = append(*bodies, jsonBody(t, r))
		return resp(r)
	}
}

func TestListRepositories(t *testing.T) {
	full := map[string]any{
		"id": repoTestID, "name": "api", "project": map[string]any{"name": "proj"},
		"defaultBranch": "refs/heads/main", "size": 2048, "isDisabled": true, "isFork": true,
		"remoteUrl": "https://o@dev.azure.com/o/proj/_git/api", "sshUrl": "git@ssh.dev.azure.com:v3/o/proj/api",
		"webUrl": "https://dev.azure.com/o/proj/_git/api",
	}
	empty := map[string]any{"name": "empty", "isFork": nil, "defaultBranch": nil}
	want := []Repository{
		{ID: repoTestID, Name: "api", Project: "proj", DefaultBranch: "main", Size: 2048, IsDisabled: true, IsFork: true,
			RemoteURL: "https://o@dev.azure.com/o/proj/_git/api", SSHURL: "git@ssh.dev.azure.com:v3/o/proj/api",
			WebURL: "https://dev.azure.com/o/proj/_git/api"},
		{Name: "empty"},
	}

	t.Run("project", func(t *testing.T) {
		org := "repo-list-proj"
		c, reg := newTestRepoClient(t, org, "proj")
		reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories"),
			httpmock.JSONResponse(map[string]any{"count": 2, "value": []any{full, empty}}))
		got, err := c.ListRepositories(context.Background())
		require.NoError(t, err)
		assert.Equal(t, want, got)
		q := reg.Requests[1].URL.Query()
		assert.Empty(t, q.Get("includeHidden"))
		assert.Empty(t, q.Get("includeLinks"))
	})

	t.Run("org-wide has no project segment", func(t *testing.T) {
		org := "repo-list-org"
		c, reg := newTestRepoClient(t, org, "")
		reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/git/repositories"),
			httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))
		got, err := c.ListRepositories(context.Background())
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("error", func(t *testing.T) {
		org := "repo-list-err"
		c, reg := newTestRepoClient(t, org, "proj")
		reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories"),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "no project"}))
		_, err := c.ListRepositories(context.Background())
		assert.True(t, IsNotFound(err))
	})
}

func TestRepositoryWithParent(t *testing.T) {
	org := "repo-get"
	c, reg := newTestRepoClient(t, org, "proj")
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/api"),
		httpmock.JSONResponse(map[string]any{
			"id": repoTestID, "name": "api", "isFork": true, "defaultBranch": "develop",
			"parentRepository": map[string]any{
				"id": repoTestParentID, "name": "upstream", "project": map[string]any{"name": "other"},
				"remoteUrl": "https://dev.azure.com/o/other/_git/upstream", "sshUrl": "git@ssh.dev.azure.com:v3/o/other/upstream",
			},
		}))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/gone"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "gone"}))

	got, err := c.Repository(context.Background(), "api")
	require.NoError(t, err)
	assert.Equal(t, "true", reg.Requests[1].URL.Query().Get("includeParent"))
	assert.Equal(t, Repository{ID: repoTestID, Name: "api", DefaultBranch: "develop", IsFork: true,
		ParentRepository: &ParentRepository{ID: repoTestParentID, Name: "upstream", Project: "other",
			RemoteURL: "https://dev.azure.com/o/other/_git/upstream", SSHURL: "git@ssh.dev.azure.com:v3/o/other/upstream"},
	}, got)

	_, err = c.Repository(context.Background(), "gone")
	assert.True(t, IsNotFound(err))
}

func TestRootItems(t *testing.T) {
	org := "repo-items"
	c, reg := newTestRepoClient(t, org, "proj")
	items := httpmock.JSONResponse(map[string]any{"count": 3, "value": []any{
		map[string]any{"path": "/", "isFolder": true, "objectId": "root"},
		map[string]any{"path": "/README.md", "objectId": "b1"},
		map[string]any{"path": "/docs", "isFolder": true, "objectId": "t1"},
	}})
	path := org + "/proj/_apis/git/repositories/api/items"
	reg.Register(httpmock.REST(http.MethodGet, path), items)
	reg.Register(httpmock.REST(http.MethodGet, path), items)
	reg.Register(httpmock.REST(http.MethodGet, path), httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "no branch"}))
	ctx := context.Background()
	want := []TreeEntry{{Name: "README.md", ObjectID: "b1"}, {Name: "docs", ObjectID: "t1", IsFolder: true}}

	got, err := c.RootItems(ctx, "api", "")
	require.NoError(t, err)
	assert.Equal(t, want, got)
	q := reg.Requests[1].URL.Query()
	assert.Equal(t, "/", q.Get("scopePath"))
	assert.Equal(t, "oneLevel", q.Get("recursionLevel"))
	assert.False(t, q.Has("versionDescriptor.version"))
	assert.False(t, q.Has("versionDescriptor.versionType"))

	got, err = c.RootItems(ctx, "api", "refs/heads/feature/x")
	require.NoError(t, err)
	assert.Equal(t, want, got)
	q = reg.Requests[2].URL.Query()
	assert.Equal(t, "feature/x", q.Get("versionDescriptor.version"))
	assert.Equal(t, "branch", q.Get("versionDescriptor.versionType"))

	_, err = c.RootItems(ctx, "api", "nope")
	assert.True(t, IsNotFound(err))
}

func TestRepoBlobContent(t *testing.T) {
	org := "repo-blob"
	c, reg := newTestRepoClient(t, org, "proj")
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/api/blobs/b1"), httpmock.StringResponse("# hi\n"))
	data, err := c.BlobContent(context.Background(), "api", "b1")
	require.NoError(t, err)
	assert.Equal(t, "# hi\n", string(data))
}

func TestGitignoreTemplates(t *testing.T) {
	org := "repo-templates"
	c, reg := newTestRepoClient(t, org, "proj")
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/templates"),
		httpmock.JSONResponse(map[string]any{"count": 2, "value": []any{
			map[string]any{"name": "Go.gitignore", "type": "gitignore"},
			map[string]any{"name": "VisualStudio.gitignore", "type": "gitignore"},
			map[string]any{"type": "gitignore"},
		}}))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/templates"),
		httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))

	got, err := c.GitignoreTemplates(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"Go.gitignore", "VisualStudio.gitignore"}, got)
	q := reg.Requests[1].URL.Query()
	assert.Equal(t, "gitignore", q.Get("type"))
	// The SDK sends the api-version in the Accept header.
	assert.Contains(t, reg.Requests[1].Header.Get("Accept"), "api-version=7.1")

	_, err = c.GitignoreTemplates(context.Background())
	var he *HTTPError
	require.ErrorAs(t, err, &he)
	assert.Equal(t, http.StatusForbidden, he.StatusCode)
}

func TestCreateRepository(t *testing.T) {
	org := "repo-create"
	c, reg := newTestRepoClient(t, org, "My Proj")
	path := org + "/My%20Proj/_apis/git/repositories"
	var bodies []map[string]any
	reg.Register(httpmock.REST(http.MethodPost, path), repoCapture(t, &bodies, httpmock.StatusJSONResponse(http.StatusCreated, map[string]any{
		"id": repoTestID, "name": "svc", "project": map[string]any{"name": "My Proj"},
		"webUrl": "https://dev.azure.com/o/My%20Proj/_git/svc",
	})))
	reg.Register(httpmock.REST(http.MethodPost, path), httpmock.StatusJSONResponse(http.StatusConflict,
		map[string]any{"message": "TF400948: A Git repository with the name svc already exists."}))
	ctx := context.Background()

	got, err := c.CreateRepository(ctx, "svc")
	require.NoError(t, err)
	assert.Equal(t, Repository{ID: repoTestID, Name: "svc", Project: "My Proj", WebURL: "https://dev.azure.com/o/My%20Proj/_git/svc"}, got)
	assert.Equal(t, map[string]any{"name": "svc"}, bodies[0])

	_, err = c.CreateRepository(ctx, "svc")
	var he *HTTPError
	require.ErrorAs(t, err, &he)
	assert.Contains(t, he.Message, "TF400948")
}

func TestInitialPush(t *testing.T) {
	org := "repo-push"
	c, reg := newTestRepoClient(t, org, "proj")
	path := org + "/proj/_apis/git/repositories/" + repoTestID + "/pushes"
	var bodies []map[string]any
	reg.Register(httpmock.REST(http.MethodPost, path), repoCapture(t, &bodies, httpmock.JSONResponse(map[string]any{"pushId": 1})))
	reg.Register(httpmock.REST(http.MethodPost, path), repoCapture(t, &bodies, httpmock.JSONResponse(map[string]any{"pushId": 2})))
	reg.Register(httpmock.REST(http.MethodPost, path), httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "bad"}))
	ctx := context.Background()

	require.NoError(t, c.InitialPush(ctx, repoTestID, "Initial commit", "# svc\n", "Go.gitignore"))
	body := bodies[0]
	assert.Equal(t, []any{map[string]any{"name": "refs/heads/main", "oldObjectId": zeroObjectID}}, body["refUpdates"])
	assert.Equal(t, []any{map[string]any{
		"comment": "Initial commit",
		"changes": []any{
			map[string]any{"changeType": "add", "item": map[string]any{"path": "/README.md"},
				"newContent": map[string]any{"content": "# svc\n", "contentType": "rawText"}},
			map[string]any{"changeType": "add", "item": map[string]any{"path": "/.gitignore"},
				"newContentTemplate": map[string]any{"name": "Go.gitignore", "type": "gitignore"}},
		},
	}}, body["commits"])

	require.NoError(t, c.InitialPush(ctx, repoTestID, "Initial commit", "", "Go.gitignore"))
	body = bodies[1]
	changes := body["commits"].([]any)[0].(map[string]any)["changes"].([]any)
	assert.Len(t, changes, 1)

	var he *HTTPError
	require.ErrorAs(t, c.InitialPush(ctx, repoTestID, "Initial commit", "# x\n", ""), &he)
	assert.Equal(t, http.StatusBadRequest, he.StatusCode)
}

func TestRecycleBin(t *testing.T) {
	org := "repo-recycle"
	c, reg := newTestRepoClient(t, org, "proj")
	ctx := context.Background()
	reg.Register(httpmock.REST(http.MethodDelete, org+"/proj/_apis/git/repositories/"+repoTestID), httpmock.StatusStringResponse(http.StatusNoContent, ""))
	reg.Register(httpmock.REST(http.MethodDelete, org+"/proj/_apis/git/recycleBin/repositories/"+repoTestID), httpmock.StringResponse(""))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/recycleBin/repositories"),
		httpmock.JSONResponse(map[string]any{"count": 2, "value": []any{
			map[string]any{"id": repoTestID, "name": "old"}, map[string]any{},
		}}))
	var bodies []map[string]any
	reg.Register(httpmock.REST(http.MethodPatch, org+"/proj/_apis/git/recycleBin/repositories/"+repoTestID),
		repoCapture(t, &bodies, httpmock.JSONResponse(map[string]any{"id": repoTestID, "name": "old"})))
	reg.Register(httpmock.REST(http.MethodDelete, org+"/proj/_apis/git/repositories/"+repoTestID),
		httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "only repo"}))

	require.NoError(t, c.DeleteRepository(ctx, repoTestID))
	require.NoError(t, c.PurgeRepository(ctx, repoTestID))
	deleted, err := c.RecycleBinRepositories(ctx)
	require.NoError(t, err)
	assert.Equal(t, []DeletedRepository{{ID: repoTestID, Name: "old"}, {}}, deleted)
	restored, err := c.RestoreRepository(ctx, repoTestID)
	require.NoError(t, err)
	assert.Equal(t, "old", restored.Name)
	assert.Equal(t, []map[string]any{{"deleted": false}}, bodies)

	var he *HTTPError
	require.ErrorAs(t, c.DeleteRepository(ctx, repoTestID), &he)
	assert.Equal(t, "only repo", he.Message)

	for _, err := range []error{
		c.DeleteRepository(ctx, "not-a-guid"),
		c.PurgeRepository(ctx, "not-a-guid"),
		func() error { _, err := c.RestoreRepository(ctx, "not-a-guid"); return err }(),
	} {
		assert.EqualError(t, err, `invalid repository ID "not-a-guid"`)
	}
}
