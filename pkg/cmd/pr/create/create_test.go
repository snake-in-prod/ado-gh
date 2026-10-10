package create

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	gitcmd "github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/internal/git/gittest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const (
	createAnnID = "11111111-1111-1111-1111-111111111111"
	createMeID  = "33333333-3333-3333-3333-333333333333"
)

type createBrowser struct{ urls []string }

func (b *createBrowser) Browse(u string) error {
	b.urls = append(b.urls, u)
	return nil
}

type createEnv struct {
	org     string
	reg     *httpmock.Registry
	ios     *iostreams.IOStreams
	in      *bytes.Buffer
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	browser *createBrowser
	git     *gittest.Runner
	// bodies holds the JSON bodies of POST/PATCH pull request requests in order.
	bodies []map[string]any
}

// newCreateEnv returns an env for its own org: the SDK caches locations per base URL.
func newCreateEnv(t *testing.T, tty bool) *createEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "prcreate-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	e := &createEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut, browser: &createBrowser{}}
	e.git = &gittest.Runner{Replies: map[string]string{
		"symbolic-ref --quiet HEAD": "refs/heads/feature/x\n",
		"remote -v": "upstream\thttps://dev.azure.com/other/P/_git/r (fetch)\n" +
			"origin\thttps://dev.azure.com/" + org + "/My%20Proj/_git/web (fetch)\n" +
			"origin\thttps://dev.azure.com/" + org + "/My%20Proj/_git/web (push)\n",
		"rev-parse --verify --quiet refs/remotes/origin/feature/x": "abc\n",
	}}
	return e
}

func (e *createEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	e.ios.In = iotest.OneByteReader(e.in)
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "web"}, nil
		},
		Browser:   e.browser,
		Prompter:  &cmdutil.Prompter{IO: e.ios, Accessible: true},
		GitClient: &gitcmd.Client{Run: e.git.Run},
	}
	cmd := NewCmdCreate(f)
	cmd.SetArgs(args)
	cmd.SetIn(e.in)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

func (e *createEnv) stubLocations(withIMS bool) {
	e.reg.StubLocations(e.org, append([]httpmock.Location{httpmock.ResourceAreasLocation}, httpmock.GitPRLocations...)...)
	if withIMS {
		e.reg.StubIMS(e.org)
	}
}

func (e *createEnv) stubRepo(defaultBranch string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web"),
		httpmock.JSONResponse(map[string]any{"id": "44444444-4444-4444-4444-444444444444", "name": "web", "defaultBranch": defaultBranch}))
}

// capture records the JSON request body, then answers with resp.
func (e *createEnv) capture(t *testing.T, resp map[string]any) httpmock.Responder {
	return func(req *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.Unmarshal(b, &body))
		e.bodies = append(e.bodies, body)
		return httpmock.JSONResponse(resp)(req)
	}
}

func (e *createEnv) stubCreate(t *testing.T) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests"),
		e.capture(t, map[string]any{"pullRequestId": 7, "title": "x", "status": "active"}))
}

func TestCreateFlagsOnly(t *testing.T) {
	e := newCreateEnv(t, false)
	e.stubLocations(false)
	e.stubCreate(t)

	require.NoError(t, e.run(t, "-t", "Add feature", "-b", "Details", "-B", "develop", "--work-item", "12", "--work-item", "34"))
	e.reg.Verify(t)
	require.Len(t, e.bodies, 1)
	b := e.bodies[0]
	assert.Equal(t, "Add feature", b["title"])
	assert.Equal(t, "Details", b["description"])
	assert.Equal(t, "refs/heads/feature/x", b["sourceRefName"])
	assert.Equal(t, "refs/heads/develop", b["targetRefName"])
	assert.Equal(t, false, b["isDraft"])
	assert.Equal(t, []any{map[string]any{"id": "12"}, map[string]any{"id": "34"}}, b["workItemRefs"])
	assert.NotContains(t, b, "reviewers")
	assert.Equal(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_git/web/pullrequest/7\n", e.out.String())
	assert.Empty(t, e.errOut.String())
	assert.NotContains(t, strings.Join(e.git.Calls, "\n"), "push")
}

func TestCreateDefaultBaseAndDraft(t *testing.T) {
	e := newCreateEnv(t, true)
	e.stubLocations(false)
	e.stubRepo("refs/heads/trunk")
	e.stubCreate(t)

	require.NoError(t, e.run(t, "-t", "WIP", "-b", "", "-d", "-H", "topic"))
	e.reg.Verify(t)
	b := e.bodies[0]
	assert.Equal(t, "refs/heads/trunk", b["targetRefName"])
	assert.Equal(t, "refs/heads/topic", b["sourceRefName"])
	assert.Equal(t, true, b["isDraft"])
	assert.Contains(t, e.errOut.String(), "Creating draft pull request for topic into trunk in My Proj/web")
	assert.NotContains(t, e.git.Calls, "symbolic-ref --quiet HEAD", "-H skips the current branch")
}

func TestCreateFill(t *testing.T) {
	tests := []struct {
		name, log, title, body string
		args                   []string
	}{
		{name: "one commit", log: "\x1eFix login\x1fLong\nbody\n\n", title: "Fix login", body: "Long\nbody"},
		{name: "many commits", log: "\x1eThird\x1f\n\x1eSecond\x1fsb\n\x1eFirst\x1f\n", title: "feature/x", body: "- First\n- Second\n- Third"},
		{name: "no commits", log: "", title: "feature/x", body: ""},
		{name: "flags win", log: "\x1eFix login\x1fbody\n", title: "Mine", body: "body", args: []string{"-t", "Mine"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newCreateEnv(t, false)
			e.git.Replies["-c log.showSignature=false log --format=%x1e%s%x1f%b refs/remotes/origin/main..feature/x"] = tt.log
			e.stubLocations(false)
			e.stubRepo("refs/heads/main")
			e.stubCreate(t)

			require.NoError(t, e.run(t, append([]string{"--fill"}, tt.args...)...))
			e.reg.Verify(t)
			assert.Equal(t, tt.title, e.bodies[0]["title"])
			assert.Equal(t, tt.body, e.bodies[0]["description"])
		})
	}
}

func TestCreateBodyFile(t *testing.T) {
	e := newCreateEnv(t, false)
	p := filepath.Join(t.TempDir(), "body.md")
	require.NoError(t, os.WriteFile(p, []byte("From file\n"), 0o600))
	e.stubLocations(false)
	e.stubCreate(t)

	require.NoError(t, e.run(t, "-t", "T", "-F", p, "-B", "main"))
	assert.Equal(t, "From file\n", e.bodies[0]["description"])
}

func TestCreateBodyFileStdin(t *testing.T) {
	e := newCreateEnv(t, false)
	e.in.WriteString("piped")
	e.stubLocations(false)
	e.stubCreate(t)

	require.NoError(t, e.run(t, "-t", "T", "-F", "-", "-B", "main"))
	assert.Equal(t, "piped", e.bodies[0]["description"])
}

func TestCreateReviewers(t *testing.T) {
	e := newCreateEnv(t, false)
	e.stubLocations(true)
	e.reg.Register(func(req *http.Request) bool {
		return req.URL.Host == "vssps.dev.azure.com" && req.URL.Query().Get("filterValue") == "ann@contoso.com"
	}, httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{{"id": createAnnID}}}))
	e.stubCreate(t)

	require.NoError(t, e.run(t, "-t", "T", "-B", "main", "-r", "ann@contoso.com", "-r", createAnnID+",22222222-2222-2222-2222-222222222222"))
	e.reg.Verify(t)
	assert.Equal(t, []any{
		map[string]any{"id": createAnnID},
		map[string]any{"id": "22222222-2222-2222-2222-222222222222"},
	}, e.bodies[0]["reviewers"], "optional reviewers only, deduplicated")
}

func TestCreateAuto(t *testing.T) {
	e := newCreateEnv(t, false)
	e.stubLocations(false)
	e.stubCreate(t)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"),
		httpmock.JSONResponse(map[string]any{"authenticatedUser": map[string]any{"id": createMeID}}))
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/7"),
		e.capture(t, map[string]any{"pullRequestId": 7}))

	require.NoError(t, e.run(t, "-t", "T", "-B", "main", "--auto"))
	e.reg.Verify(t)
	require.Len(t, e.bodies, 2)
	assert.NotContains(t, e.bodies[0], "autoCompleteSetBy", "auto-complete can't be set on create")
	assert.Equal(t, map[string]any{"id": createMeID}, e.bodies[1]["autoCompleteSetBy"])
	assert.Contains(t, e.bodies[1], "completionOptions")
	assert.Len(t, e.bodies[1], 2, "the update sends only auto-complete fields")
	assert.Equal(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_git/web/pullrequest/7\n", e.out.String())
}

func TestCreatePushesUnpushedBranch(t *testing.T) {
	const push = "push --set-upstream origin HEAD:refs/heads/feature/x"
	tests := []struct {
		name     string
		tty      bool
		input    string
		wantPush bool
		wantErr  error
	}{
		{name: "non-TTY pushes", wantPush: true},
		{name: "TTY confirmed", tty: true, input: "y\n", wantPush: true},
		{name: "TTY declined", tty: true, input: "n\n", wantErr: cmdutil.CancelError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newCreateEnv(t, tt.tty)
			delete(e.git.Replies, "rev-parse --verify --quiet refs/remotes/origin/feature/x")
			e.git.Replies[push] = ""
			e.in.WriteString(tt.input)
			e.stubLocations(false)
			if tt.wantErr == nil {
				e.stubCreate(t)
			}

			err := e.run(t, "-t", "T", "-b", "B", "-B", "main")
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.NotContains(t, e.git.Calls, push)
				return
			}
			require.NoError(t, err)
			e.reg.Verify(t)
			assert.Contains(t, e.git.Calls, push)
			if tt.tty {
				assert.Contains(t, e.out.String(), "Push branch feature/x to origin?")
			}
		})
	}
}

func TestCreateTTYPrompts(t *testing.T) {
	e := newCreateEnv(t, true)
	e.in.WriteString("Prompted title\nPrompted body\n" + createAnnID + "\n")
	e.stubLocations(false)
	e.stubCreate(t)

	require.NoError(t, e.run(t, "-B", "main"))
	e.reg.Verify(t)
	b := e.bodies[0]
	assert.Equal(t, "Prompted title", b["title"])
	assert.Equal(t, "Prompted body", b["description"])
	assert.Equal(t, []any{map[string]any{"id": createAnnID}}, b["reviewers"])
	assert.Contains(t, e.out.String(), "https://dev.azure.com/"+e.org+"/My%20Proj/_git/web/pullrequest/7")
}

func TestCreateTTYEmptyTitle(t *testing.T) {
	e := newCreateEnv(t, true)
	e.in.WriteString("\n\n\n")
	require.ErrorContains(t, e.run(t, "-B", "main"), "title is required")
}

func TestCreateNonTTYMissingTitle(t *testing.T) {
	e := newCreateEnv(t, false)
	err := e.run(t, "-b", "body")
	var flagErr *cmdutil.FlagError
	require.ErrorAs(t, err, &flagErr)
	assert.Contains(t, err.Error(), "--title")
	assert.Empty(t, e.git.Calls, "fails before touching git")
}

func TestCreateWeb(t *testing.T) {
	e := newCreateEnv(t, true)
	require.NoError(t, e.run(t, "--web", "-B", "main"))
	assert.Equal(t, []string{"https://dev.azure.com/" + e.org + "/My%20Proj/_git/web/pullrequestcreate?sourceRef=feature%2Fx&targetRef=main"}, e.browser.urls)
}

func TestCreateFlagErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"-t", "T", "-b", "x", "-F", "f"}, "none of the others can be"},
		{[]string{"-t", "T", "--web", "--auto"}, "none of the others can be"},
		{[]string{"-t", "T", "--work-item", "abc"}, "invalid argument"},
	}
	for _, tt := range tests {
		e := newCreateEnv(t, false)
		require.ErrorContains(t, e.run(t, tt.args...), tt.want, tt.args)
	}
}

func TestCreateDetachedHead(t *testing.T) {
	e := newCreateEnv(t, false)
	delete(e.git.Replies, "symbolic-ref --quiet HEAD")
	require.ErrorContains(t, e.run(t, "-t", "T", "-B", "main"), "--head")
}

func TestCreateAutoUpdateFails(t *testing.T) {
	e := newCreateEnv(t, false)
	e.stubLocations(false)
	e.stubCreate(t)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"),
		httpmock.JSONResponse(map[string]any{"authenticatedUser": map[string]any{"id": createMeID}}))
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/7"),
		httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "nope"}))

	err := e.run(t, "-t", "T", "-B", "main", "--auto")
	require.ErrorContains(t, err, "created https://dev.azure.com/"+e.org+"/My%20Proj/_git/web/pullrequest/7 but could not set auto-complete")
	assert.Contains(t, err.Error(), "nope")
}

func TestCreateNoDefaultBranch(t *testing.T) {
	e := newCreateEnv(t, false)
	e.stubLocations(false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web"),
		httpmock.JSONResponse(map[string]any{"name": "web"}))
	require.ErrorContains(t, e.run(t, "-t", "T"), "--base")
}

func TestCreateUnknownRemoteFallsBackToOrigin(t *testing.T) {
	e := newCreateEnv(t, false)
	e.git.Replies["remote -v"] = "fork\thttps://dev.azure.com/other/P/_git/r (fetch)\n"
	e.stubLocations(false)
	e.stubCreate(t)
	require.NoError(t, e.run(t, "-t", "T", "-B", "main"))
	assert.Contains(t, e.git.Calls, "rev-parse --verify --quiet refs/remotes/origin/feature/x")
}

func TestCreateBodyFileMissing(t *testing.T) {
	e := newCreateEnv(t, false)
	require.ErrorContains(t, e.run(t, "-t", "T", "-F", filepath.Join(t.TempDir(), "nope")), "reading the description")
}
