package logs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

type relLogsEnv struct {
	org         string
	reg         *httpmock.Registry
	out, errOut *bytes.Buffer
	ios         *iostreams.IOStreams
}

// newRelLogsEnv returns an env for its own org (the SDK caches discovery per base URL).
func newRelLogsEnv(t *testing.T) *relLogsEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_REPO", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	org := "rellogs-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &relLogsEnv{org: org, reg: &httpmock.Registry{}, out: out, errOut: errOut, ios: ios}
	t.Cleanup(func() { e.reg.Verify(t) })
	return e
}

func (e *relLogsEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "proj", Name: "ado-gh"}, nil
		},
	}
	cmd := NewCmdLogs(f)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

// stubRelease serves release 3 (release_failed_task.json, or fixture) on the vsrm host.
func (e *relLogsEnv) stubRelease(t *testing.T, id int, fixture string) {
	e.reg.StubLocations(e.org, httpmock.ResourceAreasLocation)
	e.reg.StubVSRM(e.org)
	e.reg.Register(httpmock.REST(http.MethodGet, fmt.Sprintf("%s/proj/_apis/release/releases/%d", e.org, id)),
		httpmock.JSONResponse(json.RawMessage(pipelinefixtures.Read(t, fixture))))
}

// stubTaskLogs answers each {environment, deploy phase, task} log of release 3 once.
func (e *relLogsEnv) stubTaskLogs(logs ...[3]int) {
	for _, l := range logs {
		e.reg.Register(httpmock.REST(http.MethodGet,
			fmt.Sprintf("%s/proj/_apis/release/releases/3/environments/%d/deployphases/%d/tasks/%d/logs", e.org, l[0], l[1], l[2])),
			httpmock.StringResponse(fmt.Sprintf("env %d task %d\n", l[0], l[2])))
	}
}

func TestReleaseLogsStdout(t *testing.T) {
	tests := []struct {
		name string
		args []string
		logs [][3]int
		want string
	}{
		{"all", nil, [][3]int{{5, 6, 2}, {5, 6, 3}, {5, 6, 5}, {6, 7, 2}, {6, 7, 3}}, "" +
			"Agent job 1\tInitialize job\tenv 5 task 2\nAgent job 1\tBash\tenv 5 task 3\nAgent job 2\tInitialize job\tenv 5 task 5\n" +
			"Agent job\tInitialize job\tenv 6 task 2\nAgent job\tRun script\tenv 6 task 3\n"},
		{"failed", []string{"--failed"}, [][3]int{{6, 7, 3}}, "Agent job\tRun script\tenv 6 task 3\n"},
		{"environment by name", []string{"--environment", "dev"}, [][3]int{{5, 6, 2}, {5, 6, 3}, {5, 6, 5}},
			"Agent job 1\tInitialize job\tenv 5 task 2\nAgent job 1\tBash\tenv 5 task 3\nAgent job 2\tInitialize job\tenv 5 task 5\n"},
		{"environment by id", []string{"--environment", "6"}, [][3]int{{6, 7, 2}, {6, 7, 3}},
			"Agent job\tInitialize job\tenv 6 task 2\nAgent job\tRun script\tenv 6 task 3\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRelLogsEnv(t)
			e.stubRelease(t, 3, "release_failed_task.json")
			e.stubTaskLogs(tt.logs...)
			require.NoError(t, e.run(t, append([]string{"3"}, tt.args...)...))
			assert.Equal(t, tt.want, e.out.String())
			assert.Empty(t, e.errOut.String())
		})
	}
}

func TestReleaseLogsOutput(t *testing.T) {
	e := newRelLogsEnv(t)
	e.stubRelease(t, 3, "release_failed_task.json")
	e.stubTaskLogs([3]int{5, 6, 2}, [3]int{5, 6, 3}, [3]int{5, 6, 5}, [3]int{6, 7, 2}, [3]int{6, 7, 3})
	dir := filepath.Join(t.TempDir(), "out")
	require.NoError(t, e.run(t, "3", "-o", dir))
	assert.Empty(t, e.out.String())
	assert.Equal(t, "✓ Wrote 5 logs to "+dir+"\n", e.errOut.String())
	files := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		files[filepath.ToSlash(rel)] = string(b)
		return err
	}))
	assert.Equal(t, map[string]string{
		"Dev/Agent job 1/01_Initialize job.log": "env 5 task 2\n",
		"Dev/Agent job 1/02_Bash.log":           "env 5 task 3\n",
		"Dev/Agent job 2/01_Initialize job.log": "env 5 task 5\n",
		"Prod/Agent job/01_Initialize job.log":  "env 6 task 2\n",
		"Prod/Agent job/02_Run script.log":      "env 6 task 3\n",
	}, files)
}

func TestReleaseLogsNone(t *testing.T) {
	e := newRelLogsEnv(t)
	e.stubRelease(t, 1, "release.json")
	require.NoError(t, e.run(t, "1"))
	assert.Empty(t, e.out.String())
	assert.Equal(t, "no task logs found\n", e.errOut.String())
}

func TestReleaseLogsUnknownEnvironment(t *testing.T) {
	e := newRelLogsEnv(t)
	e.stubRelease(t, 3, "release_failed_task.json")
	assert.EqualError(t, e.run(t, "3", "--environment", "Staging"),
		`no environment "Staging" in release 3; its environments: Dev, QA, Prod`)
}

func TestReleaseLogsErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"x"}, "invalid release"},
		{[]string{}, "accepts 1 arg"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			e := newRelLogsEnv(t)
			err := e.run(t, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Empty(t, e.reg.Requests)
		})
	}
}

func TestReleaseLogsServerErrors(t *testing.T) {
	t.Run("release", func(t *testing.T) {
		e := newRelLogsEnv(t)
		e.reg.StubLocations(e.org, httpmock.ResourceAreasLocation)
		e.reg.StubVSRM(e.org)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/release/releases/404"),
			httpmock.StatusStringResponse(http.StatusNotFound, `{"message": "VS402856: Release with id 404 does not exist."}`))
		err := e.run(t, "404")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "VS402856")
	})
	t.Run("output", func(t *testing.T) {
		e := newRelLogsEnv(t)
		e.stubRelease(t, 3, "release_failed_task.json")
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, nil, 0o644))
		require.Error(t, e.run(t, "3", "--failed", "-o", file))
	})
}
