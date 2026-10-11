package clone

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/internal/git/gittest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const repoCloneParentID = "99999999-8888-7777-6666-555555555555"

var repoCloneOrgSeq atomic.Int64

type repoCloneEnv struct {
	org    string
	reg    *httpmock.Registry
	git    *gittest.Runner
	cfg    *config.Config
	out    *bytes.Buffer
	errOut *bytes.Buffer
	f      *cmdutil.Factory
}

// newRepoCloneEnv returns an env for its own org: the SDK caches locations per base URL for the
// whole process.
func newRepoCloneEnv(t *testing.T) *repoCloneEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_ORG", "")
	t.Setenv("ADO_PROJECT", "")
	keyring.MockInit()
	cfg, err := config.Load()
	require.NoError(t, err)
	ios, _, out, errOut := iostreams.Test()
	e := &repoCloneEnv{
		org: fmt.Sprintf("repoclone-%d", repoCloneOrgSeq.Add(1)),
		reg: &httpmock.Registry{}, git: &gittest.Runner{}, cfg: cfg, out: out, errOut: errOut,
	}
	base := adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "web"}
	e.f = &cmdutil.Factory{
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo:   func() (adocontext.Repo, error) { return base, nil },
		GitClient:  &git.Client{Run: e.git.Run, Stream: e.git.Stream},
	}
	return e
}

func (e *repoCloneEnv) run(args ...string) error {
	cmd := NewCmdClone(e.f)
	cmd.SetArgs(args)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true // as under the root command
	return cmd.Execute()
}

func (e *repoCloneEnv) remoteURL(name string) string {
	return "https://" + e.org + "@dev.azure.com/" + e.org + "/My%20Proj/_git/" + name
}

func (e *repoCloneEnv) sshURL(name string) string {
	return "git@ssh.dev.azure.com:v3/" + e.org + "/My%20Proj/" + name
}

// stubRepo stubs the location OPTIONS and the GET of repository escapedName, answering name;
// extra are key/value overrides (nil deletes the key).
func (e *repoCloneEnv) stubRepo(escapedName, name string, extra ...any) {
	r := map[string]any{
		"id": "11111111-2222-3333-4444-555555555555", "name": name, "project": map[string]any{"name": "My Proj"},
		"remoteUrl": e.remoteURL(escapedName), "sshUrl": e.sshURL(escapedName),
	}
	for i := 0; i+1 < len(extra); i += 2 {
		if extra[i+1] == nil {
			delete(r, extra[i].(string))
		} else {
			r[extra[i].(string)] = extra[i+1]
		}
	}
	e.reg.StubLocations(e.org, httpmock.GitRepoLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/"+escapedName),
		httpmock.JSONResponse(r))
}

// parent returns a parentRepository object; empty URLs are left out.
func (e *repoCloneEnv) parent(remoteURL, sshURL string) map[string]any {
	p := map[string]any{"id": repoCloneParentID, "name": "core", "project": map[string]any{"name": "My Proj"}}
	if remoteURL != "" {
		p["remoteUrl"] = remoteURL
	}
	if sshURL != "" {
		p["sshUrl"] = sshURL
	}
	return p
}

func TestRepoCloneURLAndDirectory(t *testing.T) {
	tests := []struct {
		name        string
		ssh         bool
		args        []string
		escapedName string // repository path segment requested from the API
		apiName     string // name returned by the API
		want        func(e *repoCloneEnv) string
	}{
		{"https keeps org@", false, []string{"api"}, "api", "api",
			func(e *repoCloneEnv) string { return "clone -- " + e.remoteURL("api") + " api" }},
		{"ssh by protocol", true, []string{"api"}, "api", "api",
			func(e *repoCloneEnv) string { return "clone -- " + e.sshURL("api") + " api" }},
		{"canonical name is the directory", false, []string{"API"}, "API", "api",
			func(e *repoCloneEnv) string { return "clone -- " + e.remoteURL("API") + " api" }},
		{"decoded name with spaces", false, []string{"My Repo"}, "My%20Repo", "My Repo",
			func(e *repoCloneEnv) string { return "clone -- " + e.remoteURL("My%20Repo") + " My Repo" }},
		{"name starting with -", false, []string{"My Proj/-evil"}, "-evil", "-evil",
			func(e *repoCloneEnv) string { return "clone -- " + e.remoteURL("-evil") + " -evil" }},
		{"directory", false, []string{"api", "my dir"}, "api", "api",
			func(e *repoCloneEnv) string { return "clone -- " + e.remoteURL("api") + " my dir" }},
		{"git flags after --", false, []string{"api", "--", "--depth", "1"}, "api", "api",
			func(e *repoCloneEnv) string { return "clone --depth 1 -- " + e.remoteURL("api") + " api" }},
		{"directory and git flags", false, []string{"api", "d", "--", "--depth", "1"}, "api", "api",
			func(e *repoCloneEnv) string { return "clone --depth 1 -- " + e.remoteURL("api") + " d" }},
		{"--bare adds .git", false, []string{"api", "--", "--bare"}, "api", "api",
			func(e *repoCloneEnv) string { return "clone --bare -- " + e.remoteURL("api") + " api.git" }},
		{"--mirror adds .git", false, []string{"My Repo", "--", "--mirror"}, "My%20Repo", "My Repo",
			func(e *repoCloneEnv) string { return "clone --mirror -- " + e.remoteURL("My%20Repo") + " My Repo.git" }},
		{"--bare with directory kept", false, []string{"api", "x", "--", "--bare"}, "api", "api",
			func(e *repoCloneEnv) string { return "clone --bare -- " + e.remoteURL("api") + " x" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRepoCloneEnv(t)
			if tt.ssh {
				e.cfg.Set("dev.azure.com", "git_protocol", "ssh")
			}
			e.stubRepo(tt.escapedName, tt.apiName)
			require.NoError(t, e.run(tt.args...))
			e.reg.Verify(t)
			assert.Equal(t, []string{tt.want(e)}, e.git.Calls)
			assert.Equal(t, []string{""}, e.git.Dirs)
			for _, r := range e.reg.Requests {
				assert.Empty(t, r.URL.User, "no userinfo in API URLs")
			}
		})
	}
}

func TestRepoCloneNoTokenInURL(t *testing.T) {
	e := newRepoCloneEnv(t)
	e.stubRepo("api", "api")
	require.NoError(t, e.run("api"))
	for _, c := range e.git.Calls {
		assert.NotContains(t, c, "pat")
	}
}

func TestRepoCloneFullURL(t *testing.T) {
	t.Run("git URL cloned as given", func(t *testing.T) {
		e := newRepoCloneEnv(t)
		e.cfg.Set("dev.azure.com", "git_protocol", "ssh")
		e.stubRepo("api", "api", "remoteUrl", "https://dev.azure.com/other")
		given := "https://someone@dev.azure.com/" + e.org + "/My%20Proj/_git/api"
		require.NoError(t, e.run(given))
		assert.Equal(t, []string{"clone -- " + given + " api"}, e.git.Calls)
	})
	t.Run("web URL uses the repository URL", func(t *testing.T) {
		e := newRepoCloneEnv(t)
		e.stubRepo("api", "api")
		require.NoError(t, e.run("https://dev.azure.com/"+e.org+"/My%20Proj/_git/api?path=/x&version=GBmain"))
		assert.Equal(t, []string{"clone -- " + e.remoteURL("api") + " api"}, e.git.Calls)
	})
}

func TestRepoClonePasswordURLRefused(t *testing.T) {
	e := newRepoCloneEnv(t)
	err := e.run("https://me:s3cret@dev.azure.com/" + e.org + "/My%20Proj/_git/api")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "credential manager")
	assert.NotContains(t, err.Error(), "s3cret")
	assert.NotContains(t, err.Error(), "dev.azure.com")
	assert.Empty(t, e.reg.Requests)
	assert.Empty(t, e.git.Calls)
}

func TestRepoCloneUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no repo", nil},
		{"only git flags", []string{"--", "--depth", "1"}},
		{"too many", []string{"a", "b", "c"}},
		{"dot", []string{"api", "."}},
		{"dotdot", []string{"api", ".."}},
		{"slash", []string{"api", "a/b"}},
		{"backslash", []string{"api", `a\b`}},
		{"parent path", []string{"api", "../x"}},
		{"empty directory", []string{"api", ""}},
		{"upstream name with -", []string{"api", "-u", "-x"}},
		{"empty upstream name", []string{"api", "-u", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRepoCloneEnv(t)
			err := e.run(tt.args...)
			var flagErr *cmdutil.FlagError
			require.ErrorAs(t, err, &flagErr)
			assert.Empty(t, e.reg.Requests)
			assert.Empty(t, e.git.Calls)
		})
	}
}

func TestRepoCloneFork(t *testing.T) {
	tests := []struct {
		name   string
		ssh    bool
		args   []string
		dir    string
		remote string
		want   func(e *repoCloneEnv) string // parent URL
	}{
		{"https upstream", false, []string{"api"}, "api", "upstream",
			func(e *repoCloneEnv) string { return e.remoteURL("core") }},
		{"ssh upstream", true, []string{"api"}, "api", "upstream",
			func(e *repoCloneEnv) string { return e.sshURL("core") }},
		{"-u renames", false, []string{"api", "-u", "parent"}, "api", "parent",
			func(e *repoCloneEnv) string { return e.remoteURL("core") }},
		{"in the given directory", false, []string{"api", "my dir"}, "my dir", "upstream",
			func(e *repoCloneEnv) string { return e.remoteURL("core") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRepoCloneEnv(t)
			if tt.ssh {
				e.cfg.Set("dev.azure.com", "git_protocol", "ssh")
			}
			e.stubRepo("api", "api", "isFork", true, "parentRepository", e.parent(e.remoteURL("core"), e.sshURL("core")))
			require.NoError(t, e.run(tt.args...))
			clone := e.git.Calls[0]
			assert.Equal(t, []string{
				clone, "remote add -- " + tt.remote + " " + tt.want(e), "fetch " + tt.remote,
			}, e.git.Calls, "no ado-resolved config is set")
			assert.Equal(t, []string{"", tt.dir, tt.dir}, e.git.Dirs)
		})
	}
}

func TestRepoCloneForkFlags(t *testing.T) {
	cmd := NewCmdClone(&cmdutil.Factory{})
	assert.Nil(t, cmd.Flags().Lookup("no-upstream"))
	assert.Equal(t, "upstream", cmd.Flags().Lookup("upstream-remote-name").DefValue)
}

func TestRepoCloneForkParentByID(t *testing.T) {
	e := newRepoCloneEnv(t)
	e.stubRepo("api", "api", "isFork", true, "parentRepository", e.parent("", ""))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/git/repositories/"+repoCloneParentID),
		httpmock.JSONResponse(map[string]any{"id": repoCloneParentID, "name": "core", "remoteUrl": e.remoteURL("core")}))
	require.NoError(t, e.run("api"))
	e.reg.Verify(t)
	assert.Equal(t, []string{
		"clone -- " + e.remoteURL("api") + " api", "remote add -- upstream " + e.remoteURL("core"), "fetch upstream",
	}, e.git.Calls)
}

func TestRepoCloneForkParentErrors(t *testing.T) {
	t.Run("lookup fails", func(t *testing.T) {
		e := newRepoCloneEnv(t)
		e.stubRepo("api", "api", "isFork", true, "parentRepository", e.parent("", ""))
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/git/repositories/"+repoCloneParentID),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401019: not found"}))
		err := e.run("api")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot read the parent of fork api")
		assert.Len(t, e.git.Calls, 1)
	})
	t.Run("no parent URL", func(t *testing.T) {
		e := newRepoCloneEnv(t)
		e.cfg.Set("dev.azure.com", "git_protocol", "ssh")
		e.stubRepo("api", "api", "isFork", true, "parentRepository", e.parent(e.remoteURL("core"), ""))
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/git/repositories/"+repoCloneParentID),
			httpmock.JSONResponse(map[string]any{"id": repoCloneParentID, "name": "core", "remoteUrl": e.remoteURL("core")}))
		err := e.run("api")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot find the clone URL of the parent of fork api")
	})
	t.Run("fork without parent clones only", func(t *testing.T) {
		e := newRepoCloneEnv(t)
		e.stubRepo("api", "api", "isFork", true)
		require.NoError(t, e.run("api"))
		assert.Len(t, e.git.Calls, 1)
	})
}

func TestRepoCloneGitFailure(t *testing.T) {
	e := newRepoCloneEnv(t)
	e.stubRepo("api", "api")
	url := e.remoteURL("api")
	e.git.Fails = map[string]error{"clone -- " + url + " api": &git.Error{
		Args: []string{"clone", "--", url, "api"}, ExitCode: 128, Stderr: "fatal: repository not found\n",
	}}
	err := e.run("api")
	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Equal(t, "fatal: repository not found\n", e.errOut.String(), "git's stderr is shown")

	var stderr bytes.Buffer
	assert.Equal(t, cmdutil.ExitError, cmdutil.HandleError(&stderr, err, ""))
	assert.Empty(t, stderr.String(), "the URL is not echoed")
}

func TestRepoCloneUpstreamFailures(t *testing.T) {
	for _, failing := range []string{"remote add", "fetch"} {
		t.Run(failing, func(t *testing.T) {
			e := newRepoCloneEnv(t)
			e.stubRepo("api", "api", "isFork", true, "parentRepository", e.parent(e.remoteURL("core"), ""))
			key := "fetch upstream"
			if failing == "remote add" {
				key = "remote add -- upstream " + e.remoteURL("core")
			}
			e.git.Fails = map[string]error{key: &git.Error{ExitCode: 1, Stderr: "error: boom\n"}}
			require.ErrorIs(t, e.run("api"), cmdutil.SilentError)
			assert.Equal(t, key, e.git.Calls[len(e.git.Calls)-1])
		})
	}
}

func TestRepoCloneAPIErrors(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		e := newRepoCloneEnv(t)
		e.reg.StubLocations(e.org, httpmock.GitRepoLocations...)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/api"),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401019: repository api not found"}))
		require.Error(t, e.run("api"))
		assert.Empty(t, e.git.Calls)
	})
	t.Run("no clone URL", func(t *testing.T) {
		e := newRepoCloneEnv(t)
		e.stubRepo("api", "api", "remoteUrl", nil)
		err := e.run("api")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "repository api has no clone URL")
		assert.Empty(t, e.git.Calls)
	})
	t.Run("missing organization", func(t *testing.T) {
		e := newRepoCloneEnv(t)
		e.f.BaseRepo = func() (adocontext.Repo, error) { return adocontext.Repo{}, fmt.Errorf("no git remotes") }
		require.Error(t, e.run("api"))
		assert.Empty(t, e.git.Calls)
	})
	t.Run("unauthenticated", func(t *testing.T) {
		e := newRepoCloneEnv(t)
		t.Setenv("ADO_TOKEN", "")
		require.Error(t, e.run("api"))
		assert.Empty(t, e.git.Calls)
	})
}

// TestRepoCloneExported covers Clone as `repo create --clone` calls it: default upstream name.
func TestRepoCloneExported(t *testing.T) {
	e := newRepoCloneEnv(t)
	e.stubRepo("svc", "svc", "isFork", true, "parentRepository", e.parent(e.remoteURL("core"), ""))
	repo := adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "svc"}
	require.NoError(t, Clone(context.Background(), e.f, Options{Repo: repo}))
	assert.Equal(t, []string{
		"clone -- " + e.remoteURL("svc") + " svc", "remote add -- upstream " + e.remoteURL("core"), "fetch upstream",
	}, e.git.Calls)
}
