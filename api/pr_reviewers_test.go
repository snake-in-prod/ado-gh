package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddReviewer(t *testing.T) {
	org := "addrevorg"
	c, reg := newTestPRClient(t, org)
	var bodies []map[string]any
	for _, id := range []string{"req-id", "opt-id"} {
		reg.Register(httpmock.REST(http.MethodPut, org+prBase+"/9/reviewers/"+id), func(r *http.Request) (*http.Response, error) {
			bodies = append(bodies, jsonBody(t, r))
			return httpmock.JSONResponse(map[string]any{"id": id, "isRequired": id == "req-id", "vote": 0})(r)
		})
	}
	ctx := context.Background()

	out, err := c.AddReviewer(ctx, 9, "req-id", true)
	require.NoError(t, err)
	assert.True(t, *out.IsRequired)
	_, err = c.AddReviewer(ctx, 9, "opt-id", false)
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{
		{"id": "req-id", "isRequired": true},
		{"id": "opt-id"},
	}, bodies, "no vote is sent, so an existing vote isn't overwritten")
}

func TestRemoveReviewer(t *testing.T) {
	org := "rmrevorg"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodDelete, org+prBase+"/9/reviewers/a"), httpmock.StringResponse(""))
	reg.Register(httpmock.REST(http.MethodDelete, org+prBase+"/9/reviewers/b"),
		httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "required by policy"}))
	ctx := context.Background()

	require.NoError(t, c.RemoveReviewer(ctx, 9, "a"))
	assert.EqualError(t, c.RemoveReviewer(ctx, 9, "b"), "required by policy (HTTP 400)")
}

func TestAddReviewerError(t *testing.T) {
	org := "addreverrorg"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.MatchAny, httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "gone"}))
	_, err := c.AddReviewer(context.Background(), 9, "x", true)
	assert.True(t, IsNotFound(err))
}
