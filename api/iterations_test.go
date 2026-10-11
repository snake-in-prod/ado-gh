package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIterations(t *testing.T) {
	org := "iterations-list"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+prBase+"/9/iterations"), page([]map[string]any{{"id": 1}, {"id": 2}}))

	its, err := c.Iterations(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, its, 2)
	assert.Equal(t, 2, *its[1].Id)
}

func TestIterationsError(t *testing.T) {
	org := "iterations-list-err"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+prBase+"/9/iterations"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "gone"}))

	_, err := c.Iterations(context.Background(), 9)
	assert.True(t, IsNotFound(err))
}

func TestIterationChangesPaging(t *testing.T) {
	org := "iterations-changes"
	c, reg := newTestPRClient(t, org)
	path := org + prBase + "/9/iterations/3/changes"
	reg.Register(httpmock.REST(http.MethodGet, path), httpmock.JSONResponse(map[string]any{
		"changeEntries": []map[string]any{{"changeTrackingId": 1}, {"changeTrackingId": 2}}, "nextSkip": 2, "nextTop": 2000,
	}))
	// No nextSkip on the last page: nil counts as 0.
	reg.Register(httpmock.REST(http.MethodGet, path), httpmock.JSONResponse(map[string]any{
		"changeEntries": []map[string]any{{"changeTrackingId": 3}},
	}))

	changes, err := c.IterationChanges(context.Background(), 9, 3)
	require.NoError(t, err)
	require.Len(t, changes, 3)
	assert.Equal(t, 3, *changes[2].ChangeTrackingId)
	assert.Equal(t, []string{"2000", "2000"}, queries(reg, "$top"))
	assert.Equal(t, []string{"0", "2"}, queries(reg, "$skip"))
}

func TestIterationChangesError(t *testing.T) {
	org := "iterations-changes-err"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+prBase+"/9/iterations/3/changes"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "gone"}))

	_, err := c.IterationChanges(context.Background(), 9, 3)
	assert.True(t, IsNotFound(err))
}
