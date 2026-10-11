package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/witfixtures"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkItemClient_ProcessMetadata(t *testing.T) {
	org := "witprocessorg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "inherited", "types"))))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes/User%20Story/states"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.TypeStates(t, "agile", "User Story"))))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypecategories"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "basic", "categories"))))
	ctx := context.Background()

	types, err := c.WorkItemTypes(ctx)
	require.NoError(t, err)
	require.Len(t, types, 10)
	assert.Equal(t, WorkItemType{Name: "Bug", ReferenceName: "AgileCustom.Bug", States: []WorkItemState{
		{"New", "Proposed"}, {"Triaged", "Proposed"}, {"Active", "InProgress"}, {"Resolved", "Resolved"}, {"Closed", "Completed"},
	}}, types[0])
	assert.True(t, types[9].IsDisabled, "Issue is disabled")

	states, err := c.WorkItemTypeStates(ctx, "User Story")
	require.NoError(t, err)
	assert.Equal(t, []WorkItemState{
		{"New", "Proposed"}, {"Active", "InProgress"}, {"Resolved", "InProgress"}, {"Closed", "Completed"}, {"Removed", "Removed"},
	}, states)

	cats, err := c.WorkItemTypeCategories(ctx)
	require.NoError(t, err)
	assert.Equal(t, WorkItemTypeCategory{ReferenceName: "Microsoft.BugCategory", DefaultType: "Issue", Types: []string{"Issue"}}, cats[0])
}

func TestWorkItemClient_ProcessMetadataErrors(t *testing.T) {
	org := "witprocesserrorg"
	c, reg := newWITClient(t, org)
	notFound := httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF200016: project does not exist"})
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes"), notFound)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes/Bug/states"), notFound)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypecategories"), notFound)
	ctx := context.Background()

	_, err := c.WorkItemTypes(ctx)
	assert.ErrorContains(t, err, "TF200016")
	_, err = c.WorkItemTypeStates(ctx, "Bug")
	assert.ErrorContains(t, err, "TF200016")
	_, err = c.WorkItemTypeCategories(ctx)
	assert.ErrorContains(t, err, "TF200016")
}
