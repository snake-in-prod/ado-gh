package shared

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

func loadTimelineRecords(t *testing.T, name string) []build.TimelineRecord {
	t.Helper()
	var tl build.Timeline
	if err := json.Unmarshal(pipelinefixtures.Read(t, name), &tl); err != nil {
		t.Fatal(err)
	}
	return *tl.Records
}

// timelineOutline lists nodes as "<indent><type> <name>", depth first.
func timelineOutline(nodes []*Node) []string {
	var out []string
	var visit func([]*Node, string)
	visit = func(nodes []*Node, indent string) {
		for _, n := range nodes {
			out = append(out, indent+n.Type+" "+n.Name)
			visit(n.Children, indent+"  ")
		}
	}
	visit(nodes, "")
	return out
}

func assertTimelineLines(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestNewTimelineShapes(t *testing.T) {
	tests := []struct {
		fixture string
		want    []string
	}{
		{"timeline_yaml_stages.json", []string{
			"Stage Build",
			"  Job Build",
			"    Task Initialize job",
			"    Task Checkout ado-gh@main to s",
			"    Task go build",
			"    Task Publish webapp",
			"    Task Finalize Job",
			"Stage Test",
			"  Job Unit tests",
			"    Task Initialize job",
			"    Task Checkout ado-gh@main to s",
			"    Task go test",
			"    Task Publish coverage",
			"    Task Finalize Job",
			"Stage Deploy",
			"  Job Deploy web",
		}},
		// The implicit stage is hidden: its jobs are roots, as with classic builds.
		{"timeline_default_stage.json", []string{
			"Job Lint",
			"  Task Initialize job",
			"  Task Checkout ado-gh@main to s",
			"  Task golangci-lint",
			"  Task Finalize Job",
			"Job Build",
			"  Task Initialize job",
			"  Task Checkout ado-gh@main to s",
			"  Task go build",
			"  Task Finalize Job",
		}},
		{"timeline_classic.json", []string{
			"Job Agent job 1",
			"  Task Initialize job",
			"  Task Get sources",
			"  Task npm install",
			"  Task Publish Artifact: drop",
			"  Task Finalize Job",
			"Job Agent job 2",
			"  Task Initialize job",
			"  Task Command Line Script",
		}},
		// Captured live (2026-10-12): jobs carry ".__default" identifiers and same-named
		// tasks in different jobs have no identifier; all are kept. A phase waiting for an
		// approval has no Job record yet and stands in for its job.
		{"live_timeline_pending_approval.json", []string{
			"Stage Build",
			"  Job compile",
			"    Task Initialize job",
			"    Task Checkout ado-live@main to s",
			"    Task say hello",
			"    Task PublishPipelineArtifact",
			"    Task Post-job: Checkout ado-live@main to s",
			"    Task Finalize Job",
			"Stage Gate",
			"  Job wait",
			"    Task Delay",
			"Stage Fail",
			"  Job boom",
			"    Task Initialize job",
			"    Task Checkout ado-live@main to s",
			"    Task fail on purpose",
			"    Task Post-job: Checkout ado-live@main to s",
			"Stage Deploy",
			"  Job deploy",
		}},
		// Checkpoint records are hidden.
		{"timeline_checkpoint.json", []string{
			"Stage Build",
			"  Job Build",
			"    Task Initialize job",
			"    Task Checkout ado-gh@main to s",
			"    Task go build",
			"    Task Finalize Job",
			"Stage Deploy",
			"  Job Deploy web",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			tl := NewTimeline(loadTimelineRecords(t, tt.fixture))
			assertTimelineLines(t, timelineOutline(tl.Nodes), tt.want)
		})
	}
}

func TestNewTimelineLivePendingApproval(t *testing.T) {
	tl := NewTimeline(loadTimelineRecords(t, "live_timeline_pending_approval.json"))
	var approvals []string
	for _, p := range tl.Pending {
		if p.IsApproval() {
			approvals = append(approvals, p.ID)
		}
	}
	// The approval's identifier is the approval id the approvals API expects.
	if want := []string{"00000000-0000-0000-0000-000000000930"}; !slices.Equal(approvals, want) {
		t.Errorf("pending approvals = %v, want %v", approvals, want)
	}
}

func TestNewTimelineLiveFailed(t *testing.T) {
	tl := NewTimeline(loadTimelineRecords(t, "live_timeline_failed.json"))
	anns := Annotations(tl.Nodes)
	if len(anns) != 1 {
		t.Fatalf("annotations = %+v, want 1", anns)
	}
	got := strings.Join(anns[0].Path, " / ") + ": " + anns[0].Issue.Message
	if want := "Fail / boom / fail on purpose: Bash exited with code '1'."; got != want {
		t.Errorf("annotation = %q, want %q", got, want)
	}
	if len(tl.Pending) != 0 {
		t.Errorf("pending = %+v, want none after completion", tl.Pending)
	}
}

func TestNewTimelineNodeFields(t *testing.T) {
	tl := NewTimeline(loadTimelineRecords(t, "timeline_yaml_stages.json"))
	test := tl.Nodes[1]
	if test.Identifier != "Test" || test.State != "completed" || test.Result != "failed" || test.Attempt != 1 {
		t.Errorf("stage = %+v", test)
	}
	if test.ID != "00000000-0000-0000-0000-000000010109" {
		t.Errorf("ID = %q", test.ID)
	}
	if test.LogID != nil {
		t.Errorf("stage LogID = %v, want nil", *test.LogID)
	}
	if d := test.Duration(); d.Seconds() != 29 {
		t.Errorf("Duration = %v, want 29s", d)
	}
	goTest := test.Children[0].Children[2]
	if goTest.LogID == nil || *goTest.LogID != 10 {
		t.Errorf("go test LogID = %v", goTest.LogID)
	}
	want := []Issue{{"warning", "coverage below threshold: 61.2% < 80%"}, {"error", "Bash exited with code '1'."}}
	if !slices.Equal(goTest.Issues, want) {
		t.Errorf("Issues = %v", goTest.Issues)
	}
	skipped := test.Children[0].Children[3]
	if skipped.Duration() != 0 || skipped.StartTime != nil || skipped.Result != "skipped" {
		t.Errorf("skipped task = %+v", skipped)
	}

	running := NewTimeline(loadTimelineRecords(t, "timeline_in_progress.json")).Nodes[0]
	if running.Duration() != 0 || running.Result != "" || running.State != "inProgress" {
		t.Errorf("running stage = %+v", running)
	}
}

func TestNewTimelineRetried(t *testing.T) {
	tl := NewTimeline(loadTimelineRecords(t, "timeline_retried.json"))
	if got := tl.Nodes[1]; got.Name != "Test" || got.Attempt != 2 {
		t.Errorf("Test stage = %+v", got)
	}
}

type tlRec struct {
	id, parent, typ, name, identifier, state string
	order, attempt                           int
}

func timelineRecordsOf(recs ...tlRec) []build.TimelineRecord {
	id := func(s string) *uuid.UUID {
		u := uuid.NewSHA1(uuid.Nil, []byte(s))
		return &u
	}
	out := make([]build.TimelineRecord, len(recs))
	for i, r := range recs {
		state := build.TimelineRecordState(r.state)
		out[i] = build.TimelineRecord{
			Id: id(r.id), Type: &r.typ, Name: &r.name, Order: &r.order, Attempt: &r.attempt, State: &state,
		}
		if r.parent != "" {
			out[i].ParentId = id(r.parent)
		}
		if r.identifier != "" {
			out[i].Identifier = &r.identifier
		}
	}
	return out
}

func TestNewTimelineDedupe(t *testing.T) {
	recs := timelineRecordsOf(
		// Both attempts of a stage side by side: the latest wins, with its subtree only.
		tlRec{id: "s1", typ: "Stage", name: "Test", identifier: "Test", order: 2, attempt: 1},
		tlRec{id: "j1", parent: "s1", typ: "Job", name: "old job", identifier: "Test.J", order: 1, attempt: 1},
		tlRec{id: "s2", typ: "Stage", name: "Test", identifier: "Test", order: 2, attempt: 2},
		tlRec{id: "j2", parent: "s2", typ: "Job", name: "new job", identifier: "Test.J", order: 1, attempt: 2},
		// Same-named tasks of one attempt are distinct steps; a retried one without an
		// identifier is deduped by name.
		tlRec{id: "t1", parent: "j2", typ: "Task", name: "Bash", order: 2, attempt: 1},
		tlRec{id: "t2", parent: "j2", typ: "Task", name: "Bash", order: 1, attempt: 1},
		tlRec{id: "t3", parent: "j2", typ: "Task", name: "Retry me", order: 3, attempt: 1},
		tlRec{id: "t4", parent: "j2", typ: "Task", name: "Retry me", order: 3, attempt: 2},
		// A record whose parent is missing is a root; one that is its own parent is unreachable.
		tlRec{id: "o", parent: "gone", typ: "Job", name: "orphan", order: 1, attempt: 1},
		tlRec{id: "loop", parent: "loop", typ: "Job", name: "loop", order: 0, attempt: 1},
	)
	tl := NewTimeline(recs)
	assertTimelineLines(t, timelineOutline(tl.Nodes), []string{
		"Job orphan",
		"Stage Test",
		"  Job new job",
		"    Task Bash",
		"    Task Bash",
		"    Task Retry me",
	})
	if tl.Nodes[1].Attempt != 2 || tl.Nodes[1].Children[0].Children[2].Attempt != 2 {
		t.Error("kept an older attempt")
	}
	// Records without an id are ignored.
	if got := NewTimeline([]build.TimelineRecord{{}}); len(got.Nodes) != 0 {
		t.Errorf("Nodes = %v", got.Nodes)
	}
}

func TestNewTimelinePending(t *testing.T) {
	tl := NewTimeline(loadTimelineRecords(t, "timeline_checkpoint.json"))
	want := []Pending{
		{ID: "00000000-0000-0000-0000-000000010599", Type: "Checkpoint.Approval", Name: "Checkpoint.Approval", Stage: "Deploy", State: "inProgress"},
		{ID: "00000000-0000-0000-0000-000000010598", Type: "Checkpoint.TaskCheck", Name: "Business Hours", Stage: "Deploy", State: "inProgress"},
	}
	if !slices.Equal(tl.Pending, want) {
		t.Errorf("Pending = %+v", tl.Pending)
	}
	if !tl.Pending[0].IsApproval() || tl.Pending[1].IsApproval() {
		t.Error("IsApproval")
	}

	recs := timelineRecordsOf(
		tlRec{id: "s", typ: "Stage", name: "__default", identifier: "__default", order: 1, attempt: 1},
		tlRec{id: "c", parent: "s", typ: "Checkpoint", name: "Checkpoint", state: "inProgress", order: 1, attempt: 1},
		// No identifier: keyed by id.
		tlRec{id: "a", parent: "c", typ: "Checkpoint.Approval", name: "Checkpoint.Approval", state: "pending", order: 1, attempt: 1},
		tlRec{id: "done", parent: "c", typ: "Checkpoint.Approval", name: "Checkpoint.Approval", identifier: "done", state: "completed", order: 2, attempt: 1},
		tlRec{id: "x", parent: "c", typ: "Checkpoint.ExtendsCheck", name: "Template check", identifier: "x", state: "inProgress", order: 3, attempt: 1},
	)
	tl = NewTimeline(recs)
	if len(tl.Nodes) != 0 {
		t.Errorf("Nodes = %v", timelineOutline(tl.Nodes))
	}
	aID := uuid.NewSHA1(uuid.Nil, []byte("a")).String()
	want = []Pending{
		{ID: aID, Type: "Checkpoint.Approval", Name: "Checkpoint.Approval", State: "pending"},
		{ID: "x", Type: "Checkpoint.ExtendsCheck", Name: "Template check", State: "inProgress"},
	}
	if !slices.Equal(tl.Pending, want) {
		t.Errorf("Pending = %+v", tl.Pending)
	}
}

func TestPrintTimelineTree(t *testing.T) {
	yaml := NewTimeline(loadTimelineRecords(t, "timeline_yaml_stages.json")).Nodes
	tests := []struct {
		name    string
		nodes   []*Node
		verbose bool
		want    string
	}{
		// Failed jobs expand to their failed tasks.
		{"default", yaml, false, `✓ Build in 20s
  ✓ Build in 19s
X Test in 29s
  X Unit tests in 28s
    X go test in 20s
- Deploy
  - Deploy web
`},
		{"verbose", yaml[1:2], true, `X Test in 29s
  X Unit tests in 28s
    ✓ Initialize job in 2s
    ✓ Checkout ado-gh@main to s in 4s
    X go test in 20s
    - Publish coverage
    ✓ Finalize Job in 1s
`},
		{"retried", NewTimeline(loadTimelineRecords(t, "timeline_retried.json")).Nodes[1:], false, `✓ Test (attempt 2) in 40s
  ✓ Unit tests (attempt 2) in 39s
`},
		{"running", NewTimeline(loadTimelineRecords(t, "timeline_in_progress.json")).Nodes, true, `● Build
  ● Build
    ✓ Initialize job in 2s
    ✓ Checkout ado-gh@main to s in 4s
    ● go build
    ⏸ Publish webapp
    ⏸ Finalize Job
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, _, _ := iostreams.Test()
			var buf bytes.Buffer
			PrintTree(&buf, ios.ColorScheme(), tt.nodes, tt.verbose)
			if buf.String() != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", buf.String(), tt.want)
			}
		})
	}
}

func TestTimelineAnnotations(t *testing.T) {
	nodes := NewTimeline(loadTimelineRecords(t, "timeline_yaml_stages.json")).Nodes
	// The job repeats its task's error; only the task's copy is kept.
	got := Annotations(nodes)
	path := []string{"Test", "Unit tests", "go test"}
	if len(got) != 2 || !slices.Equal(got[0].Path, path) || !slices.Equal(got[1].Path, path) ||
		got[0].Issue != (Issue{"warning", "coverage below threshold: 61.2% < 80%"}) ||
		got[1].Issue != (Issue{"error", "Bash exited with code '1'."}) {
		t.Errorf("Annotations = %+v", got)
	}

	// An issue only on the job is kept there.
	job := &Node{Name: "job", Issues: []Issue{{"error", "job only"}}, Children: []*Node{{Name: "task"}}}
	if got := Annotations([]*Node{job}); len(got) != 1 || !slices.Equal(got[0].Path, []string{"job"}) {
		t.Errorf("Annotations = %+v", got)
	}

	ios, _, _, _ := iostreams.Test()
	var buf bytes.Buffer
	PrintAnnotations(&buf, ios.ColorScheme(), Annotations(nodes))
	want := `! coverage below threshold: 61.2% < 80%
  Test / Unit tests / go test
X Bash exited with code '1'.
  Test / Unit tests / go test
`
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestPrintTimelineWaiting(t *testing.T) {
	pending := []Pending{
		{ID: "1", Type: "Checkpoint.Approval", Name: "Checkpoint.Approval", Stage: "Deploy", State: "inProgress"},
		{ID: "2", Type: "Checkpoint.TaskCheck", Name: "Business Hours", Stage: "Deploy", State: "pending"},
		{ID: "3", Type: "Checkpoint.Unknown", Name: "Something new", State: "inProgress"},
	}
	ios, _, _, _ := iostreams.Test()
	var buf bytes.Buffer
	PrintWaiting(&buf, ios.ColorScheme(), pending)
	want := `● Deploy: approval pending
⏸ Deploy: waiting: Business Hours
● waiting: Something new
`
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestTimelineNodeJSON(t *testing.T) {
	test := NewTimeline(loadTimelineRecords(t, "timeline_yaml_stages.json")).Nodes[1]
	b, err := json.Marshal(test)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	keys := []string{"attempt", "children", "duration", "finishTime", "issues", "logId", "name", "result", "startTime", "state", "type"}
	gotKeys := make([]string, 0, len(got))
	for k := range got {
		gotKeys = append(gotKeys, k)
	}
	slices.Sort(gotKeys)
	if !slices.Equal(gotKeys, keys) {
		t.Errorf("keys = %v", gotKeys)
	}
	if got["name"] != "Test" || got["type"] != "Stage" || got["result"] != "failed" || got["duration"] != 29.0 ||
		got["logId"] != nil || got["attempt"] != 1.0 || got["startTime"] != "2026-10-01T10:00:21.1233333Z" {
		t.Errorf("stage JSON = %s", b)
	}
	if issues, ok := got["issues"].([]any); !ok || len(issues) != 0 {
		t.Errorf("issues = %v, want []", got["issues"])
	}
	job := got["children"].([]any)[0].(map[string]any)
	goTest := job["children"].([]any)[2].(map[string]any)
	if goTest["logId"] != 10.0 || len(goTest["issues"].([]any)) != 2 {
		t.Errorf("task JSON = %v", goTest)
	}
	issue := goTest["issues"].([]any)[0].(map[string]any)
	if issue["type"] != "warning" || issue["message"] != "coverage below threshold: 61.2% < 80%" {
		t.Errorf("issue JSON = %v", issue)
	}
	skipped := job["children"].([]any)[3].(map[string]any)
	if skipped["startTime"] != nil || skipped["duration"] != 0.0 {
		t.Errorf("skipped JSON = %v", skipped)
	}
	if c, ok := skipped["children"].([]any); !ok || len(c) != 0 {
		t.Errorf("children = %v, want []", skipped["children"])
	}
}
