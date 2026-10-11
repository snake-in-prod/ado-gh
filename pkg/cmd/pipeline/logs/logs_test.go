package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

type plLogsEnv struct {
	org    string
	reg    *httpmock.Registry
	ios    *iostreams.IOStreams
	in     *bytes.Buffer
	out    *bytes.Buffer
	errOut *bytes.Buffer
	// polls are the times of the build GETs, in order.
	polls []time.Time
	// runQueries are the query strings of the build list requests, in order.
	runQueries []url.Values
}

// newPLLogsEnv returns an env for its own org (the SDK caches discovery per base URL).
func newPLLogsEnv(t *testing.T, tty bool) *plLogsEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_REPO", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	ios.In = iotest.OneByteReader(in)
	org := "pllogs-" + strings.NewReplacer("/", "-", " ", "-", "_", "-", ",", "-").Replace(strings.ToLower(t.Name()))
	e := &plLogsEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut}
	e.reg.StubLocations(org, slices.Concat(httpmock.PipelineLocations, httpmock.GitRepoLocations)...)
	return e
}

func (e *plLogsEnv) path(p string) string { return e.org + "/My%20Proj/_apis/" + p }

// stubTimeline answers the next timeline request of run id with body.
func (e *plLogsEnv) stubTimeline(id int, body any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.path(fmt.Sprintf("build/builds/%d/timeline", id))), httpmock.JSONResponse(body))
}

// stubLogs answers one request for each log id of run id with "<id>a\n<id>b\n".
func (e *plLogsEnv) stubLogs(id int, logIDs ...int) {
	for _, l := range logIDs {
		e.reg.Register(httpmock.REST(http.MethodGet, e.path(fmt.Sprintf("build/builds/%d/logs/%d", id, l))),
			httpmock.StringResponse(fmt.Sprintf("%da\n%db\n", l, l)))
	}
}

// stubStatus answers the next GET of run id with status, recording when it came.
func (e *plLogsEnv) stubStatus(id int, status string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.path(fmt.Sprintf("build/builds/%d", id))), func(r *http.Request) (*http.Response, error) {
		e.polls = append(e.polls, time.Now())
		return httpmock.JSONResponse(map[string]any{"id": id, "status": status})(r)
	})
}

func (e *plLogsEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	return e.runCtx(context.Background(), t, args...)
}

func (e *plLogsEnv) runCtx(ctx context.Context, t *testing.T, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "web"}, nil
		},
		Prompter: &cmdutil.Prompter{IO: e.ios, Accessible: true},
		GitClient: &git.Client{Run: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("refs/heads/main\n"), nil
		}},
	}
	cmd := NewCmdLogs(f)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.ExecuteContext(ctx)
}

// plLogsTimeline returns the timeline fixture name with the fields of the records whose id ends
// in a key of edits replaced.
func plLogsTimeline(t *testing.T, name string, edits map[string]map[string]any) map[string]any {
	t.Helper()
	var tl map[string]any
	require.NoError(t, json.Unmarshal(pipelinefixtures.Read(t, name), &tl))
	for _, r := range tl["records"].([]any) {
		rec := r.(map[string]any)
		for suffix, fields := range edits {
			if strings.HasSuffix(rec["id"].(string), suffix) {
				for k, v := range fields {
					rec[k] = v
				}
			}
		}
	}
	return tl
}

func plLogsDone(logID int) map[string]any {
	return map[string]any{"state": "completed", "result": "succeeded", "log": map[string]any{"id": logID}}
}

// plLogsLines is the PrintLogs output of the stubbed logs, in order.
func plLogsLines(job string, logs ...any) string {
	var b strings.Builder
	for i := 0; i < len(logs); i += 2 {
		task, id := logs[i], logs[i+1]
		fmt.Fprintf(&b, "%s\t%s\t%da\n%s\t%s\t%db\n", job, task, id, job, task, id)
	}
	return b.String()
}

func TestLogsPrintsCompletedTaskLogs(t *testing.T) {
	e := newPLLogsEnv(t, false)
	e.stubTimeline(101, json.RawMessage(pipelinefixtures.Read(t, "timeline_yaml_stages.json")))
	// Job logs (1, 7) repeat their tasks' output; the skipped task has none.
	e.stubLogs(101, 2, 3, 4, 5, 6, 8, 9, 10, 11)
	require.NoError(t, e.run(t, "101"))
	e.reg.Verify(t)
	assert.Equal(t, plLogsLines("Build", "Initialize job", 2, "Checkout ado-gh@main to s", 3, "go build", 4, "Publish webapp", 5, "Finalize Job", 6)+
		plLogsLines("Unit tests", "Initialize job", 8, "Checkout ado-gh@main to s", 9, "go test", 10, "Finalize Job", 11), e.out.String())
	assert.Empty(t, e.errOut.String())
}

func TestLogsFilters(t *testing.T) {
	tests := []struct {
		name string
		args []string
		logs []int
		want string
	}{
		{"failed", []string{"--failed"}, []int{10}, plLogsLines("Unit tests", "go test", 10)},
		{"job", []string{"--job", "unit tests"}, []int{8, 9, 10, 11},
			plLogsLines("Unit tests", "Initialize job", 8, "Checkout ado-gh@main to s", 9, "go test", 10, "Finalize Job", 11)},
		{"job and task", []string{"--job", "Build", "--task", "go build"}, []int{4}, plLogsLines("Build", "go build", 4)},
		{"task in every job", []string{"--task", "Finalize Job"}, []int{6, 11},
			plLogsLines("Build", "Finalize Job", 6) + plLogsLines("Unit tests", "Finalize Job", 11)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newPLLogsEnv(t, false)
			e.stubTimeline(101, json.RawMessage(pipelinefixtures.Read(t, "timeline_yaml_stages.json")))
			e.stubLogs(101, tt.logs...)
			require.NoError(t, e.run(t, append([]string{"101"}, tt.args...)...))
			e.reg.Verify(t)
			assert.Equal(t, tt.want, e.out.String())
		})
	}
}

func TestLogsNoneMatch(t *testing.T) {
	e := newPLLogsEnv(t, false)
	e.stubTimeline(101, json.RawMessage(pipelinefixtures.Read(t, "timeline_yaml_stages.json")))
	require.NoError(t, e.run(t, "101", "--job", "Deploy web"))
	e.reg.Verify(t)
	assert.Empty(t, e.out.String())
	assert.Equal(t, "no task logs found\n", e.errOut.String())
}

func TestLogsEmptyTimeline(t *testing.T) {
	e := newPLLogsEnv(t, false)
	e.stubTimeline(101, map[string]any{})
	require.NoError(t, e.run(t, "101"))
	e.reg.Verify(t)
	assert.Equal(t, "no task logs found\n", e.errOut.String())
}

func TestLogsTimelineError(t *testing.T) {
	e := newPLLogsEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/builds/101/timeline")),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]string{"message": "Build 101 not found."}))
	err := e.run(t, "101")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Build 101 not found.")
}

func TestLogsOutputWritesTree(t *testing.T) {
	e := newPLLogsEnv(t, false)
	e.stubTimeline(101, json.RawMessage(pipelinefixtures.Read(t, "timeline_yaml_stages.json")))
	e.stubLogs(101, 8, 9, 10, 11)
	dir := filepath.Join(t.TempDir(), "out")
	require.NoError(t, e.run(t, "101", "--job", "Unit tests", "-o", dir))
	e.reg.Verify(t)
	assert.Empty(t, e.out.String())
	assert.Equal(t, "✓ Wrote 4 logs to "+dir+"\n", e.errOut.String())
	for name, id := range map[string]int{
		"01_Initialize job.log": 8, "02_Checkout ado-gh@main to s.log": 9, "03_go test.log": 10, "05_Finalize Job.log": 11,
	} {
		b, err := os.ReadFile(filepath.Join(dir, "Test", "Unit tests", name))
		require.NoError(t, err, name)
		assert.Equal(t, fmt.Sprintf("%da\n%db\n", id, id), string(b))
	}
	entries, err := os.ReadDir(filepath.Join(dir, "Test", "Unit tests"))
	require.NoError(t, err)
	assert.Len(t, entries, 4)
}

func TestLogsRunURLNamesProject(t *testing.T) {
	e := newPLLogsEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/Other/_apis/build/builds/101/timeline"),
		httpmock.JSONResponse(json.RawMessage(pipelinefixtures.Read(t, "timeline_yaml_stages.json"))))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/Other/_apis/build/builds/101/logs/4"), httpmock.StringResponse("built\n"))
	require.NoError(t, e.run(t, "https://dev.azure.com/"+e.org+"/Other/_build/results?buildId=101&view=logs", "--task", "go build"))
	e.reg.Verify(t)
	assert.Equal(t, "Build\tgo build\tbuilt\n", e.out.String())
}

func TestLogsInvalidRun(t *testing.T) {
	e := newPLLogsEnv(t, false)
	err := e.run(t, "abc")
	assert.ErrorContains(t, err, "invalid run")
	assert.Empty(t, e.reg.Requests)
}

func TestLogsNoRunNonTTY(t *testing.T) {
	e := newPLLogsEnv(t, false)
	err := e.run(t)
	var flagErr *cmdutil.FlagError
	require.ErrorAs(t, err, &flagErr)
	assert.EqualError(t, err, "<run> argument required when not running interactively")
	assert.Empty(t, e.reg.Requests)
}

// stubRunPrompt serves repository "web", defs as its pipelines and one recent run, 101.
func (e *plLogsEnv) stubRunPrompt(defs ...map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("git/repositories/web")),
		httpmock.JSONResponse(map[string]any{"id": "00000000-0000-0000-0000-000000000011", "name": "web"}))
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/definitions")),
		httpmock.JSONResponse(map[string]any{"count": len(defs), "value": defs}))
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/builds")), func(r *http.Request) (*http.Response, error) {
		e.runQueries = append(e.runQueries, r.URL.Query())
		return httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{{
			"id": 101, "buildNumber": "20261001.1", "status": "completed", "result": "failed",
			"sourceBranch": "refs/heads/main", "definition": map[string]any{"id": 1, "name": "ado-gh"},
		}}})(r)
	})
}

func TestLogsNoRunPrompts(t *testing.T) {
	t.Run("default pipeline on the current branch", func(t *testing.T) {
		e := newPLLogsEnv(t, true)
		e.stubRunPrompt(map[string]any{"id": 1, "name": "ado-gh", "path": `\`})
		e.stubTimeline(101, json.RawMessage(pipelinefixtures.Read(t, "timeline_yaml_stages.json")))
		e.stubLogs(101, 10)
		e.in.WriteString("1\n")
		require.NoError(t, e.run(t, "--failed"))
		e.reg.Verify(t)
		require.Len(t, e.runQueries, 1)
		assert.Equal(t, "1", e.runQueries[0].Get("definitions"))
		assert.Equal(t, "refs/heads/main", e.runQueries[0].Get("branchName"))
		assert.Contains(t, e.out.String(), "X ado-gh 20261001.1 [main] ID 101")
		assert.True(t, strings.HasSuffix(e.out.String(), plLogsLines("Unit tests", "go test", 10)), e.out.String())
	})

	t.Run("project runs without a default pipeline", func(t *testing.T) {
		e := newPLLogsEnv(t, true)
		e.stubRunPrompt()
		e.stubTimeline(101, json.RawMessage(pipelinefixtures.Read(t, "timeline_yaml_stages.json")))
		e.stubLogs(101, 10)
		e.in.WriteString("1\n")
		require.NoError(t, e.run(t, "--failed"))
		e.reg.Verify(t)
		require.Len(t, e.runQueries, 1)
		assert.False(t, e.runQueries[0].Has("definitions"))
		assert.False(t, e.runQueries[0].Has("branchName"))
	})
}

func TestLogsFollow(t *testing.T) {
	const tl = "timeline_in_progress.json"
	// Records of timeline_in_progress.json: job ...10603 with tasks Initialize job ...10604 (log
	// 1), Checkout ...10605 (completed, no log yet), go build ...10606 (running), Publish webapp
	// ...10607 and Finalize Job ...10608 (pending).
	t.Run("prints logs as tasks complete, in order, until the run completes", func(t *testing.T) {
		e := newPLLogsEnv(t, false)
		synctest.Test(t, func(t *testing.T) {
			e.stubStatus(106, "inProgress")
			e.stubTimeline(106, plLogsTimeline(t, tl, nil))
			e.stubLogs(106, 1)
			// go build has its log, but Checkout's may still come: hold go build back.
			e.stubStatus(106, "inProgress")
			e.stubTimeline(106, plLogsTimeline(t, tl, map[string]map[string]any{"10606": plLogsDone(3)}))
			e.stubStatus(106, "inProgress")
			e.stubTimeline(106, plLogsTimeline(t, tl, map[string]map[string]any{"10605": plLogsDone(2), "10606": plLogsDone(3)}))
			e.stubLogs(106, 2, 3)
			// Publish webapp completed without a log, but the job has one now: none will come.
			e.stubStatus(106, "inProgress")
			e.stubTimeline(106, plLogsTimeline(t, tl, map[string]map[string]any{
				"10603": plLogsDone(6), "10605": plLogsDone(2), "10606": plLogsDone(3),
				"10607": {"state": "completed", "result": "succeeded"}, "10608": plLogsDone(5),
			}))
			e.stubLogs(106, 5)
			e.stubStatus(106, "completed")
			e.stubTimeline(106, plLogsTimeline(t, tl, map[string]map[string]any{
				"10603": plLogsDone(6), "10605": plLogsDone(2), "10606": plLogsDone(3),
				"10607": {"state": "completed", "result": "succeeded"}, "10608": plLogsDone(5),
			}))
			require.NoError(t, e.run(t, "106", "--follow"))
		})
		e.reg.Verify(t)
		require.Len(t, e.polls, 5)
		for i := 1; i < len(e.polls); i++ {
			assert.Equal(t, followInterval, e.polls[i].Sub(e.polls[i-1]))
		}
		assert.Equal(t, plLogsLines("Build", "Initialize job", 1, "Checkout ado-gh@main to s", 2, "go build", 3, "Finalize Job", 5), e.out.String())
	})

	t.Run("run completion releases held logs", func(t *testing.T) {
		e := newPLLogsEnv(t, false)
		held := map[string]map[string]any{"10604": {"log": nil}, "10606": plLogsDone(3)}
		synctest.Test(t, func(t *testing.T) {
			e.stubStatus(106, "inProgress")
			e.stubTimeline(106, plLogsTimeline(t, tl, held))
			e.stubStatus(106, "completed")
			e.stubTimeline(106, plLogsTimeline(t, tl, held))
			e.stubLogs(106, 3)
			require.NoError(t, e.run(t, "106", "-f"))
		})
		e.reg.Verify(t)
		assert.Len(t, e.polls, 2)
		assert.Equal(t, plLogsLines("Build", "go build", 3), e.out.String())
	})

	t.Run("completed run writes its logs once", func(t *testing.T) {
		e := newPLLogsEnv(t, false)
		dir := t.TempDir()
		synctest.Test(t, func(t *testing.T) {
			e.stubStatus(101, "completed")
			e.stubTimeline(101, json.RawMessage(pipelinefixtures.Read(t, "timeline_yaml_stages.json")))
			e.stubLogs(101, 10)
			require.NoError(t, e.run(t, "101", "-f", "--failed", "-o", dir))
		})
		e.reg.Verify(t)
		assert.Len(t, e.polls, 1)
		b, err := os.ReadFile(filepath.Join(dir, "Test", "Unit tests", "03_go test.log"))
		require.NoError(t, err)
		assert.Equal(t, "10a\n10b\n", string(b))
	})

	t.Run("stops when the context ends", func(t *testing.T) {
		e := newPLLogsEnv(t, false)
		synctest.Test(t, func(t *testing.T) {
			e.stubStatus(106, "inProgress")
			e.stubTimeline(106, plLogsTimeline(t, tl, map[string]map[string]any{"10604": {"log": nil}}))
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			assert.ErrorIs(t, e.runCtx(ctx, t, "106", "-f"), context.DeadlineExceeded)
		})
		e.reg.Verify(t)
		assert.Empty(t, e.out.String())
	})

	t.Run("status error", func(t *testing.T) {
		e := newPLLogsEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/builds/106")),
			httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]string{"message": "boom"}))
		assert.ErrorContains(t, e.run(t, "106", "-f"), "boom")
	})

	t.Run("timeline error", func(t *testing.T) {
		e := newPLLogsEnv(t, false)
		e.stubStatus(106, "inProgress")
		e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/builds/106/timeline")),
			httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]string{"message": "boom"}))
		assert.ErrorContains(t, e.run(t, "106", "-f"), "boom")
	})
}
