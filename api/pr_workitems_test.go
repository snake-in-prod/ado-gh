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

func wiPatchBody(t *testing.T, r *http.Request) []map[string]any {
	t.Helper()
	var ops []map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&ops))
	return ops
}

func TestPullRequestArtifactURL(t *testing.T) {
	assert.Equal(t, wiArtifact, PullRequestArtifactURL("p-guid", "r-guid", 9))
}

func TestLabels(t *testing.T) {
	org := "labelsorg"
	c, reg := newTestPRClient(t, org)
	var body map[string]any
	reg.Register(httpmock.REST(http.MethodPost, org+prBase+"/9/labels"), func(r *http.Request) (*http.Response, error) {
		body = jsonBody(t, r)
		return httpmock.JSONResponse(map[string]any{"name": "bug"})(r)
	})
	reg.Register(httpmock.REST(http.MethodDelete, org+prBase+"/9/labels/l-id"), httpmock.StringResponse(""))
	reg.Register(httpmock.REST(http.MethodDelete, org+prBase+"/9/labels/gone"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "not found"}))
	reg.Register(httpmock.REST(http.MethodPost, org+prBase+"/9/labels"),
		httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "bad"}))
	ctx := context.Background()

	require.NoError(t, c.AddLabel(ctx, 9, "bug"))
	assert.Equal(t, map[string]any{"name": "bug"}, body)
	require.NoError(t, c.RemoveLabel(ctx, 9, "l-id"))
	assert.True(t, IsNotFound(c.RemoveLabel(ctx, 9, "gone")))
	assert.EqualError(t, c.AddLabel(ctx, 9, "x"), "bad (HTTP 400)")
}

func TestLinkWorkItem(t *testing.T) {
	org := "linkwiorg"
	c, reg := newTestPRClient(t, org)
	var ops []map[string]any
	reg.Register(httpmock.REST(http.MethodPatch, org+"/_apis/wit/workitems/5"), func(r *http.Request) (*http.Response, error) {
		ops = wiPatchBody(t, r)
		return httpmock.JSONResponse(map[string]any{"id": 5})(r)
	})
	reg.Register(httpmock.REST(http.MethodPatch, org+"/_apis/wit/workitems/6"), httpmock.StatusJSONResponse(http.StatusBadRequest,
		map[string]any{"message": "TF201035: Relation already exists."}))
	reg.Register(httpmock.REST(http.MethodPatch, org+"/_apis/wit/workitems/7"), httpmock.StatusJSONResponse(http.StatusNotFound,
		map[string]any{"message": "TF401232: Work item 7 does not exist"}))
	ctx := context.Background()

	require.NoError(t, c.LinkWorkItem(ctx, 5, wiArtifact))
	assert.Equal(t, []map[string]any{{
		"op": "add", "path": "/relations/-",
		"value": map[string]any{"rel": "ArtifactLink", "url": wiArtifact, "attributes": map[string]any{"name": "Pull Request"}},
	}}, ops)
	require.NoError(t, c.LinkWorkItem(ctx, 6, wiArtifact), "an existing link is success")
	assert.True(t, IsNotFound(c.LinkWorkItem(ctx, 7, wiArtifact)))
}

func TestUnlinkWorkItem(t *testing.T) {
	org := "unlinkwiorg"
	c, reg := newTestPRClient(t, org)
	other := "vstfs:///Git/PullRequestId/p-guid%2Fr-guid%2F99"
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/wit/workitems/5"), func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "relations", r.URL.Query().Get("$expand"))
		return httpmock.JSONResponse(map[string]any{"id": 5, "rev": 4, "relations": []map[string]any{
			{"rel": "ArtifactLink", "url": "vstfs:///Git/Ref/p-guid%2Fr-guid%2FGBfeat", "attributes": map[string]any{"name": "Branch"}},
			{"rel": "ArtifactLink", "url": other},
			{"rel": "System.LinkTypes.Related", "url": "https://dev.azure.com/" + org + "/_apis/wit/workItems/8"},
			{"rel": "ArtifactLink", "url": "VSTFS:///Git/PullRequestId/P-GUID%2fR-GUID%2f9"},
			{"rel": "ArtifactLink", "url": wiArtifact},
		}})(r)
	})
	var ops []map[string]any
	reg.Register(httpmock.REST(http.MethodPatch, org+"/_apis/wit/workitems/5"), func(r *http.Request) (*http.Response, error) {
		ops = wiPatchBody(t, r)
		return httpmock.JSONResponse(map[string]any{"id": 5})(r)
	})
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/wit/workitems/6"), httpmock.JSONResponse(map[string]any{"id": 6, "rev": 1}))
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/wit/workitems/7"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "gone"}))
	ctx := context.Background()

	found, err := c.UnlinkWorkItem(ctx, 5, wiArtifact)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, []map[string]any{
		{"op": "test", "path": "/rev", "value": float64(4)},
		{"op": "remove", "path": "/relations/3"},
	}, ops, "first case-insensitive match; branch and other links kept")

	found, err = c.UnlinkWorkItem(ctx, 6, wiArtifact)
	require.NoError(t, err)
	assert.False(t, found)

	_, err = c.UnlinkWorkItem(ctx, 7, wiArtifact)
	assert.True(t, IsNotFound(err))
}

func TestUnlinkWorkItemPatchError(t *testing.T) {
	org := "unlinkwierrorg"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/wit/workitems/5"), httpmock.JSONResponse(map[string]any{
		"id": 5, "rev": 2, "relations": []map[string]any{{"rel": "ArtifactLink", "url": wiArtifact}},
	}))
	reg.Register(httpmock.REST(http.MethodPatch, org+"/_apis/wit/workitems/5"),
		httpmock.StatusJSONResponse(http.StatusPreconditionFailed, map[string]any{"message": "rev mismatch"}))

	_, err := c.UnlinkWorkItem(context.Background(), 5, wiArtifact)
	assert.EqualError(t, err, "rev mismatch (HTTP 412)")
}

func TestListLabels(t *testing.T) {
	org := "listlabelsorg"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+prBase+"/9/labels"),
		httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{{"name": "smoke", "active": true}}}))
	got, err := c.ListLabels(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "smoke", *got[0].Name)
}
