package board

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/browsertest"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// boardBacklogs is an Agile team's backlog configuration with the Epics level hidden.
const boardBacklogs = `{
 "backlogFields":{"typeFields":{"Order":"Microsoft.VSTS.Common.StackRank"}},
 "hiddenBacklogs":["Microsoft.EpicCategory"],
 "requirementBacklog":{"id":"Microsoft.RequirementCategory","name":"Stories","rank":2,"type":"requirement"},
 "portfolioBacklogs":[
  {"id":"Microsoft.EpicCategory","name":"Epics","rank":4,"type":"portfolio"},
  {"id":"Microsoft.FeatureCategory","name":"Features","rank":3,"type":"portfolio"}]}`

// boardStories is the Stories board: a split Active column with a WIP limit, an unsplit
// Resolved column with no isSplit, and bugs shown with the stories.
const boardStories = `{"name":"Stories","columns":[
 {"name":"New","columnType":"incoming","itemLimit":0,"stateMappings":{"User Story":"New","Bug":"New"}},
 {"name":"Active","columnType":"inProgress","itemLimit":5,"isSplit":true,"stateMappings":{"User Story":"Active","Bug":"Active"}},
 {"name":"Resolved","columnType":"inProgress","itemLimit":0,"stateMappings":{"User Story":"Resolved","Bug":"Resolved"}},
 {"name":"Closed","columnType":"outgoing","itemLimit":0,"isSplit":null,"stateMappings":{"User Story":"Closed","Bug":"Closed"}}],
 "fields":{"columnField":{"referenceName":"WEF_1_Kanban.Column"},"doneField":{"referenceName":"WEF_1_Kanban.Column.Done"}}}`

// boardWIQLWant is the card query of the Stories board for Web Team.
const boardWIQLWant = "SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = @project" +
	" AND [System.WorkItemType] IN ('Bug', 'User Story')" +
	` AND [System.AreaPath] = @TeamAreas('[My Proj]\Web Team')` +
	" AND [System.State] IN ('Active', 'Closed', 'New', 'Resolved')" +
	" AND ([System.State] NOT IN ('Closed') OR [System.ChangedDate] >= @Today - 30)" +
	" ORDER BY [Microsoft.VSTS.Common.StackRank] ASC, [System.Id] ASC"

var boardEnvs int

type boardEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	out, errOut *bytes.Buffer
	browser     *browsertest.Browser
	// wiqls and batches are the WIQL queries and workitemsbatch bodies sent, in order.
	wiqls   []string
	batches []map[string]any
}

func newBoardEnv(t *testing.T, tty bool) *boardEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	ios.SetTerminalWidth(120)
	// The SDK caches discovery per base URL: every env gets its own org.
	boardEnvs++
	org := "board-" + strconv.Itoa(boardEnvs)
	e := &boardEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut, browser: &browsertest.Browser{}}
	t.Cleanup(func() { e.reg.Verify(t) })
	e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, org+"/My%20Proj/Web%20Team/_apis/work/backlogconfiguration"),
		httpmock.StringResponse(boardBacklogs))
	return e
}

// stubBoard answers GET boards/{name}.
func (e *boardEnv) stubBoard(name, body string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/Web%20Team/_apis/work/boards/"+name), httpmock.StringResponse(body))
}

// stubCards answers the team-scoped WIQL with the items' IDs and the batch call with items.
func (e *boardEnv) stubCards(t *testing.T, items ...map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/Web%20Team/_apis/wit/wiql"), func(r *http.Request) (*http.Response, error) {
		var body struct{ Query string }
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		e.wiqls = append(e.wiqls, body.Query)
		refs := []map[string]any{}
		for _, it := range items {
			refs = append(refs, map[string]any{"id": it["id"]})
		}
		return httpmock.JSONResponse(map[string]any{"queryType": "flat", "workItems": refs})(r)
	})
	if len(items) == 0 {
		return
	}
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/wit/workitemsbatch"), func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		e.batches = append(e.batches, body)
		return httpmock.JSONResponse(map[string]any{"count": len(items), "value": items})(r)
	})
}

func (e *boardEnv) run(t *testing.T, args ...string) error {
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
	cmd := NewCmdBoard(f)
	cmd.SetArgs(append(args, "--team", "Web Team"))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

// boardCard is a work item as the batch call returns it.
func boardCard(id int, typ, title, state string, fields map[string]any) map[string]any {
	f := map[string]any{"System.Id": id, "System.WorkItemType": typ, "System.Title": title, "System.State": state}
	for k, v := range fields {
		f[k] = v
	}
	return map[string]any{"id": id, "fields": f}
}

// boardCards are the Stories board cards: placed by the WEF column field (not
// System.BoardColumn), by state when the column field is unset, dropped when neither matches.
func boardCards() []map[string]any {
	ann := map[string]any{"displayName": "Ann Lee", "uniqueName": "ann@example.com", "id": "a1"}
	return []map[string]any{
		boardCard(1, "User Story", "Login page", "Active", map[string]any{"WEF_1_Kanban.Column": "Active",
			"WEF_1_Kanban.Column.Done": false, "System.AssignedTo": ann}),
		boardCard(2, "User Story", "Logout", "Active", map[string]any{"WEF_1_Kanban.Column": "Active", "WEF_1_Kanban.Column.Done": true}),
		boardCard(3, "Bug", "Crash on save", "New", map[string]any{"WEF_1_Kanban.Column": "New", "System.BoardColumn": "Closed"}),
		boardCard(4, "User Story", "Search", "Resolved", nil),
		boardCard(5, "User Story", "Orphan", "Gone", map[string]any{"WEF_1_Kanban.Column": "Renamed"}),
	}
}

func TestBoard_TTY(t *testing.T) {
	e := newBoardEnv(t, true)
	e.stubBoard("Stories", boardStories)
	e.stubCards(t, boardCards()...)
	require.NoError(t, e.run(t))

	assert.Equal(t, []string{boardWIQLWant}, e.wiqls)
	require.Len(t, e.batches, 1)
	assert.Equal(t, []any{1.0, 2.0, 3.0, 4.0, 5.0}, e.batches[0]["ids"])
	assert.ElementsMatch(t, []any{"System.Id", "System.WorkItemType", "System.Title", "System.State", "System.AssignedTo",
		"WEF_1_Kanban.Column", "WEF_1_Kanban.Column.Done"}, e.batches[0]["fields"])

	out := e.out.String()
	lines := strings.Split(out, "\n")
	assert.Equal(t, "Stories · Web Team", lines[0])
	assert.Regexp(t, `^New 1 +Active 2/5 +Resolved 1 +Closed 0$`, lines[2])
	assert.Regexp(t, `^ +Doing +Done$`, lines[3])
	assert.Regexp(t, `^#3 Crash on save +#1 Login page \(Ann \.\.\. +#2 Logout +#4 Search$`, lines[4])
	assert.NotContains(t, out, "Orphan")
}

func TestBoard_NonTTY(t *testing.T) {
	e := newBoardEnv(t, false)
	e.stubBoard("Stories", boardStories)
	e.stubCards(t, boardCards()...)
	require.NoError(t, e.run(t))
	assert.Equal(t, "New\t3\tCrash on save\t\nActive/Doing\t1\tLogin page\tAnn Lee\nActive/Done\t2\tLogout\t\nResolved\t4\tSearch\t\n",
		e.out.String())
}

func TestBoard_NonTTYTabInTitle(t *testing.T) {
	e := newBoardEnv(t, false)
	e.stubBoard("Stories", boardStories)
	e.stubCards(t, boardCard(3, "Bug", "Crash\ton\r\nsave", "New", map[string]any{"WEF_1_Kanban.Column": "New"}))
	require.NoError(t, e.run(t))
	assert.Equal(t, "New\t3\tCrash on save\t\n", e.out.String(), "tabs and newlines never split a row")
}

func TestBoard_JSON(t *testing.T) {
	e := newBoardEnv(t, false)
	e.stubBoard("Stories", boardStories)
	e.stubCards(t, boardCards()...)
	require.NoError(t, e.run(t, "--json", "name,team,columns"))
	assert.JSONEq(t, `{"name":"Stories","team":"Web Team","columns":[
		{"name":"New","isSplit":false,"wipLimit":0,"items":[
			{"id":3,"type":"Bug","title":"Crash on save","state":"New","assignedTo":null,"done":false}]},
		{"name":"Active","isSplit":true,"wipLimit":5,"items":[
			{"id":1,"type":"User Story","title":"Login page","state":"Active",
			 "assignedTo":{"id":"a1","displayName":"Ann Lee","uniqueName":"ann@example.com"},"done":false},
			{"id":2,"type":"User Story","title":"Logout","state":"Active","assignedTo":null,"done":true}]},
		{"name":"Resolved","isSplit":false,"wipLimit":0,"items":[
			{"id":4,"type":"User Story","title":"Search","state":"Resolved","assignedTo":null,"done":false}]},
		{"name":"Closed","isSplit":false,"wipLimit":0,"items":[]}]}`, e.out.String())
}

func TestBoard_Levels(t *testing.T) {
	features := `{"name":"Features","columns":[
	 {"name":"New","columnType":"incoming","stateMappings":{"Feature":"New"}},
	 {"name":"Closed","columnType":"outgoing","stateMappings":{"Feature":"Closed"}}],
	 "fields":{"columnField":{"referenceName":"WEF_2_Kanban.Column"},"doneField":{"referenceName":"WEF_2_Kanban.Column.Done"}}}`
	for _, arg := range []string{"features", "FEATURES", "Microsoft.FeatureCategory"} {
		t.Run(arg, func(t *testing.T) {
			e := newBoardEnv(t, false)
			e.stubBoard("Features", features)
			e.stubCards(t, boardCard(9, "Feature", "Big thing", "New", map[string]any{"WEF_2_Kanban.Column": "New"}))
			require.NoError(t, e.run(t, arg))
			assert.Contains(t, e.wiqls[0], "[System.WorkItemType] IN ('Feature')")
			assert.Equal(t, "New\t9\tBig thing\t\n", e.out.String())
		})
	}
}

func TestBoard_LevelErrors(t *testing.T) {
	e := newBoardEnv(t, false)
	require.EqualError(t, e.run(t, "tasks"), `unknown backlog "tasks"; available: Stories, Epics, Features`)
	e = newBoardEnv(t, false)
	require.EqualError(t, e.run(t, "epics"), `backlog "Epics" is hidden for team "Web Team"; turn it on in the team's backlog settings`)
}

func TestBoard_Assignee(t *testing.T) {
	tests := []struct{ arg, clause string }{
		{"@me", " AND [System.AssignedTo] = @Me ORDER BY"},
		{"none", " AND [System.AssignedTo] = '' ORDER BY"},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			e := newBoardEnv(t, true)
			e.stubBoard("Stories", boardStories)
			e.stubCards(t)
			require.NoError(t, e.run(t, "-a", tt.arg))
			require.Len(t, e.wiqls, 1)
			assert.Contains(t, e.wiqls[0], tt.clause)
			// No cards: no batch call, the columns still render.
			assert.Contains(t, e.out.String(), "New 0")
		})
	}
}

func TestBoard_Web(t *testing.T) {
	e := newBoardEnv(t, true)
	require.NoError(t, e.run(t, "-w"))
	want := "https://dev.azure.com/" + e.org + "/My%20Proj/_boards/board/t/Web%20Team/Microsoft.RequirementCategory"
	assert.Equal(t, []string{want}, e.browser.URLs)
	assert.Empty(t, e.out.String())
}

func TestBoard_Errors(t *testing.T) {
	e := newBoardEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/Web%20Team/_apis/work/boards/Stories"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "no board"}))
	require.ErrorContains(t, e.run(t), "no board")

	e = newBoardEnv(t, false)
	e.stubBoard("Stories", boardStories)
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/Web%20Team/_apis/wit/wiql"),
		httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "bad query"}))
	require.ErrorContains(t, e.run(t), "bad query")

	cmd := NewCmdBoard(&cmdutil.Factory{})
	cmd.SetArgs([]string{"a", "b"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	require.ErrorContains(t, cmd.Execute(), "accepts at most 1 arg")
}

func TestBoardWIQL_Quoting(t *testing.T) {
	b := &api.Board{Columns: []api.BoardColumn{{Name: "Doing", ColumnType: "inProgress", StateMappings: map[string]string{"Task": "Doing"}}}}
	got := boardWIQL("O'Proj", "Team's", b, "", shared.IdentityFilter{Value: "Bob O'Neil"})
	assert.Contains(t, got, `@TeamAreas('[O''Proj]\Team''s')`)
	assert.Contains(t, got, `[System.AssignedTo] = 'Bob O''Neil'`)
	assert.True(t, strings.HasSuffix(got, " ORDER BY [System.Id] ASC"), got)
	assert.NotContains(t, got, "NOT IN")
}
