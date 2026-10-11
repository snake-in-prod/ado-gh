package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlob(t *testing.T) {
	org := "blob-meta"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/repo/blobs/abc123"),
		httpmock.JSONResponse(map[string]any{"objectId": "abc123", "size": 6 << 20}))

	b, err := c.Blob(context.Background(), "abc123")
	require.NoError(t, err)
	assert.Equal(t, uint64(6<<20), *b.Size)
	assert.True(t, strings.HasPrefix(reg.Requests[1].Header.Get("Accept"), "application/json"))
}

func TestBlobContent(t *testing.T) {
	org := "blob-content"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/repo/blobs/abc123"),
		httpmock.StringResponse("line\x00bin"))

	data, err := c.BlobContent(context.Background(), "abc123")
	require.NoError(t, err)
	assert.Equal(t, "line\x00bin", string(data))
	assert.True(t, strings.HasPrefix(reg.Requests[1].Header.Get("Accept"), "application/octet-stream"))
}

func TestBlobForkRepoHasNoProject(t *testing.T) {
	org := "blob-fork"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/git/repositories/fork-id/blobs/abc123"), httpmock.StringResponse("x"))

	fork := &PRClient{Git: c.Git, Repo: "fork-id"}
	data, err := fork.BlobContent(context.Background(), "abc123")
	require.NoError(t, err)
	assert.Equal(t, "x", string(data))
}

func TestItemContentAt(t *testing.T) {
	org := "item-content"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/repo/items"), httpmock.StringResponse("old\n"))

	data, err := c.ItemContentAt(context.Background(), "/dir/a.go", "c0ffee")
	require.NoError(t, err)
	assert.Equal(t, "old\n", string(data))
	q := reg.Requests[1].URL.Query()
	assert.Equal(t, "/dir/a.go", q.Get("path"))
	assert.Equal(t, "c0ffee", q.Get("versionDescriptor.version"))
	assert.Equal(t, "commit", q.Get("versionDescriptor.versionType"))
}

func TestBlobErrors(t *testing.T) {
	org := "blob-errors"
	c, reg := newTestPRClient(t, org)
	gone := httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "gone"})
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/repo/blobs/abc123"), gone)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/repo/blobs/abc123"), gone)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/repo/items"), gone)
	ctx := context.Background()

	_, err := c.Blob(ctx, "abc123")
	assert.True(t, IsNotFound(err))
	_, err = c.BlobContent(ctx, "abc123")
	assert.True(t, IsNotFound(err))
	_, err = c.ItemContentAt(ctx, "/a", "c0ffee")
	assert.True(t, IsNotFound(err))
}
