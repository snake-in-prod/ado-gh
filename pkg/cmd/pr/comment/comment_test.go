package comment

import (
	"bytes"
	"encoding/json"
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

const commentRepoPath = "/My%20Proj/_apis/git/repositories/web/pullRequests/12"

type commentBrowser struct{ urls []string }

func (b *commentBrowser) Browse(u string) error {
	b.urls = append(b.urls, u)
	return nil
}

type commentEnv struct {
	org             string
	reg             *httpmock.Registry
	ios             *iostreams.IOStreams
	in, out, errOut *bytes.Buffer
	browser         *commentBrowser
	// calls records the HTTP writes in order with their JSON bodies.
	calls  []string
	bodies []map[string]any
}

// newCommentEnv returns an env for its own org: the SDK caches locations per base URL for the
// whole process.
func newCommentEnv(t *testing.T, tty bool) *commentEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "prcomment-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	return &commentEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut, browser: &commentBrowser{}}
}

func (e *commentEnv) run(t *testing.T, args ...string) error {
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
		Browser:  e.browser,
		Prompter: &cmdutil.Prompter{IO: e.ios, Accessible: true},
	}
	cmd := NewCmdComment(f)
	cmd.SetArgs(args)
	cmd.SetIn(e.in)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

func (e *commentEnv) prURL() string {
	return "https://dev.azure.com/" + e.org + "/My%20Proj/_git/web/pullrequest/12"
}

func (e *commentEnv) stubPR() {
	e.reg.StubLocations(e.org, httpmock.GitPRLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/12"), httpmock.JSONResponse(map[string]any{
		"pullRequestId": 12, "title": "Add feature", "status": "active", "repository": map[string]any{"name": "web"},
	}))
}

// stubWrite answers method on path (below the pull request) with resp, recording name and body.
func (e *commentEnv) stubWrite(t *testing.T, method, path, name string, resp httpmock.Responder) {
	e.reg.Register(httpmock.REST(method, e.org+commentRepoPath+path), func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &body))
		e.calls = append(e.calls, name)
		e.bodies = append(e.bodies, body)
		return resp(r)
	})
}

// stubThread answers GET of thread 7 with comments 3 (a reply) and 1 (the root).
func (e *commentEnv) stubThread() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+commentRepoPath+"/threads/7"), httpmock.JSONResponse(map[string]any{
		"id": 7, "status": "active", "comments": []map[string]any{{"id": 3, "parentCommentId": 1}, {"id": 1}},
	}))
}

func TestCommentNewThread(t *testing.T) {
	e := newCommentEnv(t, false)
	e.stubPR()
	e.stubWrite(t, http.MethodPost, "/threads", "thread", httpmock.JSONResponse(map[string]any{"id": 9}))

	require.NoError(t, e.run(t, "12", "-b", "Looks good"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"thread"}, e.calls)
	assert.Equal(t, map[string]any{
		"comments": []any{map[string]any{"content": "Looks good", "commentType": "text"}},
		"status":   "active",
	}, e.bodies[0])
	assert.Equal(t, e.prURL()+"?discussionId=9\n", e.out.String())
	assert.Empty(t, e.errOut.String())
}

func TestCommentBodyFromStdin(t *testing.T) {
	e := newCommentEnv(t, false)
	e.stubPR()
	e.stubWrite(t, http.MethodPost, "/threads", "thread", httpmock.JSONResponse(map[string]any{"id": 9}))
	e.in.WriteString("from stdin")

	require.NoError(t, e.run(t, "12", "-F", "-"))
	assert.Equal(t, "from stdin", e.bodies[0]["comments"].([]any)[0].(map[string]any)["content"])
}

func TestCommentReply(t *testing.T) {
	e := newCommentEnv(t, false)
	e.stubPR()
	e.stubThread()
	e.stubWrite(t, http.MethodPost, "/threads/7/comments", "reply", httpmock.JSONResponse(map[string]any{"id": 4}))

	require.NoError(t, e.run(t, "12", "--thread", "7", "-b", "Done"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"reply"}, e.calls)
	assert.Equal(t, map[string]any{"content": "Done", "commentType": "text", "parentCommentId": float64(1)}, e.bodies[0])
	assert.Equal(t, e.prURL()+"?discussionId=7\n", e.out.String())
}

func TestCommentReplyThreadNotFound(t *testing.T) {
	e := newCommentEnv(t, false)
	e.stubPR()
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+commentRepoPath+"/threads/7"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "not found"}))

	err := e.run(t, "12", "--thread", "7", "-b", "Done")
	require.EqualError(t, err, "thread 7 not found in pull request !12")
	assert.Empty(t, e.calls)
}

func TestCommentStatusOnly(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-tty", true: "tty"}[tty], func(t *testing.T) {
			e := newCommentEnv(t, tty)
			e.stubPR()
			e.stubWrite(t, http.MethodPatch, "/threads/7", "status", httpmock.JSONResponse(map[string]any{"id": 7, "status": "fixed"}))

			require.NoError(t, e.run(t, "12", "--thread", "7", "--status", "fixed"))
			e.reg.Verify(t)
			assert.Equal(t, []string{"status"}, e.calls, "no body and no editor")
			assert.Equal(t, map[string]any{"status": "fixed"}, e.bodies[0])
			assert.Equal(t, e.prURL()+"?discussionId=7\n", e.out.String())
		})
	}
}

func TestCommentStatusThreadNotFound(t *testing.T) {
	e := newCommentEnv(t, false)
	e.stubPR()
	e.stubWrite(t, http.MethodPatch, "/threads/7", "status",
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "not found"}))

	require.EqualError(t, e.run(t, "12", "--thread", "7", "--status", "closed"), "thread 7 not found in pull request !12")
	assert.Empty(t, e.out.String())
}

func TestCommentReplyAndStatus(t *testing.T) {
	e := newCommentEnv(t, false)
	e.stubPR()
	e.stubThread()
	e.stubWrite(t, http.MethodPost, "/threads/7/comments", "reply", httpmock.JSONResponse(map[string]any{"id": 4}))
	e.stubWrite(t, http.MethodPatch, "/threads/7", "status", httpmock.JSONResponse(map[string]any{"id": 7, "status": "wontFix"}))

	require.NoError(t, e.run(t, "12", "--thread", "7", "--status", "WONTFIX", "-b", "Not now"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"reply", "status"}, e.calls)
	assert.Equal(t, map[string]any{"status": "wontFix"}, e.bodies[1])
	assert.Equal(t, e.prURL()+"?discussionId=7\n", e.out.String())
}

func TestCommentReplyAndStatusPartialFailure(t *testing.T) {
	e := newCommentEnv(t, false)
	e.stubPR()
	e.stubThread()
	e.stubWrite(t, http.MethodPost, "/threads/7/comments", "reply", httpmock.JSONResponse(map[string]any{"id": 4}))
	e.stubWrite(t, http.MethodPatch, "/threads/7", "status",
		httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))

	err := e.run(t, "12", "--thread", "7", "--status", "fixed", "-b", "Done")
	require.EqualError(t, err, "replied to thread 7 ("+e.prURL()+"?discussionId=7), but setting its status to fixed failed: denied (HTTP 403)")
	assert.Equal(t, []string{"reply", "status"}, e.calls)
}

func TestParseStatus(t *testing.T) {
	for in, want := range map[string]string{
		"active": "active", "FIXED": "fixed", "wontfix": "wontFix", "Closed": "closed", "bydesign": "byDesign", "pending": "pending",
	} {
		got, err := parseStatus(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, string(got))
	}
}

func TestCommentFlagErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"status without thread", []string{"12", "--status", "fixed"}, "--status requires --thread"},
		{"invalid status", []string{"12", "--thread", "7", "--status", "done"},
			`invalid value for --status: "done" (valid values: active, fixed, wontFix, closed, byDesign, pending)`},
		{"unknown is not settable", []string{"12", "--thread", "7", "--status", "unknown"}, `invalid value for --status: "unknown"`},
		{"invalid thread", []string{"12", "--thread", "0", "-b", "x"}, "invalid thread ID 0"},
		{"web with body", []string{"12", "-w", "-b", "x"}, "if any flags in the group [web body body-file editor] are set none of the others can be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newCommentEnv(t, false)
			err := e.run(t, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, e.reg.Requests, "no request before flag validation")
		})
	}
}

func TestCommentFlagErrorsAreUsageErrors(t *testing.T) {
	e := newCommentEnv(t, false)
	var fe *cmdutil.FlagError
	require.ErrorAs(t, e.run(t, "12", "--status", "fixed"), &fe)
}

func TestCommentNonInteractiveNeedsBody(t *testing.T) {
	e := newCommentEnv(t, false)
	e.stubPR()

	err := e.run(t, "12")
	var fe *cmdutil.FlagError
	require.ErrorAs(t, err, &fe)
	assert.Contains(t, err.Error(), "--body")
	assert.Empty(t, e.calls)
}

func TestCommentWeb(t *testing.T) {
	e := newCommentEnv(t, true)
	e.stubPR()

	require.NoError(t, e.run(t, "12", "-w"))
	e.reg.Verify(t)
	assert.Equal(t, []string{e.prURL()}, e.browser.urls)
	assert.Equal(t, "Opening "+e.prURL()+" in your browser.\n", e.errOut.String())
	assert.Empty(t, e.calls)
}
