package view

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/browsertest"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const repoViewID = "11111111-2222-3333-4444-555555555555"

var repoViewOrgSeq atomic.Int64

type repoViewEnv struct {
	org     string
	base    adocontext.Repo
	reg     *httpmock.Registry
	ios     *iostreams.IOStreams
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	browser *browsertest.Browser
}

// newRepoViewEnv returns an env for its own org: the SDK caches locations per base URL for the
// whole process.
func newRepoViewEnv(t *testing.T, tty bool) *repoViewEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_ORG", "")
	t.Setenv("ADO_PROJECT", "")
	t.Setenv("GLAMOUR_STYLE", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	ios.SetColorEnabled(tty)
	ios.SetTerminalWidth(100)
	org := fmt.Sprintf("repoview-%d", repoViewOrgSeq.Add(1))
	return &repoViewEnv{
		org: org, base: adocontext.Repo{Host: "dev.azure.com", Org: org, Project: "My Proj", Name: "web"},
		reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut, browser: &browsertest.Browser{},
	}
}

func (e *repoViewEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo:   func() (adocontext.Repo, error) { return e.base, nil },
		Browser:    e.browser,
	}
	cmd := NewCmdView(f)
	cmd.SetArgs(args)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

func (e *repoViewEnv) repoPath() string {
	return e.org + "/My%20Proj/_apis/git/repositories/"
}

// stubRepo stubs the location OPTIONS and the repository GET; extra are key/value overrides
// (nil deletes the key).
func (e *repoViewEnv) stubRepo(extra ...any) {
	r := map[string]any{
		"id": repoViewID, "name": "web", "project": map[string]any{"name": "My Proj"},
		"defaultBranch": "refs/heads/main", "size": 2048, "isFork": false, "isDisabled": false,
		"remoteUrl": "https://o@dev.azure.com/o/My%20Proj/_git/web",
		"sshUrl":    "git@ssh.dev.azure.com:v3/o/My%20Proj/web",
		"webUrl":    "https://dev.azure.com/o/My%20Proj/_git/web",
	}
	for i := 0; i+1 < len(extra); i += 2 {
		if extra[i+1] == nil {
			delete(r, extra[i].(string))
		} else {
			r[extra[i].(string)] = extra[i+1]
		}
	}
	e.reg.StubLocations(e.org, httpmock.GitRepoLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, e.repoPath()+"web"), httpmock.JSONResponse(r))
}

// stubItems stubs the root listing with files (name → objectId) plus the root folder and a
// README folder that must be ignored.
func (e *repoViewEnv) stubItems(files ...string) {
	items := []any{
		map[string]any{"path": "/", "isFolder": true, "objectId": "root"},
		map[string]any{"path": "/readme-dir", "isFolder": true, "objectId": "dir"},
	}
	for i := 0; i+1 < len(files); i += 2 {
		items = append(items, map[string]any{"path": "/" + files[i], "objectId": files[i+1]})
	}
	e.reg.Register(httpmock.REST(http.MethodGet, e.repoPath()+repoViewID+"/items"),
		httpmock.JSONResponse(map[string]any{"count": len(items), "value": items}))
}

func (e *repoViewEnv) stubBlob(objectID, content string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.repoPath()+repoViewID+"/blobs/"+objectID),
		httpmock.StringResponse(content))
}

func (e *repoViewEnv) itemsRequests() []*http.Request {
	var out []*http.Request
	for _, r := range e.reg.Requests {
		if strings.HasSuffix(r.URL.Path, "/items") {
			out = append(out, r)
		}
	}
	return out
}

const repoViewReadme = "# Hello\n\nSome **bold** text.\n"

func TestRepoViewTTY(t *testing.T) {
	e := newRepoViewEnv(t, true)
	e.stubRepo()
	e.stubItems("LICENSE", "lic", "README.md", "b1")
	e.stubBlob("b1", repoViewReadme)
	require.NoError(t, e.run(t))
	e.reg.Verify(t)

	out := e.out.String()
	for _, want := range []string{
		"My Proj/web", "main", "2.0 KiB",
		"https://o@dev.azure.com/o/My%20Proj/_git/web", "git@ssh.dev.azure.com:v3/o/My%20Proj/web",
		"View this repository on Azure DevOps: https://dev.azure.com/o/My%20Proj/_git/web",
		"Hello", "\x1b[",
	} {
		assert.Contains(t, out, want)
	}
	assert.NotContains(t, out, "**bold**", "markdown rendered with glamour")
	assert.NotContains(t, out, "fork")
	assert.Empty(t, e.errOut.String())
	q := e.itemsRequests()[0].URL.Query()
	assert.False(t, q.Has("versionDescriptor.version"), "default branch without a descriptor")
}

func TestRepoViewPiped(t *testing.T) {
	e := newRepoViewEnv(t, false)
	e.stubRepo("isFork", true, "parentRepository", map[string]any{
		"id": "99999999-2222-3333-4444-555555555555", "name": "upstream", "project": map[string]any{"name": "other"},
	})
	e.stubItems("README.md", "b1")
	e.stubBlob("b1", "\uFEFF# Hello\r\n\r\nSome **bold** text.\r\n")
	require.NoError(t, e.run(t))

	assert.Equal(t, "name:\tMy Proj/web\n"+
		"project:\tMy Proj\n"+
		"default branch:\tmain\n"+
		"size:\t2048\n"+
		"https url:\thttps://o@dev.azure.com/o/My%20Proj/_git/web\n"+
		"ssh url:\tgit@ssh.dev.azure.com:v3/o/My%20Proj/web\n"+
		"url:\thttps://dev.azure.com/o/My%20Proj/_git/web\n"+
		"parent:\tother/upstream\n"+
		"--\n"+
		repoViewReadme, e.out.String())
}

func TestRepoViewForkTTY(t *testing.T) {
	e := newRepoViewEnv(t, true)
	e.ios.SetColorEnabled(false)
	e.stubRepo("isFork", true, "parentRepository", map[string]any{"name": "upstream", "project": map[string]any{"name": "other"}})
	e.stubItems()
	require.NoError(t, e.run(t))
	assert.Contains(t, e.out.String(), "Fork of other/upstream")
	assert.Contains(t, e.out.String(), "This repository does not have a README")
}

func TestRepoViewBranch(t *testing.T) {
	e := newRepoViewEnv(t, false)
	e.stubRepo()
	e.stubItems("readme.markdown", "b1")
	e.stubBlob("b1", "hi\n")
	require.NoError(t, e.run(t, "--branch", "feature/x"))
	q := e.itemsRequests()[0].URL.Query()
	assert.Equal(t, "feature/x", q.Get("versionDescriptor.version"))
	assert.Equal(t, "branch", q.Get("versionDescriptor.versionType"))
	assert.True(t, strings.HasSuffix(e.out.String(), "--\nhi\n"))
}

func TestRepoViewNonMarkdownReadmeRawOnTTY(t *testing.T) {
	e := newRepoViewEnv(t, true)
	e.stubRepo()
	e.stubItems("README.txt", "b1")
	e.stubBlob("b1", "Plain **text**\r\n")
	require.NoError(t, e.run(t))
	assert.Contains(t, e.out.String(), "\nPlain **text**\n")
}

func TestRepoViewArgument(t *testing.T) {
	e := newRepoViewEnv(t, false)
	e.base = adocontext.Repo{}
	e.org = fmt.Sprintf("repoview-arg-%d", repoViewOrgSeq.Add(1))
	e.stubRepo()
	e.stubItems()
	require.NoError(t, e.run(t, e.org+"/My Proj/web"))
	assert.Contains(t, e.out.String(), "--\nThis repository does not have a README\n")
}

func TestRepoViewEmptyRepoNoItemsCall(t *testing.T) {
	e := newRepoViewEnv(t, true)
	e.stubRepo("defaultBranch", nil, "size", 0)
	require.NoError(t, e.run(t))
	e.reg.Verify(t)
	assert.Empty(t, e.itemsRequests())
	assert.Contains(t, e.out.String(), "This repository does not have a README")
	assert.Contains(t, e.out.String(), "0 B")
}

func TestRepoViewListingNotFound(t *testing.T) {
	e := newRepoViewEnv(t, false)
	e.stubRepo()
	e.reg.Register(httpmock.REST(http.MethodGet, e.repoPath()+repoViewID+"/items"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401174: branch not found"}))
	require.NoError(t, e.run(t, "-b", "nope"))
	assert.True(t, strings.HasSuffix(e.out.String(), "--\nThis repository does not have a README\n"))
}

func TestRepoViewListingError(t *testing.T) {
	e := newRepoViewEnv(t, false)
	e.stubRepo()
	e.reg.Register(httpmock.REST(http.MethodGet, e.repoPath()+repoViewID+"/items"),
		httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "TF401175: bad version"}))
	assert.ErrorContains(t, e.run(t, "-b", "nope"), "TF401175")
}

func TestRepoViewBlobError(t *testing.T) {
	e := newRepoViewEnv(t, false)
	e.stubRepo()
	e.stubItems("README.md", "b1")
	e.reg.Register(httpmock.REST(http.MethodGet, e.repoPath()+repoViewID+"/blobs/b1"),
		httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
	assert.ErrorContains(t, e.run(t), "boom")
}

func TestRepoViewRepoNotFound(t *testing.T) {
	e := newRepoViewEnv(t, false)
	e.reg.StubLocations(e.org, httpmock.GitRepoLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, e.repoPath()+"web"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401019: repo not found"}))
	assert.ErrorContains(t, e.run(t), "TF401019")
}

func TestRepoViewDisabled(t *testing.T) {
	for _, tty := range []bool{true, false} {
		t.Run(fmt.Sprint("tty=", tty), func(t *testing.T) {
			e := newRepoViewEnv(t, tty)
			e.stubRepo("isDisabled", true)
			require.NoError(t, e.run(t))
			e.reg.Verify(t)
			assert.Empty(t, e.itemsRequests())
			assert.Contains(t, e.out.String(), "This repository is disabled")
			assert.NotContains(t, e.out.String(), "README")
		})
	}
	e := newRepoViewEnv(t, false)
	e.stubRepo("isDisabled", true)
	require.NoError(t, e.run(t))
	assert.Contains(t, e.out.String(), "disabled:\ttrue\n")
}

func TestRepoViewJSON(t *testing.T) {
	e := newRepoViewEnv(t, false)
	e.stubRepo()
	e.stubItems("README.md", "b1")
	e.stubBlob("b1", "\uFEFF# Hi\r\n")
	require.NoError(t, e.run(t, "--json", "name,defaultBranch,readme,parentRepository"))
	var got map[string]any
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &got))
	assert.Equal(t, map[string]any{"name": "web", "defaultBranch": "main", "readme": "# Hi\n", "parentRepository": nil}, got)
}

func TestRepoViewJSONWithoutReadmeSkipsItems(t *testing.T) {
	e := newRepoViewEnv(t, false)
	e.stubRepo()
	require.NoError(t, e.run(t, "--json", "id,size,isDisabled,isFork,remoteUrl,sshUrl,webUrl,project"))
	assert.Empty(t, e.itemsRequests())
	assert.Contains(t, e.out.String(), `"id":"`+repoViewID+`"`)
}

func TestRepoViewWeb(t *testing.T) {
	e := newRepoViewEnv(t, true)
	require.NoError(t, e.run(t, "-w"))
	assert.Equal(t, []string{"https://dev.azure.com/" + e.org + "/My%20Proj/_git/web"}, e.browser.URLs)
	assert.Contains(t, e.errOut.String(), "Opening https://dev.azure.com/"+e.org+"/My%20Proj/_git/web in your browser.")
	assert.Empty(t, e.reg.Requests, "no API call")

	e = newRepoViewEnv(t, false)
	require.NoError(t, e.run(t, "--web", "-b", "feature/a b"))
	assert.Equal(t, []string{"https://dev.azure.com/" + e.org + "/My%20Proj/_git/web?version=GBfeature%2Fa%20b"}, e.browser.URLs)
	assert.Empty(t, e.errOut.String())
}

func TestRepoViewBadArgs(t *testing.T) {
	e := newRepoViewEnv(t, false)
	assert.Error(t, e.run(t, "a", "b"))
}

func TestPickReadme(t *testing.T) {
	file := func(n string) api.TreeEntry { return api.TreeEntry{Name: n, ObjectID: n} }
	for _, tc := range []struct {
		name    string
		entries []api.TreeEntry
		want    string
	}{
		{"none", []api.TreeEntry{file("LICENSE"), file("main.go")}, ""},
		{"folder ignored", []api.TreeEntry{{Name: "README", IsFolder: true}}, ""},
		{"case-insensitive", []api.TreeEntry{file("main.go"), file("ReadMe")}, "ReadMe"},
		{"markdown first", []api.TreeEntry{file("README"), file("README.txt"), file("readme.MD")}, "readme.MD"},
		{"markdown variants", []api.TreeEntry{file("README.rst"), file("README.mkdown")}, "README.mkdown"},
		{"sorted among markdown", []api.TreeEntry{file("README.mdown"), file("README.markdown")}, "README.markdown"},
		{"sorted among others", []api.TreeEntry{file("README.txt"), file("README.rst")}, "README.rst"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pickReadme(tc.entries)
			assert.Equal(t, tc.want != "", ok)
			assert.Equal(t, tc.want, got.Name)
		})
	}
}

func TestRepoViewWrapWidth(t *testing.T) {
	long := strings.Repeat("word ", 60) + "\n"
	for _, tc := range []struct{ width, max int }{{200, 120}, {60, 60}} {
		e := newRepoViewEnv(t, true)
		e.ios.SetColorEnabled(false) // notty style: no escapes to measure around
		e.ios.SetTerminalWidth(tc.width)
		e.stubRepo()
		e.stubItems("README.md", "b1")
		e.stubBlob("b1", long)
		require.NoError(t, e.run(t))
		longest := 0
		for _, l := range strings.Split(e.out.String(), "\n") {
			if strings.Contains(l, "word") {
				longest = max(longest, len(strings.TrimRight(l, " ")))
			}
		}
		assert.LessOrEqual(t, longest, tc.max, tc.width)
		assert.Greater(t, longest, tc.max-10, tc.width)
	}
}
