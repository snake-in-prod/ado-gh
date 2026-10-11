package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkItemClient_TeamIterations(t *testing.T) {
	org := "workitersorg"
	c, reg := newWITClient(t, org)
	var timeframes []string
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/Web%20Team/_apis/work/teamsettings/iterations"), func(r *http.Request) (*http.Response, error) {
		timeframes = append(timeframes, r.URL.Query().Get("$timeframe"))
		return httpmock.StringResponse(`{"count":2,"value":[
			{"id":"a0000000-0000-0000-0000-000000000001","name":"Sprint 1","path":"proj\\Sprint 1",
			 "attributes":{"startDate":"2026-09-28T00:00:00Z","finishDate":"2026-10-09T00:00:00Z","timeFrame":"past"}},
			{"id":"a0000000-0000-0000-0000-000000000002","name":"Sprint 2","path":"proj\\Sprint 2",
			 "attributes":{"startDate":null,"finishDate":null,"timeFrame":"current"}}]}`)(r)
	})
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/Web%20Team/_apis/work/teamsettings/iterations"), func(r *http.Request) (*http.Response, error) {
		timeframes = append(timeframes, r.URL.Query().Get("$timeframe"))
		return httpmock.StringResponse(`{"count":0,"value":[]}`)(r)
	})
	ctx := context.Background()

	got, err := c.TeamIterations(ctx, "Web Team", false)
	require.NoError(t, err)
	assert.Equal(t, []Iteration{
		{ID: "a0000000-0000-0000-0000-000000000001", Name: "Sprint 1", Path: `proj\Sprint 1`, TimeFrame: "past",
			StartDate: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), FinishDate: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)},
		{ID: "a0000000-0000-0000-0000-000000000002", Name: "Sprint 2", Path: `proj\Sprint 2`, TimeFrame: "current"},
	}, got)

	got, err = c.TeamIterations(ctx, "Web Team", true)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, []string{"", "current"}, timeframes)
}

func TestWorkItemClient_WorkingDays(t *testing.T) {
	org := "workdaysorg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/Web%20Team/_apis/work/teamsettings"),
		httpmock.StringResponse(`{"bugsBehavior":"off","workingDays":["monday","Tuesday","friday","someday"]}`))
	got, err := c.WorkingDays(context.Background(), "Web Team")
	require.NoError(t, err)
	assert.Equal(t, []time.Weekday{time.Monday, time.Tuesday, time.Friday}, got)
}

func TestWorkItemClient_TeamDaysOff(t *testing.T) {
	org := "workdaysofforg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet,
		org+"/proj/Web%20Team/_apis/work/teamsettings/iterations/a0000000-0000-0000-0000-000000000001/teamdaysoff"),
		httpmock.StringResponse(`{"daysOff":[{"start":"2026-10-12T00:00:00Z","end":"2026-10-13T00:00:00Z"}]}`))
	got, err := c.TeamDaysOff(context.Background(), "Web Team", "a0000000-0000-0000-0000-000000000001")
	require.NoError(t, err)
	assert.Equal(t, []DateRange{{Start: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)}}, got)

	_, err = c.TeamDaysOff(context.Background(), "Web Team", "not-a-guid")
	require.Error(t, err)
}

func TestWorkItemClient_WorkErrors(t *testing.T) {
	org := "workerrorg"
	c, reg := newWITClient(t, org)
	for range 3 {
		reg.Register(func(r *http.Request) bool { return r.Method != http.MethodOptions },
			httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
	}
	ctx := context.Background()
	var he *HTTPError
	_, err := c.TeamIterations(ctx, "t", false)
	require.ErrorAs(t, err, &he)
	_, err = c.WorkingDays(ctx, "t")
	require.ErrorAs(t, err, &he)
	_, err = c.TeamDaysOff(ctx, "t", "a0000000-0000-0000-0000-000000000001")
	require.ErrorAs(t, err, &he)
}
