package shared

import (
	"net/url"
	"strconv"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
)

// JSONFields are the public `--json` fields of PRJSON.
var JSONFields = []string{
	"id", "title", "status", "isDraft", "author", "sourceRefName", "targetRefName",
	"createdDate", "url", "reviewers", "mergeStatus",
}

// PRJSON is the public `--json` shape of a pull request.
type PRJSON struct {
	ID            *int                        `json:"id"`
	Title         *string                     `json:"title"`
	Status        *git.PullRequestStatus      `json:"status"`
	IsDraft       *bool                       `json:"isDraft"`
	Author        *webapi.IdentityRef         `json:"author"`
	SourceRefName *string                     `json:"sourceRefName"`
	TargetRefName *string                     `json:"targetRefName"`
	CreatedDate   *azuredevops.Time           `json:"createdDate"`
	URL           string                      `json:"url"`
	Reviewers     []git.IdentityRefWithVote   `json:"reviewers"`
	MergeStatus   *git.PullRequestAsyncStatus `json:"mergeStatus"`
}

// ToJSON returns the `--json` shape of pr, a pull request of the repository at repoURL.
func ToJSON(pr git.GitPullRequest, repoURL string) PRJSON {
	reviewers := []git.IdentityRefWithVote{}
	if pr.Reviewers != nil {
		reviewers = *pr.Reviewers
	}
	return PRJSON{
		ID: pr.PullRequestId, Title: pr.Title, Status: pr.Status, IsDraft: pr.IsDraft, Author: pr.CreatedBy,
		SourceRefName: pr.SourceRefName, TargetRefName: pr.TargetRefName, CreatedDate: pr.CreationDate,
		URL:       PRURL(repoURL, pr),
		Reviewers: reviewers, MergeStatus: pr.MergeStatus,
	}
}

// RepoURL returns the web URL of repo, e.g. https://dev.azure.com/org/proj/_git/repo.
func RepoURL(repo adocontext.Repo) string {
	return authshared.OrgURL(repo.Host, repo.Org) + url.PathEscape(repo.Project) + "/_git/" + url.PathEscape(repo.Name)
}

// PRURL returns the web URL of pr, a pull request of the repository at repoURL.
func PRURL(repoURL string, pr git.GitPullRequest) string {
	id := 0
	if pr.PullRequestId != nil {
		id = *pr.PullRequestId
	}
	return repoURL + "/pullrequest/" + strconv.Itoa(id)
}
