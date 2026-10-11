package delete

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/iotest"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const repoDeleteID = "11111111-2222-3333-4444-555555555555"

var repoDeleteOrgSeq atomic.Int64

type repoDeleteEnv struct {
	org    string
	reg    *httpmock.Registry
	in     *bytes.Buffer
	out    *bytes.Buffer
	errOut *bytes.Buffer
	f      *cmdutil.Factory
}

// newRepoDeleteEnv returns an env for its own org (the SDK caches locations per base URL) whose
// current repository is "My Proj"/web.
func newRepoDeleteEnv(t *testing.T, tty bool) *repoDeleteEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_ORG", "")
	t.Setenv("ADO_PROJECT", "")
	t.Setenv("ADO_REPO", "")
	keyring.MockInit()
	cfg, err := config.Load()
	require.NoError(t, err)
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	ios.In = iotest.OneByteReader(in)
	e := &repoDeleteEnv{
		org: fmt.Sprintf("repodelete-%d", repoDeleteOrgSeq.Add(1)),
		reg: &httpmock.Registry{}, in: in, out: out, errOut: errOut,
	}
	base := adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "web"}
	e.f = &cmdutil.Factory{
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo:   func() (adocontext.Repo, error) { return base, nil },
		Prompter:   &cmdutil.Prompter{IO: ios, Accessible: true},
	}
	e.reg.StubLocations(e.org, httpmock.GitRepoLocations...)
	return e
}

func (e *repoDeleteEnv) run(args ...string) error {
	cmd := NewCmdDelete(e.f)
	cmd.SetArgs(args)
	cmd.SetIn(e.in)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true // as under the root command
	return cmd.Execute()
}

// stubGet answers the lookup of repository name in escapedProject.
func (e *repoDeleteEnv) stubGet(escapedProject, project, name string, disabled bool) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/"+escapedProject+"/_apis/git/repositories/"+name),
		httpmock.JSONResponse(map[string]any{
			"id": repoDeleteID, "name": name, "project": map[string]any{"name": project}, "isDisabled": disabled,
		}))
}

func (e *repoDeleteEnv) stubSoftDelete(escapedProject string, resp httpmock.Responder) {
	e.reg.Register(httpmock.REST(http.MethodDelete, e.org+"/"+escapedProject+"/_apis/git/repositories/"+repoDeleteID), resp)
}

func (e *repoDeleteEnv) stubPurge(escapedProject string, resp httpmock.Responder) {
	e.reg.Register(httpmock.REST(http.MethodDelete, e.org+"/"+escapedProject+"/_apis/git/recycleBin/repositories/"+repoDeleteID), resp)
}

func repoDeleteNoContent() httpmock.Responder {
	return httpmock.StatusStringResponse(http.StatusNoContent, "")
}

func TestRepoDeleteSoft(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		status int
	}{
		{"current repo, 204", []string{"-y"}, http.StatusNoContent},
		{"bare name, 200", []string{"web", "--yes"}, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRepoDeleteEnv(t, false)
			e.stubGet("My%20Proj", "My Proj", "web", false)
			e.stubSoftDelete("My%20Proj", httpmock.StatusStringResponse(tt.status, ""))
			require.NoError(t, e.run(tt.args...))
			e.reg.Verify(t)
			assert.Empty(t, e.out.String())
			assert.Equal(t, "Deleted repository My Proj/web (restorable for 30 days: ado repo restore web)\n", e.errOut.String())
		})
	}
	t.Run("other project hint is qualified", func(t *testing.T) {
		e := newRepoDeleteEnv(t, false)
		e.stubGet("Other", "Other", "api", false)
		e.stubSoftDelete("Other", repoDeleteNoContent())
		require.NoError(t, e.run("Other/api", "-y"))
		e.reg.Verify(t)
		assert.Contains(t, e.errOut.String(), "ado repo restore "+e.org+"/Other/api)")
	})
	t.Run("hint with spaces is quoted", func(t *testing.T) {
		e := newRepoDeleteEnv(t, false)
		e.stubGet("My%20Proj", "My Proj", "api", false)
		e.stubSoftDelete("My%20Proj", repoDeleteNoContent())
		require.NoError(t, e.run(e.org+"/My Proj/api", "-y"))
		e.reg.Verify(t)
		assert.Contains(t, e.errOut.String(), `ado repo restore '`+e.org+`/My Proj/api')`)
	})
	t.Run("hint with shell metacharacters is single-quoted", func(t *testing.T) {
		e := newRepoDeleteEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/it%27s&%28x%29"),
			httpmock.JSONResponse(map[string]any{"id": repoDeleteID, "name": "it's&(x)", "project": map[string]any{"name": "My Proj"}}))
		e.stubSoftDelete("My%20Proj", repoDeleteNoContent())
		require.NoError(t, e.run("it's&(x)", "-y"))
		e.reg.Verify(t)
		assert.Contains(t, e.errOut.String(), `ado repo restore 'it'\''s&(x)')`)
	})
	for _, tt := range []struct {
		name string
		set  func(t *testing.T, e *repoDeleteEnv)
	}{
		{"-R target hint is qualified", func(_ *testing.T, e *repoDeleteEnv) { e.f.RepoOverride = e.org + "/My Proj/web" }},
		{"ADO_REPO target hint is qualified", func(t *testing.T, e *repoDeleteEnv) { t.Setenv("ADO_REPO", e.org+"/My Proj/web") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newRepoDeleteEnv(t, false)
			tt.set(t, e)
			e.stubGet("My%20Proj", "My Proj", "web", false)
			e.stubSoftDelete("My%20Proj", repoDeleteNoContent())
			require.NoError(t, e.run("-y"))
			e.reg.Verify(t)
			assert.Contains(t, e.errOut.String(), `ado repo restore '`+e.org+`/My Proj/web')`)
		})
	}
	t.Run("disabled repo is not blocked", func(t *testing.T) {
		e := newRepoDeleteEnv(t, false)
		e.stubGet("My%20Proj", "My Proj", "web", true)
		e.stubSoftDelete("My%20Proj", repoDeleteNoContent())
		require.NoError(t, e.run("-y"))
		e.reg.Verify(t)
	})
}

func TestRepoDeletePermanent(t *testing.T) {
	t.Run("soft delete then purge", func(t *testing.T) {
		e := newRepoDeleteEnv(t, false)
		e.stubGet("My%20Proj", "My Proj", "web", false)
		e.stubSoftDelete("My%20Proj", repoDeleteNoContent())
		e.stubPurge("My%20Proj", repoDeleteNoContent())
		require.NoError(t, e.run("--permanent", "-y"))
		e.reg.Verify(t)
		var paths []string
		for _, r := range e.reg.Requests {
			if r.Method == http.MethodDelete {
				paths = append(paths, r.URL.Path)
			}
		}
		assert.Equal(t, []string{
			"/" + e.org + "/My Proj/_apis/git/repositories/" + repoDeleteID,
			"/" + e.org + "/My Proj/_apis/git/recycleBin/repositories/" + repoDeleteID,
		}, paths, "purge after soft delete, with the project")
		assert.Equal(t, "Permanently deleted repository My Proj/web\n", e.errOut.String())
	})
	t.Run("purge failure says it is still in the recycle bin", func(t *testing.T) {
		e := newRepoDeleteEnv(t, false)
		e.stubGet("My%20Proj", "My Proj", "web", false)
		e.stubSoftDelete("My%20Proj", repoDeleteNoContent())
		e.stubPurge("My%20Proj", httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))
		err := e.run("--permanent", "-y")
		require.Error(t, err)
		e.reg.Verify(t)
		assert.Contains(t, err.Error(), "My Proj/web is still in the recycle bin: purging it failed")
		assert.Contains(t, err.Error(), "denied")
		assert.Contains(t, err.Error(), "ado repo restore web")
	})
}

func TestRepoDeleteServerErrorPassedThrough(t *testing.T) {
	e := newRepoDeleteEnv(t, false)
	e.stubGet("My%20Proj", "My Proj", "web", false)
	msg := "TF401019: You can't delete the only repository in the project."
	e.stubSoftDelete("My%20Proj", httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": msg}))
	err := e.run("--permanent", "-y")
	require.EqualError(t, err, msg+" (HTTP 400)")
	e.reg.Verify(t) // no purge stub registered: none attempted
	assert.Empty(t, e.errOut.String())
}

func TestRepoDeleteLookupFails(t *testing.T) {
	e := newRepoDeleteEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/nope"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "not found"}))
	require.ErrorContains(t, e.run("nope", "-y"), "not found")
	e.reg.Verify(t)
}

func TestRepoDeleteConfirmation(t *testing.T) {
	t.Run("typed name matches", func(t *testing.T) {
		e := newRepoDeleteEnv(t, true)
		e.stubGet("My%20Proj", "My Proj", "web", false)
		e.stubSoftDelete("My%20Proj", repoDeleteNoContent())
		e.in.WriteString("web\n")
		require.NoError(t, e.run())
		e.reg.Verify(t)
		assert.Contains(t, e.out.String(), "Type web to move My Proj/web to the recycle bin")
		assert.Contains(t, e.out.String(), "30 days")
	})
	t.Run("permanent prompt mentions permanence", func(t *testing.T) {
		e := newRepoDeleteEnv(t, true)
		e.stubGet("My%20Proj", "My Proj", "web", false)
		e.stubSoftDelete("My%20Proj", repoDeleteNoContent())
		e.stubPurge("My%20Proj", repoDeleteNoContent())
		e.in.WriteString("web\n")
		require.NoError(t, e.run("--permanent"))
		e.reg.Verify(t)
		assert.Contains(t, e.out.String(), "Type web to permanently delete My Proj/web")
		assert.Contains(t, e.out.String(), "cannot be undone")
	})
	t.Run("mismatch cancels with exit 2", func(t *testing.T) {
		e := newRepoDeleteEnv(t, true)
		e.stubGet("My%20Proj", "My Proj", "web", false)
		e.in.WriteString("Web\n")
		err := e.run()
		require.ErrorIs(t, err, cmdutil.CancelError)
		assert.Equal(t, cmdutil.ExitCancel, cmdutil.HandleError(io.Discard, err, ""))
		e.reg.Verify(t)
		for _, r := range e.reg.Requests {
			assert.NotEqual(t, http.MethodDelete, r.Method, "nothing deleted")
		}
		assert.Contains(t, e.errOut.String(), "did not match")
	})
	t.Run("-y skips the prompt", func(t *testing.T) {
		e := newRepoDeleteEnv(t, true)
		e.stubGet("My%20Proj", "My Proj", "web", false)
		e.stubSoftDelete("My%20Proj", repoDeleteNoContent())
		require.NoError(t, e.run("-y"))
		e.reg.Verify(t)
		assert.NotContains(t, e.out.String(), "Type web")
	})
}

func TestRepoDeleteNonInteractiveNeedsYes(t *testing.T) {
	e := newRepoDeleteEnv(t, false)
	err := e.run()
	require.EqualError(t, err, "--yes required when not running interactively")
	var fe *cmdutil.FlagError
	assert.ErrorAs(t, err, &fe)
	assert.Empty(t, e.reg.Requests)
}
