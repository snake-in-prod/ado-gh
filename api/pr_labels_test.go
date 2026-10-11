package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
