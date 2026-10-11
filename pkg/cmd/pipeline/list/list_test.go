package list

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

const plListUserID = "a0000000-0000-0000-0000-000000000007"

type plListEnv struct {
	org    string
	reg    *httpmock.Registry
	ios    *iostreams.IOStreams
	out    *bytes.Buffer
	errOut *bytes.Buffer
	// queries are the query strings of the build list requests, in order.
	queries []url.Values
}

// newPLListEnv returns an env for its own org (the SDK caches discovery per base URL).
func newPLListEnv(t *testing.T, tty bool) *plListEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "pllist-" + strings.NewReplacer("/", "-", " ", "-", "@", "-", ",", "-", "*", "-").Replace(strings.ToLower(t.Name()))
	e := &plListEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut}
	t.Cleanup(func() { e.reg.Verify(t) })
	e.reg.StubLocations(org, httpmock.PipelineLocations...)
	return e
}

// stubBuilds answers the next build list request with runs and continuation token after
// recording its query.
func (e *plListEnv) stubBuilds(token string, runs ...map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/build/builds"), func(r *http.Request) (*http.Response, error) {
		e.queries = append(e.queries, r.URL.Query())
		if runs == nil {
			runs = []map[string]any{}
		}
		resp := httpmock.JSONResponse(map[string]any{"count": len(runs), "value": runs})
		if token != "" {
			resp = httpmock.WithHeader(resp, "X-MS-ContinuationToken", token)
		}
		return resp(r)
	})
}

func (e *plListEnv) run(t *testing.T, args ...string) error {
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
	}
	cmd := NewCmdList(f)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

// plRun is a build as the list returns it.
func plRun(id int, branch, sha, status, result string) map[string]any {
	b := map[string]any{
		"id": id, "buildNumber": fmt.Sprintf("20261001.%d", id), "status": status,
		"sourceBranch": branch, "sourceVersion": sha, "reason": "individualCI",
		"queueTime": "2026-10-01T10:00:00Z", "startTime": "2026-10-01T10:00:05Z",
		"definition":   map[string]any{"id": 1, "name": "ado-gh", "path": `\`},
		"requestedFor": map[string]any{"id": plListUserID, "displayName": "Ann Lee", "uniqueName": "ann@contoso.com"},
		"requestedBy":  map[string]any{"id": plListUserID, "displayName": "Ann Lee", "uniqueName": "ann@contoso.com"},
	}
	if status == "completed" {
		b["result"], b["finishTime"] = result, "2026-10-01T10:01:35Z"
	}
	return b
}

func TestListRunsFilters(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		want  map[string]string
		setup func(*plListEnv)
	}{
		{"none", nil, map[string]string{}, nil},
		{"branch short", []string{"-b", "feature/x"}, map[string]string{"branchName": "refs/heads/feature/x"}, nil},
		{"branch full ref kept", []string{"--branch", "refs/pull/7/merge"}, map[string]string{"branchName": "refs/pull/7/merge"}, nil},
		{"status", []string{"-s", "inProgress"}, map[string]string{"statusFilter": "inProgress"}, nil},
		{"result", []string{"--result", "failed"}, map[string]string{"resultFilter": "failed"}, nil},
		{"event", []string{"-e", "pullRequest"}, map[string]string{"reasonFilter": "pullRequest"}, nil},
		{"user guid", []string{"-u", plListUserID}, map[string]string{"requestedFor": plListUserID}, nil},
		{"user me", []string{"-u", "@me"}, map[string]string{"requestedFor": plListUserID}, func(e *plListEnv) {
			e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"),
				httpmock.JSONResponse(map[string]any{"authenticatedUser": map[string]any{"id": plListUserID}}))
		}},
		{"pipeline id", []string{"5"}, map[string]string{"definitions": "5"}, nil},
		{"pipeline name", []string{"Apps/Web/deploy"}, map[string]string{"definitions": "4"}, func(e *plListEnv) {
			e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/build/definitions"),
				httpmock.JSONResponse(map[string]any{"count": 2, "value": []map[string]any{
					{"id": 3, "name": "deploy", "path": `\Infra`}, {"id": 4, "name": "deploy", "path": `\Apps\Web`},
				}}))
		}},
		{"all combined", []string{"7", "-b", "main", "-s", "completed", "--result", "succeeded", "-e", "manual"}, map[string]string{
			"definitions": "7", "branchName": "refs/heads/main", "statusFilter": "completed", "resultFilter": "succeeded", "reasonFilter": "manual",
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newPLListEnv(t, false)
			if tt.setup != nil {
				tt.setup(e)
			}
			e.stubBuilds("", plRun(1, "refs/heads/main", "abc", "completed", "succeeded"))
			require.NoError(t, e.run(t, tt.args...))
			require.Len(t, e.queries, 1)
			q := e.queries[0]
			assert.Equal(t, "queueTimeDescending", q.Get("queryOrder"))
			assert.Equal(t, "20", q.Get("$top"), "--limit defaults to 20")
			for _, k := range []string{"branchName", "statusFilter", "resultFilter", "reasonFilter", "requestedFor", "definitions"} {
				assert.Equal(t, tt.want[k], q.Get(k), k)
			}
		})
	}
}

func TestListRunsTTY(t *testing.T) {
	e := newPLListEnv(t, true)
	e.ios.SetTerminalWidth(200)
	pr := plRun(12, "refs/pull/3/merge", "abc", "inProgress", "")
	pr["reason"] = "pullRequest"
	e.stubBuilds("", plRun(11, "refs/heads/main", "abc", "completed", "failed"), pr)
	require.NoError(t, e.run(t))
	out := e.out.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 3)
	assert.Regexp(t, `^STATUS\s+NUMBER\s+PIPELINE\s+BRANCH\s+EVENT\s+ID\s+ELAPSED\s+AGE$`, lines[0])
	assert.Regexp(t, `^X\s+20261001\.11\s+ado-gh\s+main\s+individualCI\s+11\s+1m30s\s+.*ago$`, lines[1])
	assert.Regexp(t, `^●\s+20261001\.12\s+ado-gh\s+refs/pull/3/merge\s+pullRequest\s+12\s+\S+\s+.*ago$`, lines[2])
	assert.Empty(t, e.errOut.String())
}

func TestListRunsTSV(t *testing.T) {
	e := newPLListEnv(t, false)
	queued := plRun(13, "refs/heads/dev", "abc", "notStarted", "")
	delete(queued, "startTime")
	e.stubBuilds("", plRun(11, "refs/heads/main", "abc", "completed", "failed"), queued)
	require.NoError(t, e.run(t))
	assert.Equal(t,
		"completed\tfailed\t20261001.11\tado-gh\tmain\tindividualCI\t11\t1m30s\t2026-10-01T10:00:00Z\n"+
			"notStarted\t\t20261001.13\tado-gh\tdev\tindividualCI\t13\t\t2026-10-01T10:00:00Z\n", e.out.String())
}

func TestListRunsJSON(t *testing.T) {
	e := newPLListEnv(t, false)
	e.stubBuilds("", plRun(11, "refs/heads/main", "abc", "completed", "failed"))
	require.NoError(t, e.run(t, "--json", strings.Join([]string{
		"id", "number", "pipeline", "status", "result", "sourceBranch", "sourceVersion", "reason",
		"requestedBy", "requestedFor", "queueTime", "startTime", "finishTime", "url",
	}, ",")))
	var got []map[string]any
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &got))
	user := map[string]any{"id": plListUserID, "displayName": "Ann Lee", "uniqueName": "ann@contoso.com"}
	assert.Equal(t, []map[string]any{{
		"id": float64(11), "number": "20261001.11", "pipeline": map[string]any{"id": float64(1), "name": "ado-gh", "path": `\`},
		"status": "completed", "result": "failed", "sourceBranch": "refs/heads/main", "sourceVersion": "abc",
		"reason": "individualCI", "requestedBy": user, "requestedFor": user,
		"queueTime": "2026-10-01T10:00:00Z", "startTime": "2026-10-01T10:00:05Z", "finishTime": "2026-10-01T10:01:35Z",
		"url": "https://dev.azure.com/" + e.org + "/My%20Proj/_build/results?buildId=11",
	}}, got)
}

func TestListRunsEmpty(t *testing.T) {
	for _, tty := range []bool{true, false} {
		t.Run(fmt.Sprint(tty), func(t *testing.T) {
			e := newPLListEnv(t, tty)
			e.stubBuilds("")
			require.NoError(t, e.run(t, "-b", "nope"))
			assert.Empty(t, e.out.String())
			assert.Equal(t, "no runs match your search\n", e.errOut.String())
		})
	}
}

func TestListRunsEmptyJSON(t *testing.T) {
	e := newPLListEnv(t, false)
	e.stubBuilds("")
	require.NoError(t, e.run(t, "--json", "id"))
	assert.Equal(t, "[]\n", e.out.String())
	assert.Empty(t, e.errOut.String())
}

func TestListRunsPagesToLimit(t *testing.T) {
	e := newPLListEnv(t, false)
	e.stubBuilds("tok1", plRun(1, "refs/heads/main", "a", "completed", "succeeded"), plRun(2, "refs/heads/main", "a", "completed", "succeeded"))
	e.stubBuilds("tok2", plRun(3, "refs/heads/main", "a", "completed", "succeeded"))
	require.NoError(t, e.run(t, "-L", "3"))
	require.Len(t, e.queries, 2)
	assert.Equal(t, "3", e.queries[0].Get("$top"))
	assert.Empty(t, e.queries[0].Get("continuationToken"))
	assert.Equal(t, "1", e.queries[1].Get("$top"))
	assert.Equal(t, "tok1", e.queries[1].Get("continuationToken"))
	assert.Equal(t, 3, strings.Count(e.out.String(), "\n"))
}

func TestListRunsCommitSpansPages(t *testing.T) {
	e := newPLListEnv(t, false)
	e.stubBuilds("tok1", plRun(1, "refs/heads/main", "aaa111", "completed", "succeeded"),
		plRun(2, "refs/heads/main", "BEEF0001", "completed", "failed"))
	e.stubBuilds("tok2", plRun(3, "refs/heads/main", "ccc333", "completed", "succeeded"),
		plRun(4, "refs/heads/main", "beef0001", "completed", "succeeded"), plRun(5, "refs/heads/main", "beef0001", "completed", "succeeded"))
	require.NoError(t, e.run(t, "-c", "beef", "-L", "2", "-b", "main", "9"))
	require.Len(t, e.queries, 2)
	for i, q := range e.queries {
		assert.Equal(t, "100", q.Get("$top"), i)
		assert.Equal(t, "refs/heads/main", q.Get("branchName"), "narrowed server-side")
		assert.Equal(t, "9", q.Get("definitions"), "narrowed server-side")
		assert.Empty(t, q.Get("sourceVersion"))
	}
	assert.Equal(t, "tok1", e.queries[1].Get("continuationToken"))
	ids := []string{}
	for _, l := range strings.Split(strings.TrimSpace(e.out.String()), "\n") {
		ids = append(ids, strings.Split(l, "\t")[6])
	}
	assert.Equal(t, []string{"2", "4"}, ids)
	assert.Empty(t, e.errOut.String())
}

func TestListRunsCommitPageCap(t *testing.T) {
	e := newPLListEnv(t, false)
	for i := range maxCommitPages {
		e.stubBuilds(fmt.Sprintf("tok%d", i+1), plRun(i+1, "refs/heads/main", "aaa", "completed", "succeeded"))
	}
	require.NoError(t, e.run(t, "-c", "beef"))
	assert.Len(t, e.queries, maxCommitPages)
	assert.Equal(t, "! stopped after the newest 1000 runs; results may be incomplete (narrow the search with a pipeline or --branch)\n"+
		"no runs match your search\n", e.errOut.String())
}

func TestListRunsUserNotFound(t *testing.T) {
	e := newPLListEnv(t, false)
	e.reg = &httpmock.Registry{} // no build API call is made
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{}))
	err := e.run(t, "-u", "@me")
	assert.ErrorContains(t, err, "reading the current user")
	assert.Empty(t, e.queries)
}

func TestListRunsServerError(t *testing.T) {
	e := newPLListEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/build/builds"),
		httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "TF400813: no access"}))
	assert.ErrorContains(t, e.run(t), "TF400813")
}

func TestListFlagErrors(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"-L", "0"}, "invalid value for --limit: 0"},
		{[]string{"-s", "done"}, `invalid value for --status: "done"`},
		{[]string{"--result", "ok"}, `invalid value for --result: "ok"`},
		{[]string{"-e", "push"}, `invalid value for --event: "push"`},
		{[]string{"-d", "-b", "main"}, "--branch cannot be used with --definitions"},
		{[]string{"-d", "-c", "abc"}, "--commit cannot be used with --definitions"},
		{[]string{"-d", "ci"}, "a <pipeline> argument cannot be used with --definitions"},
		{[]string{"-d", "--match", "[x"}, `invalid --match pattern "[x"`},
		{[]string{"--folder", "x"}, "--folder requires --definitions"},
		{[]string{"-a"}, "--all requires --definitions"},
		{[]string{"--json", "queueStatus"}, `unknown JSON field "queueStatus"`},
		{[]string{"-d", "--json", "sourceBranch"}, `unknown JSON field "sourceBranch"`},
		{[]string{"a", "b"}, "accepts at most 1 arg"},
		{[]string{"/"}, "invalid pipeline"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			e := newPLListEnv(t, false)
			e.reg = &httpmock.Registry{} // nothing may be sent
			err := e.run(t, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Empty(t, e.reg.Requests)
		})
	}
}

// stubDefinitions serves definitions.json for the full-definitions read after checking it
// asks for full definitions with their latest builds.
func (e *plListEnv) stubDefinitions(t *testing.T) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/build/definitions"), func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "true", r.URL.Query().Get("includeAllProperties"))
		assert.Equal(t, "true", r.URL.Query().Get("includeLatestBuilds"))
		return httpmock.JSONResponse(json.RawMessage(pipelinefixtures.Read(t, "definitions.json")))(r)
	})
}

func TestListDefinitionsFilters(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string // ids
	}{
		{"enabled only", nil, []string{"1", "4"}},
		{"all", []string{"-a"}, []string{"1", "4", "3", "2"}},
		{"folder", []string{"-a", "--folder", "infra"}, []string{"3", "2"}},
		{"folder below", []string{"-a", "--folder", "/Apps"}, []string{"4"}},
		{"folder root", []string{"-a", "--folder", `\`}, []string{"1", "4", "3", "2"}},
		{"match", []string{"-a", "--match", "DEP*"}, []string{"4", "3"}},
		{"match enabled", []string{"--match", "dep*"}, []string{"4"}},
		{"limit", []string{"-a", "-L", "2"}, []string{"1", "4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newPLListEnv(t, false)
			e.stubDefinitions(t)
			require.NoError(t, e.run(t, append([]string{"-d"}, tt.args...)...))
			ids := []string{}
			for _, l := range strings.Split(strings.TrimSpace(e.out.String()), "\n") {
				ids = append(ids, strings.Split(l, "\t")[0])
			}
			assert.Equal(t, tt.want, ids)
		})
	}
}

func TestListDefinitionsTSV(t *testing.T) {
	e := newPLListEnv(t, false)
	e.stubDefinitions(t)
	require.NoError(t, e.run(t, "--definitions", "--all"))
	assert.Equal(t,
		"1\tado-gh\t\\\tyaml\tado-gh\tenabled\tfailed\n"+
			"4\tdeploy\t\\Apps\\Web\tyaml\tweb\tenabled\tsucceeded\n"+
			"3\tdeploy\t\\Infra\tyaml\tado-gh\tdisabled\t\n"+
			"2\tnightly\t\\Infra\tclassic\tado-gh\tpaused\tsucceeded\n", e.out.String())
}

func TestListDefinitionsTTY(t *testing.T) {
	e := newPLListEnv(t, true)
	e.ios.SetTerminalWidth(200)
	e.stubDefinitions(t)
	require.NoError(t, e.run(t, "-d"))
	lines := strings.Split(strings.TrimSpace(e.out.String()), "\n")
	require.Len(t, lines, 3)
	assert.Regexp(t, `^ID\s+NAME\s+FOLDER\s+TYPE\s+REPOSITORY\s+STATUS\s+LAST RUN$`, lines[0])
	assert.Regexp(t, `^1\s+ado-gh\s+\\\s+yaml\s+ado-gh\s+enabled\s+X failed$`, lines[1])
	assert.Regexp(t, `^4\s+deploy\s+\\Apps\\Web\s+yaml\s+web\s+enabled\s+✓ succeeded$`, lines[2])
}

func TestListDefinitionsJSON(t *testing.T) {
	e := newPLListEnv(t, false)
	e.stubDefinitions(t)
	require.NoError(t, e.run(t, "-d", "-a", "--json", "id,name,path,type,repository,queueStatus,latestRun,url"))
	var got []map[string]any
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &got))
	require.Len(t, got, 4)
	ado := got[0]
	assert.Equal(t, float64(1), ado["id"])
	assert.Equal(t, "ado-gh", ado["name"])
	assert.Equal(t, `\`, ado["path"])
	assert.Equal(t, "yaml", ado["type"])
	assert.Equal(t, "enabled", ado["queueStatus"])
	assert.Equal(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_build?definitionId=1", ado["url"])
	assert.Equal(t, map[string]any{"id": "00000000-0000-0000-0000-000000000011", "name": "ado-gh", "type": "TfsGit",
		"defaultBranch": "refs/heads/main"}, ado["repository"])
	latest := ado["latestRun"].(map[string]any)
	assert.Equal(t, float64(101), latest["id"])
	assert.Equal(t, "failed", latest["result"])
	assert.Equal(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_build/results?buildId=101", latest["url"])
	assert.Nil(t, got[2]["latestRun"], "definition 3 has no runs")
	assert.Equal(t, "classic", got[3]["type"])
}

func TestListDefinitionsEmpty(t *testing.T) {
	e := newPLListEnv(t, false)
	e.stubDefinitions(t)
	require.NoError(t, e.run(t, "-d", "--match", "nothing*"))
	assert.Empty(t, e.out.String())
	assert.Equal(t, "no pipelines match your search\n", e.errOut.String())
}

func TestListRunsPipelineURL(t *testing.T) {
	e := newPLListEnv(t, false)
	e.stubBuilds("")
	require.NoError(t, e.run(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_build?definitionId=5"))
	require.Len(t, e.queries, 1)
	assert.Equal(t, "5", e.queries[0].Get("definitions"))
}
