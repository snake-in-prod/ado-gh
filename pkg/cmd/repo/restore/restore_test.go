package restore

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const (
	repoRestoreID      = "11111111-2222-3333-4444-555555555555"
	repoRestoreOtherID = "66666666-7777-8888-9999-000000000000"
)

var repoRestoreOrgSeq atomic.Int64

type repoRestoreEnv struct {
	org    string
	reg    *httpmock.Registry
	ios    *iostreams.IOStreams
	out    *bytes.Buffer
	errOut *bytes.Buffer
}

// newRepoRestoreEnv returns an env for its own org: the SDK caches locations per base URL for
// the whole process. Every stub must be matched by the end of the test.
func newRepoRestoreEnv(t *testing.T, tty bool) *repoRestoreEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_ORG", "")
	t.Setenv("ADO_PROJECT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	org := fmt.Sprintf("reporestore-%d", repoRestoreOrgSeq.Add(1))
	e := &repoRestoreEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut}
	e.reg.StubLocations(org, httpmock.GitRepoLocations...)
	t.Cleanup(func() { e.reg.Verify(t) })
	return e
}

func (e *repoRestoreEnv) run(t *testing.T, args ...string) error {
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
	}
	cmd := NewCmdRestore(f)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard) // cobra usage; command output goes to e.ios
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

// stubBin stubs the recycle-bin listing of project (URL-escaped) with id/name pairs.
func (e *repoRestoreEnv) stubBin(project string, pairs ...string) {
	value := []any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		value = append(value, map[string]any{"id": pairs[i], "name": pairs[i+1]})
	}
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/"+project+"/_apis/git/recycleBin/repositories"),
		httpmock.JSONResponse(map[string]any{"count": len(value), "value": value}))
}

func (e *repoRestoreEnv) stubRestore(project, id string, resp httpmock.Responder) {
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/"+project+"/_apis/git/recycleBin/repositories/"+id), resp)
}

func repoRestoreOK(name string) httpmock.Responder {
	return httpmock.JSONResponse(map[string]any{
		"id": repoRestoreID, "name": name, "project": map[string]any{"name": "My Proj"},
		"webUrl": "https://dev.azure.com/o/My%20Proj/_git/" + name,
	})
}

func TestRepoRestore(t *testing.T) {
	tests := []struct {
		name    string
		tty     bool
		arg     string
		project string
		wantOut string
		wantErr string
	}{
		{name: "by name TTY", tty: true, arg: "api", project: "My%20Proj",
			wantOut: "https://dev.azure.com/o/My%20Proj/_git/api\n", wantErr: "Restored repository My Proj/api\n"},
		{name: "by name case-insensitive non-TTY", arg: "API", project: "My%20Proj",
			wantOut: "https://dev.azure.com/o/My%20Proj/_git/api\n"},
		{name: "by ID", arg: repoRestoreID, project: "My%20Proj",
			wantOut: "https://dev.azure.com/o/My%20Proj/_git/api\n"},
		{name: "project/repo", tty: true, arg: "other/api", project: "other",
			wantOut: "https://dev.azure.com/o/My%20Proj/_git/api\n", wantErr: "Restored repository other/api\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRepoRestoreEnv(t, tt.tty)
			e.stubBin(tt.project, repoRestoreOtherID, "web", repoRestoreID, "api")
			var bodies []string
			e.stubRestore(tt.project, repoRestoreID, func(r *http.Request) (*http.Response, error) {
				b, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				bodies = append(bodies, string(b))
				return repoRestoreOK("api")(r)
			})
			require.NoError(t, e.run(t, tt.arg))
			assert.Equal(t, tt.wantOut, e.out.String())
			assert.Equal(t, tt.wantErr, e.errOut.String())
			require.Len(t, bodies, 1)
			assert.JSONEq(t, `{"deleted": false}`, bodies[0])
		})
	}
}

func TestRepoRestoreOrgProjectRepo(t *testing.T) {
	e := newRepoRestoreEnv(t, false)
	e.stubBin("p2", repoRestoreID, "api")
	e.stubRestore("p2", repoRestoreID, repoRestoreOK("api"))
	require.NoError(t, e.run(t, e.org+"/p2/api"))
	assert.Equal(t, "https://dev.azure.com/o/My%20Proj/_git/api\n", e.out.String())
}

func TestRepoRestoreFallbackURL(t *testing.T) {
	e := newRepoRestoreEnv(t, false)
	e.stubBin("My%20Proj", repoRestoreID, "api")
	e.stubRestore("My%20Proj", repoRestoreID, httpmock.JSONResponse(map[string]any{"id": repoRestoreID}))
	require.NoError(t, e.run(t, "api"))
	want := adocontext.RepoURL(adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "api"})
	assert.Equal(t, want+"\n", e.out.String())
}

func TestRepoRestoreNotInRecycleBin(t *testing.T) {
	e := newRepoRestoreEnv(t, false)
	e.stubBin("My%20Proj", repoRestoreOtherID, "web")
	err := e.run(t, "api")
	require.EqualError(t, err, `no repository "api" in the recycle bin of project My Proj`)
	assert.Empty(t, e.out.String())
}

func TestRepoRestoreAmbiguous(t *testing.T) {
	e := newRepoRestoreEnv(t, false)
	e.stubBin("My%20Proj", repoRestoreID, "api", repoRestoreOtherID, "api")
	err := e.run(t, "api")
	require.EqualError(t, err, `several deleted repositories are named "api"; restore one by ID: `+
		repoRestoreID+", "+repoRestoreOtherID)
}

func TestRepoRestoreErrorsPassedThrough(t *testing.T) {
	t.Run("restore 403", func(t *testing.T) {
		e := newRepoRestoreEnv(t, false)
		e.stubBin("My%20Proj", repoRestoreID, "api")
		e.stubRestore("My%20Proj", repoRestoreID, httpmock.StatusJSONResponse(http.StatusForbidden,
			map[string]any{"message": "TF401027: You need the Git 'DeleteRepository' permission"}))
		err := e.run(t, "api")
		var he *api.HTTPError
		require.ErrorAs(t, err, &he)
		assert.Equal(t, http.StatusForbidden, he.StatusCode)
		assert.Contains(t, err.Error(), "TF401027")
		assert.Empty(t, e.out.String())
	})
	t.Run("list 403", func(t *testing.T) {
		e := newRepoRestoreEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/recycleBin/repositories"),
			httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))
		err := e.run(t, "api")
		var he *api.HTTPError
		require.ErrorAs(t, err, &he)
		assert.Equal(t, "denied", he.Message)
	})
}

func TestRepoRestoreArgs(t *testing.T) {
	e := newRepoRestoreEnv(t, false)
	e.reg = &httpmock.Registry{} // no requests expected, so no location stub either
	require.Error(t, e.run(t))
	require.Error(t, e.run(t, "a", "b"))
	assert.Empty(t, e.reg.Requests)
}
