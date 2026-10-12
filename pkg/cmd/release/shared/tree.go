package shared

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	pshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
)

// Environment is the public JSON shape of a release environment: its status, the approvals of
// all its deployments, and the jobs (each with its tasks) of its latest deployment.
type Environment struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	// Attempt and OperationStatus are those of the latest deployment (0 and "" before the
	// first). OperationStatus tells a failed deployment (e.g. PhaseFailed) from a rejected
	// approval when Status is rejected.
	Attempt         int             `json:"attempt"`
	OperationStatus string          `json:"operationStatus"`
	Approvals       []Approval      `json:"approvals"`
	Jobs            []*pshared.Node `json:"jobs"`
}

// Approval is a pre- or post-deployment approval of an environment.
type Approval struct {
	ID int `json:"id"`
	// Type is preDeploy or postDeploy.
	Type      string            `json:"type"`
	Status    string            `json:"status"`
	Approver  *pshared.UserJSON `json:"approver"`
	Automated bool              `json:"isAutomated"`
	Comments  string            `json:"comments"`
}

// Tree is a release's environments, both in JSON form and as run tree nodes for the pipeline
// renderer and log helpers: one Stage node per environment → one Job node per deployment job
// (named after its deploy phase when it is the phase's only job) → Task nodes. Statuses are
// mapped onto the build timeline's state and result.
type Tree struct {
	Environments []Environment
	Nodes        []*pshared.Node
	logs         map[*pshared.Node]taskRef
}

// taskRef locates a task log: environment, deploy phase (ReleaseDeployPhase.Id) and task.
type taskRef struct{ env, phase, task int }

// NewTree builds the Tree of r, fetched with its tasks.
func NewTree(r release.Release) Tree {
	t := Tree{Environments: []Environment{}, Nodes: []*pshared.Node{}, logs: map[*pshared.Node]taskRef{}}
	for _, e := range prshared.Deref(r.Environments) {
		env := Environment{
			ID: prshared.Deref(e.Id), Name: prshared.Deref(e.Name), Status: string(prshared.Deref(e.Status)),
			Approvals: []Approval{}, Jobs: []*pshared.Node{},
		}
		for _, a := range slices.Concat(prshared.Deref(e.PreDeployApprovals), prshared.Deref(e.PostDeployApprovals)) {
			env.Approvals = append(env.Approvals, Approval{
				ID: prshared.Deref(a.Id), Type: string(prshared.Deref(a.ApprovalType)), Status: string(prshared.Deref(a.Status)),
				Approver: userJSON(a.Approver), Automated: prshared.Deref(a.IsAutomated), Comments: prshared.Deref(a.Comments),
			})
		}
		n := &pshared.Node{ID: strconv.Itoa(env.ID), Name: env.Name, Type: "Stage"}
		n.State, n.Result = statusOf(env.Status)
		if steps := prshared.Deref(e.DeploySteps); len(steps) > 0 {
			step := slices.MaxFunc(steps, func(a, b release.DeploymentAttempt) int {
				return prshared.Deref(a.Attempt) - prshared.Deref(b.Attempt)
			})
			env.Attempt, env.OperationStatus = prshared.Deref(step.Attempt), string(prshared.Deref(step.OperationStatus))
			n.Attempt, n.Issues = env.Attempt, issuesOf(step.Issues)
			for _, p := range prshared.Deref(step.ReleaseDeployPhases) {
				env.Jobs = append(env.Jobs, t.jobs(env.ID, p)...)
			}
		}
		n.Children = env.Jobs
		for _, j := range env.Jobs {
			if j.StartTime != nil && (n.StartTime == nil || j.StartTime.Before(*n.StartTime)) {
				n.StartTime = j.StartTime
			}
			if n.State == "completed" && j.FinishTime != nil && (n.FinishTime == nil || j.FinishTime.After(*n.FinishTime)) {
				n.FinishTime = j.FinishTime
			}
		}
		t.Environments = append(t.Environments, env)
		t.Nodes = append(t.Nodes, n)
	}
	return t
}

// jobs returns the Job nodes of deploy phase p of environment env. A phase that has not
// started has no deployment jobs yet, so it stands in for its job.
func (t Tree) jobs(env int, p release.ReleaseDeployPhase) []*pshared.Node {
	phaseID := prshared.Deref(p.Id)
	djs := prshared.Deref(p.DeploymentJobs)
	if len(djs) == 0 {
		n := &pshared.Node{Name: prshared.Deref(p.Name), Type: "Job"}
		n.State, n.Result = statusOf(string(prshared.Deref(p.Status)))
		return []*pshared.Node{n}
	}
	var out []*pshared.Node
	for _, dj := range djs {
		n := nodeOf(prshared.Deref(dj.Job), "Job")
		if len(djs) == 1 && prshared.Deref(p.Name) != "" {
			n.Name = prshared.Deref(p.Name)
		}
		for _, task := range prshared.Deref(dj.Tasks) {
			tn := nodeOf(task, "Task")
			if prshared.Deref(task.LogUrl) != "" && prshared.Deref(task.Id) != 0 {
				id := prshared.Deref(task.Id)
				tn.LogID = &id
				t.logs[tn] = taskRef{env, phaseID, id}
			}
			n.Children = append(n.Children, tn)
		}
		out = append(out, n)
	}
	return out
}

// EnvironmentIndex returns the index in t.Environments and t.Nodes of the environment of
// release releaseID named ref (case-insensitively) or with ID ref.
func (t Tree) EnvironmentIndex(ref string, releaseID int) (int, error) {
	i := slices.IndexFunc(t.Environments, func(e Environment) bool {
		return strings.EqualFold(e.Name, ref) || strconv.Itoa(e.ID) == ref
	})
	if i < 0 {
		names := make([]string, len(t.Environments))
		for j, e := range t.Environments {
			names[j] = e.Name
		}
		return 0, fmt.Errorf("no environment %q in release %d; its environments: %s", ref, releaseID, strings.Join(names, ", "))
	}
	return i, nil
}

// LogOpener opens the task logs of release releaseID listed by t's nodes.
func (t Tree) LogOpener(c *api.ReleaseClient, releaseID int) pshared.LogOpener {
	return func(ctx context.Context, l pshared.TaskLog) (io.ReadCloser, error) {
		r := t.logs[l.Task]
		return c.TaskLog(ctx, releaseID, r.env, r.phase, r.task)
	}
}

func nodeOf(task release.ReleaseTask, typ string) *pshared.Node {
	n := &pshared.Node{
		ID: strconv.Itoa(prshared.Deref(task.Id)), Name: prshared.Deref(task.Name), Type: typ,
		StartTime: api.TimeOf(task.StartTime), FinishTime: api.TimeOf(task.FinishTime),
		Issues: issuesOf(task.Issues),
	}
	n.State, n.Result = statusOf(string(prshared.Deref(task.Status)))
	return n
}

func issuesOf(is *[]release.Issue) []pshared.Issue {
	var out []pshared.Issue
	for _, i := range prshared.Deref(is) {
		out = append(out, pshared.Issue{Type: strings.ToLower(prshared.Deref(i.IssueType)), Message: prshared.Deref(i.Message)})
	}
	return out
}

// statusOf maps a release environment, deploy phase or task status onto a build timeline
// record's state and result, so the pipeline renderer marks it and the log filters see
// completed and failed tasks. Unfinished statuses (inProgress, queued, notStarted, ...) stay
// the state.
func statusOf(s string) (state, result string) {
	switch s {
	case "succeeded", "success":
		return "completed", "succeeded"
	case "failed", "failure":
		return "completed", "failed"
	case "rejected", "partiallySucceeded", "canceled", "skipped":
		return "completed", s
	}
	return s, ""
}

func userJSON(r *webapi.IdentityRef) *pshared.UserJSON {
	if r == nil {
		return nil
	}
	return &pshared.UserJSON{ID: prshared.Deref(r.Id), DisplayName: prshared.Deref(r.DisplayName), UniqueName: prshared.Deref(r.UniqueName)}
}
