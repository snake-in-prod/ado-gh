package diff

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	apiOldSHA   = strings.Repeat("a", 40)
	apiNewSHA   = strings.Repeat("b", 40)
	apiAddSHA   = strings.Repeat("c", 40)
	apiDelSHA   = strings.Repeat("d", 40)
	apiBinSHA   = strings.Repeat("e", 40)
	apiEmptySHA = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"
	apiBaseSHA  = strings.Repeat("1", 40)
	apiForkID   = uuid.MustParse("8b6f0a8e-0000-0000-0000-00000000f0f0")
)

// apiChange builds an iteration change entry; empty IDs and originalPath are left out.
func apiChange(changeType, path, objectID, originalObjectID, originalPath string) map[string]any {
	item := map[string]any{"path": path}
	if objectID != "" {
		item["objectId"] = objectID
	}
	if originalObjectID != "" {
		item["originalObjectId"] = originalObjectID
	}
	ch := map[string]any{"changeType": changeType, "item": item}
	if originalPath != "" {
		ch["originalPath"] = originalPath
	}
	return ch
}

// apiBlobStub is a blob served by the test: its metadata (size, len(content) unless set) and,
// unless sizeOnly, its content; from the fork when fork is set.
type apiBlobStub struct {
	sha, content string
	size         int
	sizeOnly     bool
	fork         bool
}

// apiBlobMatcher matches GET .../blobs/sha asking for JSON metadata or, with content, the bytes.
// Both share a URL; only Accept tells them apart.
func apiBlobMatcher(repoPath, sha string, content bool) httpmock.Matcher {
	accept := "application/json"
	if content {
		accept = "application/octet-stream"
	}
	return func(req *http.Request) bool {
		return req.Method == http.MethodGet && req.URL.EscapedPath() == "/"+repoPath+"/blobs/"+sha &&
			strings.HasPrefix(req.Header.Get("Accept"), accept)
	}
}

type apiDiffEnv struct {
	org  string
	reg  *httpmock.Registry
	c    *api.PRClient
	f    *cmdutil.Factory
	ios  *iostreams.IOStreams
	out  *bytes.Buffer
	pr   *git.GitPullRequest
	repo string
}

func newAPIDiffEnv(t *testing.T, org string, fork bool) *apiDiffEnv {
	t.Helper()
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.GitPRLocations...)
	t.Cleanup(func() { reg.Verify(t) })
	ios, _, out, _ := iostreams.Test()
	id := 7
	pr := &git.GitPullRequest{PullRequestId: &id}
	if fork {
		pr.ForkSource = &git.GitForkRef{Repository: &git.GitRepository{Id: &apiForkID}}
	}
	return &apiDiffEnv{
		org: org, reg: reg, ios: ios, out: out, pr: pr,
		c:    api.NewPRClient(api.NewHTTPClient(api.ClientOptions{Transport: reg}), "https://dev.azure.com/"+org, "proj", "web"),
		f:    &cmdutil.Factory{IOStreams: ios},
		repo: org + "/proj/_apis/git/repositories/web",
	}
}

func (e *apiDiffEnv) stubIteration(pages ...[]map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.repo+"/pullRequests/7/iterations"), httpmock.JSONResponse(map[string]any{"count": 2, "value": []map[string]any{
		{"id": 2, "commonRefCommit": map[string]any{"commitId": apiBaseSHA}},
		{"id": 1, "commonRefCommit": map[string]any{"commitId": strings.Repeat("9", 40)}},
	}}))
	for i, p := range pages {
		body := map[string]any{"changeEntries": p}
		if i < len(pages)-1 {
			body["nextSkip"] = 2000 * (i + 1)
			body["nextTop"] = 2000
		}
		e.reg.Register(httpmock.REST(http.MethodGet, e.repo+"/pullRequests/7/iterations/2/changes"), httpmock.JSONResponse(body))
	}
}

func (e *apiDiffEnv) stubBlob(b apiBlobStub) {
	repoPath := e.repo
	if b.fork {
		repoPath = e.org + "/_apis/git/repositories/" + apiForkID.String()
	}
	size := b.size
	if size == 0 {
		size = len(b.content)
	}
	e.reg.Register(apiBlobMatcher(repoPath, b.sha, false), httpmock.JSONResponse(map[string]any{"objectId": b.sha, "size": size}))
	if !b.sizeOnly && size > 0 {
		e.reg.Register(apiBlobMatcher(repoPath, b.sha, true), httpmock.StringResponse(b.content))
	}
}

func (e *apiDiffEnv) stubItem(path, commit, content string) {
	e.reg.Register(func(req *http.Request) bool {
		q := req.URL.Query()
		return httpmock.REST(http.MethodGet, e.repo+"/items")(req) && q.Get("path") == path && q.Get("versionDescriptor.version") == commit
	}, httpmock.StringResponse(content))
}

func TestAPIDiff(t *testing.T) {
	big := strings.Repeat("x", 10)
	tests := []struct {
		name      string
		fork      bool
		pages     [][]map[string]any
		blobs     []apiBlobStub
		items     map[string]string
		nameOnly  bool
		patch     bool
		colorMode string
		want      string
		wantErr   string
	}{
		{
			name:  "edit",
			pages: [][]map[string]any{{apiChange("edit", "/src/x.go", apiNewSHA, apiOldSHA, "")}},
			blobs: []apiBlobStub{{sha: apiOldSHA, content: "a\nb\nc\n"}, {sha: apiNewSHA, content: "a\nB\nc\n"}},
			want: "diff --git a/src/x.go b/src/x.go\n" +
				"index aaaaaaa..bbbbbbb 100644\n" +
				"--- a/src/x.go\n" +
				"+++ b/src/x.go\n" +
				"@@ -1,3 +1,3 @@\n" +
				" a\n-b\n+B\n c\n",
		},
		{
			name:  "add",
			pages: [][]map[string]any{{apiChange("add", "/new.txt", apiAddSHA, "", "")}},
			blobs: []apiBlobStub{{sha: apiAddSHA, content: "hello\n"}},
			want: "diff --git a/new.txt b/new.txt\n" +
				"new file mode 100644\n" +
				"index 0000000..ccccccc\n" +
				"--- /dev/null\n" +
				"+++ b/new.txt\n" +
				"@@ -0,0 +1 @@\n" +
				"+hello\n",
		},
		{
			name:  "delete",
			pages: [][]map[string]any{{apiChange("delete", "/gone.txt", "", apiDelSHA, "")}},
			blobs: []apiBlobStub{{sha: apiDelSHA, content: "bye\n"}},
			want: "diff --git a/gone.txt b/gone.txt\n" +
				"deleted file mode 100644\n" +
				"index ddddddd..0000000\n" +
				"--- a/gone.txt\n" +
				"+++ /dev/null\n" +
				"@@ -1 +0,0 @@\n" +
				"-bye\n",
		},
		{
			name:  "rename only prints no index and downloads nothing",
			pages: [][]map[string]any{{apiChange("rename", "/new.go", apiOldSHA, apiOldSHA, "/old.go")}},
			want: "diff --git a/old.go b/new.go\n" +
				"rename from old.go\n" +
				"rename to new.go\n",
		},
		{
			name:  "rename and edit as comma-separated flags",
			pages: [][]map[string]any{{apiChange("rename, edit", "/new.go", apiNewSHA, apiOldSHA, "/old.go")}},
			blobs: []apiBlobStub{{sha: apiOldSHA, content: "a\nb\n"}, {sha: apiNewSHA, content: "a\nc\n"}},
			want: "diff --git a/old.go b/new.go\n" +
				"rename from old.go\n" +
				"rename to new.go\n" +
				"index aaaaaaa..bbbbbbb 100644\n" +
				"--- a/old.go\n" +
				"+++ b/new.go\n" +
				"@@ -1,2 +1,2 @@\n" +
				" a\n-b\n+c\n",
		},
		{
			name:  "binary add",
			pages: [][]map[string]any{{apiChange("add", "/img.png", apiBinSHA, "", "")}},
			blobs: []apiBlobStub{{sha: apiBinSHA, content: "\x89PNG\x00\x01"}},
			want: "diff --git a/img.png b/img.png\n" +
				"new file mode 100644\n" +
				"index 0000000..eeeeeee\n" +
				"Binary files /dev/null and b/img.png differ\n",
		},
		{
			name:  "binary edit",
			pages: [][]map[string]any{{apiChange("edit", "/img.png", apiNewSHA, apiOldSHA, "")}},
			blobs: []apiBlobStub{{sha: apiOldSHA, content: "text"}, {sha: apiNewSHA, content: "bin\x00"}},
			want: "diff --git a/img.png b/img.png\n" +
				"index aaaaaaa..bbbbbbb 100644\n" +
				"Binary files a/img.png and b/img.png differ\n",
		},
		{
			name:  "empty new file has headers only",
			pages: [][]map[string]any{{apiChange("add", "/empty", apiEmptySHA, "", "")}},
			blobs: []apiBlobStub{{sha: apiEmptySHA}},
			want: "diff --git a/empty b/empty\n" +
				"new file mode 100644\n" +
				"index 0000000..e69de29\n",
		},
		{
			name:  "no trailing newline",
			pages: [][]map[string]any{{apiChange("edit", "/a.txt", apiNewSHA, apiOldSHA, "")}},
			blobs: []apiBlobStub{{sha: apiOldSHA, content: "a\n"}, {sha: apiNewSHA, content: "a\nb"}},
			want: "diff --git a/a.txt b/a.txt\n" +
				"index aaaaaaa..bbbbbbb 100644\n" +
				"--- a/a.txt\n" +
				"+++ b/a.txt\n" +
				"@@ -1 +1,2 @@\n" +
				" a\n+b\n" +
				"\\ No newline at end of file\n",
		},
		{
			name:  "missing originalObjectId reads the old file at the common commit",
			pages: [][]map[string]any{{apiChange("edit", "/src/x.go", apiNewSHA, "", "")}},
			blobs: []apiBlobStub{{sha: apiNewSHA, content: "a\nB\nc\n"}},
			items: map[string]string{"/src/x.go": "a\nb\nc\n"},
			want: "diff --git a/src/x.go b/src/x.go\n" +
				"index de98044..bbbbbbb 100644\n" +
				"--- a/src/x.go\n" +
				"+++ b/src/x.go\n" +
				"@@ -1,3 +1,3 @@\n" +
				" a\n-b\n+B\n c\n",
		},
		{
			name:  "missing originalObjectId on a rename reads the original path",
			pages: [][]map[string]any{{apiChange("rename, edit", "/new.go", apiNewSHA, "", "/old.go")}},
			blobs: []apiBlobStub{{sha: apiNewSHA, content: "a\nB\nc\n"}},
			items: map[string]string{"/old.go": "a\nb\nc\n"},
			want: "diff --git a/old.go b/new.go\n" +
				"rename from old.go\n" +
				"rename to new.go\n" +
				"index de98044..bbbbbbb 100644\n" +
				"--- a/old.go\n" +
				"+++ b/new.go\n" +
				"@@ -1,3 +1,3 @@\n" +
				" a\n-b\n+B\n c\n",
		},
		{
			name:  "fork reads new-side blobs from the fork repository",
			fork:  true,
			pages: [][]map[string]any{{apiChange("edit", "/a.txt", apiNewSHA, apiOldSHA, "")}},
			blobs: []apiBlobStub{{sha: apiOldSHA, content: "x\n"}, {sha: apiNewSHA, content: "y\n", fork: true}},
			want: "diff --git a/a.txt b/a.txt\n" +
				"index aaaaaaa..bbbbbbb 100644\n" +
				"--- a/a.txt\n" +
				"+++ b/a.txt\n" +
				"@@ -1 +1 @@\n" +
				"-x\n+y\n",
		},
		{
			name:  "blob over 5 MB is not downloaded",
			pages: [][]map[string]any{{apiChange("edit", "/huge.bin", apiNewSHA, apiOldSHA, "")}},
			blobs: []apiBlobStub{{sha: apiOldSHA, content: big}, {sha: apiNewSHA, size: maxBlobSize + 1, sizeOnly: true}},
			want: "diff --git a/huge.bin b/huge.bin\n" +
				"index aaaaaaa..bbbbbbb 100644\n" +
				"file too large to diff (over 5 MB)\n",
		},
		{
			name: "multi-page changes keep the change list order; folders are skipped",
			pages: [][]map[string]any{
				{apiChange("add", "/z.txt", apiAddSHA, "", ""), {"changeType": "add", "item": map[string]any{"path": "/dir", "isFolder": true}}},
				{apiChange("delete", "/a.txt", "", apiDelSHA, "")},
			},
			blobs: []apiBlobStub{{sha: apiAddSHA, content: "z\n"}, {sha: apiDelSHA, content: "a\n"}},
			want: "diff --git a/z.txt b/z.txt\nnew file mode 100644\nindex 0000000..ccccccc\n--- /dev/null\n+++ b/z.txt\n@@ -0,0 +1 @@\n+z\n" +
				"diff --git a/a.txt b/a.txt\ndeleted file mode 100644\nindex ddddddd..0000000\n--- a/a.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-a\n",
		},
		{
			name: "--name-only lists paths from the change list without downloads and wins over --patch",
			pages: [][]map[string]any{{
				apiChange("edit", "/src/x.go", apiNewSHA, apiOldSHA, ""),
				apiChange("rename", "/new.go", apiOldSHA, apiOldSHA, "/old.go"),
				apiChange("delete", "/gone.txt", "", apiDelSHA, ""),
			}},
			nameOnly:  true,
			patch:     true,
			colorMode: "always",
			want:      "src/x.go\nnew.go\ngone.txt\n",
		},
		{
			name:      "--color always colours",
			pages:     [][]map[string]any{{apiChange("edit", "/a.txt", apiNewSHA, apiOldSHA, "")}},
			blobs:     []apiBlobStub{{sha: apiOldSHA, content: "x\n"}, {sha: apiNewSHA, content: "y\n"}},
			colorMode: "always",
			want: "\x1b[1;37mdiff --git a/a.txt b/a.txt\x1b[m\n" +
				"\x1b[1;37mindex aaaaaaa..bbbbbbb 100644\x1b[m\n" +
				"\x1b[1;37m--- a/a.txt\x1b[m\n" +
				"\x1b[1;37m+++ b/a.txt\x1b[m\n" +
				"@@ -1 +1 @@\n" +
				"\x1b[31m-x\x1b[m\n\x1b[32m+y\x1b[m\n",
		},
		{
			name:      "--color auto without a TTY does not colour",
			pages:     [][]map[string]any{{apiChange("edit", "/a.txt", apiNewSHA, apiOldSHA, "")}},
			blobs:     []apiBlobStub{{sha: apiOldSHA, content: "x\n"}, {sha: apiNewSHA, content: "y\n"}},
			colorMode: "auto",
			want:      "diff --git a/a.txt b/a.txt\nindex aaaaaaa..bbbbbbb 100644\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-x\n+y\n",
		},
		{
			name:  "no changes prints nothing",
			pages: [][]map[string]any{{}},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAPIDiffEnv(t, fmt.Sprintf("apidifforg%d", i), tt.fork)
			if tt.pages != nil {
				e.stubIteration(tt.pages...)
			}
			for _, b := range tt.blobs {
				e.stubBlob(b)
			}
			for p, content := range tt.items {
				e.stubItem(p, apiBaseSHA, content)
			}
			color, err := useColor(e.ios, cmp.Or(tt.colorMode, "never"))
			require.NoError(t, err)

			err = apiDiff(context.Background(), e.f, e.c, e.pr, tt.nameOnly, tt.patch, color)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, e.out.String())
		})
	}
}

func TestAPIDiffErrors(t *testing.T) {
	t.Run("--patch needs a clone", func(t *testing.T) {
		ios, _, _, _ := iostreams.Test()
		err := apiDiff(context.Background(), &cmdutil.Factory{IOStreams: ios}, nil, &git.GitPullRequest{}, false, true, false)
		assert.EqualError(t, err, "--patch needs a local clone of the repository; run it inside a clone of the pull request's repository")
	})
	t.Run("no iterations", func(t *testing.T) {
		e := newAPIDiffEnv(t, "apidifferr-noiter", false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.repo+"/pullRequests/7/iterations"), httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))
		err := apiDiff(context.Background(), e.f, e.c, e.pr, false, false, false)
		assert.EqualError(t, err, "pull request !7 has no iterations")
	})
	t.Run("iterations error", func(t *testing.T) {
		e := newAPIDiffEnv(t, "apidifferr-iter", false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.repo+"/pullRequests/7/iterations"),
			httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
		err := apiDiff(context.Background(), e.f, e.c, e.pr, false, false, false)
		assert.EqualError(t, err, "boom (HTTP 500)")
	})
	t.Run("changes error", func(t *testing.T) {
		e := newAPIDiffEnv(t, "apidifferr-changes", false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.repo+"/pullRequests/7/iterations"), httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{{"id": 2}}}))
		e.reg.Register(httpmock.REST(http.MethodGet, e.repo+"/pullRequests/7/iterations/2/changes"),
			httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
		err := apiDiff(context.Background(), e.f, e.c, e.pr, false, false, false)
		assert.EqualError(t, err, "boom (HTTP 500)")
	})
	for _, kind := range []string{"size", "content", "item"} {
		t.Run(kind+" error names the file", func(t *testing.T) {
			e := newAPIDiffEnv(t, "apidifferr-"+kind, false)
			boom := httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"})
			switch kind {
			case "size":
				e.stubIteration([]map[string]any{apiChange("add", "/a.txt", apiAddSHA, "", "")})
				e.reg.Register(apiBlobMatcher(e.repo, apiAddSHA, false), boom)
			case "content":
				e.stubIteration([]map[string]any{apiChange("add", "/a.txt", apiAddSHA, "", "")})
				e.stubBlob(apiBlobStub{sha: apiAddSHA, size: 3, sizeOnly: true})
				e.reg.Register(apiBlobMatcher(e.repo, apiAddSHA, true), boom)
			case "item":
				// The old side is read first; its failure stops the file before the new side.
				e.stubIteration([]map[string]any{apiChange("edit", "/a.txt", apiNewSHA, "", "")})
				e.reg.Register(httpmock.REST(http.MethodGet, e.repo+"/items"), boom)
			}
			err := apiDiff(context.Background(), e.f, e.c, e.pr, false, false, false)
			assert.EqualError(t, err, "cannot read a.txt: boom (HTTP 500)")
		})
	}
}

func TestAPIDiffWorkers(t *testing.T) {
	const files = 9
	e := newAPIDiffEnv(t, "apidiffworkers", false)
	var page []map[string]any
	var want strings.Builder
	for i := range files {
		sha := fmt.Sprintf("%040d", i)
		page = append(page, apiChange("add", fmt.Sprintf("/f%d", i), sha, "", ""))
		fmt.Fprintf(&want, "diff --git a/f%d b/f%d\nnew file mode 100644\nindex 0000000..%s\n--- /dev/null\n+++ b/f%d\n@@ -0,0 +1 @@\n+%d\n", i, i, sha[:7], i, i)
	}
	e.stubIteration(page)

	// Size requests block until 4 of them are in flight at once (or a timeout), so a
	// smaller pool fails the peak check; later files answer first to scramble completion order.
	var mu sync.Mutex
	var open sync.Once
	inFlight, peak := 0, 0
	gate := make(chan struct{})
	for i := range files {
		sha := fmt.Sprintf("%040d", i)
		e.reg.Register(apiBlobMatcher(e.repo, sha, false), func(req *http.Request) (*http.Response, error) {
			mu.Lock()
			inFlight++
			peak = max(peak, inFlight)
			if inFlight == 4 {
				open.Do(func() { close(gate) })
			}
			mu.Unlock()
			select {
			case <-gate:
			case <-time.After(2 * time.Second):
			}
			time.Sleep(time.Duration(files-i) * time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
			return httpmock.JSONResponse(map[string]any{"objectId": sha, "size": 2})(req)
		})
		e.reg.Register(apiBlobMatcher(e.repo, sha, true), httpmock.StringResponse(fmt.Sprintf("%d\n", i)))
	}

	require.NoError(t, apiDiff(context.Background(), e.f, e.c, e.pr, false, false, false))
	assert.Equal(t, 4, peak, "downloads run on exactly 4 workers")
	assert.Equal(t, want.String(), e.out.String(), "output follows the change list order")
}

func TestIsBinary(t *testing.T) {
	assert.False(t, isBinary([]byte("plain text\n")))
	assert.True(t, isBinary([]byte("a\x00b")))
	nulAt := func(i int) []byte { b := []byte(strings.Repeat("x", i+1)); b[i] = 0; return b }
	assert.True(t, isBinary(nulAt(7999)), "NUL within the first 8000 bytes")
	assert.False(t, isBinary(nulAt(8000)), "NUL after the first 8000 bytes is text, like git")
}
