package cancel

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
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

type plCancelEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	in          *bytes.Buffer
	out, errOut *bytes.Buffer
	// calls records the build requests in order, as "GET" or "PATCH <status>".
	calls []string
	// runQueries are the query strings of the build list requests.
	runQueries []url.Values
}

// newPLCancelEnv returns an env for its own org (the SDK caches discovery per base URL).
func newPLCancelEnv(t *testing.T, tty bool) *plCancelEnv {
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
	org := "plcancel-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &plCancelEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut}
	e.reg.StubLocations(org, httpmock.PipelineLocations...)
	return e
}

func (e *plCancelEnv) run(t *testing.T, args ...string) error {
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
		Prompter: &cmdutil.Prompter{IO: e.ios, Accessible: true},
	}
	cmd := NewCmdCancel(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

// stubGet answers the GET of run 812 with status.
func (e *plCancelEnv) stubGet(status string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/build/builds/812"), func(r *http.Request) (*http.Response, error) {
		e.calls = append(e.calls, "GET")
		return httpmock.JSONResponse(map[string]any{"id": 812, "status": status, "buildNumber": "20261001.1",
			"definition": map[string]any{"id": 1, "name": "ado-gh"}})(r)
	})
}

// stubPatch answers the PATCH of run 812 with a run in status respStatus, recording the sent status.
func (e *plCancelEnv) stubPatch(t *testing.T, respStatus string) {
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/My%20Proj/_apis/build/builds/812"), func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		e.calls = append(e.calls, "PATCH "+body["status"].(string))
		return httpmock.JSONResponse(map[string]any{"id": 812, "status": respStatus})(r)
	})
}

func TestPLCancelYes(t *testing.T) {
	e := newPLCancelEnv(t, false)
	e.stubGet("inProgress")
	e.stubPatch(t, "cancelling")

	require.NoError(t, e.run(t, "812", "-y"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"GET", "PATCH cancelling"}, e.calls)
	assert.Contains(t, e.errOut.String(), "Requested cancellation of run 812")
}

func TestPLCancelRunURL(t *testing.T) {
	e := newPLCancelEnv(t, false)
	e.stubGet("notStarted")
	e.stubPatch(t, "cancelling")

	require.NoError(t, e.run(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_build/results?buildId=812", "--yes"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"GET", "PATCH cancelling"}, e.calls)
}

func TestPLCancelCompleted(t *testing.T) {
	e := newPLCancelEnv(t, false)
	e.stubGet("completed")

	err := e.run(t, "812", "--yes")
	require.EqualError(t, err, "run 812 has already completed")
	e.reg.Verify(t)
	assert.Equal(t, []string{"GET"}, e.calls)
}

func TestPLCancelPatchReturnsCompleted(t *testing.T) {
	e := newPLCancelEnv(t, false)
	e.stubGet("inProgress")
	e.stubPatch(t, "completed")

	require.EqualError(t, e.run(t, "812", "--yes"), "run 812 has already completed")
	e.reg.Verify(t)
}

func TestPLCancelAlreadyCancelling(t *testing.T) {
	e := newPLCancelEnv(t, true)
	e.stubGet("cancelling")

	require.NoError(t, e.run(t, "812"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"GET"}, e.calls)
	assert.Contains(t, e.errOut.String(), "Run 812 is already being canceled")
}

func TestPLCancelNonTTYNeedsYes(t *testing.T) {
	e := newPLCancelEnv(t, false)

	err := e.run(t, "812")
	var fe *cmdutil.FlagError
	require.ErrorAs(t, err, &fe)
	assert.Contains(t, err.Error(), "--yes required")
	assert.Empty(t, e.calls)
}

func TestPLCancelConfirm(t *testing.T) {
	t.Run("accept", func(t *testing.T) {
		e := newPLCancelEnv(t, true)
		e.stubGet("inProgress")
		e.stubPatch(t, "cancelling")
		e.in.WriteString("y\n")

		require.NoError(t, e.run(t, "812"))
		e.reg.Verify(t)
		assert.Equal(t, []string{"GET", "PATCH cancelling"}, e.calls)
		assert.Contains(t, e.out.String(), "Cancel run 812 (ado-gh 20261001.1)?")
	})

	t.Run("decline", func(t *testing.T) {
		e := newPLCancelEnv(t, true)
		e.stubGet("inProgress")
		e.in.WriteString("n\n")

		require.ErrorIs(t, e.run(t, "812"), cmdutil.CancelError)
		e.reg.Verify(t)
		assert.Equal(t, []string{"GET"}, e.calls)
	})
}

func TestPLCancelPromptsForActiveRuns(t *testing.T) {
	e := newPLCancelEnv(t, true)
	// In-progress and queued runs both count, merged newest queued first: the queued 813 comes
	// before the in-progress 812, so the second option is 812.
	byStatus := map[string]map[string]any{
		"inProgress": {"id": 812, "status": "inProgress", "buildNumber": "20261001.1", "queueTime": "2026-10-01T10:00:00Z",
			"sourceBranch": "refs/heads/main", "definition": map[string]any{"id": 1, "name": "ado-gh"}},
		"notStarted": {"id": 813, "status": "notStarted", "buildNumber": "20261001.2", "queueTime": "2026-10-01T10:05:00Z",
			"sourceBranch": "refs/heads/main", "definition": map[string]any{"id": 1, "name": "ado-gh"}},
	}
	for range 2 {
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/build/builds"), func(r *http.Request) (*http.Response, error) {
			e.runQueries = append(e.runQueries, r.URL.Query())
			run := byStatus[r.URL.Query().Get("statusFilter")]
			return httpmock.JSONResponse(map[string]any{"count": 1, "value": []any{run}})(r)
		})
	}
	e.stubGet("inProgress")
	e.stubPatch(t, "cancelling")
	e.in.WriteString("2\ny\n")

	require.NoError(t, e.run(t))
	e.reg.Verify(t)
	require.Len(t, e.runQueries, 2)
	assert.Equal(t, "inProgress", e.runQueries[0].Get("statusFilter"))
	assert.Equal(t, "notStarted", e.runQueries[1].Get("statusFilter"))
	assert.Equal(t, []string{"GET", "PATCH cancelling"}, e.calls)
}

func TestPLCancelErrors(t *testing.T) {
	t.Run("invalid ref", func(t *testing.T) {
		e := newPLCancelEnv(t, false)
		require.ErrorContains(t, e.run(t, "abc", "-y"), "invalid run")
	})

	t.Run("GET fails", func(t *testing.T) {
		e := newPLCancelEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/build/builds/812"),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "build 812 not found"}))
		require.ErrorContains(t, e.run(t, "812", "-y"), "not found")
	})

	t.Run("PATCH fails", func(t *testing.T) {
		e := newPLCancelEnv(t, false)
		e.stubGet("inProgress")
		e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/My%20Proj/_apis/build/builds/812"),
			httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "access denied"}))
		require.ErrorContains(t, e.run(t, "812", "-y"), "access denied")
	})
}
