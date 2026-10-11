package diff

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

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

type cmdDiffBrowser struct{ urls []string }

func (b *cmdDiffBrowser) Browse(u string) error {
	b.urls = append(b.urls, u)
	return nil
}

type cmdDiffEnv struct {
	org         string
	reg         *httpmock.Registry
	git         *gittest.Runner
	ios         *iostreams.IOStreams
	out, errOut *bytes.Buffer
	browser     *cmdDiffBrowser
}

// newCmdDiffEnv returns an env for its own org (the SDK caches locations per base URL).
func newCmdDiffEnv(t *testing.T, tty bool) *cmdDiffEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	org := "prdiff-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	reg := &httpmock.Registry{}
	t.Cleanup(func() { reg.Verify(t) })
	return &cmdDiffEnv{org: org, reg: reg, git: &gittest.Runner{Replies: map[string]string{}, Fails: map[string]error{}},
		ios: ios, out: out, errOut: errOut, browser: &cmdDiffBrowser{}}
}

// stubPR stubs the SDK location lookup and pull request 7 of ORG/proj/web.
func (e *cmdDiffEnv) stubPR() {
	e.reg.StubLocations(e.org, httpmock.GitPRLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/git/pullRequests/7"), httpmock.JSONResponse(map[string]any{
		"pullRequestId": 7, "sourceRefName": "refs/heads/feat", "targetRefName": "refs/heads/main",
		"repository": map[string]any{"name": "web"},
	}))
}

// stubIterations stubs the pull request and its single iteration.
func (e *cmdDiffEnv) stubIterations() {
	e.stubPR()
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/git/repositories/web/pullRequests/7/iterations"),
		httpmock.JSONResponse(map[string]any{"count": 1, "value": []any{cloneIteration(2, cloneBaseSHA, cloneSrcSHA, cloneTgtSHA)}}))
}

func (e *cmdDiffEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "proj", Name: "web"}, nil
		},
		GitClient: &gitcmd.Client{Run: e.git.Run},
		Browser:   e.browser,
	}
	cmd := NewCmdDiff(f)
	cmd.SetArgs(args)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

func TestDiffWeb(t *testing.T) {
	for _, tty := range []bool{true, false} {
		t.Run(map[bool]string{true: "tty", false: "non-tty"}[tty], func(t *testing.T) {
			e := newCmdDiffEnv(t, tty)
			e.stubPR()
			require.NoError(t, e.run(t, "7", "--web"))
			u := "https://dev.azure.com/" + e.org + "/proj/_git/web/pullrequest/7?_a=files"
			assert.Equal(t, []string{u}, e.browser.urls)
			if tty {
				assert.Equal(t, "Opening "+u+" in your browser.\n", e.errOut.String())
			} else {
				assert.Empty(t, e.errOut.String())
			}
			assert.Empty(t, e.out.String())
			assert.Empty(t, e.git.Calls, "--web runs no git")
		})
	}
}

func TestDiffPathSelection(t *testing.T) {
	cloneNames := "-c diff.noprefix=false -c diff.mnemonicPrefix=false -c diff.relative=false diff --no-color --no-ext-diff --no-textconv --name-only " + cloneBaseSHA + " " + cloneSrcSHA
	tests := []struct {
		name      string
		remotes   string
		remoteErr error
		wantClone bool
	}{
		{name: "cwd remote matches the PR repo: clone path", remotes: "origin\thttps://dev.azure.com/ORG/proj/_git/web (fetch)\n", wantClone: true},
		{name: "cwd remote matches case-insensitively: clone path", remotes: "up\thttps://ORG@dev.azure.com/ORG/PROJ/_git/WEB (fetch)\n", wantClone: true},
		{name: "cwd is a clone of another repo: API path", remotes: "origin\thttps://dev.azure.com/ORG/proj/_git/other (fetch)\n"},
		{name: "cwd has no remotes: API path"},
		{name: "cwd is not a git repository: API path", remoteErr: &gitcmd.Error{ExitCode: 128, Stderr: "fatal: not a git repository"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newCmdDiffEnv(t, false)
			e.stubIterations()
			e.git.Replies["remote -v"] = strings.ReplaceAll(tt.remotes, "ORG", e.org)
			if tt.remoteErr != nil {
				e.git.Fails["remote -v"] = tt.remoteErr
			}
			if tt.wantClone {
				e.git.Replies[cloneNames] = "a.go\n"
			} else {
				e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/git/repositories/web/pullRequests/7/iterations/2/changes"),
					httpmock.JSONResponse(map[string]any{"changeEntries": []any{apiChange("edit", "/b.go", apiNewSHA, apiOldSHA, "")}}))
			}

			require.NoError(t, e.run(t, "7", "--name-only"))
			if tt.wantClone {
				assert.Equal(t, "a.go\n", e.out.String())
				assert.Contains(t, e.git.Calls, cloneNames)
			} else {
				assert.Equal(t, "b.go\n", e.out.String())
				assert.Equal(t, []string{"remote -v"}, e.git.Calls, "the API path runs no git beyond the remote check")
			}
		})
	}
}

func TestDiffColorFlag(t *testing.T) {
	t.Run("invalid value is a flag error", func(t *testing.T) {
		e := newCmdDiffEnv(t, false)
		err := e.run(t, "7", "--color", "sometimes")
		var flagErr *cmdutil.FlagError
		require.ErrorAs(t, err, &flagErr)
		assert.EqualError(t, err, `invalid value for --color: "sometimes" (want auto, always or never)`)
	})
	t.Run("always colours piped API output", func(t *testing.T) {
		e := newCmdDiffEnv(t, false)
		e.stubIterations()
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/git/repositories/web/pullRequests/7/iterations/2/changes"),
			httpmock.JSONResponse(map[string]any{"changeEntries": []any{apiChange("rename", "/new.go", apiNewSHA, apiNewSHA, "/old.go")}}))
		require.NoError(t, e.run(t, "7", "--color", "always"))
		assert.Equal(t, "\x1b[1;37mdiff --git a/old.go b/new.go\x1b[m\nrename from old.go\nrename to new.go\n", e.out.String())
	})
}

func TestDiffFindError(t *testing.T) {
	e := newCmdDiffEnv(t, false)
	assert.EqualError(t, e.run(t, "!x"), `invalid pull request number "!x"`)
}
