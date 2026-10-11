package list

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

var repoListOrgSeq atomic.Int64

type repoListEnv struct {
	org    string
	base   adocontext.Repo
	reg    *httpmock.Registry
	ios    *iostreams.IOStreams
	out    *bytes.Buffer
	errOut *bytes.Buffer
}

// newRepoListEnv returns an env for its own org: the SDK caches locations per base URL for the
// whole process.
func newRepoListEnv(t *testing.T, tty bool) *repoListEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	ios.SetTerminalWidth(120)
	org := fmt.Sprintf("repolist-%d", repoListOrgSeq.Add(1))
	return &repoListEnv{
		org: org, base: adocontext.Repo{Host: "dev.azure.com", Org: org, Project: "My Proj", Name: "web"},
		reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut,
	}
}

func (e *repoListEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			if e.base.Org == "" {
				return adocontext.Repo{}, errors.New("not an Azure DevOps repository")
			}
			return e.base, nil
		},
	}
	cmd := NewCmdList(f)
	cmd.SetArgs(args)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

// stubRepos stubs the location OPTIONS on basePath's org and one list response at basePath.
func (e *repoListEnv) stubRepos(basePath string, repos ...map[string]any) {
	e.reg.StubLocations(e.org, httpmock.GitRepoLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, basePath+"/_apis/git/repositories"),
		httpmock.JSONResponse(map[string]any{"count": len(repos), "value": repos}))
}

const repoListID = "00000000-0000-0000-0000-000000000001"

// repoListRepo returns a repo payload; extra is key/value overrides, and a nil value removes the
// key (simulating fields ADO omits for empty or minimal repos).
func repoListRepo(name, project string, extra ...any) map[string]any {
	r := map[string]any{
		"id": repoListID, "name": name,
		"project": map[string]any{"name": project}, "defaultBranch": "refs/heads/main",
		"size": 2048, "isFork": false, "isDisabled": false,
		"remoteUrl": "https://dev.azure.com/o/" + project + "/_git/" + name,
		"sshUrl":    "git@ssh.dev.azure.com:v3/o/" + project + "/" + name,
		"webUrl":    "https://dev.azure.com/o/" + project + "/_git/" + name,
	}
	for i := 0; i+1 < len(extra); i += 2 {
		k := extra[i].(string)
		if extra[i+1] == nil {
			delete(r, k)
		} else {
			r[k] = extra[i+1]
		}
	}
	return r
}

func repoListNames(t *testing.T, out string) []string {
	t.Helper()
	var got []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	names := []string{}
	for _, r := range got {
		names = append(names, r["name"].(string))
	}
	return names
}

func TestRepoListCurrentProjectTTY(t *testing.T) {
	e := newRepoListEnv(t, true)
	e.stubRepos(e.org+"/My%20Proj",
		repoListRepo("web", "My Proj"),
		repoListRepo("Api", "My Proj", "isFork", true, "size", 3*1024*1024),
		repoListRepo("old", "My Proj", "isDisabled", true),
		repoListRepo("blank", "My Proj", "defaultBranch", nil, "size", 0),
	)
	require.NoError(t, e.run(t))
	e.reg.Verify(t)

	out := e.out.String()
	assert.Contains(t, out, "Showing 4 of 4 repositories in "+e.org+"/My Proj")
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "DEFAULT BRANCH")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 7)
	assert.Regexp(t, `^Api\s+main\s+fork\s+3\.0 MiB$`, lines[3])
	assert.Regexp(t, `^blank\s+empty\s+0 B$`, lines[4])
	assert.Regexp(t, `^old\s+main\s+disabled\s+2\.0 KiB$`, lines[5])
	assert.Regexp(t, `^web\s+main\s+2\.0 KiB$`, lines[6])
	assert.Empty(t, e.errOut.String())
}

func TestRepoListOrgWide(t *testing.T) {
	e := newRepoListEnv(t, true)
	e.stubRepos(e.org, repoListRepo("b", "P2"), repoListRepo("a", "P1"))

	require.NoError(t, e.run(t, e.org))
	e.reg.Verify(t)
	assert.Equal(t, "/"+e.org+"/_apis/git/repositories", e.reg.Requests[1].URL.EscapedPath())
	out := e.out.String()
	assert.Contains(t, out, "repositories in "+e.org+"\n")
	assert.Regexp(t, `(?m)^P1/a\s+main`, out)
	assert.Regexp(t, `(?m)^P2/b\s+main`, out)
	assert.Less(t, strings.Index(out, "P1/a"), strings.Index(out, "P2/b"))
}

func TestRepoListOrgProject(t *testing.T) {
	e := newRepoListEnv(t, false)
	e.base = adocontext.Repo{}
	e.stubRepos(e.org+"/Other", repoListRepo("x", "Other"))

	require.NoError(t, e.run(t, e.org+"/Other"))
	e.reg.Verify(t)
	assert.Equal(t, "https", e.reg.Requests[1].URL.Scheme)
	assert.Equal(t, "dev.azure.com", e.reg.Requests[1].URL.Host)
	assert.Equal(t, "x\tmain\t\t2048\n", e.out.String())
}

func TestRepoListOrgKeepsBaseHost(t *testing.T) {
	e := newRepoListEnv(t, false)
	e.base.Host = e.org + ".visualstudio.com"
	e.reg.StubLocations("", httpmock.GitRepoLocations...)
	e.reg.Register(func(req *http.Request) bool {
		return req.URL.Host == e.org+".visualstudio.com" && req.URL.Path == "/_apis/git/repositories"
	}, httpmock.JSONResponse(map[string]any{"count": 1, "value": []any{repoListRepo("x", "P")}}))

	require.NoError(t, e.run(t, strings.ToUpper(e.org)))
	e.reg.Verify(t)
	assert.Equal(t, "P/x\tmain\t\t2048\n", e.out.String())
}

func TestRepoListBaseRepoError(t *testing.T) {
	e := newRepoListEnv(t, false)
	e.base = adocontext.Repo{}
	assert.EqualError(t, e.run(t), "not an Azure DevOps repository")
}

func TestRepoListInvalidArgs(t *testing.T) {
	for _, args := range [][]string{
		{"a/b/c"}, {"a/"}, {"/b"}, {`a\b`}, {"a", "b"},
		{"-L", "0"}, {"--fork", "--source"}, {"--disabled", "--no-disabled"},
		{"--archived", "--no-disabled"}, {"--match", "["},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			e := newRepoListEnv(t, false)
			assert.Error(t, e.run(t, args...))
			assert.Empty(t, e.reg.Requests)
		})
	}
}

func TestRepoListFilterSortLimit(t *testing.T) {
	repos := []map[string]any{
		repoListRepo("svc-b", "P", "isFork", true),
		repoListRepo("svc-a", "P"),
		repoListRepo("SVC-C", "P", "isDisabled", true),
		repoListRepo("web", "P", "isFork", true, "isDisabled", true),
		repoListRepo("svc-d", "P", "isFork", nil, "isDisabled", nil, "size", nil, "defaultBranch", nil, "webUrl", nil),
	}
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{nil, []string{"svc-a", "svc-b", "SVC-C", "svc-d", "web"}},
		{[]string{"-L", "2"}, []string{"svc-a", "svc-b"}},
		{[]string{"--fork"}, []string{"svc-b", "web"}},
		{[]string{"--source"}, []string{"svc-a", "SVC-C", "svc-d"}},
		{[]string{"--disabled"}, []string{"SVC-C", "web"}},
		{[]string{"--archived"}, []string{"SVC-C", "web"}},
		{[]string{"--no-disabled"}, []string{"svc-a", "svc-b", "svc-d"}},
		{[]string{"--no-archived"}, []string{"svc-a", "svc-b", "svc-d"}},
		{[]string{"--match", "svc-*"}, []string{"svc-a", "svc-b", "SVC-C", "svc-d"}},
		// filter before limit: the limit applies to the matching repos only
		{[]string{"--match", "svc-?", "--source", "-L", "1"}, []string{"svc-a"}},
		{[]string{"--fork", "--no-disabled", "-L", "1"}, []string{"svc-b"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			e := newRepoListEnv(t, false)
			e.stubRepos(e.org+"/My%20Proj", repos...)
			require.NoError(t, e.run(t, append(tc.args, "--json", "name")...))
			e.reg.Verify(t)
			assert.Equal(t, tc.want, repoListNames(t, e.out.String()))
			assert.Len(t, e.reg.Requests, 2, "one unpaged list request")
		})
	}
}

func TestRepoListDefaultLimit(t *testing.T) {
	e := newRepoListEnv(t, false)
	var repos []map[string]any
	for i := range 31 {
		repos = append(repos, repoListRepo(fmt.Sprintf("r%02d", i), "My Proj"))
	}
	e.stubRepos(e.org+"/My%20Proj", repos...)
	require.NoError(t, e.run(t, "--json", "name"))
	names := repoListNames(t, e.out.String())
	assert.Len(t, names, 30)
	assert.Equal(t, "r29", names[29])
}

func TestRepoListJSON(t *testing.T) {
	e := newRepoListEnv(t, false)
	e.stubRepos(e.org+"/My%20Proj",
		repoListRepo("a", "My Proj", "isFork", true, "parentRepository", map[string]any{"name": "up"}),
		repoListRepo("empty", "My Proj", "isFork", nil, "isDisabled", nil, "size", nil, "defaultBranch", nil, "webUrl", nil, "sshUrl", nil),
	)
	require.NoError(t, e.run(t, "--json", "id,name,project,defaultBranch,size,isDisabled,isFork,remoteUrl,sshUrl,webUrl"))
	e.reg.Verify(t)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &got))
	require.Len(t, got, 2)
	assert.Equal(t, map[string]any{
		"id": repoListID, "name": "a", "project": "My Proj", "defaultBranch": "main", "size": 2048.0,
		"isDisabled": false, "isFork": true, "remoteUrl": "https://dev.azure.com/o/My Proj/_git/a",
		"sshUrl": "git@ssh.dev.azure.com:v3/o/My Proj/a", "webUrl": "https://dev.azure.com/o/My Proj/_git/a",
	}, got[0])
	assert.Equal(t, "", got[1]["defaultBranch"])
	assert.Equal(t, 0.0, got[1]["size"])
	assert.Equal(t, false, got[1]["isFork"])
	assert.Equal(t, false, got[1]["isDisabled"])
	assert.Equal(t, "", got[1]["webUrl"])
	assert.Equal(t, "", got[1]["sshUrl"])
}

func TestRepoListJSONNoParentRepository(t *testing.T) {
	e := newRepoListEnv(t, false)
	err := e.run(t, "--json", "parentRepository")
	assert.ErrorContains(t, err, `Unknown JSON field: "parentRepository"`)
	assert.NotContains(t, err.Error(), "  parentRepository")
}

func TestRepoListNullFieldsRender(t *testing.T) {
	e := newRepoListEnv(t, false)
	e.stubRepos(e.org+"/My%20Proj",
		map[string]any{"name": "bare", "project": nil},
		repoListRepo("svc", "My Proj", "isFork", nil, "isDisabled", nil, "size", nil, "webUrl", nil),
	)
	require.NoError(t, e.run(t))
	e.reg.Verify(t)
	assert.Equal(t, "bare\t\tempty\t0\nsvc\tmain\t\t0\n", e.out.String())
}

func TestRepoListEmptyResult(t *testing.T) {
	for _, tty := range []bool{true, false} {
		e := newRepoListEnv(t, tty)
		e.stubRepos(e.org+"/My%20Proj", repoListRepo("web", "My Proj"))
		require.NoError(t, e.run(t, "--match", "nope*"))
		assert.Empty(t, e.out.String())
		assert.Equal(t, "no repositories match your search\n", e.errOut.String())
	}
}

func TestRepoListEmptyResultJSON(t *testing.T) {
	e := newRepoListEnv(t, false)
	e.stubRepos(e.org + "/My%20Proj")
	require.NoError(t, e.run(t, "--json", "name"))
	assert.Equal(t, "[]\n", e.out.String())
	assert.Empty(t, e.errOut.String())
}

func TestRepoListAPIError(t *testing.T) {
	e := newRepoListEnv(t, false)
	e.reg.StubLocations(e.org, httpmock.GitRepoLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF200016: project not found"}))
	assert.ErrorContains(t, e.run(t), "TF200016")
}

func TestRepoListHiddenAliases(t *testing.T) {
	cmd := NewCmdList(&cmdutil.Factory{})
	assert.True(t, cmd.Flags().Lookup("archived").Hidden)
	assert.True(t, cmd.Flags().Lookup("no-archived").Hidden)
	assert.False(t, cmd.Flags().Lookup("disabled").Hidden)
}
