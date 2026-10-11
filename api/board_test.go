package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkItemClient_Backlogs(t *testing.T) {
	org := "workbacklogsorg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/Web%20Team/_apis/work/backlogconfiguration"), httpmock.StringResponse(`{
		"backlogFields":{"typeFields":{"Order":"Microsoft.VSTS.Common.StackRank","Team":"System.AreaPath"}},
		"hiddenBacklogs":["Microsoft.FeatureCategory"],
		"requirementBacklog":{"id":"Microsoft.RequirementCategory","name":"Stories","rank":2,"type":"requirement","isHidden":false},
		"portfolioBacklogs":[
			{"id":"Microsoft.EpicCategory","name":"Epics","rank":4,"type":"portfolio","isHidden":true},
			{"id":"Microsoft.FeatureCategory","name":"Features","rank":3,"type":"portfolio"}]}`))

	got, err := c.Backlogs(context.Background(), "Web Team")
	require.NoError(t, err)
	assert.Equal(t, &Backlogs{
		Requirement: BacklogLevel{ID: "Microsoft.RequirementCategory", Name: "Stories", Type: "requirement"},
		Portfolio: []BacklogLevel{
			{ID: "Microsoft.EpicCategory", Name: "Epics", Type: "portfolio", Hidden: true},
			{ID: "Microsoft.FeatureCategory", Name: "Features", Type: "portfolio", Hidden: true},
		},
		OrderField: "Microsoft.VSTS.Common.StackRank",
	}, got)
}

func TestWorkItemClient_Board(t *testing.T) {
	org := "workboardorg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/Web%20Team/_apis/work/boards/Stories"), httpmock.StringResponse(`{
		"name":"Stories",
		"columns":[
			{"name":"New","columnType":"incoming","itemLimit":0,"stateMappings":{"User Story":"New"}},
			{"name":"Active","columnType":"inProgress","itemLimit":5,"isSplit":true,"stateMappings":{"User Story":"Active","Bug":"Active"}},
			{"name":"Closed","columnType":"outgoing","isSplit":null,"stateMappings":{"User Story":"Closed"}}],
		"fields":{"columnField":{"referenceName":"WEF_1_Kanban.Column"},"doneField":{"referenceName":"WEF_1_Kanban.Column.Done"},
			"rowField":{"referenceName":"WEF_1_Kanban.Lane"}}}`))

	got, err := c.Board(context.Background(), "Web Team", "Stories")
	require.NoError(t, err)
	assert.Equal(t, &Board{
		Columns: []BoardColumn{
			{Name: "New", ColumnType: "incoming", StateMappings: map[string]string{"User Story": "New"}},
			{Name: "Active", ColumnType: "inProgress", ItemLimit: 5, IsSplit: true,
				StateMappings: map[string]string{"User Story": "Active", "Bug": "Active"}},
			{Name: "Closed", ColumnType: "outgoing", StateMappings: map[string]string{"User Story": "Closed"}},
		},
		ColumnField: "WEF_1_Kanban.Column", DoneField: "WEF_1_Kanban.Column.Done",
	}, got)
}

func TestWorkItemClient_BoardErrors(t *testing.T) {
	org := "workboarderrorg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/Web%20Team/_apis/work/boards/Stories"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "board not found"}))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/Web%20Team/_apis/work/backlogconfiguration"),
		httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "no access"}))
	ctx := context.Background()
	_, err := c.Board(ctx, "Web Team", "Stories")
	require.ErrorContains(t, err, "board not found")
	_, err = c.Backlogs(ctx, "Web Team")
	require.ErrorContains(t, err, "no access")
}
