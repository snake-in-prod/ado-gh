package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReleaseClient_ReleaseDefinitionAndCreate(t *testing.T) {
	org := "relcreateorg"
	c, reg := newReleaseTestClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/release/definitions/1"),
		httpmock.StringResponse(`{"id":1,"name":"rd","variables":{"greeting":{"value":"hi","allowOverride":true}}}`))
	var body map[string]any
	reg.Register(httpmock.REST(http.MethodPost, org+"/proj/_apis/release/releases"), func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &body))
		return httpmock.StringResponse(`{"id":2,"name":"Release-2"}`)(r)
	})
	ctx := context.Background()

	d, err := c.ReleaseDefinition(ctx, 1)
	require.NoError(t, err)
	assert.True(t, *(*d.Variables)["greeting"].AllowOverride)

	def, draft := 1, false
	r, err := c.CreateRelease(ctx, release.ReleaseStartMetadata{DefinitionId: &def, IsDraft: &draft})
	require.NoError(t, err)
	assert.Equal(t, "Release-2", *r.Name)
	assert.Equal(t, map[string]any{"definitionId": float64(1), "isDraft": false}, body)
}

func TestReleaseClient_CreateDiscoveryErrors(t *testing.T) {
	ctx := context.Background()
	reg := &httpmock.Registry{} // no stubs: discovery fails
	c := NewReleaseClient(NewHTTPClient(ClientOptions{Transport: reg}), "https://dev.azure.com/relcreatedisc", "proj")
	_, err := c.ReleaseDefinition(ctx, 1)
	assert.ErrorContains(t, err, "no registered HTTP stubs matched OPTIONS https://dev.azure.com/relcreatedisc/_apis")
	c = NewReleaseClient(NewHTTPClient(ClientOptions{Transport: reg}), "https://dev.azure.com/relcreatedisc2", "proj")
	_, err = c.CreateRelease(ctx, release.ReleaseStartMetadata{})
	assert.ErrorContains(t, err, "no registered HTTP stubs matched OPTIONS https://dev.azure.com/relcreatedisc2/_apis")
}
