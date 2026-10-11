package checks

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

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
	checksCmdProjectID = "6ce954b1-ce1f-45d1-b94d-e6bf2464ba2c"
	checksCmdPRURL     = "https://dev.azure.com/%s/My%20Proj/_git/web/pullrequest/12"
	checksCmdBuildType = "0609b952-1397-4640-95ec-e00a01b2c241"
	checksCmdReviewTyp = "fa4e907d-c16b-4a4c-9dfa-4906e5d171dd"
	checksCmdWorkItem  = "40e92b44-2fe1-4dd6-b3d8-74a9c21d0c6e"
	checksCmdComments  = "c6a1889d-b943-4856-b76f-9e46bb6b0df2"
)

type checksCmdBrowser struct{ urls []string }

func (b *checksCmdBrowser) Browse(u string) error {
	b.urls = append(b.urls, u)
	return nil
}

type checksCmdEnv struct {
	org     string
	reg     *httpmock.Registry
	ios     *iostreams.IOStreams
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	browser *checksCmdBrowser
	// polls are the (fake) times of every policy evaluations request.
	polls []time.Time
}

// newChecksCmdEnv returns an env for its own org (the SDK caches locations per base URL for
// the whole process) with the OPTIONS location stub registered.
func newChecksCmdEnv(t *testing.T, tty bool) *checksCmdEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	ios.SetTerminalWidth(200)
	org := "prchecks-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.GitPRLocations...)
	return &checksCmdEnv{org: org, reg: reg, ios: ios, out: out, errOut: errOut, browser: &checksCmdBrowser{}}
}

func (e *checksCmdEnv) run(t *testing.T, args ...string) error {
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
		Browser: e.browser,
	}
	cmd := NewCmdChecks(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true // the root command does this in production
	cmd.SetArgs(args)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

func (e *checksCmdEnv) prURL() string { return strings.ReplaceAll(checksCmdPRURL, "%s", e.org) }

func (e *checksCmdEnv) stubChecksPR() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/12"), httpmock.JSONResponse(map[string]any{
		"pullRequestId": 12, "title": "Add feature", "status": "active",
		"repository": map[string]any{"name": "web", "project": map[string]any{"id": checksCmdProjectID}},
	}))
}

// stubChecksRound stubs one fetch of the policy evaluations and PR statuses.
func (e *checksCmdEnv) stubChecksRound(evals []map[string]any, statuses ...map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/"+checksCmdProjectID+"/_apis/policy/evaluations"),
		func(req *http.Request) (*http.Response, error) {
			e.polls = append(e.polls, time.Now())
			return httpmock.JSONResponse(map[string]any{"count": len(evals), "value": evals})(req)
		})
	if statuses == nil {
		statuses = []map[string]any{}
	}
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web/pullRequests/12/statuses"),
		httpmock.JSONResponse(map[string]any{"count": len(statuses), "value": statuses}))
}

func checksCmdEval(typeID, typeName, status string, blocking bool, settings map[string]any) map[string]any {
	return map[string]any{"status": status, "configuration": map[string]any{
		"isBlocking": blocking, "type": map[string]any{"id": typeID, "displayName": typeName}, "settings": settings,
	}}
}

// checksCmdBuild is a build policy evaluation named name; a completed one ran for 1m5s.
func checksCmdBuild(name, status string, blocking bool) map[string]any {
	e := checksCmdEval(checksCmdBuildType, "Build", status, blocking, map[string]any{"displayName": name})
	e["startedDate"] = "2026-10-01T10:00:00Z"
	if status != "running" && status != "queued" {
		e["completedDate"] = "2026-10-01T10:01:05Z"
	}
	return e
}

func checksCmdStatus(id int, name, state string) map[string]any {
	return map[string]any{
		"id": id, "state": state, "context": map[string]any{"genre": "ci", "name": name},
		"description": name + " " + state, "targetUrl": "https://ci/" + name,
	}
}

func checksCmdExit(err error) cmdutil.ExitCode { return cmdutil.HandleError(io.Discard, err, "") }

func TestChecksOutput(t *testing.T) {
	evals := []map[string]any{
		checksCmdBuild("CI", "approved", true),
		checksCmdBuild("E2E", "running", false),
		checksCmdBuild("Lint", "rejected", true),
		checksCmdEval(checksCmdReviewTyp, "Minimum number of reviewers", "approved", true, nil),
	}
	statuses := []map[string]any{checksCmdStatus(1, "scan", "notApplicable")}

	t.Run("tty", func(t *testing.T) {
		e := newChecksCmdEnv(t, true)
		e.stubChecksPR()
		e.stubChecksRound(evals, statuses...)
		err := e.run(t, "12")
		e.reg.Verify(t)
		assert.Equal(t, cmdutil.ExitError, checksCmdExit(err))
		want := "Some checks were not successful\n" +
			"1 failing, 1 successful, 1 skipped, and 1 pending checks\n\n" +
			"   NAME                         REQUIRED  STATE          ELAPSED  LINK\n" +
			"X  Lint                         required  rejected       1m5s     " + e.prURL() + "\n" +
			"*  E2E                          optional  running                 " + e.prURL() + "\n" +
			"✓  CI                           required  approved       1m5s     " + e.prURL() + "\n" +
			"✓  Minimum number of reviewers  required  approved                " + e.prURL() + "\n" +
			"-  ci/scan                      optional  notApplicable           https://ci/scan\n"
		assert.Equal(t, want, e.out.String())
		assert.Empty(t, e.errOut.String())
	})

	t.Run("non-tty TSV", func(t *testing.T) {
		e := newChecksCmdEnv(t, false)
		e.stubChecksPR()
		e.stubChecksRound(evals, statuses...)
		err := e.run(t, "12")
		e.reg.Verify(t)
		assert.Equal(t, cmdutil.ExitError, checksCmdExit(err))
		want := "Lint\tfail\trequired\trejected\t1m5s\t" + e.prURL() + "\n" +
			"E2E\tpending\toptional\trunning\t0\t" + e.prURL() + "\n" +
			"CI\tpass\trequired\tapproved\t1m5s\t" + e.prURL() + "\n" +
			"Minimum number of reviewers\tpass\trequired\tapproved\t0\t" + e.prURL() + "\n" +
			"ci/scan\tskipping\toptional\tnotApplicable\t0\thttps://ci/scan\n"
		assert.Equal(t, want, e.out.String())
	})
}

func TestChecksExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		evals    []map[string]any
		statuses []map[string]any
		want     cmdutil.ExitCode
		summary  string
		rows     []string
		absent   []string
	}{
		{name: "all pass", evals: []map[string]any{checksCmdBuild("CI", "approved", true)},
			statuses: []map[string]any{checksCmdStatus(1, "scan", "succeeded")},
			want:     cmdutil.ExitOK, summary: "All checks were successful\n0 failing, 2 successful, 0 skipped, and 0 pending checks"},
		{name: "fail", evals: []map[string]any{checksCmdBuild("CI", "approved", true)},
			statuses: []map[string]any{checksCmdStatus(1, "scan", "failed")},
			want:     cmdutil.ExitError, summary: "Some checks were not successful"},
		{name: "pending", evals: []map[string]any{checksCmdBuild("CI", "queued", true), checksCmdBuild("Lint", "approved", true)},
			want: cmdutil.ExitPending, summary: "Some checks are still pending\n0 failing, 1 successful, 0 skipped, and 1 pending checks"},
		{name: "expired build is pending", evals: []map[string]any{checksCmdBuild("CI", "expired", true)},
			want: cmdutil.ExitPending, rows: []string{"*  CI"}},
		{name: "reviewer work item and comment policies are listed only",
			evals: []map[string]any{
				checksCmdBuild("CI", "approved", true),
				checksCmdEval(checksCmdReviewTyp, "Minimum number of reviewers", "rejected", true, nil),
				checksCmdEval(checksCmdWorkItem, "Work item linking", "queued", true, nil),
				checksCmdEval(checksCmdComments, "Comment requirements", "rejected", true, nil),
			},
			want: cmdutil.ExitOK, summary: "All checks were successful\n0 failing, 1 successful",
			rows: []string{"X  Minimum number of reviewers", "*  Work item linking", "X  Comment requirements"}},
		{name: "required filter", args: []string{"--required"},
			evals:    []map[string]any{checksCmdBuild("CI", "approved", true), checksCmdBuild("Docs", "rejected", false)},
			statuses: []map[string]any{checksCmdStatus(1, "scan", "failed")},
			want:     cmdutil.ExitOK, rows: []string{"✓  CI"}, absent: []string{"Docs", "scan"}},
		{name: "without required filter", evals: []map[string]any{checksCmdBuild("CI", "approved", true), checksCmdBuild("Docs", "rejected", false)},
			want: cmdutil.ExitError, rows: []string{"✓  CI", "X  Docs"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newChecksCmdEnv(t, true)
			e.stubChecksPR()
			e.stubChecksRound(tt.evals, tt.statuses...)
			err := e.run(t, append([]string{"12"}, tt.args...)...)
			e.reg.Verify(t)
			assert.Equal(t, tt.want, checksCmdExit(err))
			assert.True(t, strings.HasPrefix(e.out.String(), tt.summary), e.out.String())
			for _, r := range tt.rows {
				assert.Contains(t, e.out.String(), r)
			}
			for _, r := range tt.absent {
				assert.NotContains(t, e.out.String(), r)
			}
		})
	}
}

func TestChecksNoChecks(t *testing.T) {
	tests := []struct {
		name, msg string
		args      []string
		evals     []map[string]any
	}{
		{name: "none", msg: "no checks reported\n"},
		{name: "none required", args: []string{"--required"}, evals: []map[string]any{checksCmdBuild("Docs", "rejected", false)},
			msg: "no required checks reported\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newChecksCmdEnv(t, true)
			e.stubChecksPR()
			e.stubChecksRound(tt.evals)
			err := e.run(t, append([]string{"12"}, tt.args...)...)
			e.reg.Verify(t)
			require.NoError(t, err)
			assert.Empty(t, e.out.String())
			assert.Equal(t, tt.msg, e.errOut.String())
		})
	}
}

func TestChecksJSON(t *testing.T) {
	tests := []struct {
		name  string
		evals []map[string]any
		want  cmdutil.ExitCode
		json  string
	}{
		{name: "fail exits 1", evals: []map[string]any{checksCmdBuild("CI", "rejected", true)}, want: cmdutil.ExitError,
			json: `[{"name":"CI","bucket":"fail","isRequired":true}]`},
		{name: "pending exits 8", evals: []map[string]any{checksCmdBuild("CI", "running", false)}, want: cmdutil.ExitPending,
			json: `[{"name":"CI","bucket":"pending","isRequired":false}]`},
		{name: "pass exits 0", evals: []map[string]any{checksCmdBuild("CI", "approved", true)}, want: cmdutil.ExitOK,
			json: `[{"name":"CI","bucket":"pass","isRequired":true}]`},
		{name: "no checks", want: cmdutil.ExitOK, json: `[]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newChecksCmdEnv(t, false)
			e.stubChecksPR()
			e.stubChecksRound(tt.evals)
			err := e.run(t, "12", "--json", "name,bucket,isRequired")
			e.reg.Verify(t)
			assert.Equal(t, tt.want, checksCmdExit(err))
			assert.JSONEq(t, tt.json, e.out.String())
			assert.Empty(t, e.errOut.String())
		})
	}

	t.Run("all fields", func(t *testing.T) {
		e := newChecksCmdEnv(t, false)
		e.stubChecksPR()
		e.stubChecksRound([]map[string]any{checksCmdBuild("CI", "approved", true)})
		require.NoError(t, e.run(t, "12", "--json", "name,state,bucket,type,isRequired,description,link,startedAt,completedAt"))
		var got []map[string]any
		require.NoError(t, json.Unmarshal(e.out.Bytes(), &got))
		assert.Equal(t, []map[string]any{{
			"name": "CI", "state": "approved", "bucket": "pass", "type": "build", "isRequired": true, "description": "",
			"link": e.prURL(), "startedAt": "2026-10-01T10:00:00Z", "completedAt": "2026-10-01T10:01:05Z",
		}}, got)
	})
}

func TestChecksWeb(t *testing.T) {
	for _, tty := range []bool{true, false} {
		t.Run(map[bool]string{true: "tty", false: "non-tty"}[tty], func(t *testing.T) {
			e := newChecksCmdEnv(t, tty)
			e.stubChecksPR()
			require.NoError(t, e.run(t, "12", "--web"))
			e.reg.Verify(t)
			assert.Equal(t, []string{e.prURL()}, e.browser.urls)
			if tty {
				assert.Equal(t, "Opening "+e.prURL()+" in your browser.\n", e.errOut.String())
			} else {
				assert.Empty(t, e.errOut.String())
			}
		})
	}
}

func TestChecksFlagErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"--fail-fast"}, "cannot use `--fail-fast` flag without `--watch` flag"},
		{[]string{"--interval", "5"}, "cannot use `--interval` flag without `--watch` flag"},
		{[]string{"--watch", "--interval", "0"}, "invalid value for --interval: 0"},
		{[]string{"--watch", "--json", "name"}, "cannot use `--watch` with `--json` flag"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			e := newChecksCmdEnv(t, false)
			err := e.run(t, append([]string{"12"}, tt.args...)...)
			require.EqualError(t, err, tt.want)
			var flagErr *cmdutil.FlagError
			assert.ErrorAs(t, err, &flagErr)
			assert.Empty(t, e.reg.Requests)
		})
	}
}

// checksCmdPending and checksCmdPass are rounds of the --watch fixtures.
var (
	checksCmdPending = []map[string]any{checksCmdBuild("CI", "running", true)}
	checksCmdPass    = []map[string]any{checksCmdBuild("CI", "approved", true)}
)

func TestChecksWatch(t *testing.T) {
	t.Run("tty redraws every interval until nothing is pending", func(t *testing.T) {
		e := newChecksCmdEnv(t, true)
		synctest.Test(t, func(t *testing.T) {
			e.stubChecksPR()
			e.stubChecksRound(checksCmdPending)
			e.stubChecksRound(checksCmdPending)
			e.stubChecksRound(checksCmdPass)
			require.NoError(t, e.run(t, "12", "--watch", "-i", "5"))
		})
		e.reg.Verify(t)
		require.Len(t, e.polls, 3)
		assert.Equal(t, 5*time.Second, e.polls[1].Sub(e.polls[0]))
		assert.Equal(t, 5*time.Second, e.polls[2].Sub(e.polls[1]))
		out := e.out.String()
		assert.Equal(t, 3, strings.Count(out, "\x1b[0;0H\x1b[J"), out)
		assert.Equal(t, 2, strings.Count(out, "Refreshing checks status every 5 seconds. Press Ctrl+C to quit.\n\n"), out)
		final := out[strings.LastIndex(out, "\x1b[J")+len("\x1b[J"):]
		assert.True(t, strings.HasPrefix(final, "All checks were successful\n"), final)
	})

	t.Run("non-tty prints only the final state with the default interval", func(t *testing.T) {
		e := newChecksCmdEnv(t, false)
		synctest.Test(t, func(t *testing.T) {
			e.stubChecksPR()
			e.stubChecksRound(checksCmdPending)
			e.stubChecksRound(checksCmdPass)
			require.NoError(t, e.run(t, "12", "--watch"))
		})
		e.reg.Verify(t)
		require.Len(t, e.polls, 2)
		assert.Equal(t, 10*time.Second, e.polls[1].Sub(e.polls[0]))
		assert.Equal(t, "CI\tpass\trequired\tapproved\t1m5s\t"+e.prURL()+"\n", e.out.String())
	})

	t.Run("keeps polling past failures without fail-fast", func(t *testing.T) {
		e := newChecksCmdEnv(t, false)
		failing := checksCmdBuild("Lint", "rejected", true)
		synctest.Test(t, func(t *testing.T) {
			e.stubChecksPR()
			e.stubChecksRound(append(checksCmdPending, failing))
			e.stubChecksRound(append(checksCmdPass, failing))
			err := e.run(t, "12", "--watch")
			assert.Equal(t, cmdutil.ExitError, checksCmdExit(err))
		})
		e.reg.Verify(t)
		assert.Len(t, e.polls, 2)
	})

	t.Run("fail-fast stops on the first failure", func(t *testing.T) {
		e := newChecksCmdEnv(t, true)
		synctest.Test(t, func(t *testing.T) {
			e.stubChecksPR()
			e.stubChecksRound(checksCmdPending)
			e.stubChecksRound(append(checksCmdPending, checksCmdBuild("Lint", "rejected", true)))
			err := e.run(t, "12", "--watch", "--fail-fast")
			assert.Equal(t, cmdutil.ExitError, checksCmdExit(err))
		})
		e.reg.Verify(t)
		assert.Len(t, e.polls, 2)
		out := e.out.String()
		final := out[strings.LastIndex(out, "\x1b[J")+len("\x1b[J"):]
		assert.True(t, strings.HasPrefix(final, "Some checks were not successful\n"), final)
	})

	t.Run("stops with a requeue warning when only expired checks remain", func(t *testing.T) {
		e := newChecksCmdEnv(t, false)
		synctest.Test(t, func(t *testing.T) {
			e.stubChecksPR()
			e.stubChecksRound(checksCmdPending)
			e.stubChecksRound([]map[string]any{checksCmdBuild("CI", "expired", true), checksCmdBuild("Lint", "approved", true)})
			err := e.run(t, "12", "--watch")
			assert.Equal(t, cmdutil.ExitPending, checksCmdExit(err))
		})
		e.reg.Verify(t)
		assert.Len(t, e.polls, 2)
		assert.Equal(t, "! Only expired checks are pending; requeue them on the pull request: "+e.prURL()+"\n", e.errOut.String())
		assert.Equal(t, "CI\tpending\trequired\texpired\t1m5s\t"+e.prURL()+"\n"+
			"Lint\tpass\trequired\tapproved\t1m5s\t"+e.prURL()+"\n", e.out.String())
	})

	t.Run("expired alongside running keeps polling", func(t *testing.T) {
		e := newChecksCmdEnv(t, false)
		synctest.Test(t, func(t *testing.T) {
			e.stubChecksPR()
			e.stubChecksRound([]map[string]any{checksCmdBuild("Old", "expired", true), checksCmdBuild("CI", "running", true)})
			e.stubChecksRound(checksCmdPass)
			require.NoError(t, e.run(t, "12", "--watch"))
		})
		e.reg.Verify(t)
		assert.Len(t, e.polls, 2)
		assert.Empty(t, e.errOut.String())
	})

	t.Run("fetch error while watching", func(t *testing.T) {
		e := newChecksCmdEnv(t, false)
		synctest.Test(t, func(t *testing.T) {
			e.stubChecksPR()
			e.stubChecksRound(checksCmdPending)
			e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/"+checksCmdProjectID+"/_apis/policy/evaluations"),
				httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
			assert.EqualError(t, e.run(t, "12", "--watch"), "boom (HTTP 500)")
		})
		e.reg.Verify(t)
		assert.Empty(t, e.out.String())
	})
}
