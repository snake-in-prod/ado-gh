package shared

import (
	"testing"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
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

func TestToJSONLabels(t *testing.T) {
	inactive := false
	got := ToJSON(git.GitPullRequest{Labels: &[]core.WebApiTagDefinition{
		{Id: ptrUUID("6f1c2a5e-0000-4000-8000-000000000001"), Name: ptrStr("smoke")},
		{Name: ptrStr("old"), Active: &inactive},
	}}, "u")
	if len(got.Labels) != 1 || got.Labels[0].Name != "smoke" || got.Labels[0].ID == "" {
		t.Errorf("labels = %+v, want only the active label smoke with its id", got.Labels)
	}
	if empty := ToJSON(git.GitPullRequest{}, "u"); empty.Labels == nil || len(empty.Labels) != 0 {
		t.Errorf("no labels must give an empty array, got %#v", empty.Labels)
	}
	assert.NotContains(t, JSONFields, "labels", "labels need an extra request per PR, so only pr view offers them")
}

func ptrStr(s string) *string { return &s }

func ptrUUID(s string) *uuid.UUID { u := uuid.MustParse(s); return &u }
