package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListPullRequestsProjectScope(t *testing.T) {
	org := "listprojorg"
	c, reg := newTestPRClient(t, org)
	c.Repo = ""
	for _, p := range [][]map[string]any{prs(1, 100, false), prs(101, 5, false), nil} {
		reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/pullrequests"), page(p))
	}
	status := git.PullRequestStatusValues.Active
	got, err := c.ListPullRequests(context.Background(), git.GitPullRequestSearchCriteria{Status: &status}, 120, false)
	require.NoError(t, err)
	assert.Len(t, got, 105)
	assert.Equal(t, []string{"0", "100", "105"}, queries(reg, "$skip"))
	assert.Equal(t, "active", queries(reg, "searchCriteria.status")[0])
}
