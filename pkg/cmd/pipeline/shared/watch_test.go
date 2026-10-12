package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Watch.Run calls signal.NotifyContext, whose first call starts the runtime's signal goroutines;
// started inside a synctest bubble they deadlock it, so start them here, outside any bubble.
func init() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Stop(c)
}

// watchTestEnv serves run 812's timeline fixtures, one per poll, then its build once.
type watchTestEnv struct {
	reg   *httpmock.Registry
	w     *Watch
	org   string
	polls []time.Time
	// summary is the build and timeline the summary callback got.
	summary   *build.Build
	summaryTL Timeline
}

func newWatchTestEnv(t *testing.T, org string) *watchTestEnv {
	t.Helper()
	c, reg := newRefTestClient(t, org)
	e := &watchTestEnv{reg: reg, org: org}
	e.w = BuildWatch(c, 812, 3*time.Second, func(b *build.Build, tl Timeline) {
		e.summary, e.summaryTL = b, tl
	})
	return e
}

func (e *watchTestEnv) stubTimelines(t *testing.T, fixtures ...string) {
	for _, f := range fixtures {
		body := pipelinefixtures.Read(t, f)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/build/builds/812/timeline"), func(r *http.Request) (*http.Response, error) {
			e.polls = append(e.polls, time.Now())
			return httpmock.JSONResponse(json.RawMessage(body))(r)
		})
	}
}

func (e *watchTestEnv) stubBuild(status, result string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/build/builds/812"),
		httpmock.JSONResponse(map[string]any{"id": 812, "status": status, "result": result}))
}

// requests lists the requests' paths without the org and project, location lookups left out.
func (e *watchTestEnv) requests() []string {
	var out []string
	for _, r := range e.reg.Requests {
		if p, ok := strings.CutPrefix(r.URL.Path, "/"+e.org+"/proj/_apis/build/"); ok {
			out = append(out, p)
		}
	}
	return out
}

func TestWatchNonTTYTransitions(t *testing.T) {
	e := newWatchTestEnv(t, "watchnonttyorg")
	ios, _, out, _ := iostreams.Test()
	var err error
	synctest.Test(t, func(t *testing.T) {
		e.stubTimelines(t, "timeline_in_progress.json", "timeline_in_progress.json", "timeline_checkpoint.json", "timeline_yaml_stages.json")
		e.stubBuild("completed", "failed")
		e.w.Summary = wrapSummary(e.w.Summary, func() { fmt.Fprintln(ios.Out, "SUMMARY") })
		err = e.w.Run(context.Background(), ios)
	})
	assert.ErrorIs(t, err, cmdutil.SilentError)
	assert.Equal(t, []string{"builds/812/timeline", "builds/812/timeline", "builds/812/timeline", "builds/812/timeline", "builds/812"}, e.requests(),
		"only the timeline is polled per tick, the build is read once at the end")
	require.Len(t, e.polls, 4)
	for i := 1; i < len(e.polls); i++ {
		assert.Equal(t, 3*time.Second, e.polls[i].Sub(e.polls[i-1]))
	}
	// The second poll changed nothing; tasks are left out; new pending checks get a line.
	assert.Equal(t, `● Build: inProgress
● Build / Build: inProgress
✓ Build: succeeded
✓ Build / Build: succeeded
⏸ Deploy: pending
⏸ Deploy / Deploy web: pending
● Deploy: approval pending
● Deploy: waiting: Business Hours
X Test: failed
X Test / Unit tests: failed
- Deploy: skipped
- Deploy / Deploy web: skipped

SUMMARY
`, out.String())
	assert.Equal(t, 812, *e.summary.Id)
	assert.Len(t, e.summaryTL.Nodes, 3, "the summary gets the last timeline")
}

func TestWatchTTYRedraws(t *testing.T) {
	e := newWatchTestEnv(t, "watchttyorg")
	ios, _, out, _ := iostreams.Test()
	ios.SetStdoutTTY(true)
	e.w.Header = "ado-gh 20261011.3"
	var err error
	synctest.Test(t, func(t *testing.T) {
		e.stubTimelines(t, "timeline_in_progress.json", "timeline_in_progress.json", "timeline_checkpoint.json", "timeline_yaml_stages.json")
		e.stubBuild("completed", "succeeded")
		e.w.Summary = wrapSummary(e.w.Summary, func() { fmt.Fprintln(ios.Out, "SUMMARY") })
		err = e.w.Run(context.Background(), ios)
	})
	require.NoError(t, err)
	frames := strings.Split(out.String(), "\x1b[0;0H\x1b[J")
	require.Len(t, frames, 5, "a redraw per poll, then a clear before the summary")
	assert.Empty(t, frames[0])
	title := "Refreshing status every 3 seconds. Press Ctrl+C to quit.\n\nado-gh 20261011.3\n\n"
	assert.Equal(t, title+"⠋ Build\n  ⠋ Build\n", frames[1], "running nodes spin")
	assert.Equal(t, title+"⠙ Build\n  ⠙ Build\n", frames[2], "the spinner moves a frame per redraw")
	assert.Equal(t, title+`✓ Build in 20s
  ✓ Build in 19s
⏸ Deploy
  ⏸ Deploy web

● Deploy: approval pending
● Deploy: waiting: Business Hours
`, frames[3])
	assert.Equal(t, "SUMMARY\n", frames[4])
}

func TestWatchCompletedRunSummaryAtOnce(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprintf("tty=%v", tty), func(t *testing.T) {
			e := newWatchTestEnv(t, fmt.Sprintf("watchdoneorg%v", tty))
			ios, _, out, _ := iostreams.Test()
			ios.SetStdoutTTY(tty)
			var err error
			synctest.Test(t, func(t *testing.T) {
				e.stubTimelines(t, "timeline_yaml_stages.json")
				e.stubBuild("completed", "canceled")
				e.w.Summary = wrapSummary(e.w.Summary, func() { fmt.Fprintln(ios.Out, "SUMMARY") })
				start := time.Now()
				err = e.w.Run(context.Background(), ios)
				assert.Zero(t, time.Since(start), "no tick waited")
			})
			assert.ErrorIs(t, err, cmdutil.CancelError)
			assert.Equal(t, "SUMMARY\n", out.String(), "no redraw, no transitions")
		})
	}
}

func TestWatchKeepsPollingUntilBuildCompletes(t *testing.T) {
	e := newWatchTestEnv(t, "watchlagorg")
	ios, _, _, _ := iostreams.Test()
	var err error
	synctest.Test(t, func(t *testing.T) {
		// The timeline completes before the build does, and a run that never started has none.
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/build/builds/812/timeline"), httpmock.JSONResponse(map[string]any{"records": nil}))
		e.stubBuild("notStarted", "")
		e.stubTimelines(t, "timeline_yaml_stages.json")
		e.stubBuild("inProgress", "")
		e.stubTimelines(t, "timeline_yaml_stages.json")
		e.stubBuild("completed", "partiallySucceeded")
		err = e.w.Run(context.Background(), ios)
	})
	assert.ErrorIs(t, err, cmdutil.SilentError)
	assert.Equal(t, []string{"builds/812/timeline", "builds/812", "builds/812/timeline", "builds/812", "builds/812/timeline", "builds/812"}, e.requests())
}

func TestWatchExitCodePerResult(t *testing.T) {
	for i, tt := range []struct {
		result string
		want   error
	}{
		{"succeeded", nil},
		{"partiallySucceeded", cmdutil.SilentError},
		{"failed", cmdutil.SilentError},
		{"canceled", cmdutil.CancelError},
	} {
		t.Run(tt.result, func(t *testing.T) {
			e := newWatchTestEnv(t, fmt.Sprintf("watchexitorg%d", i))
			ios, _, _, _ := iostreams.Test()
			e.stubTimelines(t, "timeline_yaml_stages.json")
			e.stubBuild("completed", tt.result)
			err := e.w.Run(context.Background(), ios)
			if tt.want == nil {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, tt.want)
			}
			assert.Equal(t, tt.result, string(*e.summary.Result))
		})
	}
}

func TestWatchInterrupted(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprintf("tty=%v", tty), func(t *testing.T) {
			e := newWatchTestEnv(t, fmt.Sprintf("watchintorg%v", tty))
			ios, _, _, errOut := iostreams.Test()
			ios.SetStdoutTTY(tty)
			var err error
			synctest.Test(t, func(t *testing.T) {
				e.stubTimelines(t, "timeline_in_progress.json")
				ctx, cancel := context.WithCancel(context.Background())
				go func() {
					time.Sleep(time.Second)
					cancel()
				}()
				err = e.w.Run(ctx, ios)
			})
			assert.ErrorIs(t, err, cmdutil.CancelError)
			assert.Equal(t, "Stopped watching; nothing was canceled.\n", errOut.String())
			assert.Nil(t, e.summary, "no summary, no build read")
		})
	}
}

func TestWatchPollError(t *testing.T) {
	t.Run("server error", func(t *testing.T) {
		e := newWatchTestEnv(t, "watcherrorg")
		ios, _, _, _ := iostreams.Test()
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/build/builds/812/timeline"),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "Build 812 not found."}))
		err := e.w.Run(context.Background(), ios)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Build 812 not found.")
	})
	t.Run("build read error", func(t *testing.T) {
		e := newWatchTestEnv(t, "watchbuilderrorg")
		ios, _, _, _ := iostreams.Test()
		e.stubTimelines(t, "timeline_yaml_stages.json")
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/proj/_apis/build/builds/812"),
			httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
		err := e.w.Run(context.Background(), ios)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "boom")
	})
	t.Run("canceled request", func(t *testing.T) {
		ios, _, _, errOut := iostreams.Test()
		ctx, cancel := context.WithCancel(context.Background())
		w := &Watch{Interval: time.Second, Poll: func(context.Context) (Timeline, error) {
			cancel()
			return Timeline{}, errors.New("context canceled")
		}}
		assert.ErrorIs(t, w.Run(ctx, ios), cmdutil.CancelError)
		assert.Contains(t, errOut.String(), "Stopped watching")
	})
}

// wrapSummary calls before, then summary.
func wrapSummary(summary func(Timeline) error, before func()) func(Timeline) error {
	return func(tl Timeline) error {
		before()
		return summary(tl)
	}
}
