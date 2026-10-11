package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkItemClient_RestoreWorkItem(t *testing.T) {
	org := "wirestoreorg"
	c, reg := newWITClient(t, org)
	var body map[string]any
	reg.Register(httpmock.REST(http.MethodPatch, org+"/proj/_apis/wit/recyclebin/9"), func(r *http.Request) (*http.Response, error) {
		body = witJSONBody(t, r)
		return httpmock.StringResponse(`{"id":9,"name":"Fix login","type":"Issue","project":"proj"}`)(r)
	})
	reg.Register(httpmock.REST(http.MethodPatch, org+"/proj/_apis/wit/recyclebin/10"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401232: Work item 10 does not exist"}))
	ctx := context.Background()

	title, err := c.RestoreWorkItem(ctx, 9)
	require.NoError(t, err)
	assert.Equal(t, "Fix login", title)
	assert.Equal(t, map[string]any{"isDeleted": false}, body)

	_, err = c.RestoreWorkItem(ctx, 10)
	var he *HTTPError
	require.ErrorAs(t, err, &he)
	assert.Equal(t, http.StatusNotFound, he.StatusCode)
}
