package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wiArtifact = "vstfs:///Git/PullRequestId/p-guid%2Fr-guid%2F9"

func newRelationTestClient(t *testing.T, org string) (*WorkItemClient, *httpmock.Registry) {
	t.Helper()
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.GitPRLocations...)
	t.Cleanup(func() { reg.Verify(t) })
	hc := NewHTTPClient(ClientOptions{Transport: reg})
	return NewWorkItemClient(hc, "https://dev.azure.com/"+org, "proj"), reg
}

func wiPatchBody(t *testing.T, r *http.Request) []map[string]any {
	t.Helper()
	var ops []map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&ops))
	return ops
}

func TestNewWorkItemClient(t *testing.T) {
	c := NewWorkItemClient(http.DefaultClient, "https://dev.azure.com/o", "proj")
	assert.NotNil(t, c.Wit)
	assert.Equal(t, "proj", c.Project)
	assert.NotNil(t, NewPRClient(http.DefaultClient, "https://dev.azure.com/o", "proj", "repo").Wit)
}

func TestPullRequestArtifactURL(t *testing.T) {
	assert.Equal(t, wiArtifact, PullRequestArtifactURL("p-guid", "r-guid", 9))
}

func TestCommitAndBranchArtifactURL(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	assert.Equal(t, "vstfs:///Git/Commit/p-guid%2Fr-guid%2F"+sha, CommitArtifactURL("p-guid", "r-guid", sha))
	assert.Equal(t, "vstfs:///Git/Ref/p-guid%2Fr-guid%2FGBfeat", BranchArtifactURL("p-guid", "r-guid", "feat"))
	assert.Equal(t, "vstfs:///Git/Ref/p-guid%2Fr-guid%2FGBfeature%2Fa%20b%2Bc", BranchArtifactURL("p-guid", "r-guid", "refs/heads/feature/a b+c"))
}

func TestAddRelation(t *testing.T) {
	org := "addrelorg"
	c, reg := newRelationTestClient(t, org)
	var ops, ops2 []map[string]any
	reg.Register(httpmock.REST(http.MethodPatch, org+"/_apis/wit/workitems/5"), func(r *http.Request) (*http.Response, error) {
		ops = wiPatchBody(t, r)
		return httpmock.JSONResponse(map[string]any{"id": 5})(r)
	})
	reg.Register(httpmock.REST(http.MethodPatch, org+"/_apis/wit/workitems/6"), httpmock.StatusJSONResponse(http.StatusBadRequest,
		map[string]any{"message": "TF201035: Relation already exists."}))
	reg.Register(httpmock.REST(http.MethodPatch, org+"/_apis/wit/workitems/7"), httpmock.StatusJSONResponse(http.StatusNotFound,
		map[string]any{"message": "TF401232: Work item 7 does not exist"}))
	reg.Register(httpmock.REST(http.MethodPatch, org+"/_apis/wit/workitems/8"), func(r *http.Request) (*http.Response, error) {
		ops2 = wiPatchBody(t, r)
		return httpmock.JSONResponse(map[string]any{"id": 8})(r)
	})
	ctx := context.Background()

	require.NoError(t, c.AddRelation(ctx, 5, "ArtifactLink", wiArtifact, map[string]any{"name": "Pull Request"}))
	assert.Equal(t, []map[string]any{{
		"op": "add", "path": "/relations/-",
		"value": map[string]any{"rel": "ArtifactLink", "url": wiArtifact, "attributes": map[string]any{"name": "Pull Request"}},
	}}, ops)
	require.NoError(t, c.AddRelation(ctx, 6, "ArtifactLink", wiArtifact, nil), "an existing link is success")
	assert.True(t, IsNotFound(c.AddRelation(ctx, 7, "ArtifactLink", wiArtifact, nil)))

	parent := "https://dev.azure.com/" + org + "/_apis/wit/workItems/1"
	require.NoError(t, c.AddRelation(ctx, 8, "System.LinkTypes.Hierarchy-Reverse", parent, nil))
	assert.Equal(t, []map[string]any{{
		"op": "add", "path": "/relations/-",
		"value": map[string]any{"rel": "System.LinkTypes.Hierarchy-Reverse", "url": parent},
	}}, ops2, "no attributes key without attributes")
}

func TestRemoveRelation(t *testing.T) {
	org := "removerelorg"
	c, reg := newRelationTestClient(t, org)
	other := "vstfs:///Git/PullRequestId/p-guid%2Fr-guid%2F99"
	related := "https://dev.azure.com/" + org + "/_apis/wit/workItems/8"
	relations := []map[string]any{
		{"rel": "ArtifactLink", "url": "vstfs:///Git/Ref/p-guid%2Fr-guid%2FGBfeat", "attributes": map[string]any{"name": "Branch"}},
		{"rel": "ArtifactLink", "url": other},
		{"rel": "System.LinkTypes.Related", "url": related},
		{"rel": "ArtifactLink", "url": "VSTFS:///Git/PullRequestId/P-GUID%2fR-GUID%2f9"},
		{"rel": "ArtifactLink", "url": wiArtifact},
	}
	for range 2 {
		reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/wit/workitems/5"), func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "relations", r.URL.Query().Get("$expand"))
			return httpmock.JSONResponse(map[string]any{"id": 5, "rev": 4, "relations": relations})(r)
		})
	}
	var ops []map[string]any
	reg.Register(httpmock.REST(http.MethodPatch, org+"/_apis/wit/workitems/5"), func(r *http.Request) (*http.Response, error) {
		ops = wiPatchBody(t, r)
		return httpmock.JSONResponse(map[string]any{"id": 5})(r)
	})
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/wit/workitems/6"), httpmock.JSONResponse(map[string]any{"id": 6, "rev": 1}))
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/wit/workitems/7"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "gone"}))
	ctx := context.Background()

	found, err := c.RemoveRelation(ctx, 5, "ArtifactLink", wiArtifact)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, []map[string]any{
		{"op": "test", "path": "/rev", "value": float64(4)},
		{"op": "remove", "path": "/relations/3"},
	}, ops, "first case-insensitive match; branch and other links kept")

	found, err = c.RemoveRelation(ctx, 5, "System.LinkTypes.Hierarchy-Reverse", related)
	require.NoError(t, err)
	assert.False(t, found, "the URL matches but the relation type does not")

	found, err = c.RemoveRelation(ctx, 6, "ArtifactLink", wiArtifact)
	require.NoError(t, err)
	assert.False(t, found)

	_, err = c.RemoveRelation(ctx, 7, "ArtifactLink", wiArtifact)
	assert.True(t, IsNotFound(err))
}

func TestRemoveRelationPatchError(t *testing.T) {
	org := "removerelerrorg"
	c, reg := newRelationTestClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/wit/workitems/5"), httpmock.JSONResponse(map[string]any{
		"id": 5, "rev": 2, "relations": []map[string]any{{"rel": "ArtifactLink", "url": wiArtifact}},
	}))
	reg.Register(httpmock.REST(http.MethodPatch, org+"/_apis/wit/workitems/5"),
		httpmock.StatusJSONResponse(http.StatusPreconditionFailed, map[string]any{"message": "rev mismatch"}))

	_, err := c.RemoveRelation(context.Background(), 5, "ArtifactLink", wiArtifact)
	assert.EqualError(t, err, "rev mismatch (HTTP 412)")
}
