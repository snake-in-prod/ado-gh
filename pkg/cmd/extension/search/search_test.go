package search

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/internal/browsertest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	extpkg "github.com/snake-in-prod/ado-gh/pkg/extension"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

type searchEnv struct {
	reg         *httpmock.Registry
	browser     *browsertest.Browser
	out, errOut *bytes.Buffer
	f           *cmdutil.Factory
	ios         *iostreams.IOStreams
}

func newSearchEnv(t *testing.T, tty bool) *searchEnv {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	ios.SetStderrTTY(tty)
	e := &searchEnv{reg: &httpmock.Registry{}, browser: &browsertest.Browser{}, out: out, errOut: errOut, ios: ios}
	e.f = &cmdutil.Factory{
		IOStreams:  ios,
		Browser:    e.browser,
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		AppVersion: "1.0.0",
	}
	t.Cleanup(func() { e.reg.Verify(t) })
	return e
}

func (e *searchEnv) run(args ...string) error {
	cmd := NewCmdSearch(e.f)
	cmd.SetArgs(args)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	return cmd.Execute()
}

// searchPath matches a GET of api.github.com/search/repositories.
func searchPath(req *http.Request) bool {
	return req.Method == http.MethodGet && req.URL.Host == "api.github.com" && req.URL.Path == "/search/repositories"
}

func searchItems(n, from int) []map[string]any {
	items := []map[string]any{}
	for i := from; i < from+n; i++ {
		items = append(items, map[string]any{
			"full_name": fmt.Sprintf("o/ado-x%d", i), "description": nil, "stargazers_count": i,
			"updated_at": "2026-10-01T10:00:00Z", "html_url": fmt.Sprintf("https://github.com/o/ado-x%d", i)})
	}
	return items
}

func helloResults() map[string]any {
	return map[string]any{"total_count": 2, "incomplete_results": false, "items": []map[string]any{
		{"full_name": "snake-in-prod/ado-hello", "description": "Says \x1b[31mhello\nworld", "stargazers_count": 5,
			"updated_at": "2026-10-01T10:00:00Z", "html_url": "https://github.com/snake-in-prod/ado-hello"},
		{"full_name": "o/ado-lint", "description": nil, "stargazers_count": 1,
			"updated_at": "2026-09-01T10:00:00Z", "html_url": "https://github.com/o/ado-lint"},
	}}
}

// installHello installs ado-hello as a binary extension recorded from source.
func installHello(t *testing.T, source string) {
	t.Helper()
	root := extpkg.Dir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ado-hello"), 0o755))
	require.NoError(t, extpkg.WriteRecord(root, "hello", &extpkg.Record{Source: source, Kind: extpkg.KindBinary}))
}

func TestSearchQueryComposition(t *testing.T) {
	e := newSearchEnv(t, false)
	e.reg.Register(searchPath, httpmock.JSONResponse(helloResults()))
	require.NoError(t, e.run("hello", "world", "--owner", "snake-in-prod", "--sort", "stars"))
	q := e.reg.Requests[0].URL.Query()
	assert.Equal(t, url.Values{"q": {"hello world topic:ado-extension user:snake-in-prod"}, "sort": {"stars"},
		"order": {"desc"}, "per_page": {"30"}, "page": {"1"}}, q)
	assert.Empty(t, e.reg.Requests[0].Header.Get("Authorization"))
	assert.Equal(t, "ado-cli/1.0.0", e.reg.Requests[0].Header.Get("User-Agent"))
}

func TestSearchDefaultsAndToken(t *testing.T) {
	e := newSearchEnv(t, false)
	t.Setenv("GH_TOKEN", "ghtoken")
	e.reg.Register(searchPath, httpmock.JSONResponse(helloResults()))
	require.NoError(t, e.run())
	q := e.reg.Requests[0].URL.Query()
	assert.Equal(t, "topic:ado-extension", q.Get("q"))
	assert.False(t, q.Has("sort"))
	assert.False(t, q.Has("order"))
	assert.Equal(t, "Bearer ghtoken", e.reg.Requests[0].Header.Get("Authorization"))
}

func TestSearchFlagValidation(t *testing.T) {
	tests := map[string]struct {
		args    []string
		wantErr string
	}{
		"bad sort":     {[]string{"--sort", "forks"}, `invalid --sort "forks": use stars or updated`},
		"limit zero":   {[]string{"-L", "0"}, "--limit must be between 1 and 1000"},
		"limit 1001":   {[]string{"--limit", "1001"}, "--limit must be between 1 and 1000"},
		"owner inject": {[]string{"--owner", "a user:b"}, `invalid owner "a user:b"`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			e := newSearchEnv(t, false)
			err := e.run(tt.args...)
			assert.EqualError(t, err, tt.wantErr)
			var flagErr *cmdutil.FlagError
			assert.ErrorAs(t, err, &flagErr)
			assert.Empty(t, e.reg.Requests)
		})
	}
}

func TestSearchLimitPaging(t *testing.T) {
	e := newSearchEnv(t, false)
	e.reg.Register(func(r *http.Request) bool { return searchPath(r) && r.URL.Query().Get("page") == "1" },
		httpmock.JSONResponse(map[string]any{"total_count": 1500, "items": searchItems(100, 0)}))
	e.reg.Register(func(r *http.Request) bool { return searchPath(r) && r.URL.Query().Get("page") == "2" },
		httpmock.JSONResponse(map[string]any{"total_count": 1500, "items": searchItems(100, 100)}))
	require.NoError(t, e.run("-L", "150"))
	require.Len(t, e.reg.Requests, 2)
	assert.Equal(t, "100", e.reg.Requests[1].URL.Query().Get("per_page"))
	lines := strings.Split(strings.TrimSpace(e.out.String()), "\n")
	assert.Len(t, lines, 150)
	assert.Equal(t, "false\to/ado-x149\t\t149\t2026-10-01T10:00:00Z", lines[149])
}

func TestSearchStopsAtShortPage(t *testing.T) {
	e := newSearchEnv(t, false)
	e.reg.Register(searchPath, httpmock.JSONResponse(map[string]any{"total_count": 5000, "items": searchItems(40, 0)}))
	require.NoError(t, e.run("-L", "1000"))
	assert.Len(t, e.reg.Requests, 1)
	assert.Len(t, strings.Split(strings.TrimSpace(e.out.String()), "\n"), 40)
}

func TestSearchTSV(t *testing.T) {
	e := newSearchEnv(t, false)
	installHello(t, "https://github.com/Snake-In-Prod/ado-hello")
	e.reg.Register(searchPath, httpmock.JSONResponse(helloResults()))
	require.NoError(t, e.run())
	assert.Equal(t, "true\tsnake-in-prod/ado-hello\tSays hello world\t5\t2026-10-01T10:00:00Z\n"+
		"false\to/ado-lint\t\t1\t2026-09-01T10:00:00Z\n", e.out.String())
}

func TestSearchTTY(t *testing.T) {
	e := newSearchEnv(t, true)
	installHello(t, "https://github.com/snake-in-prod/ado-hello")
	e.reg.Register(searchPath, httpmock.JSONResponse(helloResults()))
	require.NoError(t, e.run())
	out := e.out.String()
	assert.Contains(t, out, "REPO")
	assert.Contains(t, out, "DESCRIPTION")
	assert.Regexp(t, `(?m)^✓\s+snake-in-prod/ado-hello\s+Says hello world\s+5\s+\S`, out)
	assert.Regexp(t, `(?m)^\s+o/ado-lint\s+1\s+`, out)
	assert.NotContains(t, out, "\x1b")
}

func TestSearchJSON(t *testing.T) {
	e := newSearchEnv(t, false)
	installHello(t, "https://github.com/snake-in-prod/ado-hello")
	e.reg.Register(searchPath, httpmock.JSONResponse(helloResults()))
	require.NoError(t, e.run("--json", "fullName,stars,installed,url,updatedAt,description"))
	assert.JSONEq(t, `[
		{"fullName":"snake-in-prod/ado-hello","description":"Says \u001b[31mhello\nworld","stars":5,"installed":true,
		 "url":"https://github.com/snake-in-prod/ado-hello","updatedAt":"2026-10-01T10:00:00Z"},
		{"fullName":"o/ado-lint","description":"","stars":1,"installed":false,
		 "url":"https://github.com/o/ado-lint","updatedAt":"2026-09-01T10:00:00Z"}]`, e.out.String())
}

func TestSearchNotInstalledFromOtherSource(t *testing.T) {
	e := newSearchEnv(t, false)
	installHello(t, "https://dev.azure.com/org/proj/_git/ado-hello")
	e.reg.Register(searchPath, httpmock.JSONResponse(helloResults()))
	require.NoError(t, e.run("--json", "installed"))
	assert.JSONEq(t, `[{"installed":false},{"installed":false}]`, e.out.String())
}

func TestSearchNoResults(t *testing.T) {
	for _, tty := range []bool{true, false} {
		t.Run(fmt.Sprint("tty=", tty), func(t *testing.T) {
			e := newSearchEnv(t, tty)
			e.reg.Register(searchPath, httpmock.JSONResponse(map[string]any{"total_count": 0, "items": []any{}}))
			require.NoError(t, e.run("nothing"))
			assert.Empty(t, e.out.String())
			if tty {
				assert.Equal(t, "no extensions found\n", e.errOut.String())
			} else {
				assert.Empty(t, e.errOut.String())
			}
		})
	}
}

func TestSearchWeb(t *testing.T) {
	e := newSearchEnv(t, true)
	require.NoError(t, e.run("hello", "--owner", "o", "--sort", "updated", "-w"))
	require.Len(t, e.browser.URLs, 1)
	u, err := url.Parse(e.browser.URLs[0])
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/search", u.Scheme+"://"+u.Host+u.Path)
	assert.Equal(t, url.Values{"q": {"hello topic:ado-extension user:o"}, "type": {"repositories"},
		"s": {"updated"}, "o": {"desc"}}, u.Query())
	assert.Contains(t, e.errOut.String(), "Opening https://github.com/search?")
	assert.Empty(t, e.reg.Requests)
}

func TestSearchRateLimit(t *testing.T) {
	reset := time.Date(2026, 10, 12, 15, 4, 5, 0, time.UTC)
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			e := newSearchEnv(t, false)
			resp := httpmock.WithHeader(httpmock.StatusStringResponse(status, `{"message":"API rate limit exceeded"}`),
				"X-RateLimit-Remaining", "0")
			e.reg.Register(searchPath, httpmock.WithHeader(resp, "X-RateLimit-Reset", fmt.Sprint(reset.Unix())))
			err := e.run()
			assert.EqualError(t, err, "searching GitHub: GitHub API rate limit exceeded (it resets at "+
				reset.Local().Format("15:04:05 MST")+"); set GH_TOKEN to a GitHub token to raise it")
		})
	}
}

func TestSearchHTTPErrors(t *testing.T) {
	tests := map[string]struct {
		resp    httpmock.Responder
		wantErr string
	}{
		"forbidden, not rate limited": {httpmock.StatusStringResponse(http.StatusForbidden, ""), "searching GitHub: HTTP 403"},
		"validation failed":           {httpmock.StatusStringResponse(http.StatusUnprocessableEntity, ""), "searching GitHub: HTTP 422"},
		"bad json":                    {httpmock.StringResponse("{"), "searching GitHub: unexpected EOF"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			e := newSearchEnv(t, false)
			e.reg.Register(searchPath, tt.resp)
			assert.EqualError(t, e.run(), tt.wantErr)
		})
	}
}
