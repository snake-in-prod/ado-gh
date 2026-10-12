package watch

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// The watch loop calls signal.NotifyContext; start the runtime's signal goroutines outside any
// synctest bubble, or the bubble deadlocks (see shared/watch_test.go).
func init() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Stop(c)
}

type plWatchEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	in          *bytes.Buffer
	out, errOut *bytes.Buffer
	// polls are the times run 812's timeline was read.
	polls []time.Time
	// runQueries are the query strings of the build list requests.
	runQueries []url.Values
}

// newPLWatchEnv returns an env for its own org (the SDK caches discovery per base URL).
func newPLWatchEnv(t *testing.T, tty bool) *plWatchEnv {
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
	org := "plwatch-" + strings.NewReplacer("/", "-", " ", "-", "_", "-", "=", "-").Replace(strings.ToLower(t.Name()))
	e := &plWatchEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut}
	e.reg.StubLocations(org, httpmock.PipelineLocations...)
	return e
}

func (e *plWatchEnv) run(t *testing.T, args ...string) error {
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
	cmd := NewCmdWatch(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

// stubTimelines answers run 812's timeline with fixtures, one per poll.
func (e *plWatchEnv) stubTimelines(t *testing.T, fixtures ...string) {
	for _, name := range fixtures {
		body := pipelinefixtures.Read(t, name)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/build/builds/812/timeline"), func(r *http.Request) (*http.Response, error) {
			e.polls = append(e.polls, time.Now())
			return httpmock.JSONResponse(json.RawMessage(body))(r)
		})
	}
}

// stubBuild answers the GET of run 812 as completed with result.
func (e *plWatchEnv) stubBuild(result string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/build/builds/812"),
		httpmock.JSONResponse(map[string]any{"id": 812, "status": "completed", "result": result,
			"buildNumber": "20261011.3", "definition": map[string]any{"id": 1, "name": "ado-gh"}}))
}

func TestPLWatchCompletedRun(t *testing.T) {
	for _, tt := range []struct {
		result string
		err    error
	}{
		{"succeeded", nil},
		{"failed", cmdutil.SilentError},
		{"partiallySucceeded", cmdutil.SilentError},
		{"canceled", cmdutil.CancelError},
	} {
		t.Run(tt.result, func(t *testing.T) {
			e := newPLWatchEnv(t, false)
			var err error
			synctest.Test(t, func(t *testing.T) {
				e.stubTimelines(t, "timeline_yaml_stages.json")
				e.stubBuild(tt.result)
				err = e.run(t, "812")
			})
			e.reg.Verify(t)
			if tt.err == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.err)
			}
			out := e.out.String()
			_, firstLine, _ := strings.Cut(strings.SplitN(out, "\n", 2)[0], " ")
			assert.Equal(t, "ado-gh 20261011.3 · "+tt.result+" · ID 812", firstLine, "a completed run gets only its summary")
			assert.Contains(t, out, "\n\nSTAGES\n")
			assert.Contains(t, out, "Unit tests")
			assert.Contains(t, out, "https://dev.azure.com/"+e.org+"/My%20Proj/_build/results?buildId=812")
		})
	}
}

func TestPLWatchInterval(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want time.Duration
	}{
		{"default", []string{"812"}, 3 * time.Second},
		{"-i", []string{"812", "-i", "5"}, 5 * time.Second},
		{"--interval", []string{"https://dev.azure.com/ORG/My%20Proj/_build/results?buildId=812", "--interval", "10"}, 10 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newPLWatchEnv(t, false)
			args := tt.args
			args[0] = strings.Replace(args[0], "ORG", e.org, 1)
			var err error
			synctest.Test(t, func(t *testing.T) {
				e.stubTimelines(t, "timeline_in_progress.json", "timeline_in_progress.json", "timeline_yaml_stages.json")
				e.stubBuild("succeeded")
				err = e.run(t, args...)
			})
			require.NoError(t, err)
			e.reg.Verify(t)
			require.Len(t, e.polls, 3)
			for i := 1; i < len(e.polls); i++ {
				assert.Equal(t, tt.want, e.polls[i].Sub(e.polls[i-1]))
			}
			assert.True(t, strings.HasPrefix(e.out.String(), "● Build: inProgress\n"), "non-TTY prints transitions")
		})
	}
}

func TestPLWatchTTYHeader(t *testing.T) {
	e := newPLWatchEnv(t, true)
	var err error
	synctest.Test(t, func(t *testing.T) {
		e.stubTimelines(t, "timeline_in_progress.json", "timeline_yaml_stages.json")
		e.stubBuild("succeeded")
		err = e.run(t, "812", "-i", "2")
	})
	require.NoError(t, err)
	assert.Contains(t, e.out.String(), "Refreshing status every 2 seconds. Press Ctrl+C to quit.\n\nRun 812\n\n")
}

func TestPLWatchExitStatusIsNoOp(t *testing.T) {
	for _, result := range []string{"succeeded", "failed"} {
		t.Run(result, func(t *testing.T) {
			without, with := newPLWatchEnv(t, false), newPLWatchEnv(t, false)
			with.org = without.org + "-exit"
			with.reg.StubLocations(with.org, httpmock.PipelineLocations...)
			var errWithout, errWith error
			synctest.Test(t, func(t *testing.T) {
				without.stubTimelines(t, "timeline_yaml_stages.json")
				without.stubBuild(result)
				errWithout = without.run(t, "812")
				with.stubTimelines(t, "timeline_yaml_stages.json")
				with.stubBuild(result)
				errWith = with.run(t, "812", "--exit-status")
			})
			assert.Equal(t, errWithout, errWith)
			assert.Equal(t, strings.ReplaceAll(without.out.String(), without.org, "ORG"), strings.ReplaceAll(with.out.String(), with.org, "ORG"))
		})
	}
}

func TestPLWatchPromptsForInProgressRun(t *testing.T) {
	e := newPLWatchEnv(t, true)
	var err error
	synctest.Test(t, func(t *testing.T) {
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/build/builds"), func(r *http.Request) (*http.Response, error) {
			e.runQueries = append(e.runQueries, r.URL.Query())
			return httpmock.JSONResponse(map[string]any{"count": 1, "value": []any{map[string]any{
				"id": 812, "status": "inProgress", "buildNumber": "20261011.3",
				"sourceBranch": "refs/heads/main", "definition": map[string]any{"id": 1, "name": "ado-gh"},
			}}})(r)
		})
		e.stubTimelines(t, "timeline_yaml_stages.json")
		e.stubBuild("succeeded")
		e.in.WriteString("1\n")
		err = e.run(t)
	})
	require.NoError(t, err)
	e.reg.Verify(t)
	require.Len(t, e.runQueries, 1)
	assert.Equal(t, "inProgress", e.runQueries[0].Get("statusFilter"))
	assert.Contains(t, e.out.String(), "ado-gh 20261011.3 [main] ID 812")
}

func TestPLWatchErrors(t *testing.T) {
	t.Run("no run when not interactive", func(t *testing.T) {
		e := newPLWatchEnv(t, false)
		err := e.run(t)
		var fe *cmdutil.FlagError
		require.ErrorAs(t, err, &fe)
		assert.Contains(t, err.Error(), "<run> argument required")
	})

	t.Run("invalid ref", func(t *testing.T) {
		e := newPLWatchEnv(t, false)
		require.ErrorContains(t, e.run(t, "abc"), "invalid run")
	})

	for _, i := range []string{"0", "-1"} {
		t.Run("interval "+i, func(t *testing.T) {
			e := newPLWatchEnv(t, false)
			err := e.run(t, "812", "-i", i)
			var fe *cmdutil.FlagError
			require.ErrorAs(t, err, &fe)
			assert.Contains(t, err.Error(), "--interval must be at least 1")
		})
	}

	t.Run("timeline fails", func(t *testing.T) {
		e := newPLWatchEnv(t, false)
		var err error
		synctest.Test(t, func(t *testing.T) {
			e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/build/builds/812/timeline"),
				httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "build 812 not found"}))
			err = e.run(t, "812")
		})
		require.ErrorContains(t, err, "not found")
	})
}
