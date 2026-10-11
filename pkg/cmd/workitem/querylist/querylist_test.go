package querylist

import (
	"bytes"
	"io"
	"net/http"
	"strings"
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

const (
	qlTeamID = "6f0c0000-0000-0000-0000-00000000000a"
	qlDeepID = "6f0c0000-0000-0000-0000-00000000000b"
)

// qlRoots is a GetQueries $depth=2 response: Shared Queries/Team/Deep is below the depth, so
// it has hasChildren and no children.
const qlRoots = `{"count":2,"value":[
 {"id":"6f0c0000-0000-0000-0000-000000000001","name":"My Queries","path":"My Queries","isFolder":true,"hasChildren":true,
  "children":[{"id":"6f0c0000-0000-0000-0000-000000000002","name":"Mine","path":"My Queries/Mine","queryType":"flat"}]},
 {"id":"6f0c0000-0000-0000-0000-000000000003","name":"Shared Queries","path":"Shared Queries","isFolder":true,"isPublic":true,"hasChildren":true,
  "children":[{"id":"` + qlTeamID + `","name":"Team","path":"Shared Queries/Team","isFolder":true,"isPublic":true,"hasChildren":true,
   "children":[{"id":"` + qlDeepID + `","name":"Deep","path":"Shared Queries/Team/Deep","isFolder":true,"isPublic":true,"hasChildren":true}]}]}]}`

// qlDeep is the GetQuery(Deep, $depth=2) response.
const qlDeep = `{"id":"` + qlDeepID + `","name":"Deep","path":"Shared Queries/Team/Deep","isFolder":true,"isPublic":true,"hasChildren":true,
 "children":[{"id":"6f0c0000-0000-0000-0000-00000000000c","name":"Bugs","path":"Shared Queries/Team/Deep/Bugs","queryType":"tree","isPublic":true}]}`

type qlEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	out, errOut *bytes.Buffer
}

func newQLEnv(t *testing.T, tty bool) *qlEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	org := "qlist-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	e := &qlEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut}
	t.Cleanup(func() { e.reg.Verify(t) })
	e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	return e
}

// stubQuery answers GET queries{suffix} with body, asserting $depth=2.
func (e *qlEnv) stubQuery(t *testing.T, suffix, body string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/queries"+suffix), func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "2", r.URL.Query().Get("$depth"))
		return httpmock.StringResponse(body)(r)
	})
}

func (e *qlEnv) run(t *testing.T, args ...string) error {
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
	}
	cmd := NewCmdList(f)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

func TestQueryList_TTYTreeRecursesBelowDepth(t *testing.T) {
	e := newQLEnv(t, true)
	e.stubQuery(t, "", qlRoots)
	e.stubQuery(t, "/"+qlDeepID, qlDeep)
	require.NoError(t, e.run(t))
	lines := strings.Split(strings.TrimRight(e.out.String(), "\n"), "\n")
	require.Len(t, lines, 7)
	assert.Regexp(t, `^NAME\s+TYPE\s+ID$`, lines[0])
	assert.Regexp(t, `^My Queries/\s+folder\s+6f0c0000-0000-0000-0000-000000000001$`, lines[1])
	assert.Regexp(t, `^  Mine\s+flat\s+6f0c0000-0000-0000-0000-000000000002$`, lines[2])
	assert.Regexp(t, `^Shared Queries/\s+folder`, lines[3])
	assert.Regexp(t, `^  Team/\s+folder\s+`+qlTeamID+`$`, lines[4])
	assert.Regexp(t, `^    Deep/\s+folder\s+`+qlDeepID+`$`, lines[5])
	assert.Regexp(t, `^      Bugs\s+tree\s+6f0c0000-0000-0000-0000-00000000000c$`, lines[6])
}

func TestQueryList_NonTTYPaths(t *testing.T) {
	e := newQLEnv(t, false)
	e.stubQuery(t, "", qlRoots)
	e.stubQuery(t, "/"+qlDeepID, qlDeep)
	require.NoError(t, e.run(t))
	assert.Equal(t, "My Queries\tfolder\t6f0c0000-0000-0000-0000-000000000001\n"+
		"My Queries/Mine\tflat\t6f0c0000-0000-0000-0000-000000000002\n"+
		"Shared Queries\tfolder\t6f0c0000-0000-0000-0000-000000000003\n"+
		"Shared Queries/Team\tfolder\t"+qlTeamID+"\n"+
		"Shared Queries/Team/Deep\tfolder\t"+qlDeepID+"\n"+
		"Shared Queries/Team/Deep/Bugs\ttree\t6f0c0000-0000-0000-0000-00000000000c\n", e.out.String())
}

func TestQueryList_FolderFilterJSON(t *testing.T) {
	e := newQLEnv(t, false)
	e.stubQuery(t, "/Shared%20Queries%2FTeam", `{"id":"`+qlTeamID+`","name":"Team","path":"Shared Queries/Team","isFolder":true,
		"isPublic":true,"hasChildren":true,"children":[{"id":"`+qlDeepID+`","name":"Deep","path":"Shared Queries/Team/Deep",
		"isFolder":true,"isPublic":true,"hasChildren":true}]}`)
	e.stubQuery(t, "/"+qlDeepID, qlDeep)
	require.NoError(t, e.run(t, "--folder", "Shared Queries/Team", "--json", "id,name,path,isFolder,queryType,isPublic"))
	assert.JSONEq(t, `[
		{"id":"`+qlDeepID+`","name":"Deep","path":"Shared Queries/Team/Deep","isFolder":true,"queryType":"","isPublic":true},
		{"id":"6f0c0000-0000-0000-0000-00000000000c","name":"Bugs","path":"Shared Queries/Team/Deep/Bugs","isFolder":false,"queryType":"tree","isPublic":true}
	]`, e.out.String())
}

func TestQueryList_FolderIsQuery(t *testing.T) {
	e := newQLEnv(t, false)
	e.stubQuery(t, "/Shared%20Queries%2FBugs", `{"id":"6f0c0000-0000-0000-0000-000000000009","name":"Bugs","path":"Shared Queries/Bugs","queryType":"flat"}`)
	assert.EqualError(t, e.run(t, "--folder", "Shared Queries/Bugs"), `"Shared Queries/Bugs" is a query, not a folder`)
}

func TestQueryList_Empty(t *testing.T) {
	e := newQLEnv(t, true)
	e.stubQuery(t, "/My%20Queries", `{"id":"6f0c0000-0000-0000-0000-000000000001","name":"My Queries","path":"My Queries","isFolder":true}`)
	require.NoError(t, e.run(t, "--folder", "My Queries"))
	assert.Empty(t, e.out.String())
	assert.Equal(t, "no saved queries found\n", e.errOut.String())
}

func TestQueryList_EmptyJSON(t *testing.T) {
	e := newQLEnv(t, false)
	e.stubQuery(t, "", `{"count":0,"value":[]}`)
	require.NoError(t, e.run(t, "--json", "id"))
	assert.JSONEq(t, `[]`, e.out.String())
}

func TestQueryList_Errors(t *testing.T) {
	t.Run("missing folder", func(t *testing.T) {
		e := newQLEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/queries/Nope"),
			httpmock.StatusStringResponse(http.StatusNotFound, `{"message":"TF401243: The query Nope does not exist","typeKey":"QueryItemNotFoundException"}`))
		assert.ErrorContains(t, e.run(t, "--folder", "Nope"), "TF401243")
	})
	t.Run("recursion", func(t *testing.T) {
		e := newQLEnv(t, false)
		e.stubQuery(t, "", qlRoots)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/queries/"+qlDeepID),
			httpmock.StatusStringResponse(http.StatusInternalServerError, `{"message":"boom"}`))
		assert.ErrorContains(t, e.run(t), "boom")
	})
	t.Run("roots", func(t *testing.T) {
		e := newQLEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/queries"),
			httpmock.StatusStringResponse(http.StatusUnauthorized, `{"message":"denied"}`))
		assert.Error(t, e.run(t))
	})
}
