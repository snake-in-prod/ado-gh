package shared

import (
	"context"
	"net/http"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindForBranchNoneIsNil(t *testing.T) {
	org := "ffb-none"
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.GitPRLocations...)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/web/pullRequests"), list())
	f := testFactory(t, org, reg, "feat", nil)
	c, err := NewClient(context.Background(), f, adocontext.Repo{Host: "dev.azure.com", Org: org, Project: "proj", Name: "web"})
	require.NoError(t, err)

	pr, err := FindForBranch(context.Background(), c, "feat")
	require.NoError(t, err)
	assert.Nil(t, pr)
	reg.Verify(t)
}

func TestPRRepoURL(t *testing.T) {
	base := adocontext.Repo{Host: "dev.azure.com", Org: "org", Project: "My Proj", Name: "web"}
	other, proj := "api", "Other"
	assert.Equal(t, "https://dev.azure.com/org/My%20Proj/_git/web", PRRepoURL(base, git.GitPullRequest{}))
	assert.Equal(t, "https://dev.azure.com/org/My%20Proj/_git/api",
		PRRepoURL(base, git.GitPullRequest{Repository: &git.GitRepository{Name: &other}}))
	assert.Equal(t, "https://dev.azure.com/org/Other/_git/api",
		PRRepoURL(base, git.GitPullRequest{Repository: &git.GitRepository{Name: &other, Project: &core.TeamProjectReference{Name: &proj}}}))
}
