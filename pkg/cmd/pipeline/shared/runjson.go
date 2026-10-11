package shared

import (
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
)

// RunJSONFields are the public `--json` fields of a run.
var RunJSONFields = []string{
	"id", "number", "pipeline", "status", "result", "sourceBranch", "sourceVersion", "reason",
	"requestedBy", "requestedFor", "queueTime", "startTime", "finishTime", "url",
}

// RunJSON is the public JSON shape of a run (build).
type RunJSON struct {
	ID            int           `json:"id"`
	Number        string        `json:"number"`
	Pipeline      *PipelineJSON `json:"pipeline"`
	Status        string        `json:"status"`
	Result        string        `json:"result"`
	SourceBranch  string        `json:"sourceBranch"`
	SourceVersion string        `json:"sourceVersion"`
	Reason        string        `json:"reason"`
	RequestedBy   *UserJSON     `json:"requestedBy"`
	RequestedFor  *UserJSON     `json:"requestedFor"`
	QueueTime     *time.Time    `json:"queueTime"`
	StartTime     *time.Time    `json:"startTime"`
	FinishTime    *time.Time    `json:"finishTime"`
	URL           string        `json:"url"`
}

// PipelineJSON names the pipeline (definition) of a run.
type PipelineJSON struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// UserJSON is an identity in JSON output.
type UserJSON struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	UniqueName  string `json:"uniqueName"`
}

// NewRunJSON returns b in its public JSON shape, with its web URL in project.
func NewRunJSON(project adocontext.Repo, b build.Build) RunJSON {
	id := prshared.Deref(b.Id)
	out := RunJSON{
		ID: id, Number: prshared.Deref(b.BuildNumber), Status: string(prshared.Deref(b.Status)),
		Result: string(prshared.Deref(b.Result)), SourceBranch: prshared.Deref(b.SourceBranch),
		SourceVersion: prshared.Deref(b.SourceVersion), Reason: string(prshared.Deref(b.Reason)),
		RequestedBy: userJSON(b.RequestedBy), RequestedFor: userJSON(b.RequestedFor),
		QueueTime: api.TimeOf(b.QueueTime), StartTime: api.TimeOf(b.StartTime), FinishTime: api.TimeOf(b.FinishTime),
		URL: adocontext.RunURL(project, id),
	}
	if d := b.Definition; d != nil {
		out.Pipeline = &PipelineJSON{ID: prshared.Deref(d.Id), Name: prshared.Deref(d.Name), Path: prshared.Deref(d.Path)}
	}
	return out
}

func userJSON(r *webapi.IdentityRef) *UserJSON {
	if r == nil {
		return nil
	}
	return &UserJSON{ID: prshared.Deref(r.Id), DisplayName: prshared.Deref(r.DisplayName), UniqueName: prshared.Deref(r.UniqueName)}
}
