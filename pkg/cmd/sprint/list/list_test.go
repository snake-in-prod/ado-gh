package list

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// spListIterations is a team iterations response with one past, one current (no dates) and
// one future iteration.
const spListIterations = `{"count":3,"value":[
 {"id":"b0000000-0000-0000-0000-000000000001","name":"Sprint 1","path":"My Proj\\Sprint 1",
  "attributes":{"startDate":"2026-09-14T00:00:00Z","finishDate":"2026-09-25T00:00:00Z","timeFrame":"past"}},
 {"id":"b0000000-0000-0000-0000-000000000002","name":"Sprint 2","path":"My Proj\\Sprint 2",
  "attributes":{"startDate":null,"finishDate":null,"timeFrame":"current"}},
 {"id":"b0000000-0000-0000-0000-000000000003","name":"Sprint 3","path":"My Proj\\R1\\Sprint 3",
  "attributes":{"startDate":"2026-10-12T00:00:00Z","finishDate":"2026-10-23T00:00:00Z","timeFrame":"future"}}]}`

type spListEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	out, errOut *bytes.Buffer
	// timeframes are the $timeframe values sent, in order.
	timeframes []string
}

func newSpListEnv(t *testing.T, tty bool) *spListEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	org := "splist-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	e := &spListEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut}
	t.Cleanup(func() { e.reg.Verify(t) })
	return e
}

// stubIterations registers the SDK discovery call and answers the team iterations call of
// team (URL-escaped) with body.
func (e *spListEnv) stubIterations(team, body string) {
	e.reg.StubLocations(e.org, httpmock.WorkItemLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/"+team+"/_apis/work/teamsettings/iterations"),
		func(r *http.Request) (*http.Response, error) {
			e.timeframes = append(e.timeframes, r.URL.Query().Get("$timeframe"))
			return httpmock.StringResponse(body)(r)
		})
}

func (e *spListEnv) run(t *testing.T, args ...string) error {
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
	}
	cmd := NewCmdList(f)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

func TestSprintList_TimeframeFilter(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		timeframe string // $timeframe sent
		want      []string
	}{
		{"default all", nil, "", []string{"Sprint 1", "Sprint 2", "Sprint 3"}},
		{"all", []string{"--timeframe", "all"}, "", []string{"Sprint 1", "Sprint 2", "Sprint 3"}},
		{"past client-side", []string{"--timeframe", "past"}, "", []string{"Sprint 1"}},
		{"future client-side", []string{"--timeframe", "future"}, "", []string{"Sprint 3"}},
		{"current server-side", []string{"--timeframe", "current"}, "current", []string{"Sprint 2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSpListEnv(t, false)
			e.stubIterations("Web%20Team", spListIterations)
			require.NoError(t, e.run(t, append(tt.args, "--team", "Web Team")...))
			assert.Equal(t, []string{tt.timeframe}, e.timeframes)
			var got []string
			for l := range strings.Lines(e.out.String()) {
				got = append(got, strings.Split(l, "\t")[0])
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSprintList_NonTTYRows(t *testing.T) {
	e := newSpListEnv(t, false)
	e.stubIterations("Web%20Team", spListIterations)
	require.NoError(t, e.run(t, "--team", "Web Team"))
	assert.Equal(t, "Sprint 1\tMy Proj\\Sprint 1\t2026-09-14\t2026-09-25\tpast\n"+
		"Sprint 2\tMy Proj\\Sprint 2\t\t\tcurrent\n"+
		"Sprint 3\tMy Proj\\R1\\Sprint 3\t2026-10-12\t2026-10-23\tfuture\n", e.out.String())
}

func TestSprintList_TTYTableDefaultTeam(t *testing.T) {
	e := newSpListEnv(t, true)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/projects/My%20Proj"),
		httpmock.StringResponse(`{"id":"b0000000-0000-0000-0000-00000000000a","name":"My Proj","defaultTeam":{"name":"My Proj Team"}}`))
	e.stubIterations("My%20Proj%20Team", spListIterations)
	require.NoError(t, e.run(t))
	lines := strings.Split(strings.TrimRight(e.out.String(), "\n"), "\n")
	require.Len(t, lines, 4)
	assert.Regexp(t, `^NAME\s+PATH\s+START\s+FINISH\s+TIMEFRAME$`, lines[0])
	assert.Regexp(t, `^Sprint 1\s+My Proj\\Sprint 1\s+2026-09-14\s+2026-09-25\s+past$`, lines[1])
	assert.Regexp(t, `^Sprint 2\s+My Proj\\Sprint 2\s+-\s+-\s+current$`, lines[2])
}

func TestSprintList_JSON(t *testing.T) {
	e := newSpListEnv(t, false)
	e.stubIterations("Web%20Team", spListIterations)
	require.NoError(t, e.run(t, "--team", "Web Team", "--timeframe", "all", "--json", "id,name,path,startDate,finishDate,timeFrame"))
	assert.JSONEq(t, `[
		{"id":"b0000000-0000-0000-0000-000000000001","name":"Sprint 1","path":"My Proj\\Sprint 1",
		 "startDate":"2026-09-14T00:00:00Z","finishDate":"2026-09-25T00:00:00Z","timeFrame":"past"},
		{"id":"b0000000-0000-0000-0000-000000000002","name":"Sprint 2","path":"My Proj\\Sprint 2",
		 "startDate":null,"finishDate":null,"timeFrame":"current"},
		{"id":"b0000000-0000-0000-0000-000000000003","name":"Sprint 3","path":"My Proj\\R1\\Sprint 3",
		 "startDate":"2026-10-12T00:00:00Z","finishDate":"2026-10-23T00:00:00Z","timeFrame":"future"}
	]`, e.out.String())
}

func TestSprintList_Empty(t *testing.T) {
	e := newSpListEnv(t, true)
	e.stubIterations("Web%20Team", `{"count":0,"value":[]}`)
	require.NoError(t, e.run(t, "--team", "Web Team"))
	assert.Empty(t, e.out.String())
	assert.Equal(t, "no sprints found for team Web Team\n", e.errOut.String())
}

func TestSprintList_InvalidTimeframe(t *testing.T) {
	e := newSpListEnv(t, false)
	err := e.run(t, "--team", "Web Team", "--timeframe", "soon")
	var fe *cmdutil.FlagError
	require.ErrorAs(t, err, &fe)
	assert.EqualError(t, err, `invalid --timeframe "soon": use past, current, future or all`)
	assert.Empty(t, e.reg.Requests)
}

func TestSprintList_APIError(t *testing.T) {
	e := newSpListEnv(t, false)
	e.reg.StubLocations(e.org, httpmock.WorkItemLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/Web%20Team/_apis/work/teamsettings/iterations"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "team not found"}))
	require.ErrorContains(t, e.run(t, "--team", "Web Team"), "team not found")
}
