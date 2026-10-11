package board

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/witfixtures"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const (
	spBoardIterID = "c0000000-0000-0000-0000-000000000003"
	spBoardColTo  = "d0000000-0000-0000-0000-000000000001"
	spBoardColRev = "d0000000-0000-0000-0000-000000000002"
	spBoardColDn  = "d0000000-0000-0000-0000-000000000003"
)

// spBoardCurrent is the current iteration, as answered for $timeframe=current.
const spBoardCurrent = `{"count":1,"value":[
 {"id":"` + spBoardIterID + `","name":"Sprint 3","path":"My Proj\\R1\\Sprint 3","attributes":{"timeFrame":"current"}}]}`

// spBoardAll are all the team's iterations.
const spBoardAll = `{"count":2,"value":[
 {"id":"c0000000-0000-0000-0000-000000000002","name":"Sprint 2","path":"My Proj\\Sprint 2","attributes":{"timeFrame":"past"}},
 {"id":"` + spBoardIterID + `","name":"Sprint 3","path":"My Proj\\R1\\Sprint 3","attributes":{"timeFrame":"current"}}]}`

const spBoardCustomColumns = `{"isCustomized":true,"isValid":true,"columns":[
 {"id":"` + spBoardColDn + `","name":"Done","order":2},
 {"id":"` + spBoardColTo + `","name":"To Do","order":0},
 {"id":"` + spBoardColRev + `","name":"Review","order":1}]}`

// spBoardEnvs counts the envs created.
var spBoardEnvs int

type spBoardEnv struct {
	org  string
	team string // escaped team path segment
	reg  *httpmock.Registry
	ios  *iostreams.IOStreams
	out  *bytes.Buffer
	// batches are the workitemsbatch request bodies, in order.
	batches []map[string]any
	// iterationsTimeframes are the $timeframe values of the team iterations requests.
	iterationsTimeframes []string
}

// newSpBoardEnv returns an env whose team (escaped path segment) has iterations.
func newSpBoardEnv(t *testing.T, tty bool, team, iterations string) *spBoardEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, _ := iostreams.Test()
	ios.SetStdoutTTY(tty)
	ios.SetTerminalWidth(80)
	// The SDK caches discovery per base URL: every env gets its own org.
	spBoardEnvs++
	org := "spboard-" + strconv.Itoa(spBoardEnvs) + "-" + strings.NewReplacer("/", "-", " ", "-", "\\", "-").Replace(strings.ToLower(t.Name()))
	e := &spBoardEnv{org: org, team: team, reg: &httpmock.Registry{}, ios: ios, out: out}
	t.Cleanup(func() { e.reg.Verify(t) })
	e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, org+"/My%20Proj/"+team+"/_apis/work/teamsettings/iterations"),
		func(r *http.Request) (*http.Response, error) {
			e.iterationsTimeframes = append(e.iterationsTimeframes, r.URL.Query().Get("$timeframe"))
			return httpmock.StringResponse(iterations)(r)
		})
	return e
}

func (e *spBoardEnv) stub(path string, h httpmock.Responder) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/"+e.team+"/_apis/work/"+path), h)
}

// stubCustom answers a customized taskboard: To Do, Review, Done and the given placements.
func (e *spBoardEnv) stubCustom(placements string) {
	e.stub("taskboardcolumns", httpmock.StringResponse(spBoardCustomColumns))
	e.stub("taskboardworkitems/"+spBoardIterID, httpmock.StringResponse(`{"value":`+placements+`}`))
}

// stubBatch answers the next workitemsbatch request with items after recording it.
func (e *spBoardEnv) stubBatch(t *testing.T, items ...map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/wit/workitemsbatch"), func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		e.batches = append(e.batches, body)
		return httpmock.JSONResponse(map[string]any{"count": len(items), "value": items})(r)
	})
}

func (e *spBoardEnv) run(t *testing.T, args ...string) error {
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
	cmd := NewCmdBoard(f)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

func spBoardItem(id int, typ, state, title, assignee string, parent int) map[string]any {
	fields := map[string]any{"System.Title": title, "System.WorkItemType": typ, "System.State": state}
	if assignee != "" {
		fields["System.AssignedTo"] = map[string]any{"id": "u-" + assignee, "displayName": assignee, "uniqueName": strings.ToLower(assignee) + "@example.com"}
	}
	if parent != 0 {
		fields["System.Parent"] = parent
	}
	return map[string]any{"id": id, "rev": 1, "fields": fields}
}

// stubCustomBoard serves a customized board with #11 in Review, #12 in To Do (placed by column
// name only) and #13 in a column that no longer exists.
func (e *spBoardEnv) stubCustomBoard(t *testing.T) {
	e.stubCustom(`[
		{"workItemId":11,"column":"Review","columnId":"` + spBoardColRev + `","state":"Active"},
		{"workItemId":12,"column":"To Do","state":"New"},
		{"workItemId":13,"column":"Gone","columnId":"d0000000-0000-0000-0000-0000000000ff","state":"New"}]`)
	e.stubBatch(t,
		spBoardItem(11, "Task", "Active", "Write the parser", "Ada", 7),
		spBoardItem(12, "Task", "New", "Add tests", "", 7),
		spBoardItem(13, "Task", "New", "Lost", "", 0))
}

func TestSprintBoard_TTY(t *testing.T) {
	e := newSpBoardEnv(t, true, "Web%20Team", spBoardCurrent)
	e.stubCustomBoard(t)
	require.NoError(t, e.run(t, "--team", "Web Team"))
	assert.Equal(t, "Sprint 3 My Proj\\R1\\Sprint 3\n\n"+
		"To Do 1                    Review 1                   Done 0\n"+
		"#12 Add tests              #11 Write the parser (...\n", e.out.String())
	assert.Equal(t, []string{"current"}, e.iterationsTimeframes)
	require.Len(t, e.batches, 1)
	assert.Equal(t, []any{11.0, 12.0, 13.0}, e.batches[0]["ids"])
	assert.ElementsMatch(t, []any{"System.Title", "System.AssignedTo", "System.Parent", "System.State", "System.WorkItemType"},
		e.batches[0]["fields"])
}

func TestSprintBoard_NonTTY(t *testing.T) {
	e := newSpBoardEnv(t, false, "Web%20Team", spBoardCurrent)
	e.stubCustomBoard(t)
	require.NoError(t, e.run(t, "--team", "Web Team"))
	assert.Equal(t, "To Do\t12\tAdd tests\t\t7\nReview\t11\tWrite the parser\tAda\t7\n", e.out.String())
}

func TestSprintBoard_JSON(t *testing.T) {
	e := newSpBoardEnv(t, false, "Web%20Team", spBoardCurrent)
	e.stubCustomBoard(t)
	require.NoError(t, e.run(t, "--team", "Web Team", "--json", "name,items"))
	assert.JSONEq(t, `[
		{"name":"To Do","items":[{"id":12,"title":"Add tests","type":"Task","state":"New","assignedTo":null,"parent":7}]},
		{"name":"Review","items":[{"id":11,"title":"Write the parser","type":"Task","state":"Active",
		  "assignedTo":{"id":"u-Ada","displayName":"Ada","uniqueName":"ada@example.com"},"parent":7}]},
		{"name":"Done","items":[]}]`, e.out.String())
}

// The default (never customized) taskboard has no columns and its taskboardworkitems call
// fails: the columns are the task type's states and the cards the iteration's tasks.
func TestSprintBoard_DefaultTaskboard(t *testing.T) {
	e := newSpBoardEnv(t, false, "My%20Proj%20Team", spBoardCurrent)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/projects/My%20Proj"),
		httpmock.StringResponse(`{"id":"b0000000-0000-0000-0000-00000000000a","name":"My Proj","defaultTeam":{"name":"My Proj Team"}}`))
	e.stub("taskboardcolumns", httpmock.StringResponse(`{"columns":[],"isCustomized":false,"isValid":true}`))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypes"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "types"))))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypecategories"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "categories"))))
	e.stub("teamsettings/iterations/"+spBoardIterID+"/workitems", httpmock.StringResponse(`{"workItemRelations":[
		{"target":{"id":7}},
		{"rel":"System.LinkTypes.Hierarchy-Forward","source":{"id":7},"target":{"id":11}},
		{"rel":"System.LinkTypes.Hierarchy-Forward","source":{"id":7},"target":{"id":12}},
		{"target":{"id":14}}]}`))
	e.stubBatch(t,
		spBoardItem(7, "User Story", "Active", "Parser", "", 0),
		spBoardItem(11, "Task", "Active", "Write the parser", "Ada", 7),
		spBoardItem(12, "Task", "Removed", "Dropped", "", 7),
		spBoardItem(14, "Task", "Closed", "Orphan", "Bo", 0))
	require.NoError(t, e.run(t))
	assert.Equal(t, "Active\t11\tWrite the parser\tAda\t7\nClosed\t14\tOrphan\tBo\t\n", e.out.String())
	assert.Equal(t, []any{7.0, 11.0, 12.0, 14.0}, e.batches[0]["ids"])

	e = newSpBoardEnv(t, false, "Web%20Team", spBoardCurrent)
	e.stub("taskboardcolumns", httpmock.StringResponse(`{"columns":[],"isCustomized":false,"isValid":true}`))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypes"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "types"))))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypecategories"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "categories"))))
	e.stub("teamsettings/iterations/"+spBoardIterID+"/workitems", httpmock.StringResponse(`{"workItemRelations":[]}`))
	require.NoError(t, e.run(t, "--team", "Web Team", "--json", "name"))
	assert.JSONEq(t, `[{"name":"New"},{"name":"Active"},{"name":"Closed"}]`, e.out.String())
}

func TestSprintBoard_IterationArg(t *testing.T) {
	for _, arg := range []string{"sprint 2", `My Proj\Sprint 2`, `\My Proj\Sprint 2`, "My Proj/Sprint 2"} {
		t.Run(arg, func(t *testing.T) {
			e := newSpBoardEnv(t, false, "Web%20Team", spBoardAll)
			e.stub("taskboardcolumns", httpmock.StringResponse(spBoardCustomColumns))
			e.stub("taskboardworkitems/c0000000-0000-0000-0000-000000000002", httpmock.StringResponse(`{"value":[]}`))
			require.NoError(t, e.run(t, "--team", "Web Team", arg))
			assert.Equal(t, []string{""}, e.iterationsTimeframes)
			assert.Empty(t, e.out.String())
		})
	}
}

func TestSprintBoard_IterationErrors(t *testing.T) {
	e := newSpBoardEnv(t, false, "Web%20Team", spBoardAll)
	require.EqualError(t, e.run(t, "--team", "Web Team", "Sprint 9"),
		`sprint "Sprint 9" is not one of team "Web Team"'s iterations; see ado sprint list`)

	e = newSpBoardEnv(t, false, "Other", `{"count":0,"value":[]}`)
	require.EqualError(t, e.run(t, "--team", "Other"), `team "Other" has no current sprint; set sprint dates in the team settings`)
}

func TestSprintBoard_APIErrors(t *testing.T) {
	e := newSpBoardEnv(t, false, "Web%20Team", spBoardCurrent)
	e.stub("taskboardcolumns", httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "no columns"}))
	require.ErrorContains(t, e.run(t, "--team", "Web Team"), "no columns")

	e = newSpBoardEnv(t, false, "Web%20Team", spBoardCurrent)
	e.stub("taskboardcolumns", httpmock.StringResponse(spBoardCustomColumns))
	e.stub("taskboardworkitems/"+spBoardIterID, httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "no items"}))
	require.ErrorContains(t, e.run(t, "--team", "Web Team"), "no items")

	e = newSpBoardEnv(t, false, "Web%20Team", spBoardCurrent)
	e.stubCustom(`[{"workItemId":11,"column":"Review","columnId":"` + spBoardColRev + `"}]`)
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/wit/workitemsbatch"),
		httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "no batch"}))
	require.ErrorContains(t, e.run(t, "--team", "Web Team"), "no batch")
}
