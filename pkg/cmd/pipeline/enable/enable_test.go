package enable

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

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

const plEnableDefPath = "/My%20Proj/_apis/build/definitions/12"

type plEnableEnv struct {
	org    string
	reg    *httpmock.Registry
	errOut *bytes.Buffer
	// calls records the definition requests in order, as "GET" or "PUT <queueStatus>".
	calls []string
	// bodies are the PUT bodies.
	bodies []map[string]any
}

// newPLEnableEnv returns an env for its own org (the SDK caches discovery per base URL).
func newPLEnableEnv(t *testing.T) *plEnableEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_REPO", "")
	keyring.MockInit()
	org := "plenable-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &plEnableEnv{org: org, reg: &httpmock.Registry{}}
	e.reg.StubLocations(org, httpmock.PipelineLocations...)
	return e
}

func (e *plEnableEnv) run(t *testing.T, newCmd func(*cmdutil.Factory) *cobra.Command, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	ios, _, _, errOut := iostreams.Test()
	e.errOut = errOut
	f := &cmdutil.Factory{
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "web"}, nil
		},
	}
	cmd := newCmd(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

// stubGet answers the next GET of definition 12 with the YAML fixture in queueStatus status.
func (e *plEnableEnv) stubGet(t *testing.T, status string) {
	var def map[string]any
	require.NoError(t, json.Unmarshal(pipelinefixtures.Read(t, "definition_yaml.json"), &def))
	def["queueStatus"] = status
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+plEnableDefPath), func(r *http.Request) (*http.Response, error) {
		e.calls = append(e.calls, "GET")
		assert.Contains(t, r.Header.Get("Accept"), "api-version=7.1-preview.7")
		return httpmock.JSONResponse(def)(r)
	})
}

// stubPut answers the next PUT of definition 12 with status (and the conflict fixture on 409).
func (e *plEnableEnv) stubPut(t *testing.T, status int) {
	e.reg.Register(httpmock.REST(http.MethodPut, e.org+plEnableDefPath), func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Contains(t, r.Header.Get("Accept"), "api-version=7.1-preview.7")
		e.bodies = append(e.bodies, body)
		e.calls = append(e.calls, "PUT "+body["queueStatus"].(string))
		if status == http.StatusConflict {
			return httpmock.StatusJSONResponse(status, json.RawMessage(pipelinefixtures.Read(t, "error_definition_conflict.json")))(r)
		}
		return httpmock.JSONResponse(map[string]any{"id": 12, "revision": 4})(r)
	})
}

func TestPLEnableChangesState(t *testing.T) {
	tests := []struct {
		name   string
		cmd    func(*cmdutil.Factory) *cobra.Command
		from   string
		to     string
		output string
	}{
		{"enable disabled", NewCmdEnable, "disabled", "enabled", "Pipeline 12 is now enabled (was disabled)"},
		{"enable paused", NewCmdEnable, "paused", "enabled", "Pipeline 12 is now enabled (was paused)"},
		{"disable enabled", NewCmdDisable, "enabled", "disabled", "Pipeline 12 is now disabled (was enabled)"},
		{"disable paused", NewCmdDisable, "paused", "disabled", "Pipeline 12 is now disabled (was paused)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newPLEnableEnv(t)
			e.stubGet(t, tt.from)
			e.stubPut(t, http.StatusOK)

			require.NoError(t, e.run(t, tt.cmd, "12"))
			e.reg.Verify(t)
			assert.Equal(t, []string{"GET", "PUT " + tt.to}, e.calls)
			assert.Contains(t, e.errOut.String(), tt.output)

			var want map[string]any
			require.NoError(t, json.Unmarshal(pipelinefixtures.Read(t, "definition_yaml.json"), &want))
			want["queueStatus"] = tt.to
			assert.Equal(t, want, e.bodies[0], "only queueStatus changes; revision and the rest go back as read")
		})
	}
}

func TestPLEnableAlreadyInState(t *testing.T) {
	tests := []struct {
		name string
		cmd  func(*cmdutil.Factory) *cobra.Command
		from string
	}{
		{"enable", NewCmdEnable, "enabled"},
		{"disable", NewCmdDisable, "disabled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newPLEnableEnv(t)
			e.stubGet(t, tt.from)

			require.NoError(t, e.run(t, tt.cmd, "12"))
			e.reg.Verify(t)
			assert.Equal(t, []string{"GET"}, e.calls, "no PUT")
			assert.Equal(t, "Pipeline 12 is already "+tt.from+"\n", e.errOut.String())
		})
	}
}

func TestPLEnableConflictRetriedOnce(t *testing.T) {
	e := newPLEnableEnv(t)
	e.stubGet(t, "enabled")
	e.stubPut(t, http.StatusConflict)
	e.stubGet(t, "enabled")
	e.stubPut(t, http.StatusOK)

	require.NoError(t, e.run(t, NewCmdDisable, "12"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"GET", "PUT disabled", "GET", "PUT disabled"}, e.calls)
}

func TestPLEnableConflictTwiceFails(t *testing.T) {
	e := newPLEnableEnv(t)
	e.stubGet(t, "disabled")
	e.stubPut(t, http.StatusConflict)
	e.stubGet(t, "disabled")
	e.stubPut(t, http.StatusConflict)

	err := e.run(t, NewCmdEnable, "12")
	require.Error(t, err)
	e.reg.Verify(t)
	assert.Equal(t, []string{"GET", "PUT enabled", "GET", "PUT enabled"}, e.calls)
	assert.Contains(t, err.Error(), "pipeline 12 was modified concurrently; run the command again")
	assert.Contains(t, err.Error(), "revision 3 is out of date")
}

func TestPLEnableByName(t *testing.T) {
	e := newPLEnableEnv(t)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/build/definitions"), httpmock.JSONResponse(map[string]any{
		"count": 1, "value": []any{map[string]any{"id": 12, "name": "ado-gh", "path": `\CI`}},
	}))
	e.stubGet(t, "enabled")
	e.stubPut(t, http.StatusOK)

	require.NoError(t, e.run(t, NewCmdDisable, "CI/ado-gh"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"GET", "PUT disabled"}, e.calls)
}

func TestPLEnableByURL(t *testing.T) {
	e := newPLEnableEnv(t)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/Other/_apis/build/definitions/12"), func(r *http.Request) (*http.Response, error) {
		e.calls = append(e.calls, "GET")
		return httpmock.StringResponse(`{"id":12,"revision":1,"queueStatus":"disabled"}`)(r)
	})

	require.NoError(t, e.run(t, NewCmdDisable, "https://dev.azure.com/"+e.org+"/Other/_build?definitionId=12"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"GET"}, e.calls, "the URL's project is used")
}

func TestPLEnableErrors(t *testing.T) {
	e := newPLEnableEnv(t)
	assert.Error(t, e.run(t, NewCmdEnable), "a pipeline is required")
	assert.Error(t, e.run(t, NewCmdEnable, "https://example.com/nope"), "invalid ref")

	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/build/definitions"), httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))
	assert.ErrorContains(t, e.run(t, NewCmdEnable, "nope"), "no pipeline with that name")

	e.reg.Register(httpmock.REST(http.MethodGet, e.org+plEnableDefPath),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "definition 12 not found"}))
	err := e.run(t, NewCmdDisable, "12")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "modified concurrently")
}
