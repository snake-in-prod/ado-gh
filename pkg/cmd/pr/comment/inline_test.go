package comment

import (
	"net/http"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubInlineChanges answers the iterations list (latest = 3, listed out of order) and two pages
// of iteration 3's changes: src/a.go (tracking 5) on the first, the rename old.go → new.go
// (tracking 8), the added add.go (9) and the deleted gone.go (10) on the second.
func (e *commentEnv) stubInlineChanges() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+commentRepoPath+"/iterations"), httpmock.JSONResponse(map[string]any{
		"count": 3, "value": []map[string]any{{"id": 1}, {"id": 3}, {"id": 2}},
	}))
	changes := e.org + commentRepoPath + "/iterations/3/changes"
	e.reg.Register(httpmock.REST(http.MethodGet, changes), httpmock.JSONResponse(map[string]any{
		"changeEntries": []map[string]any{
			{"changeTrackingId": 4, "item": map[string]any{"path": "/README.md"}, "changeType": "edit"},
			{"changeTrackingId": 5, "item": map[string]any{"path": "/src/a.go"}, "changeType": "edit"},
		},
		"nextSkip": 2, "nextTop": 2000,
	}))
	e.reg.Register(httpmock.REST(http.MethodGet, changes), httpmock.JSONResponse(map[string]any{
		"changeEntries": []map[string]any{
			{"changeTrackingId": 8, "item": map[string]any{"path": "/new.go"}, "originalPath": "/old.go", "changeType": "rename"},
			{"changeTrackingId": 9, "item": map[string]any{"path": "/add.go"}, "changeType": "add"},
			{"changeTrackingId": 10, "item": map[string]any{"path": "/gone.go"}, "changeType": "delete, sourceRename"},
		},
	}))
}

func inlineIterationContext() map[string]any {
	return map[string]any{"changeTrackingId": float64(5), "iterationContext": map[string]any{
		"firstComparingIteration": float64(3), "secondComparingIteration": float64(3),
	}}
}

func pos(line int) map[string]any {
	return map[string]any{"line": float64(line), "offset": float64(1)}
}

func TestCommentInline(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantCtx map[string]any
	}{
		{"single line on the right side by default", []string{"--file", "src/a.go", "--line", "10"},
			map[string]any{"filePath": "/src/a.go", "rightFileStart": pos(10), "rightFileEnd": pos(10)}},
		{"range", []string{"--file", "src/a.go", "--line", "10:12"},
			map[string]any{"filePath": "/src/a.go", "rightFileStart": pos(10), "rightFileEnd": pos(12)}},
		{"left side", []string{"--file", "src/a.go", "--line", "3:4", "--side", "left"},
			map[string]any{"filePath": "/src/a.go", "leftFileStart": pos(3), "leftFileEnd": pos(4)}},
		{"explicit right side", []string{"--file", "src/a.go", "--line", "7", "--side", "right"},
			map[string]any{"filePath": "/src/a.go", "rightFileStart": pos(7), "rightFileEnd": pos(7)}},
		{"dot slash prefix", []string{"--file", "./src/a.go", "--line", "1"},
			map[string]any{"filePath": "/src/a.go", "rightFileStart": pos(1), "rightFileEnd": pos(1)}},
		{"backslashes", []string{"--file", `src\a.go`, "--line", "1"},
			map[string]any{"filePath": "/src/a.go", "rightFileStart": pos(1), "rightFileEnd": pos(1)}},
		{"leading slash", []string{"--file", "/src/a.go", "--line", "1"},
			map[string]any{"filePath": "/src/a.go", "rightFileStart": pos(1), "rightFileEnd": pos(1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newCommentEnv(t, false)
			e.stubPR()
			e.stubInlineChanges()
			e.stubWrite(t, http.MethodPost, "/threads", "thread", httpmock.JSONResponse(map[string]any{"id": 9}))

			require.NoError(t, e.run(t, append([]string{"12", "-b", "nit"}, tt.args...)...))
			e.reg.Verify(t)
			assert.Equal(t, map[string]any{
				"comments":                 []any{map[string]any{"content": "nit", "commentType": "text"}},
				"status":                   "active",
				"threadContext":            tt.wantCtx,
				"pullRequestThreadContext": inlineIterationContext(),
			}, e.bodies[0])
			assert.Equal(t, e.prURL()+"?discussionId=9\n", e.out.String())
		})
	}
}

func TestCommentInlineRenamedFile(t *testing.T) {
	for _, file := range []string{"new.go", "old.go"} {
		t.Run(file, func(t *testing.T) {
			e := newCommentEnv(t, false)
			e.stubPR()
			e.stubInlineChanges()
			e.stubWrite(t, http.MethodPost, "/threads", "thread", httpmock.JSONResponse(map[string]any{"id": 9}))

			require.NoError(t, e.run(t, "12", "-b", "nit", "--file", file, "--line", "2"))
			require.Len(t, e.bodies, 1)
			assert.Equal(t, map[string]any{"filePath": "/new.go", "rightFileStart": pos(2), "rightFileEnd": pos(2)}, e.bodies[0]["threadContext"])
			ptc, ok := e.bodies[0]["pullRequestThreadContext"].(map[string]any)
			require.True(t, ok, "%v", e.bodies[0])
			assert.Equal(t, float64(8), ptc["changeTrackingId"])
		})
	}
}

func TestCommentInlineAddedOrDeletedFile(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		tracking float64
		wantCtx  map[string]any
	}{
		{"added file has only a right side", []string{"--file", "add.go", "--line", "2", "--side", "left"}, 9,
			map[string]any{"filePath": "/add.go", "rightFileStart": pos(2), "rightFileEnd": pos(2)}},
		{"deleted file has only a left side", []string{"--file", "gone.go", "--line", "3"}, 10,
			map[string]any{"filePath": "/gone.go", "leftFileStart": pos(3), "leftFileEnd": pos(3)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newCommentEnv(t, false)
			e.stubPR()
			e.stubInlineChanges()
			e.stubWrite(t, http.MethodPost, "/threads", "thread", httpmock.JSONResponse(map[string]any{"id": 9}))

			require.NoError(t, e.run(t, append([]string{"12", "-b", "nit"}, tt.args...)...))
			require.Len(t, e.bodies, 1)
			assert.Equal(t, tt.wantCtx, e.bodies[0]["threadContext"])
			ptc, ok := e.bodies[0]["pullRequestThreadContext"].(map[string]any)
			require.True(t, ok, "%v", e.bodies[0])
			assert.Equal(t, tt.tracking, ptc["changeTrackingId"])
		})
	}
}

func TestCommentInlineFileNotInPR(t *testing.T) {
	e := newCommentEnv(t, false)
	e.stubPR()
	e.stubInlineChanges()

	require.EqualError(t, e.run(t, "12", "-b", "nit", "--file", "b.go", "--line", "1"), "b.go is not changed in pull request !12; list the changed files with: ado pr diff 12 --name-only")
	assert.Empty(t, e.writes())
}

func TestCommentInlineNoIterations(t *testing.T) {
	e := newCommentEnv(t, false)
	e.stubPR()
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+commentRepoPath+"/iterations"), httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))

	require.EqualError(t, e.run(t, "12", "-b", "nit", "--file", "a.go", "--line", "1"), "pull request !12 has no iterations")
}

func TestCommentInlineFlagErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"line without file", []string{"--line", "3"}, "--line requires --file"},
		{"file without line", []string{"--file", "a.go"}, "--file requires --line"},
		{"side without file", []string{"--side", "left"}, "--side requires --file"},
		{"bad side", []string{"--file", "a.go", "--line", "1", "--side", "up"}, `invalid value for --side: "up" (valid values: right, left)`},
		{"not a number", []string{"--file", "a.go", "--line", "x"}, `invalid value for --line: "x" (want <n> or <n>:<m>, 1 <= n <= m)`},
		{"zero", []string{"--file", "a.go", "--line", "0"}, `invalid value for --line: "0"`},
		{"reversed range", []string{"--file", "a.go", "--line", "5:3"}, `invalid value for --line: "5:3"`},
		{"open range", []string{"--file", "a.go", "--line", "5:"}, `invalid value for --line: "5:"`},
		{"with thread", []string{"--file", "a.go", "--line", "1", "--thread", "7"}, "none of the others can be"},
		{"empty file", []string{"--file", "", "--line", "1"}, "--file cannot be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newCommentEnv(t, false)
			err := e.run(t, append([]string{"12", "-b", "x"}, tt.args...)...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, e.reg.Requests, "no request before flag validation")
		})
	}
}
