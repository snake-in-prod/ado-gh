package shared

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	pshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func relTreeFixture(t *testing.T, name string) release.Release {
	t.Helper()
	var r release.Release
	require.NoError(t, json.Unmarshal(pipelinefixtures.Read(t, name), &r))
	return r
}

func relTreeText(nodes []*pshared.Node) string {
	var b bytes.Buffer
	ios, _, _, _ := iostreams.Test()
	pshared.PrintTree(&b, ios.ColorScheme(), nodes, true)
	return b.String()
}

func TestNewTreeLive(t *testing.T) {
	tr := NewTree(relTreeFixture(t, "release.json"))
	assert.Equal(t, "✓ Dev in 2s\n  ✓ Agentless job in 2s\n    ✓ Delay\n● Prod\n", relTreeText(tr.Nodes))
	assert.Empty(t, pshared.TaskLogs(tr.Nodes, pshared.LogFilter{}), "agentless tasks have no log")

	got, err := json.Marshal(tr.Environments)
	require.NoError(t, err)
	var envs []map[string]any
	require.NoError(t, json.Unmarshal(got, &envs))
	require.Len(t, envs, 2)
	dev, prod := envs[0], envs[1]
	assert.Equal(t, map[string]any{"id": 1.0, "name": "Dev", "status": "succeeded", "attempt": 1.0, "operationStatus": "Approved"},
		map[string]any{"id": dev["id"], "name": dev["name"], "status": dev["status"], "attempt": dev["attempt"], "operationStatus": dev["operationStatus"]})
	assert.Equal(t, []any{
		map[string]any{"id": 1.0, "type": "preDeploy", "status": "approved", "approver": nil, "isAutomated": true, "comments": ""},
		map[string]any{"id": 3.0, "type": "postDeploy", "status": "approved", "approver": nil, "isAutomated": true, "comments": ""},
	}, dev["approvals"])
	job := dev["jobs"].([]any)[0].(map[string]any)
	assert.Equal(t, "Agentless job", job["name"])
	assert.Equal(t, "Job", job["type"])
	assert.Equal(t, 2.0, job["duration"])
	task := job["children"].([]any)[0].(map[string]any)
	assert.Equal(t, map[string]any{
		"name": "Delay", "type": "Task", "state": "completed", "result": "succeeded",
		"startTime": "2026-10-12T06:58:13.29Z", "finishTime": "2026-10-12T06:58:13.58Z", "duration": 0.0,
		"attempt": 0.0, "logId": nil, "issues": []any{}, "children": []any{},
	}, task)

	assert.Equal(t, "inProgress", prod["status"])
	assert.Equal(t, "Pending", prod["operationStatus"])
	assert.Equal(t, []any{}, prod["jobs"])
	assert.Equal(t, []any{map[string]any{
		"id": 4.0, "type": "preDeploy", "status": "pending", "isAutomated": false, "comments": "",
		"approver": map[string]any{"id": "00000000-0000-0000-0000-000000000940", "displayName": "Test User", "uniqueName": ""},
	}}, prod["approvals"])
}

func TestNewTreeFailed(t *testing.T) {
	tr := NewTree(relTreeFixture(t, "release_failed_task.json"))
	assert.Equal(t, `✓ Dev in 15s
  ✓ Agent job 1 in 10s
    ✓ Initialize job in 1s
    ✓ Bash in 8s
  ✓ Agent job 2 in 15s
    ✓ Initialize job in 1s
● QA
  ⏸ Agent job
X Prod (attempt 2) in 30s
  X Agent job in 30s
    ✓ Initialize job in 2s
    X Run script in 27s
    - Publish
`, relTreeText(tr.Nodes))

	assert.Equal(t, []pshared.Annotation{
		{Issue: pshared.Issue{Type: "error", Message: "Deployment of Prod failed."}, Path: []string{"Prod"}},
		{Issue: pshared.Issue{Type: "error", Message: "Bash exited with code '1'."}, Path: []string{"Prod", "Agent job", "Run script"}},
	}, pshared.Annotations(tr.Nodes))

	var names []string
	for _, l := range pshared.TaskLogs(tr.Nodes, pshared.LogFilter{}) {
		names = append(names, l.Stage+"/"+l.Job+"/"+l.Task.Name)
	}
	assert.Equal(t, []string{
		"Dev/Agent job 1/Initialize job", "Dev/Agent job 1/Bash", "Dev/Agent job 2/Initialize job",
		"Prod/Agent job/Initialize job", "Prod/Agent job/Run script",
	}, names)
	assert.Equal(t, 2, tr.Environments[2].Attempt)
	assert.Equal(t, "PhaseFailed", tr.Environments[2].OperationStatus)
}

func TestTreeEnvironmentIndex(t *testing.T) {
	tr := NewTree(relTreeFixture(t, "release_failed_task.json"))
	for ref, want := range map[string]int{"Dev": 0, "qa": 1, "6": 2} {
		i, err := tr.EnvironmentIndex(ref, 3)
		require.NoError(t, err, ref)
		assert.Equal(t, want, i, ref)
	}
	_, err := tr.EnvironmentIndex("Staging", 3)
	require.EqualError(t, err, `no environment "Staging" in release 3; its environments: Dev, QA, Prod`)
}

func TestStatusOf(t *testing.T) {
	tests := map[string][2]string{
		"succeeded": {"completed", "succeeded"}, "success": {"completed", "succeeded"},
		"failed": {"completed", "failed"}, "failure": {"completed", "failed"},
		"rejected": {"completed", "rejected"}, "partiallySucceeded": {"completed", "partiallySucceeded"},
		"canceled": {"completed", "canceled"}, "skipped": {"completed", "skipped"},
		"inProgress": {"inProgress", ""}, "queued": {"queued", ""}, "": {"", ""},
	}
	for in, want := range tests {
		state, result := statusOf(in)
		assert.Equal(t, want, [2]string{state, result}, in)
	}
}

// relTreeFakeClient answers GetTaskLog with the route IDs it was called with.
type relTreeFakeClient struct {
	release.Client
}

func (relTreeFakeClient) GetTaskLog(_ context.Context, a release.GetTaskLogArgs) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(strings.Join([]string{
		*a.Project, strconv.Itoa(*a.ReleaseId), strconv.Itoa(*a.EnvironmentId), strconv.Itoa(*a.ReleaseDeployPhaseId), strconv.Itoa(*a.TaskId),
	}, " ") + "\n")), nil
}

func TestTreeLogOpener(t *testing.T) {
	tr := NewTree(relTreeFixture(t, "release_failed_task.json"))
	c := &api.ReleaseClient{Project: "proj", Release: relTreeFakeClient{}}
	var b bytes.Buffer
	logs := pshared.TaskLogs(tr.Nodes, pshared.LogFilter{Failed: true})
	require.NoError(t, pshared.PrintLogs(context.Background(), &b, tr.LogOpener(c, 3), logs))
	assert.Equal(t, "Agent job\tRun script\tproj 3 6 7 3\n", b.String())
}

// TestTreeSameNamedPhasesWriteApart: two deploy phases left at the editor's default name
// "Agent job", each with job ID 1, get distinct job nodes, so `release logs -o` writes both.
func TestTreeSameNamedPhasesWriteApart(t *testing.T) {
	str := func(s string) *string { return &s }
	num := func(n int) *int { return &n }
	phase := func(id int) release.ReleaseDeployPhase {
		status := release.TaskStatusValues.Succeeded
		return release.ReleaseDeployPhase{Id: num(id), Name: str("Agent job"), DeploymentJobs: &[]release.DeploymentJob{{
			Job:   &release.ReleaseTask{Id: num(1), Name: str("Agent job"), Status: &status},
			Tasks: &[]release.ReleaseTask{{Id: num(3), Name: str("Run"), Status: &status, LogUrl: str("https://x/log")}},
		}}}
	}
	envStatus := release.EnvironmentStatusValues.Succeeded
	r := release.Release{Environments: &[]release.ReleaseEnvironment{{
		Id: num(5), Name: str("Prod"), Status: &envStatus,
		DeploySteps: &[]release.DeploymentAttempt{{Attempt: num(1), ReleaseDeployPhases: &[]release.ReleaseDeployPhase{phase(10), phase(11)}}},
	}}}
	tree := NewTree(r)
	jobs := tree.Nodes[0].Children
	require.Len(t, jobs, 2)
	assert.NotEqual(t, jobs[0].ID, jobs[1].ID)

	dir := t.TempDir()
	open := func(_ context.Context, l pshared.TaskLog) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(l.JobNode.ID)), nil
	}
	require.NoError(t, pshared.WriteLogs(context.Background(), dir, open, pshared.TaskLogs(tree.Nodes, pshared.LogFilter{})))
	for rel, want := range map[string]string{"Prod/Agent job/01_Run.log": "10.1", "Prod/Agent job_2/01_Run.log": "11.1"} {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		require.NoError(t, err, rel)
		assert.Equal(t, want, string(b), rel)
	}
}

func TestPendingApprovals(t *testing.T) {
	envs := []Environment{
		{Name: "Dev", Approvals: []Approval{
			{ID: 1, Type: "preDeploy", Status: "approved"},
			{ID: 2, Type: "postDeploy", Status: "pending", Automated: true},
		}},
		{Name: "Prod", Approvals: []Approval{
			{ID: 3, Type: "preDeploy", Status: "pending"},
			{ID: 4, Type: "postDeploy", Status: "pending"},
		}},
	}
	got := PendingApprovals(envs)
	assert.Equal(t, []pshared.Pending{
		{ID: "release-approval-3", Type: "Checkpoint.Approval", Name: "preDeploy", Stage: "Prod", State: "pending"},
		{ID: "release-approval-4", Type: "Checkpoint.Approval", Name: "postDeploy", Stage: "Prod", State: "pending"},
	}, got, "only manual approvals still pending")
	assert.Equal(t, "approval pending", got[0].Label())
	assert.Empty(t, PendingApprovals(envs[:1]))
}
