package shared

import (
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/stretchr/testify/assert"
)

func TestRepoURLAndToJSON(t *testing.T) {
	repoURL := RepoURL(adocontext.Repo{Host: "dev.azure.com", Org: "o", Project: "My Proj", Name: "web"})
	assert.Equal(t, "https://dev.azure.com/o/My%20Proj/_git/web", repoURL)
	assert.Equal(t, "https://fab.visualstudio.com/p/_git/r", RepoURL(adocontext.Repo{Host: "fab.visualstudio.com", Org: "fab", Project: "p", Name: "r"}))

	id := 7
	got := ToJSON(git.GitPullRequest{PullRequestId: &id}, repoURL)
	assert.Equal(t, repoURL+"/pullrequest/7", got.URL)
	assert.Equal(t, []git.IdentityRefWithVote{}, got.Reviewers)
	assert.Equal(t, repoURL+"/pullrequest/0", PRURL(repoURL, git.GitPullRequest{}))
}
