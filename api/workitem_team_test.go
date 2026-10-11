package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkItemClient_CurrentIterationPath(t *testing.T) {
	org := "witeamcurorg"
	c, reg := newWITClient(t, org)
	var timeframe string
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/Web%20Team/_apis/work/teamsettings/iterations"), func(r *http.Request) (*http.Response, error) {
		timeframe = r.URL.Query().Get("$timeframe")
		return httpmock.StringResponse(`{"count":1,"value":[{"id":"a0000000-0000-0000-0000-000000000001","name":"Sprint 3","path":"proj\\Release 1\\Sprint 3","attributes":{"timeFrame":"current"}}]}`)(r)
	})
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/Idle/_apis/work/teamsettings/iterations"), httpmock.StringResponse(`{"count":0,"value":[]}`))
	ctx := context.Background()

	got, err := c.CurrentIterationPath(ctx, "Web Team")
	require.NoError(t, err)
	assert.Equal(t, `proj\Release 1\Sprint 3`, got)
	assert.Equal(t, "current", timeframe)

	_, err = c.CurrentIterationPath(ctx, "Idle")
	require.EqualError(t, err, `team "Idle" has no current iteration; check its sprint dates in the team settings`)
}

func TestWorkItemClient_DefaultTeam(t *testing.T) {
	org := "witeamdeforg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/projects/proj"),
		httpmock.StringResponse(`{"id":"a0000000-0000-0000-0000-000000000002","name":"proj","defaultTeam":{"id":"a0000000-0000-0000-0000-000000000003","name":"proj Team"}}`))
	got, err := c.DefaultTeam(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "proj Team", got)
}

func TestWorkItemClient_DefaultTeamMissing(t *testing.T) {
	org := "witeamnodeforg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/projects/proj"), httpmock.StringResponse(`{"id":"a0000000-0000-0000-0000-000000000002","name":"proj"}`))
	_, err := c.DefaultTeam(context.Background())
	require.EqualError(t, err, `project "proj" has no default team; use --team`)
}

func TestWorkItemClient_TeamErrors(t *testing.T) {
	org := "witeamerrorg"
	c, reg := newWITClient(t, org)
	for range 2 {
		reg.Register(func(r *http.Request) bool { return r.Method != http.MethodOptions },
			httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
	}
	ctx := context.Background()
	var he *HTTPError
	_, err := c.CurrentIterationPath(ctx, "t")
	require.ErrorAs(t, err, &he)
	_, err = c.DefaultTeam(ctx)
	require.ErrorAs(t, err, &he)
}
