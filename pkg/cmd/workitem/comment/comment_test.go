package comment

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/editortest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const (
	wiCommentMeID    = "8c8c7d32-6b1e-4f9c-a4a5-3a2a2d6b1e01"
	wiCommentOtherID = "1f1f1f1f-0000-4000-8000-000000000002"
)

type wiCommentEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	in          *bytes.Buffer
	out, errOut *bytes.Buffer
	// calls records the writes in order; bodies their decoded JSON bodies.
	calls  []string
	bodies []map[string]any
}

// newWICommentEnv returns an env for its own org (the SDK caches discovery per base URL).
func newWICommentEnv(t *testing.T, tty bool) *wiCommentEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "wicomment-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	e := &wiCommentEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut}
	e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	return e
}

func (e *wiCommentEnv) run(t *testing.T, args ...string) error {
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
	cmd := NewCmdComment(f)
	cmd.SetArgs(args)
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

func (e *wiCommentEnv) commentsPath() string {
	return e.org + "/My%20Proj/_apis/wit/workitems/42/comments"
}

func (e *wiCommentEnv) itemURL() string {
	return "https://dev.azure.com/" + e.org + "/My%20Proj/_workitems/edit/42"
}

// stubWrite answers method on path below the comments of work item 42, asserting
// format=markdown (only AddWorkItemComment/UpdateWorkItemComment send it) and recording the call as name and its JSON body.
func (e *wiCommentEnv) stubWrite(t *testing.T, method, sub, name string, resp httpmock.Responder) {
	e.reg.Register(httpmock.REST(method, e.commentsPath()+sub), func(r *http.Request) (*http.Response, error) {
		e.calls = append(e.calls, name)
		if method != http.MethodDelete {
			assert.Equal(t, "markdown", r.URL.Query().Get("format"))
			var body map[string]any
			b, _ := io.ReadAll(r.Body)
			require.NoError(t, json.Unmarshal(b, &body))
			e.bodies = append(e.bodies, body)
		}
		return resp(r)
	})
}

// stubMe answers connectionData with the signed-in user.
func (e *wiCommentEnv) stubMe() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"),
		httpmock.JSONResponse(map[string]any{"authenticatedUser": map[string]any{"id": wiCommentMeID}}))
}

// stubPages answers the newest-first comment pages of work item 42 in order, each page but
// the last pointing at the next.
func (e *wiCommentEnv) stubPages(t *testing.T, pages ...[]map[string]any) {
	for i, p := range pages {
		token := ""
		if i < len(pages)-1 {
			token = "page" + string(rune('1'+i))
		}
		want := ""
		if i > 0 {
			want = "page" + string(rune('0'+i))
		}
		e.reg.Register(httpmock.REST(http.MethodGet, e.commentsPath()), func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "desc", r.URL.Query().Get("order"))
			assert.Equal(t, want, r.URL.Query().Get("continuationToken"))
			return httpmock.JSONResponse(map[string]any{"comments": p, "continuationToken": token})(r)
		})
	}
}

func wiComment(id int, by, text string, deleted bool) map[string]any {
	return map[string]any{"id": id, "text": text, "format": "markdown", "isDeleted": deleted, "createdBy": map[string]any{"id": by}}
}

// wiCommentPages has my latest comment 5 on the second page: page one holds another user's
// newer comment and my deleted one.
var wiCommentPages = [][]map[string]any{
	{wiComment(9, wiCommentOtherID, "theirs", false), wiComment(8, wiCommentMeID, "gone", true)},
	{wiComment(5, strings.ToUpper(wiCommentMeID), "my latest", false), wiComment(3, wiCommentMeID, "older", false)},
}

// TestWICommentEditorHelperProcess is not a test: it is the fake editor of editortest.Command.
func TestWICommentEditorHelperProcess(*testing.T) { editortest.HelperProcess() }

// wiCommentEditor sets ADO_EDITOR to a fake editor writing text and returns the file where it
// records the text it was opened with.
func wiCommentEditor(t *testing.T, text string) (seen string) {
	cmd, seen := editortest.Command(t, "TestWICommentEditorHelperProcess", text)
	t.Setenv("ADO_EDITOR", cmd)
	return seen
}

func TestWICommentAdd(t *testing.T) {
	for _, tc := range []struct {
		name, stdin string
		args        []string
	}{
		{"--body", "", []string{"42", "-b", "Hello **there**"}},
		{"--body-file stdin", "Hello **there**", []string{"42", "-F", "-"}},
		{"URL ref", "", []string{"https://dev.azure.com/ORGX/My%20Proj/_workitems/edit/42", "--body", "Hello **there**"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newWICommentEnv(t, false)
			e.in.WriteString(tc.stdin)
			e.stubWrite(t, http.MethodPost, "", "add", httpmock.JSONResponse(map[string]any{"id": 77}))
			args := tc.args
			if strings.HasPrefix(args[0], "https://") {
				args = append([]string{strings.Replace(args[0], "ORGX", e.org, 1)}, args[1:]...)
			}

			require.NoError(t, e.run(t, args...))
			e.reg.Verify(t)
			assert.Equal(t, []string{"add"}, e.calls)
			assert.Equal(t, map[string]any{"text": "Hello **there**"}, e.bodies[0])
			assert.Equal(t, e.itemURL()+"#77\n", e.out.String())
		})
	}
}

func TestWICommentAddEditor(t *testing.T) {
	for _, args := range [][]string{{"42"}, {"42", "-e"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			e := newWICommentEnv(t, true)
			seen := wiCommentEditor(t, "from editor")
			e.stubWrite(t, http.MethodPost, "", "add", httpmock.JSONResponse(map[string]any{"id": 77}))

			require.NoError(t, e.run(t, args...))
			b, err := os.ReadFile(seen)
			require.NoError(t, err)
			assert.Empty(t, string(b))
			assert.Equal(t, map[string]any{"text": "from editor"}, e.bodies[0])
			assert.Equal(t, e.itemURL()+"#77\n", e.out.String())
		})
	}
}

func TestWICommentNonInteractiveNeedsBody(t *testing.T) {
	e := newWICommentEnv(t, false)
	err := e.run(t, "42")
	var fe *cmdutil.FlagError
	require.ErrorAs(t, err, &fe)
	assert.Contains(t, err.Error(), "--body")
	assert.Empty(t, e.calls)
}

func TestWICommentFlagErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"42", "--edit-last", "--delete-last"}, "if any flags in the group [edit-last delete-last] are set none of the others can be"},
		{[]string{"42", "--yes", "-b", "x"}, "--yes should only be used with --delete-last"},
		{[]string{"42", "--delete-last", "-b", "x"}, "should not provide comment body when using --delete-last"},
		{[]string{"42", "--delete-last"}, "should provide --yes to confirm deletion in non-interactive mode"},
		{[]string{"x42", "-b", "x"}, `invalid work item reference "x42"`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			e := newWICommentEnv(t, false)
			err := e.run(t, tc.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestWICommentEditLast(t *testing.T) {
	t.Run("--body pages past others and deleted comments", func(t *testing.T) {
		e := newWICommentEnv(t, false)
		e.stubMe()
		e.stubPages(t, wiCommentPages...)
		e.stubWrite(t, http.MethodPatch, "/5", "edit", httpmock.JSONResponse(map[string]any{"id": 5}))

		require.NoError(t, e.run(t, "42", "--edit-last", "-b", "edited"))
		e.reg.Verify(t)
		assert.Equal(t, []string{"edit"}, e.calls)
		assert.Equal(t, map[string]any{"text": "edited"}, e.bodies[0])
		assert.Equal(t, e.itemURL()+"#5\n", e.out.String())
	})
	t.Run("editor prefilled with the current text", func(t *testing.T) {
		e := newWICommentEnv(t, true)
		seen := wiCommentEditor(t, "from editor")
		e.stubMe()
		e.stubPages(t, wiCommentPages...)
		e.stubWrite(t, http.MethodPatch, "/5", "edit", httpmock.JSONResponse(map[string]any{"id": 5}))

		require.NoError(t, e.run(t, "42", "--edit-last"))
		b, err := os.ReadFile(seen)
		require.NoError(t, err)
		assert.Equal(t, "my latest", string(b))
		assert.Equal(t, map[string]any{"text": "from editor"}, e.bodies[0])
	})
	t.Run("HTML comment prefilled as text", func(t *testing.T) {
		e := newWICommentEnv(t, true)
		seen := wiCommentEditor(t, "from editor")
		e.stubMe()
		html := wiComment(5, wiCommentMeID, "<div>line <b>one</b></div><div>two</div>", false)
		html["format"] = "html"
		e.stubPages(t, []map[string]any{html})
		e.stubWrite(t, http.MethodPatch, "/5", "edit", httpmock.JSONResponse(map[string]any{"id": 5}))

		require.NoError(t, e.run(t, "42", "--edit-last"))
		b, err := os.ReadFile(seen)
		require.NoError(t, err)
		assert.Equal(t, "line one\ntwo", string(b))
	})
	t.Run("no own comment found", func(t *testing.T) {
		e := newWICommentEnv(t, false)
		e.stubMe()
		e.stubPages(t, []map[string]any{wiComment(9, wiCommentOtherID, "theirs", false), wiComment(8, wiCommentMeID, "gone", true)})

		require.EqualError(t, e.run(t, "42", "--edit-last", "-b", "x"), "no comment of yours found on work item 42")
		assert.Empty(t, e.calls)
	})
}

func TestWICommentDeleteLast(t *testing.T) {
	t.Run("--yes", func(t *testing.T) {
		e := newWICommentEnv(t, false)
		e.stubMe()
		e.stubPages(t, wiCommentPages...)
		e.stubWrite(t, http.MethodDelete, "/5", "delete", httpmock.StringResponse(""))

		require.NoError(t, e.run(t, "42", "--delete-last", "--yes"))
		e.reg.Verify(t)
		assert.Equal(t, []string{"delete"}, e.calls)
		assert.Equal(t, "Comment deleted\n", e.errOut.String())
		assert.Empty(t, e.out.String())
	})
	t.Run("confirmed on TTY", func(t *testing.T) {
		e := newWICommentEnv(t, true)
		e.in.WriteString("y\n")
		e.stubMe()
		e.stubPages(t, wiCommentPages...)
		e.stubWrite(t, http.MethodDelete, "/5", "delete", httpmock.StringResponse(""))

		require.NoError(t, e.run(t, "42", "--delete-last"))
		assert.Contains(t, e.out.String(), `Delete the comment: "my latest"?`)
		assert.Equal(t, []string{"delete"}, e.calls)
	})
	t.Run("declined on TTY", func(t *testing.T) {
		e := newWICommentEnv(t, true)
		e.in.WriteString("n\n")
		e.stubMe()
		e.stubPages(t, wiCommentPages...)

		require.ErrorIs(t, e.run(t, "42", "--delete-last"), cmdutil.CancelError)
		assert.Empty(t, e.calls)
	})
	t.Run("no own comment found", func(t *testing.T) {
		e := newWICommentEnv(t, false)
		e.stubMe()
		e.stubPages(t, nil)

		require.EqualError(t, e.run(t, "42", "--delete-last", "--yes"), "no comment of yours found on work item 42")
	})
}

func TestWICommentAPIErrors(t *testing.T) {
	notFound := httpmock.StatusStringResponse(http.StatusNotFound, `{"message":"TF401232: Work item 42 does not exist"}`)
	t.Run("add", func(t *testing.T) {
		e := newWICommentEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodPost, e.commentsPath()), notFound)
		require.ErrorContains(t, e.run(t, "42", "-b", "x"), "TF401232")
		assert.NotContains(t, e.out.String(), e.itemURL())
	})
	t.Run("list", func(t *testing.T) {
		e := newWICommentEnv(t, false)
		e.stubMe()
		e.reg.Register(httpmock.REST(http.MethodGet, e.commentsPath()), notFound)
		require.ErrorContains(t, e.run(t, "42", "--edit-last", "-b", "x"), "TF401232")
	})
	t.Run("update", func(t *testing.T) {
		e := newWICommentEnv(t, false)
		e.stubMe()
		e.stubPages(t, wiCommentPages...)
		e.reg.Register(httpmock.REST(http.MethodPatch, e.commentsPath()+"/5"), notFound)
		require.ErrorContains(t, e.run(t, "42", "--edit-last", "-b", "x"), "TF401232")
		assert.NotContains(t, e.out.String(), e.itemURL())
	})
}

func TestWICommentPreview(t *testing.T) {
	assert.Equal(t, "short", preview("short"))
	assert.Equal(t, strings.Repeat("é", 40)+"...", preview(strings.Repeat("é", 41)))
	assert.Equal(t, "a b", preview("a\n\nb"))
}
