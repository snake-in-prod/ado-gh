package rerun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
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

type plRerunEnv struct {
	org    string
	reg    *httpmock.Registry
	ios    *iostreams.IOStreams
	in     *bytes.Buffer
	out    *bytes.Buffer
	errOut *bytes.Buffer
}

// newPLRerunEnv returns an env for its own org (the SDK caches discovery per base URL) with
// location discovery stubbed.
func newPLRerunEnv(t *testing.T, tty bool) *plRerunEnv {
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
	org := "plrerun-" + strings.NewReplacer("/", "-", " ", "-", "_", "-", `"`, "").Replace(strings.ToLower(t.Name()))
	e := &plRerunEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut}
	t.Cleanup(func() { e.reg.Verify(t) })
	return e
}

func (e *plRerunEnv) stubLocations() {
	e.reg.StubLocations(e.org, slices.Concat(httpmock.PipelineLocations, httpmock.GitRepoLocations)...)
}

func (e *plRerunEnv) run(t *testing.T, args ...string) error {
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
		GitClient: &git.Client{Run: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("refs/heads/main\n"), nil
		}},
	}
	cmd := NewCmdRerun(f)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

func (e *plRerunEnv) path(p string) string { return e.org + "/proj/_apis/" + p }

// stubBuild answers GET run 812 with b.
func (e *plRerunEnv) stubBuild(b map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/builds/812")), httpmock.JSONResponse(b))
}

// stubDefinition answers GET definition 1 as a YAML (process type 2) or classic (1) pipeline.
func (e *plRerunEnv) stubDefinition(t *testing.T, yaml bool) {
	var def map[string]any
	require.NoError(t, json.Unmarshal(pipelinefixtures.Read(t, "definition_yaml.json"), &def))
	if !yaml {
		def["process"] = map[string]any{"type": 1}
	}
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/definitions/1")), httpmock.JSONResponse(def))
}

func (e *plRerunEnv) stubTimeline(body any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/builds/812/timeline")), httpmock.JSONResponse(body))
}

// plRerunCapture records the request's JSON body and query, then answers with resp.
func plRerunCapture(body *map[string]any, query *map[string][]string, resp httpmock.Responder) httpmock.Responder {
	return func(r *http.Request) (*http.Response, error) {
		if query != nil {
			*query = r.URL.Query()
		}
		if body != nil && r.Body != nil {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			if len(b) > 0 {
				if err := json.Unmarshal(b, body); err != nil {
					return nil, err
				}
			}
		}
		return resp(r)
	}
}

// plRerunBuild is run 812 of pipeline 1 with template parameters and queue-time variables.
func plRerunBuild(status, result string) map[string]any {
	b := map[string]any{
		"id": 812, "buildNumber": "20261012.1", "status": status,
		"sourceBranch": "refs/heads/feature/x", "sourceVersion": "0123456789abcdef0123456789abcdef01234567",
		"definition":         map[string]any{"id": 1, "name": "ado-live", "path": `\`},
		"templateParameters": map[string]string{"env": "prod"},
		"parameters":         `{"foo":"bar","system.debug":"true"}`,
	}
	if result != "" {
		b["result"] = result
	}
	return b
}

// plRerunStage is a Stage timeline record.
func plRerunStage(n int, identifier, result string, order, attempt int) map[string]any {
	return map[string]any{
		"id": fmt.Sprintf("00000000-0000-0000-0000-%012d", n), "parentId": nil, "type": "Stage",
		"name": identifier + " stage", "identifier": identifier, "state": "completed", "result": result,
		"order": order, "attempt": attempt,
	}
}

func TestRerunYAML(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprint("tty=", tty), func(t *testing.T) {
			e := newPLRerunEnv(t, tty)
			e.stubLocations()
			e.stubBuild(plRerunBuild("completed", "failed"))
			e.stubDefinition(t, true)
			var body map[string]any
			e.reg.Register(httpmock.REST(http.MethodPost, e.path("pipelines/1/runs")),
				plRerunCapture(&body, nil, httpmock.JSONResponse(map[string]any{"id": 900, "url": "https://ignored"})))
			require.NoError(t, e.run(t, "812"))
			assert.Equal(t, map[string]any{
				"templateParameters": map[string]any{"env": "prod"},
				"variables": map[string]any{
					"foo":          map[string]any{"value": "bar"},
					"system.debug": map[string]any{"value": "true"},
				},
				"resources": map[string]any{"repositories": map[string]any{"self": map[string]any{
					"refName": "refs/heads/feature/x", "version": "0123456789abcdef0123456789abcdef01234567",
				}}},
			}, body)
			assert.Equal(t, "https://dev.azure.com/"+e.org+"/proj/_build/results?buildId=900\n", e.out.String())
			if tty {
				assert.Equal(t, "✓ Queued run 900 of ado-live\n", e.errOut.String())
			} else {
				assert.Empty(t, e.errOut.String())
			}
		})
	}
}

func TestRerunYAMLNoParameters(t *testing.T) {
	e := newPLRerunEnv(t, false)
	e.stubLocations()
	b := plRerunBuild("inProgress", "")
	delete(b, "templateParameters")
	delete(b, "parameters")
	e.stubBuild(b)
	e.stubDefinition(t, true)
	var body map[string]any
	e.reg.Register(httpmock.REST(http.MethodPost, e.path("pipelines/1/runs")),
		plRerunCapture(&body, nil, httpmock.JSONResponse(map[string]any{"id": 900})))
	require.NoError(t, e.run(t, "812"))
	assert.NotContains(t, body, "templateParameters")
	assert.NotContains(t, body, "variables")
	assert.Contains(t, body, "resources")
}

func TestRerunClassic(t *testing.T) {
	e := newPLRerunEnv(t, false)
	e.stubLocations()
	e.stubBuild(plRerunBuild("completed", "succeeded"))
	e.stubDefinition(t, false)
	var body map[string]any
	e.reg.Register(httpmock.REST(http.MethodPost, e.path("build/builds")),
		plRerunCapture(&body, nil, httpmock.JSONResponse(map[string]any{"id": 901})))
	require.NoError(t, e.run(t, "https://dev.azure.com/"+e.org+"/proj/_build/results?buildId=812"))
	assert.Equal(t, map[string]any{
		"definition":    map[string]any{"id": float64(1)},
		"sourceBranch":  "refs/heads/feature/x",
		"sourceVersion": "0123456789abcdef0123456789abcdef01234567",
		"parameters":    `{"foo":"bar","system.debug":"true"}`,
	}, body)
	assert.Equal(t, "https://dev.azure.com/"+e.org+"/proj/_build/results?buildId=901\n", e.out.String())
}

func TestRerunInvalidParameters(t *testing.T) {
	e := newPLRerunEnv(t, false)
	e.stubLocations()
	b := plRerunBuild("completed", "failed")
	b["parameters"] = `not json`
	e.stubBuild(b)
	e.stubDefinition(t, true)
	err := e.run(t, "812")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read the queue-time variables of run 812")
}

func TestRerunFailedInProgress(t *testing.T) {
	e := newPLRerunEnv(t, false)
	e.stubLocations()
	e.stubBuild(plRerunBuild("inProgress", ""))
	assert.EqualError(t, e.run(t, "812", "--failed"), "run is still in progress")
}

func TestRerunFailedYAML(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprint("tty=", tty), func(t *testing.T) {
			e := newPLRerunEnv(t, tty)
			e.stubLocations()
			e.stubBuild(plRerunBuild("completed", "failed"))
			e.stubDefinition(t, true)
			e.stubTimeline(json.RawMessage(pipelinefixtures.Read(t, "live_timeline_failed.json")))
			var body map[string]any
			e.reg.Register(httpmock.REST(http.MethodPatch, e.path("build/builds/812/stages/Fail")),
				plRerunCapture(&body, nil, httpmock.StringResponse("")))
			require.NoError(t, e.run(t, "812", "--failed"))
			assert.Equal(t, map[string]any{"state": "retry", "forceRetryAllJobs": false}, body)
			assert.Equal(t, "https://dev.azure.com/"+e.org+"/proj/_build/results?buildId=812\n", e.out.String())
			if tty {
				assert.Equal(t, "✓ Retrying stage Fail of run 812\n", e.errOut.String())
			} else {
				assert.Empty(t, e.errOut.String())
			}
		})
	}
}

func TestRerunFailedStageSet(t *testing.T) {
	e := newPLRerunEnv(t, false)
	e.stubLocations()
	e.stubBuild(plRerunBuild("completed", "failed"))
	e.stubDefinition(t, true)
	// Test depends on Build, both failed (Test ran with condition: always()); Lint was
	// canceled; Pack failed on attempt 1 but succeeded on attempt 2; Docs was skipped.
	e.stubTimeline(map[string]any{"records": []map[string]any{
		plRerunStage(5, "Test", "failed", 2, 1),
		plRerunStage(1, "Build", "failed", 1, 1),
		plRerunStage(2, "Lint", "canceled", 3, 1),
		plRerunStage(3, "Pack", "failed", 4, 1),
		plRerunStage(4, "Pack", "succeeded", 4, 2),
		plRerunStage(6, "Docs", "skipped", 5, 1),
		{"id": "00000000-0000-0000-0000-000000000007", "parentId": "00000000-0000-0000-0000-000000000001",
			"type": "Phase", "identifier": "Build.compile", "result": "failed", "order": 1, "attempt": 1},
	}})
	var order []string
	for _, s := range []string{"Build", "Test", "Lint"} {
		e.reg.Register(httpmock.REST(http.MethodPatch, e.path("build/builds/812/stages/"+s)), func(r *http.Request) (*http.Response, error) {
			order = append(order, s)
			return httpmock.StringResponse("")(r)
		})
	}
	require.NoError(t, e.run(t, "812", "--failed"))
	assert.Equal(t, []string{"Build", "Test", "Lint"}, order, "timeline order, upstream first")
}

func TestRerunFailedImplicitStage(t *testing.T) {
	e := newPLRerunEnv(t, false)
	e.stubLocations()
	e.stubBuild(plRerunBuild("completed", "failed"))
	e.stubDefinition(t, true)
	e.stubTimeline(map[string]any{"records": []map[string]any{plRerunStage(1, "__default", "failed", 1, 1)}})
	e.reg.Register(httpmock.REST(http.MethodPatch, e.path("build/builds/812/stages/__default")), httpmock.StringResponse(""))
	require.NoError(t, e.run(t, "812", "--failed"))
}

func TestRerunFailedNoFailedStages(t *testing.T) {
	for name, body := range map[string]any{
		"succeeded": map[string]any{"records": []map[string]any{plRerunStage(1, "Build", "succeeded", 1, 1)}},
		"empty":     map[string]any{},
	} {
		t.Run(name, func(t *testing.T) {
			e := newPLRerunEnv(t, false)
			e.stubLocations()
			e.stubBuild(plRerunBuild("completed", "failed"))
			e.stubDefinition(t, true)
			e.stubTimeline(body)
			assert.EqualError(t, e.run(t, "812", "--failed"), "run 812 has no failed or canceled stages to retry")
		})
	}
}

func TestRerunFailedStageServerError(t *testing.T) {
	e := newPLRerunEnv(t, true)
	e.stubLocations()
	e.stubBuild(plRerunBuild("completed", "failed"))
	e.stubDefinition(t, true)
	e.stubTimeline(map[string]any{"records": []map[string]any{
		plRerunStage(1, "Build", "failed", 1, 1),
		plRerunStage(2, "Lint", "failed", 2, 1),
	}})
	e.reg.Register(httpmock.REST(http.MethodPatch, e.path("build/builds/812/stages/Build")),
		httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{
			"message": "Stage Build cannot be retried.", "typeKey": "InvalidOperationException",
		}))
	e.reg.Register(httpmock.REST(http.MethodPatch, e.path("build/builds/812/stages/Lint")), httpmock.StringResponse(""))
	err := e.run(t, "812", "--failed")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not retry stage Build: ")
	assert.Contains(t, err.Error(), "Stage Build cannot be retried.")
	assert.Equal(t, "✓ Retrying stage Lint of run 812\n", e.errOut.String(), "later stages are still retried")
	assert.Empty(t, e.out.String())
}

func TestRerunFailedClassic(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprint("tty=", tty), func(t *testing.T) {
			e := newPLRerunEnv(t, tty)
			e.stubLocations()
			e.stubBuild(plRerunBuild("completed", "failed"))
			e.stubDefinition(t, false)
			var query map[string][]string
			e.reg.Register(httpmock.REST(http.MethodPatch, e.path("build/builds/812")),
				plRerunCapture(nil, &query, httpmock.JSONResponse(plRerunBuild("notStarted", ""))))
			require.NoError(t, e.run(t, "812", "--failed"))
			assert.Equal(t, []string{"true"}, query["retry"])
			assert.Equal(t, "https://dev.azure.com/"+e.org+"/proj/_build/results?buildId=812\n", e.out.String())
			if tty {
				assert.Equal(t, "✓ Retrying the failed jobs of run 812\n", e.errOut.String())
			}
		})
	}
}

func TestRerunServerErrors(t *testing.T) {
	notFound := httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "boom from server"})
	tests := map[string]struct {
		args  []string
		stubs func(t *testing.T, e *plRerunEnv)
	}{
		"build": {[]string{"812"}, func(_ *testing.T, e *plRerunEnv) {
			e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/builds/812")), notFound)
		}},
		"definition": {[]string{"812"}, func(_ *testing.T, e *plRerunEnv) {
			e.stubBuild(plRerunBuild("completed", "failed"))
			e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/definitions/1")), notFound)
		}},
		"run pipeline": {[]string{"812"}, func(t *testing.T, e *plRerunEnv) {
			e.stubBuild(plRerunBuild("completed", "failed"))
			e.stubDefinition(t, true)
			e.reg.Register(httpmock.REST(http.MethodPost, e.path("pipelines/1/runs")), notFound)
		}},
		"queue build": {[]string{"812"}, func(t *testing.T, e *plRerunEnv) {
			e.stubBuild(plRerunBuild("completed", "failed"))
			e.stubDefinition(t, false)
			e.reg.Register(httpmock.REST(http.MethodPost, e.path("build/builds")), notFound)
		}},
		"timeline": {[]string{"812", "--failed"}, func(t *testing.T, e *plRerunEnv) {
			e.stubBuild(plRerunBuild("completed", "failed"))
			e.stubDefinition(t, true)
			e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/builds/812/timeline")), notFound)
		}},
		"retry build": {[]string{"812", "--failed"}, func(t *testing.T, e *plRerunEnv) {
			e.stubBuild(plRerunBuild("completed", "failed"))
			e.stubDefinition(t, false)
			e.reg.Register(httpmock.REST(http.MethodPatch, e.path("build/builds/812")), notFound)
		}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			e := newPLRerunEnv(t, false)
			e.stubLocations()
			tt.stubs(t, e)
			err := e.run(t, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "boom from server")
		})
	}
}

func TestRerunNoArgNonTTY(t *testing.T) {
	e := newPLRerunEnv(t, false)
	err := e.run(t)
	var flagErr *cmdutil.FlagError
	require.ErrorAs(t, err, &flagErr)
	assert.EqualError(t, err, "<run> argument required when not running interactively")
	assert.Empty(t, e.reg.Requests)
}

func TestRerunInvalidRun(t *testing.T) {
	e := newPLRerunEnv(t, false)
	require.Error(t, e.run(t, "abc"))
	assert.Empty(t, e.reg.Requests)
}

func TestRerunNoArgPromptsDefaultPipeline(t *testing.T) {
	e := newPLRerunEnv(t, true)
	e.stubLocations()
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/git/repositories/ado-gh"),
		httpmock.JSONResponse(map[string]any{"id": "00000000-0000-0000-0000-000000000011", "name": "ado-gh"}))
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/definitions")),
		httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{{"id": 1, "name": "ado-live", "path": `\`}}}))
	var query map[string][]string
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("build/builds")), plRerunCapture(nil, &query, httpmock.StringResponse(`{"count": 1, "value": [
 {"id": 812, "buildNumber": "20261012.1", "status": "inProgress", "sourceBranch": "refs/heads/main", "definition": {"id": 1, "name": "ado-live"}}
]}`)))
	e.in.WriteString("1\n")
	assert.EqualError(t, func() error {
		e.stubBuild(plRerunBuild("inProgress", ""))
		return e.run(t, "--failed")
	}(), "run is still in progress")
	assert.Equal(t, []string{"1"}, query["definitions"])
	assert.Equal(t, []string{"refs/heads/main"}, query["branchName"])
}
