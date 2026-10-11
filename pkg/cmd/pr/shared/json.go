package shared

import (
	"strconv"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
)

// JSONFields are the public `--json` fields of PRJSON.
var JSONFields = []string{
	"id", "title", "status", "isDraft", "author", "sourceRefName", "targetRefName",
	"createdDate", "url", "reviewers", "mergeStatus",
}

// LabelJSON is a PR label (an ADO tag) in `pr view --json labels`, shaped like gh's `labels`.
type LabelJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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
	Labels        []LabelJSON                 `json:"labels"`
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
		Labels: Labels(pr.Labels),
	}
}

// Labels returns the active labels of ls; inactive (deleted) tags are skipped. The pull
// request GET leaves labels out, so callers fetch them with PRClient.ListLabels.
func Labels(ls *[]core.WebApiTagDefinition) []LabelJSON {
	out := []LabelJSON{}
	if ls == nil {
		return out
	}
	for _, l := range *ls {
		if l.Active != nil && !*l.Active {
			continue
		}
		id := ""
		if l.Id != nil {
			id = l.Id.String()
		}
		out = append(out, LabelJSON{ID: id, Name: Deref(l.Name)})
	}
	return out
}

// RepoURL returns the web URL of repo; see adocontext.RepoURL.
func RepoURL(repo adocontext.Repo) string {
	return adocontext.RepoURL(repo)
}

// PRRepoURL returns the web URL of pr's own repository (from pr.Repository) in base's
// organization, or of base when pr names none; pull requests of a project-wide list belong
// to several repositories.
func PRRepoURL(base adocontext.Repo, pr git.GitPullRequest) string {
	if r := pr.Repository; r != nil && r.Name != nil {
		base.Name = *r.Name
		if r.Project != nil && r.Project.Name != nil {
			base.Project = *r.Project.Name
		}
	}
	return RepoURL(base)
}

// PRURL returns the web URL of pr, a pull request of the repository at repoURL.
func PRURL(repoURL string, pr git.GitPullRequest) string {
	id := 0
	if pr.PullRequestId != nil {
		id = *pr.PullRequestId
	}
	return repoURL + "/pullrequest/" + strconv.Itoa(id)
}
