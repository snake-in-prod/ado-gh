package view

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/browsertest"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

type relViewEnv struct {
	org     string
	reg     *httpmock.Registry
	ios     *iostreams.IOStreams
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	browser *browsertest.Browser
}

// newRelViewEnv returns an env for its own org (the SDK caches discovery per base URL).
func newRelViewEnv(t *testing.T, tty bool) *relViewEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_REPO", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	org := "relview-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &relViewEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut, browser: &browsertest.Browser{}}
	t.Cleanup(func() { e.reg.Verify(t) })
	return e
}

func (e *relViewEnv) run(t *testing.T, args ...string) error {
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
		Browser: e.browser,
	}
	cmd := NewCmdView(f)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

// stubRelease serves the release fixture as release id of org's project proj on the vsrm host.
func (e *relViewEnv) stubRelease(t *testing.T, org string, id int, fixture string) {
	e.reg.StubLocations(org, httpmock.ResourceAreasLocation)
	e.reg.StubVSRM(org)
	e.reg.Register(httpmock.REST(http.MethodGet, fmt.Sprintf("%s/proj/_apis/release/releases/%d", org, id)),
		func(req *http.Request) (*http.Response, error) {
			assert.Equal(t, "vsrm.dev.azure.com", req.URL.Host)
			assert.Equal(t, "tasks", req.URL.Query().Get("$expand"))
			return httpmock.JSONResponse(json.RawMessage(pipelinefixtures.Read(t, fixture)))(req)
		})
}

// stubTaskLog answers the log of task in deploy phase phase of environment env of release 3.
func (e *relViewEnv) stubTaskLog(env, phase, task int) {
	e.reg.Register(httpmock.REST(http.MethodGet,
		fmt.Sprintf("%s/proj/_apis/release/releases/3/environments/%d/deployphases/%d/tasks/%d/logs", e.org, env, phase, task)),
		httpmock.StringResponse(fmt.Sprintf("log %d line 1\nlog %d line 2\n", task, task)))
}

const relViewFailedBody = `
ENVIRONMENTS
✓ Dev in 15s
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

APPROVALS
✓ Prod: pre-deployment approval approved · Test User · ship it

ANNOTATIONS
X Deployment of Prod failed.
  Prod
X Bash exited with code '1'.
  Prod / Agent job / Run script
`

func TestReleaseViewTTY(t *testing.T) {
	e := newRelViewEnv(t, true)
	e.stubRelease(t, e.org, 3, "release_failed_task.json")
	require.NoError(t, e.run(t, "3"))
	out := e.out.String()
	lines := strings.Split(out, "\n")
	assert.Equal(t, "Release-3 ado-live-release · active · ID 3", lines[0])
	assert.Equal(t, "hotfix", lines[1])
	assert.Regexp(t, `^Created .* ago by Test User · manual$`, lines[2])
	assert.Equal(t, strings.Join(lines[3:], "\n"), relViewFailedBody+"\nTo see what failed, try: ado release view 3 --log-failed\n"+
		"View this release on Azure DevOps: https://dev.azure.com/"+e.org+"/proj/_release?releaseId=3&_a=release-summary\n")
	assert.Empty(t, e.errOut.String())
}

func TestReleaseViewTTYPendingApproval(t *testing.T) {
	e := newRelViewEnv(t, true)
	e.stubRelease(t, e.org, 1, "release.json")
	require.NoError(t, e.run(t, "1"))
	out := e.out.String()
	assert.Contains(t, out, "\nado live read fixtures\n")
	assert.Contains(t, out, "\nENVIRONMENTS\n✓ Dev in 2s\n  ✓ Agentless job in 2s\n    ✓ Delay\n● Prod\n\n"+
		"APPROVALS\n⏸ Prod: pre-deployment approval pending · Test User\n\nView this release")
	assert.NotContains(t, out, "--log-failed")
}

func TestReleaseViewNonTTY(t *testing.T) {
	e := newRelViewEnv(t, false)
	e.stubRelease(t, e.org, 3, "release_failed_task.json")
	require.NoError(t, e.run(t, "3"))
	assert.Equal(t, `name:	Release-3
id:	3
definition:	ado-live-release
status:	active
description:	hotfix
reason:	manual
created by:	Test User
created:	2026-10-12T08:00:00Z
url:	https://dev.azure.com/`+e.org+`/proj/_release?releaseId=3&_a=release-summary
`+relViewFailedBody, e.out.String())
}

func TestReleaseViewMinimal(t *testing.T) {
	e := newRelViewEnv(t, true)
	e.reg.StubLocations(e.org, httpmock.ResourceAreasLocation)
	e.reg.StubVSRM(e.org)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/release/releases/9"),
		httpmock.StringResponse(`{"id": 9, "name": "Release-9", "status": "draft"}`))
	require.NoError(t, e.run(t, "9"))
	assert.Equal(t, "Release-9 · draft · ID 9\n\nView this release on Azure DevOps: https://dev.azure.com/"+e.org+
		"/proj/_release?releaseId=9&_a=release-summary\n", e.out.String())
}

func TestReleaseViewLogs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		logs [][3]int
		want string
	}{
		{"all", []string{"--log"}, [][3]int{{5, 6, 2}, {5, 6, 3}, {5, 6, 5}, {6, 7, 2}, {6, 7, 3}}, "" +
			"Agent job 1\tInitialize job\tlog 2 line 1\nAgent job 1\tInitialize job\tlog 2 line 2\n" +
			"Agent job 1\tBash\tlog 3 line 1\nAgent job 1\tBash\tlog 3 line 2\n" +
			"Agent job 2\tInitialize job\tlog 5 line 1\nAgent job 2\tInitialize job\tlog 5 line 2\n" +
			"Agent job\tInitialize job\tlog 2 line 1\nAgent job\tInitialize job\tlog 2 line 2\n" +
			"Agent job\tRun script\tlog 3 line 1\nAgent job\tRun script\tlog 3 line 2\n"},
		{"failed", []string{"--log-failed"}, [][3]int{{6, 7, 3}}, "Agent job\tRun script\tlog 3 line 1\nAgent job\tRun script\tlog 3 line 2\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRelViewEnv(t, true)
			e.stubRelease(t, e.org, 3, "release_failed_task.json")
			for _, l := range tt.logs {
				e.stubTaskLog(l[0], l[1], l[2])
			}
			require.NoError(t, e.run(t, append([]string{"3"}, tt.args...)...))
			assert.Equal(t, tt.want, e.out.String())
		})
	}
}

func TestReleaseViewLogsNone(t *testing.T) {
	for flag, want := range map[string]string{
		"--log":        "no task logs found in release 1",
		"--log-failed": "no failed task logs found in release 1",
	} {
		t.Run(flag, func(t *testing.T) {
			e := newRelViewEnv(t, false)
			e.stubRelease(t, e.org, 1, "release.json")
			assert.EqualError(t, e.run(t, "1", flag), want)
		})
	}
}

func TestReleaseViewLogError(t *testing.T) {
	e := newRelViewEnv(t, false)
	e.stubRelease(t, e.org, 3, "release_failed_task.json")
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/release/releases/3/environments/6/deployphases/7/tasks/3/logs"),
		httpmock.StatusStringResponse(http.StatusNotFound, `{"message": "log gone"}`))
	err := e.run(t, "3", "--log-failed")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "log of Agent job / Run script")
}

func TestReleaseViewJSON(t *testing.T) {
	e := newRelViewEnv(t, false)
	e.stubRelease(t, e.org, 1, "release.json")
	require.NoError(t, e.run(t, "1", "--json", "id,name,definition,status,description,reason,createdBy,createdOn,url"))
	assert.JSONEq(t, `{"id":1,"name":"Release-1","status":"active","description":"ado live read fixtures","reason":"manual",
		"definition":{"id":1,"name":"ado-live-release","path":"\\ado-live"},
		"createdBy":{"id":"00000000-0000-0000-0000-000000000940","displayName":"Test User","uniqueName":""},
		"createdOn":"2026-10-12T06:58:10.06Z",
		"url":"https://dev.azure.com/`+e.org+`/proj/_release?releaseId=1&_a=release-summary"}`, e.out.String())
}

func TestReleaseViewJSONEnvironments(t *testing.T) {
	e := newRelViewEnv(t, false)
	e.stubRelease(t, e.org, 3, "release_failed_task.json")
	require.NoError(t, e.run(t, "3", "--json", "environments"))
	var got struct {
		Environments []map[string]any `json:"environments"`
	}
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &got))
	require.Len(t, got.Environments, 3)
	prod := got.Environments[2]
	assert.Equal(t, "rejected", prod["status"])
	assert.Equal(t, "PhaseFailed", prod["operationStatus"])
	assert.Equal(t, 2.0, prod["attempt"])
	job := prod["jobs"].([]any)[0].(map[string]any)
	task := job["children"].([]any)[1].(map[string]any)
	assert.Equal(t, "Run script", task["name"])
	assert.Equal(t, "failed", task["result"])
	assert.Equal(t, 3.0, task["logId"])
	assert.Equal(t, []any{map[string]any{"type": "error", "message": "Bash exited with code '1'."}}, task["issues"])
}

func TestReleaseViewWeb(t *testing.T) {
	for _, tty := range []bool{true, false} {
		t.Run(fmt.Sprint(tty), func(t *testing.T) {
			e := newRelViewEnv(t, tty)
			require.NoError(t, e.run(t, "https://dev.azure.com/other/Other%20Proj/_releaseProgress?releaseId=45", "-w"))
			want := "https://dev.azure.com/other/Other%20Proj/_release?releaseId=45&_a=release-summary"
			assert.Equal(t, []string{want}, e.browser.URLs)
			if tty {
				assert.Equal(t, "Opening "+want+" in your browser.\n", e.errOut.String())
			} else {
				assert.Empty(t, e.errOut.String())
			}
			assert.Empty(t, e.out.String())
			assert.Empty(t, e.reg.Requests)
		})
	}
}

func TestReleaseViewURLProject(t *testing.T) {
	e := newRelViewEnv(t, false)
	other := "relview-other-" + strings.ToLower(t.Name())
	e.stubRelease(t, other, 1, "release.json")
	require.NoError(t, e.run(t, "https://vsrm.dev.azure.com/"+other+"/proj/_apis/Release/releases/1", "--json", "url"))
	assert.JSONEq(t, `{"url":"https://dev.azure.com/`+other+`/proj/_release?releaseId=1&_a=release-summary"}`, e.out.String())
}

func TestReleaseViewFlagErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"3", "--log", "--log-failed"}, "none of the others can be"},
		{[]string{"3", "-w", "--json", "id"}, "cannot use `--web` with `--json`"},
		{[]string{"3", "--json", "nope"}, "Unknown JSON field"},
		{[]string{"Release-3"}, "invalid release"},
		{[]string{}, "accepts 1 arg"},
		{[]string{"1", "2"}, "accepts 1 arg"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			e := newRelViewEnv(t, false)
			err := e.run(t, tt.args...)
			require.Error(t, err)
			assert.Contains(t, strings.ToLower(err.Error()), strings.ToLower(tt.want))
			assert.Empty(t, e.reg.Requests)
		})
	}
}

func TestReleaseViewServerError(t *testing.T) {
	e := newRelViewEnv(t, false)
	e.reg.StubLocations(e.org, httpmock.ResourceAreasLocation)
	e.reg.StubVSRM(e.org)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/release/releases/404"),
		httpmock.StatusStringResponse(http.StatusNotFound, `{"message": "VS402856: Release with id 404 does not exist."}`))
	err := e.run(t, "404")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "VS402856")
}
