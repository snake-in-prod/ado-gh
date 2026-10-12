package runcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/browsertest"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const plRunCommit = "0123456789abcdef0123456789abcdef01234567"

type plRunEnv struct {
	org     string
	reg     *httpmock.Registry
	ios     *iostreams.IOStreams
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	browser *browsertest.Browser
	// body is the JSON body of the last run or queue request.
	body map[string]any
}

// newPLRunEnv returns an env for its own org (the SDK caches discovery per base URL) with
// location discovery stubbed.
func newPLRunEnv(t *testing.T, tty bool) *plRunEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_REPO", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "plrun-" + strings.NewReplacer("/", "-", " ", "-", "_", "-", `"`, "").Replace(strings.ToLower(t.Name()))
	e := &plRunEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut, browser: &browsertest.Browser{}}
	e.reg.StubLocations(org, slices.Concat(httpmock.PipelineLocations, httpmock.GitRepoLocations)...)
	t.Cleanup(func() { e.reg.Verify(t) })
	return e
}

func (e *plRunEnv) run(t *testing.T, args ...string) error {
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
		Prompter:  &cmdutil.Prompter{IO: e.ios, Accessible: true},
		Browser:   e.browser,
		GitClient: &git.Client{Run: func(context.Context, string, ...string) ([]byte, error) { return []byte("main\n"), nil }},
	}
	cmd := NewCmdRun(f)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

func (e *plRunEnv) path(p string) string { return e.org + "/proj/_apis/" + p }

func (e *plRunEnv) url(id string) string {
	return "https://dev.azure.com/" + e.org + "/proj/_build/results?buildId=" + id
}

// stubDefinition answers GET definition 1 with the YAML fixture after edit.
func (e *plRunEnv) stubDefinition(t *testing.T, edit func(map[string]any)) {
	var def map[string]any
	require.NoError(t, json.Unmarshal(pipelinefixtures.Read(t, "definition_yaml.json"), &def))
	if edit != nil {
		edit(def)
	}
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/definitions/1")), httpmock.JSONResponse(def))
}

func plRunClassic(def map[string]any) { def["process"] = map[string]any{"type": 1} }

// capture records the request's JSON body, then answers with resp.
func (e *plRunEnv) capture(resp httpmock.Responder) httpmock.Responder {
	return func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		e.body = nil
		if err := json.Unmarshal(b, &e.body); err != nil {
			return nil, err
		}
		return resp(r)
	}
}

// stubRunPipeline answers the Pipelines API run of pipeline 1 with run 900 (as the live
// server did, with a REST url that must not be printed).
func (e *plRunEnv) stubRunPipeline() {
	e.reg.Register(httpmock.REST(http.MethodPost, e.path("pipelines/1/runs")), e.capture(httpmock.JSONResponse(map[string]any{
		"id": 900, "name": "20261012.1", "state": "inProgress",
		"url": "https://dev.azure.com/" + e.org + "/proj/_apis/pipelines/1/runs/900",
	})))
}

// stubQueueBuild answers the Build API queue with build 901.
func (e *plRunEnv) stubQueueBuild() {
	e.reg.Register(httpmock.REST(http.MethodPost, e.path("build/builds")), e.capture(httpmock.JSONResponse(map[string]any{
		"id": 901, "buildNumber": "20261012.2", "status": "notStarted",
	})))
}

func TestRunYAML(t *testing.T) {
	e := newPLRunEnv(t, false)
	e.stubDefinition(t, nil)
	e.stubRunPipeline()
	require.NoError(t, e.run(t, "1", "-b", "feature/x", "--commit", plRunCommit, "-p", "env=prod", "-p", "region=eu=west", "-v", "foo=bar"))

	assert.Equal(t, map[string]any{
		"resources":          map[string]any{"repositories": map[string]any{"self": map[string]any{"refName": "refs/heads/feature/x", "version": plRunCommit}}},
		"templateParameters": map[string]any{"env": "prod", "region": "eu=west"},
		"variables":          map[string]any{"foo": map[string]any{"value": "bar"}},
	}, e.body)
	assert.Equal(t, e.url("900")+"\n", e.out.String())
	assert.Empty(t, e.errOut.String())
}

func TestRunDefaultBranch(t *testing.T) {
	t.Run("from the definition", func(t *testing.T) {
		e := newPLRunEnv(t, false)
		e.stubDefinition(t, nil)
		e.stubRunPipeline()
		require.NoError(t, e.run(t, "1"))
		assert.Equal(t, map[string]any{"resources": map[string]any{"repositories": map[string]any{
			"self": map[string]any{"refName": "refs/heads/main"}}}}, e.body)
	})
	t.Run("from the Git repository", func(t *testing.T) {
		e := newPLRunEnv(t, false)
		e.stubDefinition(t, func(def map[string]any) {
			delete(def["repository"].(map[string]any), "defaultBranch")
			plRunClassic(def)
		})
		e.reg.Register(httpmock.REST(http.MethodGet, e.path("git/repositories/00000000-0000-0000-0000-000000000011")),
			httpmock.JSONResponse(map[string]any{"id": "00000000-0000-0000-0000-000000000011", "name": "ado-gh", "defaultBranch": "refs/heads/trunk"}))
		e.stubQueueBuild()
		require.NoError(t, e.run(t, "1"))
		assert.Equal(t, map[string]any{"definition": map[string]any{"id": float64(1)}, "sourceBranch": "refs/heads/trunk"}, e.body)
	})
	t.Run("Git repository lookup fails", func(t *testing.T) {
		e := newPLRunEnv(t, false)
		e.stubDefinition(t, func(def map[string]any) { delete(def["repository"].(map[string]any), "defaultBranch") })
		e.reg.Register(httpmock.REST(http.MethodGet, e.path("git/repositories/00000000-0000-0000-0000-000000000011")),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "repository gone"}))
		err := e.run(t, "1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "repository gone")
	})
	t.Run("none known: left to the server", func(t *testing.T) {
		e := newPLRunEnv(t, false)
		e.stubDefinition(t, func(def map[string]any) { def["repository"] = map[string]any{"type": "GitHub", "id": "o/r"} })
		e.stubRunPipeline()
		require.NoError(t, e.run(t, "1", "-b", "refs/tags/v1"))
		assert.Equal(t, "refs/tags/v1", e.body["resources"].(map[string]any)["repositories"].(map[string]any)["self"].(map[string]any)["refName"])
	})
	t.Run("GitHub repository without -b", func(t *testing.T) {
		e := newPLRunEnv(t, false)
		e.stubDefinition(t, func(def map[string]any) { def["repository"] = map[string]any{"type": "GitHub", "id": "o/r"} })
		e.stubRunPipeline()
		require.NoError(t, e.run(t, "1"))
		assert.Equal(t, map[string]any{}, e.body)
	})
}

func TestRunClassic(t *testing.T) {
	e := newPLRunEnv(t, false)
	e.stubDefinition(t, plRunClassic)
	e.stubQueueBuild()
	require.NoError(t, e.run(t, "1", "-b", "feature/x", "--commit", plRunCommit, "-v", "foo=bar", "-v", "empty="))

	params, _ := e.body["parameters"].(string)
	var vars map[string]string
	require.NoError(t, json.Unmarshal([]byte(params), &vars))
	assert.Equal(t, map[string]string{"foo": "bar", "empty": ""}, vars)
	delete(e.body, "parameters")
	assert.Equal(t, map[string]any{
		"definition": map[string]any{"id": float64(1)}, "sourceBranch": "refs/heads/feature/x", "sourceVersion": plRunCommit,
	}, e.body)
	assert.Equal(t, e.url("901")+"\n", e.out.String())
}

func TestRunClassicRejectsParameters(t *testing.T) {
	e := newPLRunEnv(t, false)
	e.stubDefinition(t, plRunClassic)
	err := e.run(t, "1", "-p", "env=prod")
	var flagErr *cmdutil.FlagError
	require.ErrorAs(t, err, &flagErr)
	assert.EqualError(t, err, `--parameter is supported only for YAML pipelines; pipeline "ado-gh" is classic, use --variable`)
}

func TestRunQueueStatus(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		e := newPLRunEnv(t, false)
		e.stubDefinition(t, func(def map[string]any) { def["queueStatus"] = "disabled" })
		assert.EqualError(t, e.run(t, "1"), `pipeline "ado-gh" is disabled; run 'ado pipeline enable 1'`)
		assert.Empty(t, e.out.String())
	})
	t.Run("paused", func(t *testing.T) {
		e := newPLRunEnv(t, false)
		e.stubDefinition(t, func(def map[string]any) { def["queueStatus"] = "paused" })
		e.stubRunPipeline()
		require.NoError(t, e.run(t, "1"))
		assert.Equal(t, "! pipeline \"ado-gh\" is paused: the run is queued but won't start until it is enabled ('ado pipeline enable 1')\n", e.errOut.String())
		assert.Equal(t, e.url("900")+"\n", e.out.String())
	})
}

func TestRunServerErrors(t *testing.T) {
	t.Run("variable not settable", func(t *testing.T) {
		e := newPLRunEnv(t, false)
		e.stubDefinition(t, nil)
		e.reg.Register(httpmock.REST(http.MethodPost, e.path("pipelines/1/runs")),
			httpmock.StatusJSONResponse(http.StatusBadRequest, json.RawMessage(pipelinefixtures.Read(t, "error_pipeline_validation.json"))))
		err := e.run(t, "1", "-v", "BuildConfiguration=debug")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "You can't set the following variables (BuildConfiguration).")
	})
	t.Run("validation results", func(t *testing.T) {
		e := newPLRunEnv(t, false)
		e.stubDefinition(t, plRunClassic)
		e.reg.Register(httpmock.REST(http.MethodPost, e.path("build/builds")),
			httpmock.StatusJSONResponse(http.StatusBadRequest, json.RawMessage(pipelinefixtures.Read(t, "error_queue_validation.json"))))
		err := e.run(t, "1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Could not queue the build because there were validation errors or warnings")
		assert.Contains(t, err.Error(), "A value for the 'environment' parameter must be provided.")
		assert.Contains(t, err.Error(), "service connection prod which could not be found.")
	})
	t.Run("definition not found", func(t *testing.T) {
		e := newPLRunEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/definitions/1")),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "no definition 1"}))
		err := e.run(t, "1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no definition 1")
	})
}

func TestRunTTY(t *testing.T) {
	e := newPLRunEnv(t, true)
	e.stubDefinition(t, nil)
	e.stubRunPipeline()
	require.NoError(t, e.run(t, "1", "-b", "dev"))
	assert.Equal(t, e.url("900")+"\n", e.out.String())
	assert.Equal(t, "✓ Queued run 900 of ado-gh on dev\n\nTo see the run's progress, try: ado pipeline watch 900\n", e.errOut.String())
}

func TestRunWeb(t *testing.T) {
	e := newPLRunEnv(t, true)
	e.stubDefinition(t, nil)
	e.stubRunPipeline()
	require.NoError(t, e.run(t, "1", "-w"))
	assert.Equal(t, []string{e.url("900")}, e.browser.URLs)
	assert.Empty(t, e.out.String())
	assert.Contains(t, e.errOut.String(), "Opening "+e.url("900")+" in your browser.\n")
}

func TestRunWatch(t *testing.T) {
	tests := []struct {
		result  string
		wantErr error
	}{
		{"succeeded", nil},
		{"partiallySucceeded", cmdutil.SilentError},
		{"failed", cmdutil.SilentError},
		{"canceled", cmdutil.CancelError},
	}
	for _, tt := range tests {
		t.Run(tt.result, func(t *testing.T) {
			e := newPLRunEnv(t, false)
			e.stubDefinition(t, nil)
			e.stubRunPipeline()
			e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/builds/900/timeline")),
				httpmock.JSONResponse(json.RawMessage(pipelinefixtures.Read(t, "timeline_classic.json"))))
			e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/builds/900")), httpmock.JSONResponse(map[string]any{
				"id": 900, "buildNumber": "20261012.1", "status": "completed", "result": tt.result,
			}))
			err := e.run(t, "1", "--watch")
			assert.Equal(t, tt.wantErr, err)
			assert.True(t, strings.HasPrefix(e.out.String(), e.url("900")+"\n"), e.out.String())
			assert.Contains(t, e.out.String(), "Run 20261012.1 (ID 900) completed: "+tt.result+"\n")
		})
	}
}

func TestRunWatchTTYSummary(t *testing.T) {
	e := newPLRunEnv(t, true)
	e.stubDefinition(t, nil)
	e.stubRunPipeline()
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/builds/900/timeline")),
		httpmock.JSONResponse(json.RawMessage(pipelinefixtures.Read(t, "timeline_classic.json"))))
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/builds/900")), httpmock.JSONResponse(map[string]any{
		"id": 900, "buildNumber": "20261012.1", "status": "completed", "result": "succeeded",
	}))
	require.NoError(t, e.run(t, "1", "--watch"))
	assert.Contains(t, e.out.String(), "Agent job 1")
	assert.Contains(t, e.out.String(), "✓ Run 20261012.1 (ID 900) completed: succeeded\n")
	assert.NotContains(t, e.errOut.String(), "ado pipeline watch")
}

func TestRunDefaultPipeline(t *testing.T) {
	e := newPLRunEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("git/repositories/ado-gh")),
		httpmock.JSONResponse(map[string]any{"id": "00000000-0000-0000-0000-000000000011", "name": "ado-gh"}))
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/definitions")),
		httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{{"id": 1, "name": "ado-gh", "path": `\`}}}))
	e.stubDefinition(t, nil)
	e.stubRunPipeline()
	require.NoError(t, e.run(t))
	assert.Equal(t, e.url("900")+"\n", e.out.String())
}

func TestRunByName(t *testing.T) {
	e := newPLRunEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/definitions")),
		httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{{"id": 1, "name": "ado-gh", "path": `\`}}}))
	e.stubDefinition(t, nil)
	e.stubRunPipeline()
	require.NoError(t, e.run(t, "ADO-GH"))
	assert.Equal(t, e.url("900")+"\n", e.out.String())
}

func TestRunFlagErrors(t *testing.T) {
	tests := []struct {
		args    []string
		wantErr string
	}{
		{[]string{"1", "-p", "env"}, `invalid --parameter "env": expected key=value`},
		{[]string{"1", "-v", "=x"}, `invalid --variable "=x": expected key=value`},
		{[]string{"nope://"}, "invalid pipeline"},
		{[]string{"1", "--watch", "--web"}, "if any flags in the group [watch web] are set none of the others can be"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			e := newPLRunEnv(t, false)
			e.reg = &httpmock.Registry{} // nothing is requested, not even locations
			err := e.run(t, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, e.reg.Requests)
		})
	}
}
