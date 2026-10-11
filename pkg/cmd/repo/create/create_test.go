package create

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/iotest"

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

const repoCreateID = "11111111-2222-3333-4444-555555555555"

var repoCreateOrgSeq atomic.Int64

type repoCreateEnv struct {
	org    string
	reg    *httpmock.Registry
	git    *gittest.Runner
	cfg    *config.Config
	in     *bytes.Buffer
	out    *bytes.Buffer
	errOut *bytes.Buffer
	f      *cmdutil.Factory
	// pushes holds the JSON bodies of push requests.
	pushes []map[string]any
	// creates holds the JSON bodies of repository create requests.
	creates []map[string]any
}

// newRepoCreateEnv returns an env for its own org (the SDK caches locations per base URL) with
// the locations stubbed.
func newRepoCreateEnv(t *testing.T, tty bool) *repoCreateEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_ORG", "")
	t.Setenv("ADO_PROJECT", "")
	keyring.MockInit()
	cfg, err := config.Load()
	require.NoError(t, err)
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	ios.In = iotest.OneByteReader(in)
	e := &repoCreateEnv{
		org: fmt.Sprintf("repocreate-%d", repoCreateOrgSeq.Add(1)),
		reg: &httpmock.Registry{}, git: &gittest.Runner{}, cfg: cfg, in: in, out: out, errOut: errOut,
	}
	base := adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "web"}
	e.f = &cmdutil.Factory{
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo:   func() (adocontext.Repo, error) { return base, nil },
		Prompter:   &cmdutil.Prompter{IO: ios, Accessible: true},
		GitClient:  &git.Client{Run: e.git.Run, Stream: e.git.Stream},
	}
	e.reg.StubLocations(e.org, httpmock.GitRepoLocations...)
	return e
}

func (e *repoCreateEnv) run(args ...string) error {
	cmd := NewCmdCreate(e.f)
	cmd.SetArgs(args)
	cmd.SetIn(e.in)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true // as under the root command
	return cmd.Execute()
}

func (e *repoCreateEnv) webURL(project, name string) string {
	return "https://dev.azure.com/" + e.org + "/" + project + "/_git/" + name
}

func repoCreateCapture(t *testing.T, bodies *[]map[string]any, resp httpmock.Responder) httpmock.Responder {
	return func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		*bodies = append(*bodies, m)
		return resp(r)
	}
}

// stubCreate stubs the repository create in escapedProject, answering name.
func (e *repoCreateEnv) stubCreate(t *testing.T, escapedProject, project, name string) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/"+escapedProject+"/_apis/git/repositories"),
		repoCreateCapture(t, &e.creates, httpmock.StatusJSONResponse(http.StatusCreated, map[string]any{
			"id": repoCreateID, "name": name, "project": map[string]any{"name": project},
			"webUrl": e.webURL(escapedProject, name),
		})))
}

func (e *repoCreateEnv) stubTemplates() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/templates"),
		httpmock.JSONResponse(map[string]any{"count": 3, "value": []any{
			map[string]any{"name": "Go.gitignore", "type": "gitignore"},
			map[string]any{"name": "Gradle.gitignore", "type": "gitignore"},
			map[string]any{"name": "VisualStudio.gitignore", "type": "gitignore"},
		}}))
}

func (e *repoCreateEnv) stubPush(t *testing.T, resp httpmock.Responder) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/git/repositories/"+repoCreateID+"/pushes"),
		repoCreateCapture(t, &e.pushes, resp))
}

func TestRepoCreateProject(t *testing.T) {
	t.Run("current project", func(t *testing.T) {
		e := newRepoCreateEnv(t, false)
		e.stubCreate(t, "My%20Proj", "My Proj", "svc")
		require.NoError(t, e.run("svc"))
		e.reg.Verify(t)
		assert.Equal(t, []map[string]any{{"name": "svc"}}, e.creates)
		assert.Equal(t, e.webURL("My%20Proj", "svc")+"\n", e.out.String())
		assert.Empty(t, e.errOut.String())
		assert.Empty(t, e.git.Calls)
	})
	t.Run("--project", func(t *testing.T) {
		e := newRepoCreateEnv(t, true)
		e.stubCreate(t, "Other", "Other", "svc")
		require.NoError(t, e.run("svc", "-p", "Other"))
		e.reg.Verify(t)
		assert.Equal(t, e.webURL("Other", "svc")+"\n", e.out.String())
		assert.Contains(t, e.errOut.String(), "Created repository Other/svc")
	})
	t.Run("--project with org", func(t *testing.T) {
		e := newRepoCreateEnv(t, false)
		e.stubCreate(t, "Other", "Other", "svc")
		require.NoError(t, e.run("svc", "--project", e.org+"/Other"))
		e.reg.Verify(t)
	})
}

func TestRepoCreateInitialPush(t *testing.T) {
	zero := "0000000000000000000000000000000000000000"
	readme := map[string]any{"changeType": "add", "item": map[string]any{"path": "/README.md"},
		"newContent": map[string]any{"content": "# svc\n", "contentType": "rawText"}}
	gitignore := map[string]any{"changeType": "add", "item": map[string]any{"path": "/.gitignore"},
		"newContentTemplate": map[string]any{"name": "Go.gitignore", "type": "gitignore"}}
	tests := []struct {
		name      string
		args      []string
		templates bool
		changes   []any
	}{
		{"readme", []string{"--add-readme"}, false, []any{readme}},
		{"gitignore", []string{"-g", "go"}, true, []any{gitignore}},
		{"gitignore with suffix", []string{"--gitignore", "Go.gitignore"}, true, []any{gitignore}},
		{"both", []string{"--add-readme", "-g", "Go"}, true, []any{readme, gitignore}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRepoCreateEnv(t, false)
			if tt.templates {
				e.stubTemplates()
			}
			e.stubCreate(t, "My%20Proj", "My Proj", "svc")
			e.stubPush(t, httpmock.JSONResponse(map[string]any{"pushId": 1}))
			require.NoError(t, e.run(append([]string{"svc"}, tt.args...)...))
			e.reg.Verify(t)
			require.Len(t, e.pushes, 1)
			assert.Equal(t, []any{map[string]any{"name": "refs/heads/main", "oldObjectId": zero}}, e.pushes[0]["refUpdates"])
			commits := e.pushes[0]["commits"].([]any)
			require.Len(t, commits, 1)
			assert.Equal(t, tt.changes, commits[0].(map[string]any)["changes"])
		})
	}
}

func TestRepoCreateUnknownGitignore(t *testing.T) {
	t.Run("close matches", func(t *testing.T) {
		e := newRepoCreateEnv(t, false)
		e.stubTemplates()
		err := e.run("svc", "-g", "visualstudo")
		require.EqualError(t, err, `unknown gitignore template "visualstudo"; did you mean: VisualStudio`)
		e.reg.Verify(t)
		assert.Empty(t, e.creates, "no repository created")
	})
	t.Run("substring matches", func(t *testing.T) {
		e := newRepoCreateEnv(t, false)
		e.stubTemplates()
		require.EqualError(t, e.run("svc", "-g", "RADL"), `unknown gitignore template "RADL"; did you mean: Gradle`)
	})
	t.Run("no match", func(t *testing.T) {
		e := newRepoCreateEnv(t, false)
		e.stubTemplates()
		require.EqualError(t, e.run("svc", "-g", "Haskell"), `unknown gitignore template "Haskell"`)
	})
}

func TestRepoCreateClone(t *testing.T) {
	e := newRepoCreateEnv(t, false)
	e.cfg.Set("dev.azure.com", "git_protocol", "ssh")
	e.stubCreate(t, "My%20Proj", "My Proj", "svc")
	sshURL := "git@ssh.dev.azure.com:v3/" + e.org + "/My%20Proj/svc"
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/svc"),
		httpmock.JSONResponse(map[string]any{"id": repoCreateID, "name": "svc", "sshUrl": sshURL,
			"remoteUrl": "https://" + e.org + "@dev.azure.com/" + e.org + "/My%20Proj/_git/svc"}))
	require.NoError(t, e.run("svc", "--clone"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"clone -- " + sshURL + " svc"}, e.git.Calls)
	assert.Equal(t, e.webURL("My%20Proj", "svc")+"\n", e.out.String())
}

func TestRepoCreateNameConflict(t *testing.T) {
	e := newRepoCreateEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/git/repositories"),
		httpmock.StatusJSONResponse(http.StatusConflict, map[string]any{
			"message": "TF400948: A Git repository with the name svc already exists."}))
	err := e.run("svc", "--add-readme", "--clone")
	require.EqualError(t, err, "repository svc already exists in project My Proj")
	assert.Equal(t, cmdutil.ExitError, cmdutil.HandleError(io.Discard, err, ""))
	e.reg.Verify(t)
	assert.Empty(t, e.out.String())
	assert.Empty(t, e.git.Calls)
}

func TestRepoCreateOtherCreateError(t *testing.T) {
	e := newRepoCreateEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/git/repositories"),
		httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "TF401027: denied"}))
	require.ErrorContains(t, e.run("svc"), "TF401027")
}

func TestRepoCreatePushFailure(t *testing.T) {
	e := newRepoCreateEnv(t, false)
	e.stubCreate(t, "My%20Proj", "My Proj", "svc")
	e.stubPush(t, httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "push rejected"}))
	err := e.run("svc", "--add-readme", "--clone")
	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Equal(t, cmdutil.ExitError, cmdutil.HandleError(io.Discard, err, ""))
	e.reg.Verify(t)
	assert.Contains(t, e.errOut.String(), "warning:")
	assert.Contains(t, e.errOut.String(), "push rejected")
	assert.Equal(t, e.webURL("My%20Proj", "svc")+"\n", e.out.String(), "URL still printed")
	for _, r := range e.reg.Requests {
		assert.NotEqual(t, http.MethodDelete, r.Method, "empty repository kept")
	}
	assert.Empty(t, e.git.Calls, "no clone")
}

func TestRepoCreateTTYPrompts(t *testing.T) {
	e := newRepoCreateEnv(t, true)
	// name, project (default), README yes, .gitignore yes, template 3 (VisualStudio), clone no
	e.in.WriteString("svc\n\ny\ny\n3\nn\n")
	e.stubTemplates()
	e.stubCreate(t, "My%20Proj", "My Proj", "svc")
	e.stubPush(t, httpmock.JSONResponse(map[string]any{"pushId": 1}))
	require.NoError(t, e.run())
	e.reg.Verify(t)
	changes := e.pushes[0]["commits"].([]any)[0].(map[string]any)["changes"].([]any)
	require.Len(t, changes, 2)
	assert.Equal(t, map[string]any{"name": "VisualStudio.gitignore", "type": "gitignore"},
		changes[1].(map[string]any)["newContentTemplate"])
	assert.Contains(t, e.out.String(), "Repository name")
	assert.Contains(t, e.out.String(), e.webURL("My%20Proj", "svc")+"\n")
	assert.Empty(t, e.git.Calls)
}

func TestRepoCreateTTYPromptsProjectAndClone(t *testing.T) {
	e := newRepoCreateEnv(t, true)
	// name, project, README no, .gitignore no, clone yes
	e.in.WriteString("svc\nOther\nn\nn\ny\n")
	e.stubCreate(t, "Other", "Other", "svc")
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/Other/_apis/git/repositories/svc"),
		httpmock.JSONResponse(map[string]any{"id": repoCreateID, "name": "svc", "remoteUrl": "https://x/svc"}))
	require.NoError(t, e.run())
	e.reg.Verify(t)
	assert.Empty(t, e.pushes)
	assert.Equal(t, []string{"clone -- https://x/svc svc"}, e.git.Calls)
}

func TestRepoCreateTTYEmptyName(t *testing.T) {
	e := newRepoCreateEnv(t, true)
	e.in.WriteString("\n")
	require.ErrorContains(t, e.run(), "name is required")
}

func TestRepoCreateUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"non-TTY missing name", nil, "`<name>` required"},
		{"slash in name", []string{"a/b"}, "invalid repository name"},
		{"colon in name", []string{"a:b"}, "invalid repository name"},
		{"too many args", []string{"a", "b"}, "accepts at most 1 arg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRepoCreateEnv(t, false)
			err := e.run(tt.args...)
			require.ErrorContains(t, err, tt.want)
			var fe *cmdutil.FlagError
			if tt.name != "too many args" {
				assert.True(t, errors.As(err, &fe), "FlagError")
			}
			assert.Empty(t, e.reg.Requests)
		})
	}
}
