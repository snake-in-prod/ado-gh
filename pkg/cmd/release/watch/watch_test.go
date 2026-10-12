package watch

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"testing"
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
// synctest bubble, or the bubble deadlocks (see pipeline/shared/watch_test.go).
func init() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Stop(c)
}

type relWatchEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	out, errOut *bytes.Buffer
	// polls are the times release 3 was read.
	polls []time.Time
}

// newRelWatchEnv returns an env for its own org (the SDK caches discovery per base URL).
func newRelWatchEnv(t *testing.T, tty bool) *relWatchEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_REPO", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	org := "relwatch-" + strings.NewReplacer("/", "-", " ", "-", "_", "-", "=", "-", ",", "-").Replace(strings.ToLower(t.Name()))
	e := &relWatchEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut}
	e.reg.StubLocations(org, httpmock.ResourceAreasLocation)
	e.reg.StubVSRM(org)
	return e
}

func (e *relWatchEnv) run(t *testing.T, args ...string) error {
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
	}
	cmd := NewCmdWatch(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

// stubReleases answers the GETs of release 3 with bodies, one per poll.
func (e *relWatchEnv) stubReleases(t *testing.T, bodies ...[]byte) {
	for _, body := range bodies {
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/release/releases/3"), func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "tasks", r.URL.Query().Get("$expand"))
			e.polls = append(e.polls, time.Now())
			return httpmock.JSONResponse(json.RawMessage(body))(r)
		})
	}
}

// relWatchEnvState sets an environment's status and its latest deploy step's status and
// operationStatus; no step removes the environment's deploy steps (never deployed).
type relWatchEnvState struct {
	status, step, op string
}

// relWatchBody returns release_failed_task.json (Dev succeeded, QA inProgress, Prod rejected
// after a failed deployment: latest deploy step <failed, PhaseFailed>) with the environments
// named in states changed.
func relWatchBody(t *testing.T, states map[string]relWatchEnvState) []byte {
	t.Helper()
	var r map[string]any
	require.NoError(t, json.Unmarshal(pipelinefixtures.Read(t, "release_failed_task.json"), &r))
	for _, env := range r["environments"].([]any) {
		env := env.(map[string]any)
		s, ok := states[env["name"].(string)]
		if !ok {
			continue
		}
		env["status"] = s.status
		steps := env["deploySteps"].([]any)
		if s.step == "" {
			env["deploySteps"] = []any{}
			continue
		}
		var latest map[string]any
		for _, st := range steps {
			st := st.(map[string]any)
			if latest == nil || st["attempt"].(float64) > latest["attempt"].(float64) {
				latest = st
			}
		}
		latest["status"], latest["operationStatus"] = s.step, s.op
	}
	b, err := json.Marshal(r)
	require.NoError(t, err)
	return b
}

var (
	relWatchQADone        = relWatchEnvState{"succeeded", "succeeded", "Approved"}
	relWatchProdSucceeded = relWatchEnvState{"succeeded", "succeeded", "PhaseSucceeded"}
)

func TestReleaseWatchCompletedExitCodes(t *testing.T) {
	for _, tt := range []struct {
		name   string
		states map[string]relWatchEnvState
		err    error
		result string
		reason string
	}{
		{"all succeeded", map[string]relWatchEnvState{"QA": relWatchQADone, "Prod": relWatchProdSucceeded}, nil, "succeeded", ""},
		{"rejected by a failed deployment", map[string]relWatchEnvState{"QA": relWatchQADone}, cmdutil.SilentError, "rejected",
			"X Prod rejected: the deployment failed (PhaseFailed)\n"},
		{"rejected by an approval", map[string]relWatchEnvState{"QA": relWatchQADone, "Prod": {"rejected", "notDeployed", "Rejected"}},
			cmdutil.SilentError, "rejected", "X Prod rejected: an approval was rejected\n"},
		{"partially succeeded", map[string]relWatchEnvState{"QA": relWatchQADone, "Prod": {"partiallySucceeded", "partiallySucceeded", "PhasePartiallySucceeded"}},
			cmdutil.SilentError, "partiallySucceeded", ""},
		{"canceled", map[string]relWatchEnvState{"QA": relWatchQADone, "Prod": {"canceled", "failed", "Canceled"}}, cmdutil.CancelError, "canceled", ""},
		{"failure wins over cancel", map[string]relWatchEnvState{"QA": {"canceled", "failed", "Canceled"}}, cmdutil.SilentError, "rejected",
			"X Prod rejected: the deployment failed (PhaseFailed)\n"},
		{"fallback to a failed deploy step", map[string]relWatchEnvState{"QA": relWatchQADone, "Prod": {"undefined", "failed", "PhaseFailed"}},
			cmdutil.SilentError, "failed", ""},
		{"fallback to a canceled deploy step", map[string]relWatchEnvState{"QA": relWatchQADone, "Prod": {"undefined", "failed", "Canceled"}},
			cmdutil.CancelError, "canceled", ""},
		{"fallback to a succeeded deploy step", map[string]relWatchEnvState{"QA": relWatchQADone, "Prod": {"notStarted", "succeeded", "PhaseSucceeded"}},
			nil, "succeeded", ""},
		{"never deployed does not count", map[string]relWatchEnvState{"QA": {"notStarted", "", ""}, "Prod": relWatchProdSucceeded}, nil, "succeeded", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newRelWatchEnv(t, false)
			var err error
			synctest.Test(t, func(t *testing.T) {
				e.stubReleases(t, relWatchBody(t, tt.states))
				err = e.run(t, "3")
			})
			e.reg.Verify(t)
			if tt.err == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.err)
			}
			out := e.out.String()
			_, first, _ := strings.Cut(strings.SplitN(out, "\n", 2)[0], " ")
			assert.Equal(t, "Release-3 ado-live-release · "+tt.result+" · ID 3", first, "a completed release gets only its summary")
			assert.Contains(t, out, "\n\nENVIRONMENTS\n✓ Dev in 15s\n")
			if tt.reason != "" {
				assert.Contains(t, out, "\n\n"+tt.reason)
			} else {
				assert.NotContains(t, out, " rejected: ")
			}
			assert.True(t, strings.HasSuffix(out, "\nView this release on Azure DevOps: https://dev.azure.com/"+e.org+"/proj/_release?releaseId=3&_a=release-summary\n"))
		})
	}
}

func TestReleaseWatchFailedDeploymentSummary(t *testing.T) {
	e := newRelWatchEnv(t, false)
	var err error
	synctest.Test(t, func(t *testing.T) {
		e.stubReleases(t, relWatchBody(t, nil))
		err = e.run(t, "3", "--environment", "prod")
	})
	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Equal(t, `X Release-3 ado-live-release · rejected · ID 3

ENVIRONMENTS
X Prod (attempt 2) in 30s
  X Agent job in 30s
    X Run script in 27s

X Prod rejected: the deployment failed (PhaseFailed)

ANNOTATIONS
X Deployment of Prod failed.
  Prod
X Bash exited with code '1'.
  Prod / Agent job / Run script

View this release on Azure DevOps: https://dev.azure.com/`+e.org+`/proj/_release?releaseId=3&_a=release-summary
`, e.out.String(), "only Prod is watched, so QA in progress doesn't hold the watch")
}

func TestReleaseWatchPollsUntilWatchedEnvironmentsComplete(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want time.Duration
	}{
		{"default interval, QA by ID", []string{"3", "--environment", "7"}, 3 * time.Second},
		{"-i, URL", []string{"https://vsrm.dev.azure.com/ORG/proj/_apis/release/releases/3", "-i", "5"}, 5 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newRelWatchEnv(t, false)
			args := tt.args
			args[0] = strings.Replace(args[0], "ORG", e.org, 1)
			var err error
			synctest.Test(t, func(t *testing.T) {
				e.stubReleases(t,
					relWatchBody(t, map[string]relWatchEnvState{"Prod": relWatchProdSucceeded}),
					relWatchBody(t, map[string]relWatchEnvState{"Prod": relWatchProdSucceeded}),
					relWatchBody(t, map[string]relWatchEnvState{"QA": relWatchQADone, "Prod": relWatchProdSucceeded}))
				err = e.run(t, args...)
			})
			require.NoError(t, err)
			e.reg.Verify(t)
			require.Len(t, e.polls, 3)
			for i := 1; i < len(e.polls); i++ {
				assert.Equal(t, tt.want, e.polls[i].Sub(e.polls[i-1]))
			}
			out := e.out.String()
			assert.Contains(t, out, "● QA: inProgress\n")
			assert.Contains(t, out, "✓ QA: succeeded\n")
			assert.Equal(t, 1, strings.Count(out, "● QA: inProgress"), "non-TTY prints transitions only")
		})
	}
}

func TestReleaseWatchWaitsForAFirstDeployment(t *testing.T) {
	notStarted := relWatchEnvState{"notStarted", "", ""}
	idleBody := func(t *testing.T) []byte {
		return relWatchBody(t, map[string]relWatchEnvState{"QA": notStarted, "Prod": notStarted})
	}

	t.Run("a deployment starting within the grace polls is watched", func(t *testing.T) {
		e := newRelWatchEnv(t, false)
		var err error
		synctest.Test(t, func(t *testing.T) {
			e.stubReleases(t, idleBody(t), idleBody(t),
				relWatchBody(t, map[string]relWatchEnvState{"Prod": notStarted}),
				relWatchBody(t, map[string]relWatchEnvState{"QA": relWatchQADone, "Prod": notStarted}))
			err = e.run(t, "3", "--environment", "QA")
		})
		require.NoError(t, err)
		e.reg.Verify(t)
		assert.Len(t, e.polls, 4)
		assert.Contains(t, e.out.String(), "✓ QA: succeeded\n")
		assert.NotContains(t, e.errOut.String(), "no deployment started")
	})

	t.Run("gives up when nothing starts", func(t *testing.T) {
		e := newRelWatchEnv(t, false)
		var err error
		synctest.Test(t, func(t *testing.T) {
			e.stubReleases(t, idleBody(t), idleBody(t), idleBody(t))
			err = e.run(t, "3", "--environment", "QA")
		})
		require.ErrorIs(t, err, cmdutil.SilentError)
		e.reg.Verify(t)
		assert.Len(t, e.polls, 3)
		assert.Equal(t, "no deployment started; deploy one with: ado release deploy 3 --environment QA\n", e.errOut.String())
		assert.NotContains(t, e.out.String(), "View this release", "no summary for a release never deployed")
	})

	t.Run("hint names a placeholder without --environment", func(t *testing.T) {
		e := newRelWatchEnv(t, false)
		var err error
		allIdle := map[string]relWatchEnvState{"Dev": notStarted, "QA": notStarted, "Prod": notStarted}
		synctest.Test(t, func(t *testing.T) {
			e.stubReleases(t, relWatchBody(t, allIdle), relWatchBody(t, allIdle), relWatchBody(t, allIdle))
			err = e.run(t, "3")
		})
		require.ErrorIs(t, err, cmdutil.SilentError)
		assert.Equal(t, "no deployment started; deploy one with: ado release deploy 3 --environment <name>\n", e.errOut.String())
	})
}

// relWatchPendingBody is relWatchBody with QA waiting for a manual pre-deployment approval
// (and an automated one that must not show).
func relWatchPendingBody(t *testing.T, states map[string]relWatchEnvState) []byte {
	t.Helper()
	var r map[string]any
	require.NoError(t, json.Unmarshal(relWatchBody(t, states), &r))
	for _, env := range r["environments"].([]any) {
		env := env.(map[string]any)
		if env["name"] == "QA" {
			env["preDeployApprovals"] = []any{
				map[string]any{"id": 21, "approvalType": "preDeploy", "status": "pending", "isAutomated": false},
				map[string]any{"id": 22, "approvalType": "preDeploy", "status": "pending", "isAutomated": true},
			}
		}
	}
	b, err := json.Marshal(r)
	require.NoError(t, err)
	return b
}

func TestReleaseWatchPendingApproval(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-TTY", true: "TTY"}[tty], func(t *testing.T) {
			e := newRelWatchEnv(t, tty)
			var err error
			synctest.Test(t, func(t *testing.T) {
				e.stubReleases(t,
					relWatchPendingBody(t, map[string]relWatchEnvState{"Prod": relWatchProdSucceeded}),
					relWatchPendingBody(t, map[string]relWatchEnvState{"Prod": relWatchProdSucceeded}),
					relWatchBody(t, map[string]relWatchEnvState{"QA": relWatchQADone, "Prod": relWatchProdSucceeded}))
				err = e.run(t, "3")
			})
			require.NoError(t, err)
			e.reg.Verify(t)
			out := e.out.String()
			// The automated approval is left out: one line per redraw on a TTY (two polls
			// before completion), and one line in all when piped.
			want := 1
			if tty {
				want = 2
			}
			assert.Equal(t, want, strings.Count(out, "⏸ QA: approval pending\n"))
		})
	}
}

func TestReleaseWatchTTYHeader(t *testing.T) {
	e := newRelWatchEnv(t, true)
	var err error
	synctest.Test(t, func(t *testing.T) {
		e.stubReleases(t,
			relWatchBody(t, map[string]relWatchEnvState{"Prod": relWatchProdSucceeded}),
			relWatchBody(t, map[string]relWatchEnvState{"QA": relWatchQADone, "Prod": relWatchProdSucceeded}))
		err = e.run(t, "3", "-i", "2")
	})
	require.NoError(t, err)
	assert.Contains(t, e.out.String(), "Refreshing status every 2 seconds. Press Ctrl+C to quit.\n\nRelease 3\n\n")
}

func TestReleaseWatchErrors(t *testing.T) {
	t.Run("invalid ref", func(t *testing.T) {
		e := newRelWatchEnv(t, false)
		require.ErrorContains(t, e.run(t, "abc"), "invalid release")
	})

	t.Run("interval 0", func(t *testing.T) {
		e := newRelWatchEnv(t, false)
		err := e.run(t, "3", "-i", "0")
		var fe *cmdutil.FlagError
		require.ErrorAs(t, err, &fe)
		assert.Contains(t, err.Error(), "--interval must be at least 1")
	})

	t.Run("unknown environment", func(t *testing.T) {
		e := newRelWatchEnv(t, false)
		var err error
		synctest.Test(t, func(t *testing.T) {
			e.stubReleases(t, relWatchBody(t, nil))
			err = e.run(t, "3", "--environment", "Staging")
		})
		require.EqualError(t, err, `no environment "Staging" in release 3; its environments: Dev, QA, Prod`)
	})

	t.Run("release fails", func(t *testing.T) {
		e := newRelWatchEnv(t, false)
		var err error
		synctest.Test(t, func(t *testing.T) {
			e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/release/releases/3"),
				httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "release 3 not found"}))
			err = e.run(t, "3")
		})
		require.ErrorContains(t, err, "not found")
	})
}
