package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkItemClient_IterationWorkItems(t *testing.T) {
	org := "workiterwiorg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet,
		org+"/proj/Web%20Team/_apis/work/teamsettings/iterations/a0000000-0000-0000-0000-000000000001/workitems"),
		httpmock.StringResponse(`{"workItemRelations":[
			{"rel":null,"source":null,"target":{"id":1}},
			{"rel":"System.LinkTypes.Hierarchy-Forward","source":{"id":1},"target":{"id":3}}]}`))
	got, err := c.IterationWorkItems(context.Background(), "Web Team", "a0000000-0000-0000-0000-000000000001")
	require.NoError(t, err)
	assert.Equal(t, []WorkItemLink{{TargetID: 1}, {Rel: "System.LinkTypes.Hierarchy-Forward", SourceID: 1, TargetID: 3}}, got)

	_, err = c.IterationWorkItems(context.Background(), "Web Team", "not-a-guid")
	require.Error(t, err)

	reg.Register(httpmock.REST(http.MethodGet,
		org+"/proj/Web%20Team/_apis/work/teamsettings/iterations/a0000000-0000-0000-0000-000000000002/workitems"),
		httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
	var he *HTTPError
	_, err = c.IterationWorkItems(context.Background(), "Web Team", "a0000000-0000-0000-0000-000000000002")
	require.ErrorAs(t, err, &he)
}
