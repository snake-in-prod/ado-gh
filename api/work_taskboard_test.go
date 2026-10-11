package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tbIterID = "a0000000-0000-0000-0000-000000000001"

func TestWorkItemClient_TaskboardColumns(t *testing.T) {
	org := "taskboardcolsorg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/Web%20Team/_apis/work/taskboardcolumns"),
		httpmock.StringResponse(`{"columns":[
			{"id":"b0000000-0000-0000-0000-000000000002","name":"Review","order":2,"mappings":[]},
			{"id":"b0000000-0000-0000-0000-000000000001","name":"To Do","order":0},
			{"id":"b0000000-0000-0000-0000-000000000003","name":"Done","order":3}],"isCustomized":true,"isValid":true}`))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/Web%20Team/_apis/work/taskboardcolumns"),
		httpmock.StringResponse(`{"columns":[],"isCustomized":false,"isValid":true}`))
	ctx := context.Background()

	got, err := c.TaskboardColumns(ctx, "Web Team")
	require.NoError(t, err)
	assert.Equal(t, []TaskboardColumn{
		{ID: "b0000000-0000-0000-0000-000000000001", Name: "To Do"},
		{ID: "b0000000-0000-0000-0000-000000000002", Name: "Review"},
		{ID: "b0000000-0000-0000-0000-000000000003", Name: "Done"},
	}, got)

	got, err = c.TaskboardColumns(ctx, "Web Team")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestWorkItemClient_TaskboardItems(t *testing.T) {
	org := "taskboarditemsorg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/Web%20Team/_apis/work/taskboardworkitems/"+tbIterID),
		httpmock.StringResponse(`{"count":2,"value":[
			{"workItemId":11,"column":"Review","columnId":"b0000000-0000-0000-0000-000000000002","state":"Doing"},
			{"workItemId":12,"column":"To Do","state":"To Do"}]}`))
	got, err := c.TaskboardItems(context.Background(), "Web Team", tbIterID)
	require.NoError(t, err)
	assert.Equal(t, []TaskboardItem{
		{ID: 11, ColumnID: "b0000000-0000-0000-0000-000000000002", Column: "Review"},
		{ID: 12, Column: "To Do"},
	}, got)

	_, err = c.TaskboardItems(context.Background(), "Web Team", "not-a-guid")
	require.Error(t, err)
}

func TestWorkItemClient_IterationWorkItemIDs(t *testing.T) {
	org := "iterationidsorg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/Web%20Team/_apis/work/teamsettings/iterations/"+tbIterID+"/workitems"),
		httpmock.StringResponse(`{"workItemRelations":[
			{"rel":null,"source":null,"target":{"id":10}},
			{"rel":"System.LinkTypes.Hierarchy-Forward","source":{"id":10},"target":{"id":11}},
			{"rel":null,"source":null,"target":{"id":12}},
			{"rel":"System.LinkTypes.Hierarchy-Forward","source":{"id":12},"target":{"id":11}}]}`))
	got, err := c.IterationWorkItemIDs(context.Background(), "Web Team", tbIterID)
	require.NoError(t, err)
	assert.Equal(t, []int{10, 11, 12}, got)

	_, err = c.IterationWorkItemIDs(context.Background(), "Web Team", "not-a-guid")
	require.Error(t, err)
}

func TestWorkItemClient_TaskboardErrors(t *testing.T) {
	org := "taskboarderrorg"
	c, reg := newWITClient(t, org)
	for range 3 {
		reg.Register(func(r *http.Request) bool { return r.Method != http.MethodOptions },
			httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
	}
	ctx := context.Background()
	var he *HTTPError
	_, err := c.TaskboardColumns(ctx, "t")
	require.ErrorAs(t, err, &he)
	_, err = c.TaskboardItems(ctx, "t", tbIterID)
	require.ErrorAs(t, err, &he)
	_, err = c.IterationWorkItemIDs(ctx, "t", tbIterID)
	require.ErrorAs(t, err, &he)
}
