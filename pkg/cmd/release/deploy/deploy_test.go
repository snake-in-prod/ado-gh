package deploy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

type relDeployEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	in          *bytes.Buffer
	out, errOut *bytes.Buffer
	// patches are the bodies of the environment PATCHes, keyed by request path.
	patches map[string]map[string]any
}

// newRelDeployEnv returns an env for its own org (the SDK caches discovery per base URL).
func newRelDeployEnv(t *testing.T, tty bool) *relDeployEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_REPO", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	ios.In = iotest.OneByteReader(in)
	org := "reldeploy-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &relDeployEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut, patches: map[string]map[string]any{}}
	e.reg.StubLocations(org, httpmock.ResourceAreasLocation)
	e.reg.StubVSRM(org)
	return e
}

func (e *relDeployEnv) run(t *testing.T, newCmd func(*cmdutil.Factory) *cobra.Command, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "proj", Name: "ado-gh"}, nil
		},
		Prompter: &cmdutil.Prompter{IO: e.ios, Accessible: true},
	}
	cmd := newCmd(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

// stubRelease answers the GET of release 3 in project with release_failed_task.json: Dev (5)
// succeeded, QA (7) inProgress, Prod (6) rejected.
func (e *relDeployEnv) stubRelease(t *testing.T, project string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/"+project+"/_apis/release/releases/3"),
		httpmock.JSONResponse(json.RawMessage(pipelinefixtures.Read(t, "release_failed_task.json"))))
}

// stubPatch answers the PATCH of environment envID of release 3, recording its body.
func (e *relDeployEnv) stubPatch(t *testing.T, project, envID string) {
	path := e.org + "/" + project + "/_apis/release/releases/3/environments/" + envID
	e.reg.Register(httpmock.REST(http.MethodPatch, path), func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		e.patches[path] = body
		return httpmock.JSONResponse(map[string]any{"id": 6, "status": "queued"})(r)
	})
}

func (e *relDeployEnv) patch(project, envID string) map[string]any {
	return e.patches[e.org+"/"+project+"/_apis/release/releases/3/environments/"+envID]
}

func TestRelDeployYes(t *testing.T) {
	e := newRelDeployEnv(t, false)
	e.stubRelease(t, "proj")
	e.stubPatch(t, "proj", "6")

	require.NoError(t, e.run(t, NewCmdDeploy, "3", "--environment", "prod", "-c", "hotfix", "-y"))
	e.reg.Verify(t)
	assert.Equal(t, map[string]any{"status": "inProgress", "comment": "hotfix"}, e.patch("proj", "6"))
	assert.Contains(t, e.errOut.String(), "Started deployment of Release-3 to Prod")
}

func TestRelDeployByEnvironmentIDAndURL(t *testing.T) {
	e := newRelDeployEnv(t, false)
	e.stubRelease(t, "other")
	e.stubPatch(t, "other", "5")

	require.NoError(t, e.run(t, NewCmdDeploy, "https://dev.azure.com/"+e.org+"/other/_release?releaseId=3", "--environment", "5", "--yes"))
	e.reg.Verify(t)
	assert.Equal(t, map[string]any{"status": "inProgress"}, e.patch("other", "5"))
}

func TestRelCancelYes(t *testing.T) {
	e := newRelDeployEnv(t, false)
	e.stubRelease(t, "proj")
	e.stubPatch(t, "proj", "7")

	require.NoError(t, e.run(t, NewCmdCancel, "3", "--environment", "QA", "-y"))
	e.reg.Verify(t)
	assert.Equal(t, map[string]any{"status": "canceled"}, e.patch("proj", "7"))
	assert.Contains(t, e.errOut.String(), "Canceled the deployment of Release-3 to QA")
}

func TestRelCancelNotDeploying(t *testing.T) {
	e := newRelDeployEnv(t, false)
	e.stubRelease(t, "proj")

	err := e.run(t, NewCmdCancel, "3", "--environment", "Prod", "-y")
	require.EqualError(t, err, "Release-3 is not deploying to Prod (status rejected)")
	e.reg.Verify(t)
	assert.Empty(t, e.patches)
}

func TestRelDeployConfirm(t *testing.T) {
	t.Run("deploy accept", func(t *testing.T) {
		e := newRelDeployEnv(t, true)
		e.stubRelease(t, "proj")
		e.stubPatch(t, "proj", "6")
		e.in.WriteString("y\n")

		require.NoError(t, e.run(t, NewCmdDeploy, "3", "--environment", "Prod"))
		e.reg.Verify(t)
		assert.Contains(t, e.out.String(), "Deploy Release-3 to Prod?")
		assert.Equal(t, map[string]any{"status": "inProgress"}, e.patch("proj", "6"))
	})

	t.Run("cancel decline", func(t *testing.T) {
		e := newRelDeployEnv(t, true)
		e.stubRelease(t, "proj")
		e.in.WriteString("n\n")

		require.ErrorIs(t, e.run(t, NewCmdCancel, "3", "--environment", "QA"), cmdutil.CancelError)
		e.reg.Verify(t)
		assert.Contains(t, e.out.String(), "Cancel the deployment of Release-3 to QA?")
		assert.Empty(t, e.patches)
	})

	t.Run("prompt fails", func(t *testing.T) {
		e := newRelDeployEnv(t, true)
		e.stubRelease(t, "proj")

		require.Error(t, e.run(t, NewCmdDeploy, "3", "--environment", "Prod"))
		assert.Empty(t, e.patches)
	})
}

func TestRelDeployFlagErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		cmd  func(*cmdutil.Factory) *cobra.Command
		args []string
		want string
	}{
		{"deploy needs --yes", NewCmdDeploy, []string{"3", "--environment", "Prod"}, "--yes required"},
		{"cancel needs --yes", NewCmdCancel, []string{"3", "--environment", "QA"}, "--yes required"},
		{"deploy needs --environment", NewCmdDeploy, []string{"3", "-y"}, "--environment is required"},
		{"cancel needs --environment", NewCmdCancel, []string{"3", "-y"}, "--environment is required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newRelDeployEnv(t, false)
			err := e.run(t, tt.cmd, tt.args...)
			var fe *cmdutil.FlagError
			require.ErrorAs(t, err, &fe)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestRelDeployErrors(t *testing.T) {
	t.Run("unknown environment", func(t *testing.T) {
		e := newRelDeployEnv(t, false)
		e.stubRelease(t, "proj")
		err := e.run(t, NewCmdDeploy, "3", "--environment", "Staging", "-y")
		require.EqualError(t, err, `no environment "Staging" in release 3; its environments: Dev, QA, Prod`)
		assert.Empty(t, e.patches)
	})

	t.Run("invalid ref", func(t *testing.T) {
		e := newRelDeployEnv(t, false)
		require.ErrorContains(t, e.run(t, NewCmdDeploy, "abc", "--environment", "Prod", "-y"), "invalid release")
	})

	t.Run("GET fails", func(t *testing.T) {
		e := newRelDeployEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/release/releases/3"),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "release 3 not found"}))
		require.ErrorContains(t, e.run(t, NewCmdDeploy, "3", "--environment", "Prod", "-y"), "not found")
	})

	t.Run("PATCH fails", func(t *testing.T) {
		e := newRelDeployEnv(t, false)
		e.stubRelease(t, "proj")
		e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/proj/_apis/release/releases/3/environments/6"),
			httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "access denied"}))
		require.ErrorContains(t, e.run(t, NewCmdDeploy, "3", "--environment", "Prod", "-y"), "access denied")
	})
}
