package list

import (
	"bytes"
	"cmp"
	"encoding/json"
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

const relListUserID = "a0000000-0000-0000-0000-000000000007"

type relListEnv struct {
	org    string
	reg    *httpmock.Registry
	ios    *iostreams.IOStreams
	out    *bytes.Buffer
	errOut *bytes.Buffer
	// queries are the query strings of the release list requests, in order.
	queries []url.Values
}

// newRelListEnv returns an env for its own org (the SDK caches discovery per base URL) whose
// Release area is on vsrm.dev.azure.com.
func newRelListEnv(t *testing.T, tty bool) *relListEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "rellist-" + strings.NewReplacer("/", "-", " ", "-", "@", "-", ",", "-").Replace(strings.ToLower(t.Name()))
	e := &relListEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut}
	t.Cleanup(func() { e.reg.Verify(t) })
	e.reg.StubLocations(org, httpmock.ResourceAreasLocation)
	e.reg.StubVSRM(org)
	return e
}

// stubReleases answers the next release list request with rels after recording its query.
func (e *relListEnv) stubReleases(rels ...map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/release/releases"), func(r *http.Request) (*http.Response, error) {
		e.queries = append(e.queries, r.URL.Query())
		if rels == nil {
			rels = []map[string]any{}
		}
		return httpmock.JSONResponse(map[string]any{"count": len(rels), "value": rels})(r)
	})
}

// stubReleaseDefinitions answers the next definitions request with body after checking query.
func (e *relListEnv) stubReleaseDefinitions(t *testing.T, want map[string]string, body any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/release/definitions"), func(r *http.Request) (*http.Response, error) {
		for k, v := range want {
			assert.Equal(t, v, r.URL.Query().Get(k), k)
		}
		return httpmock.JSONResponse(body)(r)
	})
}

func (e *relListEnv) run(t *testing.T, args ...string) error {
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

// relListRelease is a release as the list returns it with $expand=environments.
func relListRelease(id int, name, status string, envs ...string) map[string]any {
	var environments []map[string]any
	for i := 0; i+1 < len(envs); i += 2 {
		environments = append(environments, map[string]any{"id": id*10 + i, "name": envs[i], "status": envs[i+1]})
	}
	return map[string]any{
		"id": id, "name": name, "status": status, "createdOn": "2026-10-01T10:00:00Z",
		"createdBy":         map[string]any{"id": relListUserID, "displayName": "Ann Lee", "uniqueName": "ann@contoso.com"},
		"releaseDefinition": map[string]any{"id": 1, "name": "ado-live-release", "path": `\ado-live`},
		"environments":      environments,
	}
}

func TestReleaseListFilters(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		want  map[string]string
		setup func(*testing.T, *relListEnv)
	}{
		{"none", nil, map[string]string{}, nil},
		{"status", []string{"-s", "abandoned"}, map[string]string{"statusFilter": "abandoned"}, nil},
		{"branch short", []string{"-b", "main"}, map[string]string{"sourceBranchFilter": "refs/heads/main"}, nil},
		{"branch full ref kept", []string{"--branch", "refs/tags/v1"}, map[string]string{"sourceBranchFilter": "refs/tags/v1"}, nil},
		{"user guid", []string{"-u", relListUserID}, map[string]string{"createdBy": relListUserID}, nil},
		{"user me", []string{"-u", "@me"}, map[string]string{"createdBy": relListUserID}, func(_ *testing.T, e *relListEnv) {
			e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"),
				httpmock.JSONResponse(map[string]any{"authenticatedUser": map[string]any{"id": relListUserID}}))
		}},
		{"definition id", []string{"5"}, map[string]string{"definitionId": "5"}, nil},
		{"definition name", []string{"ADO-live-release"}, map[string]string{"definitionId": "1"}, func(t *testing.T, e *relListEnv) {
			e.stubReleaseDefinitions(t, map[string]string{"searchText": "ADO-live-release", "isExactNameMatch": "true"},
				json.RawMessage(pipelinefixtures.Read(t, "release_definitions.json")))
		}},
		{"limit", []string{"-L", "3", "-s", "active"}, map[string]string{"$top": "3", "statusFilter": "active"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRelListEnv(t, false)
			if tt.setup != nil {
				tt.setup(t, e)
			}
			e.stubReleases(relListRelease(1, "Release-1", "active"))
			require.NoError(t, e.run(t, tt.args...))
			require.Len(t, e.queries, 1)
			q := e.queries[0]
			assert.Equal(t, "environments", q.Get("$expand"))
			assert.Equal(t, cmp.Or(tt.want["$top"], "20"), q.Get("$top"), "--limit defaults to 20")
			for _, k := range []string{"statusFilter", "sourceBranchFilter", "createdBy", "definitionId"} {
				assert.Equal(t, tt.want[k], q.Get(k), k)
			}
		})
	}
}

func TestReleaseListTTY(t *testing.T) {
	e := newRelListEnv(t, true)
	e.ios.SetTerminalWidth(200)
	e.stubReleases(
		relListRelease(2, "Release-2", "active", "Dev", "succeeded", "Prod", "inProgress"),
		relListRelease(1, "Release-1", "abandoned", "Dev", "rejected", "Prod", "notStarted"))
	require.NoError(t, e.run(t))
	lines := strings.Split(strings.TrimSpace(e.out.String()), "\n")
	require.Len(t, lines, 3)
	assert.Regexp(t, `^ID\s+NAME\s+DEFINITION\s+STATUS\s+ENVIRONMENTS\s+CREATED$`, lines[0])
	assert.Regexp(t, `^2\s+Release-2\s+ado-live-release\s+active\s+✓ Dev  ● Prod\s+.*ago$`, lines[1])
	assert.Regexp(t, `^1\s+Release-1\s+ado-live-release\s+abandoned\s+X Dev  ⏸ Prod\s+.*ago$`, lines[2])
	assert.Empty(t, e.errOut.String())
}

func TestReleaseListTSV(t *testing.T) {
	e := newRelListEnv(t, false)
	r := relListRelease(2, "Release\t2", "draft")
	delete(r, "releaseDefinition")
	e.stubReleases(relListRelease(1, "Release-1", "active", "Dev", "succeeded", "Prod", "inProgress"), r)
	require.NoError(t, e.run(t))
	assert.Equal(t,
		"1\tRelease-1\tado-live-release\tactive\tDev:succeeded,Prod:inProgress\t2026-10-01T10:00:00Z\n"+
			"2\tRelease 2\t\tdraft\t\t2026-10-01T10:00:00Z\n", e.out.String())
}

func TestReleaseListJSON(t *testing.T) {
	e := newRelListEnv(t, false)
	e.stubReleases(relListRelease(1, "Release-1", "active", "Dev", "succeeded", "Prod", "inProgress"))
	require.NoError(t, e.run(t, "--json", strings.Join(releaseJSONFields, ",")))
	var got []map[string]any
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &got))
	assert.Equal(t, []map[string]any{{
		"id": float64(1), "name": "Release-1", "status": "active",
		"definition": map[string]any{"id": float64(1), "name": "ado-live-release", "path": `\ado-live`},
		"createdBy":  map[string]any{"id": relListUserID, "displayName": "Ann Lee", "uniqueName": "ann@contoso.com"},
		"createdOn":  "2026-10-01T10:00:00Z",
		"environments": []any{
			map[string]any{"name": "Dev", "status": "succeeded"}, map[string]any{"name": "Prod", "status": "inProgress"},
		},
		"url": "https://dev.azure.com/" + e.org + "/My%20Proj/_releaseProgress?_a=release-pipeline-progress&releaseId=1",
	}}, got)
}

// TestReleaseListJSONFixture reads the live capture, which has no environments: the field is
// an empty list, not null.
func TestReleaseListJSONFixture(t *testing.T) {
	e := newRelListEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/release/releases"),
		httpmock.JSONResponse(json.RawMessage(pipelinefixtures.Read(t, "releases.json"))))
	require.NoError(t, e.run(t, "--json", "id,name,createdBy,environments"))
	assert.JSONEq(t, `[{"id":1,"name":"Release-1","environments":[],
		"createdBy":{"id":"00000000-0000-0000-0000-000000000940","displayName":"Test User","uniqueName":""}}]`, e.out.String())
}

func TestReleaseListEmpty(t *testing.T) {
	for _, tty := range []bool{true, false} {
		t.Run(map[bool]string{true: "tty", false: "notty"}[tty], func(t *testing.T) {
			e := newRelListEnv(t, tty)
			e.stubReleases()
			require.NoError(t, e.run(t, "-s", "draft"))
			assert.Empty(t, e.out.String())
			assert.Equal(t, "no releases match your search\n", e.errOut.String())
		})
	}
}

func TestReleaseListEmptyJSON(t *testing.T) {
	e := newRelListEnv(t, false)
	e.stubReleases()
	require.NoError(t, e.run(t, "--json", "id"))
	assert.Equal(t, "[]\n", e.out.String())
}

func TestReleaseListDefinitionNotFound(t *testing.T) {
	e := newRelListEnv(t, false)
	e.stubReleaseDefinitions(t, nil, map[string]any{"count": 1, "value": []map[string]any{{"id": 3, "name": "other"}}})
	err := e.run(t, "nope")
	assert.EqualError(t, err, "no release definition with that name in project My Proj; list them with: ado release list -d")
	assert.Empty(t, e.queries)
}

func TestReleaseListDefinitionAmbiguous(t *testing.T) {
	e := newRelListEnv(t, false)
	e.stubReleaseDefinitions(t, nil, map[string]any{"count": 2, "value": []map[string]any{
		{"id": 3, "name": "deploy", "path": `\`}, {"id": 4, "name": "Deploy", "path": `\Apps`},
	}})
	err := e.run(t, "deploy")
	assert.EqualError(t, err, "several release definitions in project My Proj have that name; pass an ID:\n  \\deploy (ID 3)\n  \\Apps\\Deploy (ID 4)")
}

func TestReleaseListUserNotFound(t *testing.T) {
	e := newRelListEnv(t, false)
	e.reg = &httpmock.Registry{} // no release API call is made
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{}))
	assert.ErrorContains(t, e.run(t, "-u", "@me"), "reading the current user")
	assert.Empty(t, e.queries)
}

func TestReleaseListServerError(t *testing.T) {
	e := newRelListEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/release/releases"),
		httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "TF400813: no access"}))
	assert.ErrorContains(t, e.run(t), "TF400813")
}

func TestReleaseListFlagErrors(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"-L", "0"}, "invalid value for --limit: 0"},
		{[]string{"-s", "done"}, `invalid value for --status: "done"`},
		{[]string{"-d", "-b", "main"}, "--branch cannot be used with --definitions"},
		{[]string{"-d", "-u", "@me"}, "--user cannot be used with --definitions"},
		{[]string{"-d", "1"}, "a <definition> argument cannot be used with --definitions"},
		{[]string{"--json", "path"}, `unknown JSON field "path"`},
		{[]string{"-d", "--json", "status"}, `unknown JSON field "status"`},
		{[]string{"a", "b"}, "accepts at most 1 arg"},
		{[]string{" "}, "invalid release definition"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			e := newRelListEnv(t, false)
			e.reg = &httpmock.Registry{} // nothing may be sent
			err := e.run(t, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Empty(t, e.reg.Requests)
		})
	}
}

// relListDefs is a definitions page with environments, as $expand=environments returns it.
var relListDefs = map[string]any{"count": 2, "value": []map[string]any{
	{"id": 1, "name": "ado-live-release", "path": `\ado-live`, "environments": []map[string]any{{"id": 1, "name": "Dev"}, {"id": 2, "name": "Prod"}}},
	{"id": 2, "name": "empty", "path": `\`},
}}

func TestReleaseListDefinitionsTSV(t *testing.T) {
	e := newRelListEnv(t, false)
	e.stubReleaseDefinitions(t, map[string]string{"$expand": "environments", "$top": ""}, relListDefs)
	require.NoError(t, e.run(t, "-d"))
	assert.Equal(t, "1\tado-live-release\t\\ado-live\tDev,Prod\n2\tempty\t\\\t\n", e.out.String())
}

func TestReleaseListDefinitionsTTY(t *testing.T) {
	e := newRelListEnv(t, true)
	e.ios.SetTerminalWidth(200)
	e.stubReleaseDefinitions(t, map[string]string{"$top": "1"}, relListDefs)
	require.NoError(t, e.run(t, "--definitions", "-L", "1"))
	lines := strings.Split(strings.TrimSpace(e.out.String()), "\n")
	require.Len(t, lines, 2)
	assert.Regexp(t, `^ID\s+NAME\s+FOLDER\s+ENVIRONMENTS$`, lines[0])
	assert.Regexp(t, `^1\s+ado-live-release\s+\\ado-live\s+Dev,Prod$`, lines[1])
}

func TestReleaseListDefinitionsJSON(t *testing.T) {
	e := newRelListEnv(t, false)
	e.stubReleaseDefinitions(t, nil, relListDefs)
	require.NoError(t, e.run(t, "-d", "--json", strings.Join(defJSONFields, ",")))
	base := "https://dev.azure.com/" + e.org + "/My%20Proj/_release?definitionId="
	assert.JSONEq(t, `[
		{"id":1,"name":"ado-live-release","path":"\\ado-live","environments":[{"id":1,"name":"Dev"},{"id":2,"name":"Prod"}],"url":"`+base+`1"},
		{"id":2,"name":"empty","path":"\\","environments":[],"url":"`+base+`2"}]`, e.out.String())
}

func TestReleaseListDefinitionsEmpty(t *testing.T) {
	e := newRelListEnv(t, false)
	e.stubReleaseDefinitions(t, nil, map[string]any{"count": 0, "value": []any{}})
	require.NoError(t, e.run(t, "-d"))
	assert.Empty(t, e.out.String())
	assert.Equal(t, "no release definitions found\n", e.errOut.String())
}

func TestReleaseListDefinitionsError(t *testing.T) {
	e := newRelListEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/release/definitions"),
		httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
	assert.ErrorContains(t, e.run(t, "-d"), "boom")
}
