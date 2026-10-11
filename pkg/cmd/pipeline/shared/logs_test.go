package shared

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// taskLogOutline lists task logs as "<stage>/<job>/<index> <task> #<log id>".
func taskLogOutline(logs []TaskLog) []string {
	var out []string
	for _, l := range logs {
		out = append(out, fmt.Sprintf("%s/%s/%d %s #%d", l.Stage, l.Job, l.Index, l.Task.Name, *l.Task.LogID))
	}
	return out
}

func TestTaskLogsFilters(t *testing.T) {
	yaml := NewTimeline(loadTimelineRecords(t, "timeline_yaml_stages.json")).Nodes
	unitTests := []string{
		"Test/Unit tests/1 Initialize job #8",
		"Test/Unit tests/2 Checkout ado-gh@main to s #9",
		"Test/Unit tests/3 go test #10",
		"Test/Unit tests/5 Finalize Job #11",
	}
	tests := []struct {
		name   string
		filter LogFilter
		want   []string
	}{
		// Job logs (#1, #7) repeat their tasks' output and the skipped task has no log.
		{"all", LogFilter{}, append([]string{
			"Build/Build/1 Initialize job #2",
			"Build/Build/2 Checkout ado-gh@main to s #3",
			"Build/Build/3 go build #4",
			"Build/Build/4 Publish webapp #5",
			"Build/Build/5 Finalize Job #6",
		}, unitTests...)},
		{"failed", LogFilter{Failed: true}, []string{"Test/Unit tests/3 go test #10"}},
		{"job name", LogFilter{Job: "unit TESTS"}, unitTests},
		{"job id", LogFilter{Job: "00000000-0000-0000-0000-000000010111"}, unitTests},
		{"task name", LogFilter{Task: "Go Build"}, []string{"Build/Build/3 go build #4"}},
		{"task id", LogFilter{Task: "00000000-0000-0000-0000-000000010113"}, []string{"Test/Unit tests/2 Checkout ado-gh@main to s #9"}},
		{"job and task", LogFilter{Job: "Build", Task: "Initialize job"}, []string{"Build/Build/1 Initialize job #2"}},
		{"failed in job", LogFilter{Job: "Build", Failed: true}, nil},
		{"unknown job", LogFilter{Job: "nope"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTimelineLines(t, taskLogOutline(TaskLogs(yaml, tt.filter)), tt.want)
		})
	}
}

func TestTaskLogsClassicAndIncomplete(t *testing.T) {
	classic := NewTimeline(loadTimelineRecords(t, "timeline_classic.json")).Nodes
	got := taskLogOutline(TaskLogs(classic, LogFilter{Job: "Agent job 2"}))
	assertTimelineLines(t, got, []string{"/Agent job 2/1 Initialize job #8", "/Agent job 2/2 Command Line Script #9"})

	logID := func(i int) *int { return &i }
	job := &Node{Name: "j", Type: "Job", Children: []*Node{
		{Name: "running", Type: "Task", State: "inProgress", LogID: logID(3)},
		{Name: "server", Type: "Task", State: "completed", LogID: logID(0)},
		{Name: "pending", Type: "Task", State: "completed"},
		{Name: "done", Type: "Task", State: "completed", LogID: logID(4)},
	}}
	// In-progress tasks may hold only a page of their log; agentless tasks report log 0.
	assertTimelineLines(t, taskLogOutline(TaskLogs([]*Node{job}, LogFilter{})), []string{"/j/4 done #4"})
}

// fakeLogs opens the log of each task from its contents by log id, recording the ids opened.
type fakeLogs struct {
	text   map[int]string
	opened []int
}

func (f *fakeLogs) open(_ context.Context, l TaskLog) (io.ReadCloser, error) {
	f.opened = append(f.opened, *l.Task.LogID)
	s, ok := f.text[*l.Task.LogID]
	if !ok {
		return nil, errors.New("boom")
	}
	return io.NopCloser(strings.NewReader(s)), nil
}

func taskLogOf(stage, job string, index int, task string, logID int) TaskLog {
	return TaskLog{Stage: stage, Job: job, Index: index, Task: &Node{Name: task, Type: "Task", LogID: &logID}}
}

func TestPrintLogsPrefixesLines(t *testing.T) {
	long := strings.Repeat("x", 70*1024)
	f := &fakeLogs{text: map[int]string{
		2: "\ufeffstart\n" + long + "\nend\n",
		3: "no trailing newline",
		4: "",
	}}
	var out bytes.Buffer
	err := PrintLogs(context.Background(), &out, f.open, []TaskLog{
		taskLogOf("Build", "Build", 1, "go build", 2),
		taskLogOf("", "Agent job 1", 2, "npm", 3),
		taskLogOf("", "Agent job 1", 3, "empty", 4),
	})
	require.NoError(t, err)
	assert.Equal(t, []int{2, 3, 4}, f.opened)
	want := "Build\tgo build\tstart\n" +
		"Build\tgo build\t" + long + "\n" +
		"Build\tgo build\tend\n" +
		"Agent job 1\tnpm\tno trailing newline\n"
	assert.Equal(t, want, out.String())
}

func TestPrintLogsOpenError(t *testing.T) {
	f := &fakeLogs{text: map[int]string{2: "a\n"}}
	var out bytes.Buffer
	err := PrintLogs(context.Background(), &out, f.open, []TaskLog{
		taskLogOf("Build", "Build", 1, "ok", 2), taskLogOf("Build", "Build", 2, "go test", 9),
	})
	assert.EqualError(t, err, "log of Build / go test: boom")
	assert.Equal(t, "Build\tok\ta\n", out.String())
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("reset") }

func TestPrintLogsReadError(t *testing.T) {
	open := func(context.Context, TaskLog) (io.ReadCloser, error) { return io.NopCloser(failingReader{}), nil }
	err := PrintLogs(context.Background(), io.Discard, open, []TaskLog{taskLogOf("", "j", 1, "t", 1)})
	assert.EqualError(t, err, "log of j / t: reset")
}

func TestBuildLogOpenerStreams(t *testing.T) {
	org := "logsfetchorg"
	c, reg := newRefTestClient(t, org)
	base := org + "/proj/_apis/build/builds/101/logs/"
	var accept []string
	for id, text := range map[int]string{4: "compiling\n", 10: "FAIL\n"} {
		reg.Register(httpmock.REST(http.MethodGet, fmt.Sprintf("%s%d", base, id)), func(r *http.Request) (*http.Response, error) {
			accept = append(accept, r.Header.Get("Accept"))
			return httpmock.StringResponse(text)(r)
		})
	}
	nodes := NewTimeline(loadTimelineRecords(t, "timeline_yaml_stages.json")).Nodes
	logs := append(TaskLogs(nodes, LogFilter{Task: "go build"}), TaskLogs(nodes, LogFilter{Failed: true})...)

	var out bytes.Buffer
	require.NoError(t, PrintLogs(context.Background(), &out, BuildLogOpener(c, 101), logs))
	assert.Equal(t, "Build\tgo build\tcompiling\nUnit tests\tgo test\tFAIL\n", out.String())
	for _, a := range accept {
		assert.Contains(t, a, "text/plain", "streamed as text, never GetBuildLogLines")
	}
}

func TestWriteLogsLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	f := &fakeLogs{text: map[int]string{2: "\ufeffinit\n", 3: "new", 4: "x"}}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "Build", "Build"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Build", "Build", "02_a_b.log"), []byte("old content"), 0o644))

	err := WriteLogs(context.Background(), dir, f.open, []TaskLog{
		taskLogOf("Build", "Build", 1, "Initialize job", 2),
		taskLogOf("Build", "Build", 2, "a/b", 3),
		taskLogOf("..", "CON", 12, `..\..\evil:*.`, 4),
	})
	require.NoError(t, err)
	for rel, want := range map[string]string{
		"Build/Build/01_Initialize job.log": "\ufeffinit\n",
		"Build/Build/02_a_b.log":            "new",
		"_/_CON/12_.._.._evil__.log":        "x",
	} {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if assert.NoError(t, err, rel) {
			assert.Equal(t, want, string(b), rel)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(dir))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "nothing written beside the output directory")
}

func TestWriteLogsErrors(t *testing.T) {
	f := &fakeLogs{text: map[int]string{}}
	dir := t.TempDir()
	err := WriteLogs(context.Background(), dir, f.open, []TaskLog{taskLogOf("S", "J", 1, "t", 5)})
	assert.EqualError(t, err, "log of J / t: boom")

	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	assert.Error(t, WriteLogs(context.Background(), file, f.open, nil), "output path is a file")

	open := func(context.Context, TaskLog) (io.ReadCloser, error) { return io.NopCloser(failingReader{}), nil }
	err = WriteLogs(context.Background(), dir, open, []TaskLog{taskLogOf("S", "J", 1, "t", 5)})
	assert.EqualError(t, err, "log of J / t: reset")

	// A stage directory name taken by a file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "X"), nil, 0o644))
	f.text[5] = "x"
	err = WriteLogs(context.Background(), dir, f.open, []TaskLog{taskLogOf("X", "J", 1, "t", 5)})
	assert.ErrorContains(t, err, "log of J / t:")
}
