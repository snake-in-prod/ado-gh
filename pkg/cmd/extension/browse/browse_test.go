package browse

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"testing/iotest"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/internal/browsertest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	extpkg "github.com/snake-in-prod/ado-gh/pkg/extension"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// browseEnv runs browse under a fake `extension` group whose install and remove commands
// record their arguments and return installErr / nil.
type browseEnv struct {
	f           *cmdutil.Factory
	reg         *httpmock.Registry
	browser     *browsertest.Browser
	out, errOut *bytes.Buffer
	installed   [][]string
	removed     [][]string
	installErr  error
}

func newBrowseEnv(t *testing.T, tty bool, input string) *browseEnv {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	ios, in, out, errOut := iostreams.Test()
	in.WriteString(input)
	ios.In = iotest.OneByteReader(in)
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	ios.SetStderrTTY(tty)
	reg := &httpmock.Registry{}
	t.Cleanup(func() { reg.Verify(t) })
	b := &browsertest.Browser{}
	return &browseEnv{reg: reg, browser: b, out: out, errOut: errOut, f: &cmdutil.Factory{
		IOStreams:  ios,
		Prompter:   &cmdutil.Prompter{IO: ios, Accessible: true},
		Browser:    b,
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: reg}, nil },
	}}
}

func (e *browseEnv) run(args ...string) error {
	group := &cobra.Command{Use: "extension", SilenceErrors: true, SilenceUsage: true}
	group.AddCommand(&cobra.Command{Use: "install <source>", RunE: func(_ *cobra.Command, args []string) error {
		e.installed = append(e.installed, args)
		return e.installErr
	}}, &cobra.Command{Use: "remove <name>", RunE: func(_ *cobra.Command, args []string) error {
		e.removed = append(e.removed, args)
		return nil
	}}, NewCmdBrowse(e.f))
	group.SetArgs(append([]string{"browse"}, args...))
	group.SetOut(e.out)
	group.SetErr(e.errOut)
	return group.Execute()
}

func (e *browseEnv) search(items ...map[string]any) {
	e.reg.Register(httpmock.REST("GET", "search/repositories"), httpmock.JSONResponse(map[string]any{"items": items}))
}

func TestBrowseActions(t *testing.T) {
	// hello: open README, install; again (README cached), open in browser; lint: no README,
	// remove; again, back; quit.
	e := newBrowseEnv(t, true, "1\n1\n1\n2\n2\n1\n2\n3\n3\n")
	require.NoError(t, os.MkdirAll(filepath.Join(extpkg.Dir(), "ado-lint"), 0o755))
	e.search(
		map[string]any{"full_name": "o/ado-hello", "description": "Says hi", "stargazers_count": 7},
		map[string]any{"full_name": "o/gh-hello", "description": "a gh extension"},
		map[string]any{"full_name": "o/ado-lint", "description": "Lints", "stargazers_count": 2},
	)
	e.reg.Register(httpmock.REST("GET", "repos/o/ado-hello/readme"), httpmock.StringResponse("# Hello world\n\nRun \x1b]0;pwned\x07it.\n"))
	e.reg.Register(httpmock.REST("GET", "repos/o/ado-lint/readme"), httpmock.StatusStringResponse(http.StatusNotFound, `{"message":"Not Found"}`))

	require.NoError(t, e.run())

	assert.Equal(t, [][]string{{"o/ado-hello"}}, e.installed)
	assert.Equal(t, [][]string{{"lint"}}, e.removed)
	assert.Equal(t, []string{"https://github.com/o/ado-hello"}, e.browser.URLs)
	assert.Len(t, e.reg.Requests, 3, "one search, one README each: READMEs are cached")
	q := e.reg.Requests[0].URL.Query()
	assert.Equal(t, "topic:ado-extension", q.Get("q"))
	assert.Equal(t, "100", q.Get("per_page"), "default --limit 100")
	assert.Equal(t, "application/vnd.github.raw+json", e.reg.Requests[1].Header.Get("Accept"))

	out := e.out.String()
	assert.Contains(t, out, "o/ado-hello")
	assert.Contains(t, out, "Says hi")
	assert.Contains(t, out, "✓ o/ado-lint", "installed mark")
	assert.NotContains(t, out, "gh-hello", "only ado- repositories")
	assert.Contains(t, out, "Hello world")
	assert.NotContains(t, out, "pwned", "README escape sequences stripped")
	assert.Contains(t, out, "o/ado-lint has no README")
	assert.Contains(t, out, "Install")
	assert.Contains(t, out, "Remove")
	assert.Contains(t, e.errOut.String(), "Opening https://github.com/o/ado-hello in your browser.")
}

func TestBrowseInstallFails(t *testing.T) {
	e := newBrowseEnv(t, true, "1\n1\n2\n")
	e.installErr = errors.New("declined")
	e.search(map[string]any{"full_name": "o/ado-hello"})
	e.reg.Register(httpmock.REST("GET", "repos/o/ado-hello/readme"), httpmock.StringResponse("hi"))
	require.NoError(t, e.run(), "a failed action returns to the list")
	assert.Contains(t, e.errOut.String(), "X declined")

	e = newBrowseEnv(t, true, "1\n1\n")
	e.installErr = cmdutil.CancelError
	e.search(map[string]any{"full_name": "o/ado-hello"})
	e.reg.Register(httpmock.REST("GET", "repos/o/ado-hello/readme"), httpmock.StringResponse("hi"))
	assert.ErrorIs(t, e.run(), cmdutil.CancelError)
}

func TestBrowseLimit(t *testing.T) {
	e := newBrowseEnv(t, true, "2\n")
	e.search(map[string]any{"full_name": "o/ado-hello"})
	require.NoError(t, e.run("-L", "20"))
	assert.Equal(t, "20", e.reg.Requests[0].URL.Query().Get("per_page"))

	for _, l := range []string{"0", "1001"} {
		e = newBrowseEnv(t, true, "")
		assert.EqualError(t, e.run("--limit", l), "--limit must be between 1 and 1000")
	}
}

func TestBrowseErrors(t *testing.T) {
	e := newBrowseEnv(t, false, "")
	assert.EqualError(t, e.run(), "browse requires a terminal; use 'ado extension search'")
	assert.Empty(t, e.reg.Requests)

	e = newBrowseEnv(t, true, "")
	e.search()
	assert.EqualError(t, e.run(), "no extensions found on GitHub (topic ado-extension)")

	e = newBrowseEnv(t, true, "1\n")
	e.search(map[string]any{"full_name": "o/ado-hello"})
	e.reg.Register(httpmock.REST("GET", "repos/o/ado-hello/readme"), httpmock.StatusStringResponse(http.StatusBadGateway, ""))
	assert.EqualError(t, e.run(), "README of o/ado-hello: HTTP 502")

	e = newBrowseEnv(t, true, "")
	e.reg.Register(httpmock.MatchAny, httpmock.WithHeader(httpmock.StatusStringResponse(http.StatusTooManyRequests, ""), "X-RateLimit-Remaining", "0"))
	assert.ErrorContains(t, e.run(), "set GH_TOKEN")
}
