package current

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/browsertest"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const spCurID = "c0000000-0000-0000-0000-000000000003"

// spCurDated is the current iteration: Mon 2026-10-12 to Fri 2026-10-23.
const spCurDated = `{"count":1,"value":[{"id":"` + spCurID + `","name":"Sprint 3","path":"My Proj\\R1\\Sprint 3",
 "attributes":{"startDate":"2026-10-12T00:00:00Z","finishDate":"2026-10-23T00:00:00Z","timeFrame":"current"}}]}`

// spCurUndated is a current iteration without dates, as a new project's Sprint 1.
const spCurUndated = `{"count":1,"value":[{"id":"` + spCurID + `","name":"Sprint 1","path":"My Proj\\Sprint 1",
 "attributes":{"startDate":null,"finishDate":null,"timeFrame":"current"}}]}`

// spCurEnvs counts the envs created.
var spCurEnvs int

type spCurEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	out, errOut *bytes.Buffer
	browser     *browsertest.Browser
}

// newSpCurEnv returns an env answering the current iteration of Web Team with iterations.
func newSpCurEnv(t *testing.T, tty bool, iterations string) *spCurEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	// The SDK caches discovery per base URL: every env gets its own org.
	spCurEnvs++
	org := "spcur-" + strconv.Itoa(spCurEnvs) + "-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	e := &spCurEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut, browser: &browsertest.Browser{}}
	t.Cleanup(func() { e.reg.Verify(t) })
	e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, org+"/My%20Proj/Web%20Team/_apis/work/teamsettings/iterations"),
		func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "current", r.URL.Query().Get("$timeframe"))
			return httpmock.StringResponse(iterations)(r)
		})
	return e
}

// stubDays answers the team settings (Monday to Friday) and the days off of the iteration.
func (e *spCurEnv) stubDays(daysOff string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/Web%20Team/_apis/work/teamsettings"),
		httpmock.StringResponse(`{"bugsBehavior":"off","workingDays":["monday","tuesday","wednesday","thursday","friday"]}`))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/Web%20Team/_apis/work/teamsettings/iterations/"+spCurID+"/teamdaysoff"),
		httpmock.StringResponse(`{"daysOff":`+daysOff+`}`))
}

func (e *spCurEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		Browser:    e.browser,
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "web"}, nil
		},
	}
	cmd := NewCmdCurrent(f)
	cmd.SetArgs(append(args, "--team", "Web Team"))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

// setNow fakes the clock at t.
func setNow(t *testing.T, at time.Time) {
	old := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = old })
}

func day(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC) }

func TestWorkingDaysLeft(t *testing.T) {
	it := api.Iteration{StartDate: day(10, 12), FinishDate: day(10, 23)}
	weekdays := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
	off := []api.DateRange{{Start: day(10, 15), End: day(10, 16)}}
	tests := []struct {
		name string
		now  time.Time
		off  []api.DateRange
		want int
	}{
		{"before start counts the whole sprint", day(10, 8), nil, 10},
		{"before start minus days off", day(10, 8), off, 8},
		{"midweek counts today", day(10, 14).Add(15 * time.Hour), off, 6},
		{"weekend", day(10, 17), off, 5},
		{"last day", day(10, 23).Add(23 * time.Hour), off, 1},
		{"after finish", day(10, 24), off, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, workingDaysLeft(tt.now, it, weekdays, tt.off))
		})
	}
}

func TestSprintCurrent_TTY(t *testing.T) {
	setNow(t, day(10, 14).Add(9*time.Hour))
	e := newSpCurEnv(t, true, spCurDated)
	e.stubDays(`[{"start":"2026-10-15T00:00:00Z","end":"2026-10-16T00:00:00Z"}]`)
	require.NoError(t, e.run(t))
	assert.Equal(t, "Sprint 3\nMy Proj\\R1\\Sprint 3\n2026-10-12 to 2026-10-23 · 6 working days left\n", e.out.String())
}

func TestSprintCurrent_TTYOneDayLeft(t *testing.T) {
	setNow(t, day(10, 23))
	e := newSpCurEnv(t, true, spCurDated)
	e.stubDays(`[]`)
	require.NoError(t, e.run(t))
	assert.Contains(t, e.out.String(), "· 1 working day left\n")
}

func TestSprintCurrent_NonTTY(t *testing.T) {
	setNow(t, day(10, 14))
	e := newSpCurEnv(t, false, spCurDated)
	e.stubDays(`[]`)
	require.NoError(t, e.run(t))
	assert.Equal(t, "name:\tSprint 3\npath:\tMy Proj\\R1\\Sprint 3\nstart:\t2026-10-12\nfinish:\t2026-10-23\nworking days left:\t8\n", e.out.String())
}

func TestSprintCurrent_DatesNotSet(t *testing.T) {
	e := newSpCurEnv(t, true, spCurUndated)
	require.NoError(t, e.run(t))
	assert.Equal(t, "Sprint 1\nMy Proj\\Sprint 1\nDates not set; set them in the team settings to count working days left\n", e.out.String())

	e = newSpCurEnv(t, false, spCurUndated)
	require.NoError(t, e.run(t))
	assert.Equal(t, "name:\tSprint 1\npath:\tMy Proj\\Sprint 1\nstart:\t\nfinish:\t\nworking days left:\t\n", e.out.String())
}

func TestSprintCurrent_JSON(t *testing.T) {
	setNow(t, day(10, 14))
	e := newSpCurEnv(t, false, spCurDated)
	e.stubDays(`[{"start":"2026-10-15T00:00:00Z","end":"2026-10-16T00:00:00Z"}]`)
	require.NoError(t, e.run(t, "--json", "id,name,path,startDate,finishDate,timeFrame,workingDaysLeft"))
	assert.JSONEq(t, `{"id":"`+spCurID+`","name":"Sprint 3","path":"My Proj\\R1\\Sprint 3","startDate":"2026-10-12T00:00:00Z",
		"finishDate":"2026-10-23T00:00:00Z","timeFrame":"current","workingDaysLeft":6}`, e.out.String())

	e = newSpCurEnv(t, false, spCurUndated)
	require.NoError(t, e.run(t, "--json", "startDate,finishDate,workingDaysLeft"))
	assert.JSONEq(t, `{"startDate":null,"finishDate":null,"workingDaysLeft":null}`, e.out.String())
}

func TestSprintCurrent_NoCurrentSprint(t *testing.T) {
	e := newSpCurEnv(t, true, `{"count":0,"value":[]}`)
	require.EqualError(t, e.run(t), `team "Web Team" has no current sprint; set sprint dates in the team settings`)
	assert.Empty(t, e.out.String())
}

func TestSprintCurrent_Web(t *testing.T) {
	e := newSpCurEnv(t, true, spCurDated)
	require.NoError(t, e.run(t, "-w"))
	want := "https://dev.azure.com/" + e.org + "/My%20Proj/_sprints/taskboard/Web%20Team/My%20Proj/R1%5CSprint%203"
	assert.Equal(t, []string{want}, e.browser.URLs)
	assert.Equal(t, "Opening "+want+" in your browser.\n", e.errOut.String())
	assert.Empty(t, e.out.String())
}

func TestSprintCurrent_DaysError(t *testing.T) {
	setNow(t, day(10, 14))
	e := newSpCurEnv(t, false, spCurDated)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/Web%20Team/_apis/work/teamsettings"),
		httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "no access"}))
	require.ErrorContains(t, e.run(t), "no access")

	e = newSpCurEnv(t, false, spCurDated)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/Web%20Team/_apis/work/teamsettings"),
		httpmock.StringResponse(`{"workingDays":["monday"]}`))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/Web%20Team/_apis/work/teamsettings/iterations/"+spCurID+"/teamdaysoff"),
		httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "no days"}))
	require.ErrorContains(t, e.run(t), "no days")
}
