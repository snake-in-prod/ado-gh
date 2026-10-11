package comment

import (
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/editortest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	lastMeID    = "8c8c7d32-6b1e-4f9c-a4a5-3a2a2d6b1e01"
	lastOtherID = "1f1f1f1f-0000-4000-8000-000000000002"
)

func lastAuthor(id string) map[string]any { return map[string]any{"id": id, "displayName": id[:4]} }

// lastThreads has my latest visible comment as reply 2 in another user's file thread 2.
// Newer comments of mine are deleted, system comments or in a system thread.
var lastThreads = []map[string]any{
	{"id": 1, "comments": []map[string]any{
		{"id": 1, "author": lastAuthor(lastMeID), "content": "old mine", "publishedDate": "2026-10-01T10:00:00Z", "commentType": "text"},
		{"id": 2, "author": lastAuthor(lastMeID), "isDeleted": true, "publishedDate": "2026-10-08T10:00:00Z"},
	}},
	{"id": 3, "properties": map[string]any{"CodeReviewThreadType": map[string]any{"$type": "System.String", "$value": "VoteUpdate"}},
		"comments": []map[string]any{
			{"id": 1, "author": lastAuthor(lastMeID), "content": "voted", "publishedDate": "2026-10-09T10:00:00Z", "commentType": "text"},
		}},
	{"id": 2, "threadContext": map[string]any{"filePath": "/a.go", "rightFileStart": map[string]any{"line": 4, "offset": 1}},
		"comments": []map[string]any{
			{"id": 1, "author": lastAuthor(lastOtherID), "content": "nit", "publishedDate": "2026-10-04T10:00:00Z", "commentType": "text"},
			{"id": 2, "parentCommentId": 1, "author": lastAuthor(strings.ToUpper(lastMeID)), "content": "my latest reply", "publishedDate": "2026-10-05T10:00:00Z", "commentType": "text"},
			{"id": 3, "author": lastAuthor(lastMeID), "content": "pushed", "publishedDate": "2026-10-10T10:00:00Z", "commentType": "system"},
			{"id": 4, "author": lastAuthor(lastOtherID), "content": "newer, not mine", "publishedDate": "2026-10-07T10:00:00Z", "commentType": "text"},
		}},
}

// stubLast stubs the pull request, the signed-in user and its threads.
func (e *commentEnv) stubLast(threads []map[string]any) {
	e.stubPR()
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"),
		httpmock.JSONResponse(map[string]any{"authenticatedUser": map[string]any{"id": lastMeID}}))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+commentRepoPath+"/threads"),
		httpmock.JSONResponse(map[string]any{"count": len(threads), "value": threads}))
}

// stubDeleteLast answers DELETE of comment 2 in thread 2, recording "delete".
func (e *commentEnv) stubDeleteLast() {
	e.reg.Register(httpmock.REST(http.MethodDelete, e.org+commentRepoPath+"/threads/2/comments/2"), func(r *http.Request) (*http.Response, error) {
		e.calls = append(e.calls, "delete")
		return httpmock.StringResponse("")(r)
	})
}

// TestCommentLastEditorHelperProcess is not a test: it is the fake editor of editortest.Command.
func TestCommentLastEditorHelperProcess(*testing.T) { editortest.HelperProcess() }

// lastEditorCmd sets ADO_EDITOR to a fake editor writing text and returns the file where it
// records the text it was opened with.
func lastEditorCmd(t *testing.T, text string) (seen string) {
	cmd, seen := editortest.Command(t, "TestCommentLastEditorHelperProcess", text)
	t.Setenv("ADO_EDITOR", cmd)
	return seen
}

// afterEditorReader answers its first Read, os/exec copying stdin to the editor (which Run
// waits for), with EOF, so the editor doesn't swallow the answers meant for the prompts that
// follow it.
type afterEditorReader struct {
	started *atomic.Bool
	r       io.Reader
}

func (a afterEditorReader) Read(p []byte) (int, error) {
	if a.started.CompareAndSwap(false, true) {
		return 0, io.EOF
	}
	return a.r.Read(p)
}

func TestCommentEditLastWithBody(t *testing.T) {
	e := newCommentEnv(t, false)
	e.stubLast(lastThreads)
	e.stubWrite(t, http.MethodPatch, "/threads/2/comments/2", "edit", httpmock.JSONResponse(map[string]any{"id": 2}))

	require.NoError(t, e.run(t, "12", "--edit-last", "-b", "edited"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"edit"}, e.calls)
	assert.Equal(t, map[string]any{"content": "edited"}, e.bodies[0])
	assert.Equal(t, e.prURL()+"?discussionId=2\n", e.out.String())
}

func TestCommentEditLastWithBodyFile(t *testing.T) {
	e := newCommentEnv(t, false)
	e.in.WriteString("from file")
	e.stubLast(lastThreads)
	e.stubWrite(t, http.MethodPatch, "/threads/2/comments/2", "edit", httpmock.JSONResponse(map[string]any{"id": 2}))

	require.NoError(t, e.run(t, "12", "--edit-last", "-F", "-"))
	require.Equal(t, []string{"edit"}, e.calls)
	assert.Equal(t, map[string]any{"content": "from file"}, e.bodies[0])
}

func TestCommentEditLastNonInteractiveNeedsBody(t *testing.T) {
	e := newCommentEnv(t, false)
	e.stubLast(lastThreads)

	err := e.run(t, "12", "--edit-last")
	var fe *cmdutil.FlagError
	require.ErrorAs(t, err, &fe)
	assert.Contains(t, err.Error(), "--body")
	assert.Empty(t, e.calls)
}

func TestCommentEditLastEditor(t *testing.T) {
	t.Run("submitted", func(t *testing.T) {
		e := newCommentEnv(t, true)
		seen := lastEditorCmd(t, "from editor")
		e.ios.In = afterEditorReader{started: &atomic.Bool{}, r: e.in}
		e.in.WriteString("y\n")
		e.stubLast(lastThreads)
		e.stubWrite(t, http.MethodPatch, "/threads/2/comments/2", "edit", httpmock.JSONResponse(map[string]any{"id": 2}))

		require.NoError(t, e.run(t, "12", "--edit-last"))
		e.reg.Verify(t)
		b, err := os.ReadFile(seen)
		require.NoError(t, err)
		assert.Equal(t, "my latest reply", string(b), "editor prefilled with the current text")
		assert.Equal(t, map[string]any{"content": "from editor"}, e.bodies[0])
		assert.Contains(t, e.out.String(), "Submit?")
		assert.True(t, strings.HasSuffix(e.out.String(), e.prURL()+"?discussionId=2\n"), e.out.String())
	})
	t.Run("declined", func(t *testing.T) {
		e := newCommentEnv(t, true)
		lastEditorCmd(t, "from editor")
		e.ios.In = afterEditorReader{started: &atomic.Bool{}, r: e.in}
		e.in.WriteString("n\n")
		e.stubLast(lastThreads)

		err := e.run(t, "12", "--edit-last")
		require.ErrorIs(t, err, cmdutil.CancelError)
		assert.Equal(t, "Discarding...\n", e.errOut.String())
		assert.Empty(t, e.calls)
	})
	t.Run("-e skips the confirmation", func(t *testing.T) {
		e := newCommentEnv(t, true)
		lastEditorCmd(t, "from editor")
		e.stubLast(lastThreads)
		e.stubWrite(t, http.MethodPatch, "/threads/2/comments/2", "edit", httpmock.JSONResponse(map[string]any{"id": 2}))

		require.NoError(t, e.run(t, "12", "--edit-last", "-e"))
		assert.Equal(t, []string{"edit"}, e.calls)
		assert.NotContains(t, e.out.String(), "Submit?")
	})
}

func TestCommentEditLastNoComments(t *testing.T) {
	others := []map[string]any{{"id": 5, "comments": []map[string]any{
		{"id": 1, "author": lastAuthor(lastOtherID), "content": "theirs", "publishedDate": "2026-10-04T10:00:00Z"},
	}}}
	t.Run("non-interactive fails", func(t *testing.T) {
		e := newCommentEnv(t, false)
		e.stubLast(others)

		require.EqualError(t, e.run(t, "12", "--edit-last", "-b", "x"), "no comments found for current user")
		assert.Empty(t, e.calls)
	})
	t.Run("--create-if-none creates a thread", func(t *testing.T) {
		e := newCommentEnv(t, false)
		e.stubLast(others)
		e.stubWrite(t, http.MethodPost, "/threads", "thread", httpmock.JSONResponse(map[string]any{"id": 9}))

		require.NoError(t, e.run(t, "12", "--edit-last", "--create-if-none", "-b", "new"))
		assert.Equal(t, []string{"thread"}, e.calls)
		assert.Equal(t, "new", e.bodies[0]["comments"].([]any)[0].(map[string]any)["content"])
		assert.Equal(t, e.prURL()+"?discussionId=9\n", e.out.String())
	})
	t.Run("TTY asks to create", func(t *testing.T) {
		e := newCommentEnv(t, true)
		e.stubLast(others)
		e.stubWrite(t, http.MethodPost, "/threads", "thread", httpmock.JSONResponse(map[string]any{"id": 9}))
		e.in.WriteString("y\n")

		require.NoError(t, e.run(t, "12", "--edit-last", "-b", "new"))
		assert.Contains(t, e.out.String(), "No comments found. Create one?")
		assert.Equal(t, []string{"thread"}, e.calls)
	})
	t.Run("TTY declined", func(t *testing.T) {
		e := newCommentEnv(t, true)
		e.stubLast(others)
		e.in.WriteString("n\n")

		require.EqualError(t, e.run(t, "12", "--edit-last", "-b", "new"), "no comments found for current user")
		assert.Empty(t, e.calls)
	})
}

func TestCommentDeleteLast(t *testing.T) {
	t.Run("--yes", func(t *testing.T) {
		e := newCommentEnv(t, false)
		e.stubLast(lastThreads)
		e.stubDeleteLast()

		require.NoError(t, e.run(t, "12", "--delete-last", "--yes"))
		e.reg.Verify(t)
		assert.Equal(t, []string{"delete"}, e.calls)
		assert.Empty(t, e.out.String())
		assert.Equal(t, "Comment deleted\n", e.errOut.String())
	})
	t.Run("TTY confirmed", func(t *testing.T) {
		e := newCommentEnv(t, true)
		e.stubLast(lastThreads)
		e.stubDeleteLast()
		e.in.WriteString("y\n")

		require.NoError(t, e.run(t, "12", "--delete-last"))
		assert.Equal(t, []string{"delete"}, e.calls)
		assert.Contains(t, e.out.String(), `Delete the comment: "my latest reply"?`)
		assert.Equal(t, "! Deleted comments cannot be recovered.\nComment deleted\n", e.errOut.String())
	})
	t.Run("TTY declined", func(t *testing.T) {
		e := newCommentEnv(t, true)
		e.stubLast(lastThreads)
		e.in.WriteString("n\n")

		require.ErrorIs(t, e.run(t, "12", "--delete-last"), cmdutil.CancelError)
		assert.Empty(t, e.calls)
	})
	t.Run("no comments", func(t *testing.T) {
		e := newCommentEnv(t, false)
		e.stubLast(nil)

		require.EqualError(t, e.run(t, "12", "--delete-last", "-y"), "no comments found for current user")
		assert.Empty(t, e.calls)
	})
}

func TestCommentLastPreview(t *testing.T) {
	assert.Equal(t, "short", preview("short"))
	assert.Equal(t, strings.Repeat("é", 40)+"...", preview(strings.Repeat("é", 41)))
}

func TestCommentLastFlagErrors(t *testing.T) {
	tests := []struct {
		name    string
		tty     bool
		args    []string
		wantErr string
	}{
		{"create-if-none without edit-last", false, []string{"12", "--create-if-none", "-b", "x"}, "--create-if-none can only be used with --edit-last"},
		{"yes without delete-last", false, []string{"12", "--yes", "-b", "x"}, "--yes should only be used with --delete-last"},
		{"yes with edit-last", false, []string{"12", "--edit-last", "-y", "-b", "x"}, "--yes should only be used with --delete-last"},
		{"delete-last with body", false, []string{"12", "--delete-last", "-y", "-b", "x"}, "should not provide comment body when using --delete-last"},
		{"delete-last with editor", true, []string{"12", "--delete-last", "-e"}, "should not provide comment body when using --delete-last"},
		{"delete-last non-interactive", false, []string{"12", "--delete-last"}, "should provide --yes to confirm deletion in non-interactive mode"},
		{"edit-last with delete-last", false, []string{"12", "--edit-last", "--delete-last"}, "none of the others can be"},
		{"edit-last with thread", false, []string{"12", "--edit-last", "--thread", "7", "-b", "x"}, "none of the others can be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newCommentEnv(t, tt.tty)
			err := e.run(t, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, e.reg.Requests, "no request before flag validation")
		})
	}
}
