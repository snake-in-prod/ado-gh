package view

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/browsertest"
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
	spViewID  = "c0000000-0000-0000-0000-000000000003"
	spViewID2 = "c0000000-0000-0000-0000-000000000004"
	spViewIts = `{"count":1,"value":[{"id":"` + spViewID + `","name":"Sprint 3","path":"My Proj\\R1\\Sprint 3",
 "attributes":{"startDate":null,"finishDate":null,"timeFrame":"current"}}]}`
	spViewAllIts = `{"count":2,"value":[
 {"id":"` + spViewID2 + `","name":"Sprint 4","path":"My Proj\\R1\\Sprint 4","attributes":{"timeFrame":"future"}},
 {"id":"` + spViewID + `","name":"Sprint 3","path":"My Proj\\R1\\Sprint 3","attributes":{"timeFrame":"current"}}]}`
	// spViewRels: story 1 with tasks 3 and 5, story 2, bug 4.
	spViewRels = `{"workItemRelations":[
 {"rel":null,"source":null,"target":{"id":1}},
 {"rel":"System.LinkTypes.Hierarchy-Forward","source":{"id":1},"target":{"id":3}},
 {"rel":"System.LinkTypes.Hierarchy-Forward","source":{"id":1},"target":{"id":5}},
 {"rel":null,"source":null,"target":{"id":2}},
 {"rel":null,"source":null,"target":{"id":4}}]}`
)

// spViewItems are the work items of spViewRels as the batch call returns them.
var spViewItems = map[int]map[string]any{
	1: spViewItem(1, "User Story", "Active", "Checkout", "Alice"),
	2: spViewItem(2, "User Story", "New", "Login page", ""),
	3: spViewItem(3, "Task", "New", "Write tests", "Bob"),
	4: spViewItem(4, "Bug", "Closed", "Crash on save", "Alice"),
	5: spViewItem(5, "Task", "Closed", "Spike", ""),
}

func spViewItem(id int, typ, state, title, assignee string) map[string]any {
	f := map[string]any{"System.Id": id, "System.WorkItemType": typ, "System.State": state, "System.Title": title}
	if assignee != "" {
		f["System.AssignedTo"] = map[string]any{"id": "u-" + assignee, "displayName": assignee, "uniqueName": strings.ToLower(assignee) + "@example.com"}
	}
	return map[string]any{"id": id, "rev": 1, "fields": f}
}

// spViewTrim drops the trailing padding of each line of a TTY table.
func spViewTrim(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

var spViewEnvs int

type spViewEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	out, errOut *bytes.Buffer
	browser     *browsertest.Browser
	// batchIDs and wiqls are the IDs of each workitemsbatch call and the WIQL queries sent.
	batchIDs [][]float64
	wiqls    []string
}

// newSpViewEnv returns an env with the Agile process metadata stubbed.
func newSpViewEnv(t *testing.T, tty bool) *spViewEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	// The SDK caches discovery per base URL: every env gets its own org.
	spViewEnvs++
	org := "spview-" + strconv.Itoa(spViewEnvs)
	e := &spViewEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut, browser: &browsertest.Browser{}}
	t.Cleanup(func() { e.reg.Verify(t) })
	e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	return e
}

func (e *spViewEnv) stubIterations(t *testing.T, timeframe, body string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/Web%20Team/_apis/work/teamsettings/iterations"),
		func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, timeframe, r.URL.Query().Get("$timeframe"))
			return httpmock.StringResponse(body)(r)
		})
}

func (e *spViewEnv) stubRels(id, body string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/Web%20Team/_apis/work/teamsettings/iterations/"+id+"/workitems"),
		httpmock.StringResponse(body))
}

func (e *spViewEnv) stubProcess(t *testing.T) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypes"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "types"))))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypecategories"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "categories"))))
}

// stubBatch answers workitemsbatch with the requested spViewItems.
func (e *spViewEnv) stubBatch(t *testing.T) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/wit/workitemsbatch"), func(r *http.Request) (*http.Response, error) {
		var body struct {
			IDs    []float64 `json:"ids"`
			Fields []string  `json:"fields"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, fetchFields, body.Fields)
		e.batchIDs = append(e.batchIDs, body.IDs)
		var items []map[string]any
		for _, id := range body.IDs {
			items = append(items, spViewItems[int(id)])
		}
		return httpmock.JSONResponse(map[string]any{"count": len(items), "value": items})(r)
	})
}

// stubWIQL answers the WIQL query with ids.
func (e *spViewEnv) stubWIQL(t *testing.T, ids ...int) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/wit/wiql"), func(r *http.Request) (*http.Response, error) {
		var body struct{ Query string }
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		e.wiqls = append(e.wiqls, body.Query)
		items := []map[string]any{}
		for _, id := range ids {
			items = append(items, map[string]any{"id": id})
		}
		return httpmock.JSONResponse(map[string]any{"queryType": "flat", "workItems": items})(r)
	})
}

// stubSprint stubs the current sprint with spViewRels, the process and the batch call.
func (e *spViewEnv) stubSprint(t *testing.T) {
	e.stubIterations(t, "current", spViewIts)
	e.stubRels(spViewID, spViewRels)
	e.stubProcess(t)
	e.stubBatch(t)
}

func (e *spViewEnv) run(t *testing.T, args ...string) error {
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
	cmd := NewCmdView(f)
	cmd.SetArgs(append(args, "--team", "Web Team"))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

func TestSprintView_TTY(t *testing.T) {
	e := newSpViewEnv(t, true)
	e.stubSprint(t)
	require.NoError(t, e.run(t))
	assert.Equal(t, [][]float64{{1, 3, 5, 2, 4}}, e.batchIDs)
	assert.Equal(t, `Sprint 3 · My Proj\R1\Sprint 3

Proposed
#2  User Story  New  Login page

In Progress
#1    User Story  Active  Checkout     Alice
  #3  Task        New     Write tests  Bob
  #5  Task        Closed  Spike

Completed
#4  Bug  Closed  Crash on save  Alice
`, spViewTrim(e.out.String()))
}

func TestSprintView_NonTTY(t *testing.T) {
	e := newSpViewEnv(t, false)
	e.stubSprint(t)
	require.NoError(t, e.run(t))
	assert.Equal(t, "Proposed\t2\tUser Story\tNew\tLogin page\t\t\n"+
		"InProgress\t1\tUser Story\tActive\tCheckout\tAlice\t\n"+
		"InProgress\t3\tTask\tNew\tWrite tests\tBob\t1\n"+
		"InProgress\t5\tTask\tClosed\tSpike\t\t1\n"+
		"Completed\t4\tBug\tClosed\tCrash on save\tAlice\t\n", e.out.String())
}

func TestSprintView_JSON(t *testing.T) {
	e := newSpViewEnv(t, false)
	e.stubSprint(t)
	require.NoError(t, e.run(t, "--json", "id,type,title,state,stateCategory,assignedTo,parent,url"))
	u := "https://dev.azure.com/" + e.org + "/My%20Proj/_workitems/edit/"
	assert.JSONEq(t, `[
	 {"id":1,"type":"User Story","title":"Checkout","state":"Active","stateCategory":"InProgress",
	  "assignedTo":{"id":"u-Alice","displayName":"Alice","uniqueName":"alice@example.com"},"parent":null,"url":"`+u+`1"},
	 {"id":3,"type":"Task","title":"Write tests","state":"New","stateCategory":"Proposed",
	  "assignedTo":{"id":"u-Bob","displayName":"Bob","uniqueName":"bob@example.com"},"parent":1,"url":"`+u+`3"},
	 {"id":5,"type":"Task","title":"Spike","state":"Closed","stateCategory":"Completed","assignedTo":null,"parent":1,"url":"`+u+`5"},
	 {"id":2,"type":"User Story","title":"Login page","state":"New","stateCategory":"Proposed","assignedTo":null,"parent":null,"url":"`+u+`2"},
	 {"id":4,"type":"Bug","title":"Crash on save","state":"Closed","stateCategory":"Completed",
	  "assignedTo":{"id":"u-Alice","displayName":"Alice","uniqueName":"alice@example.com"},"parent":null,"url":"`+u+`4"}]`, e.out.String())
}

func TestSprintView_Filters(t *testing.T) {
	e := newSpViewEnv(t, false)
	e.stubSprint(t)
	e.stubWIQL(t, 3)
	require.NoError(t, e.run(t, "--type", "task", "-a", "@me"))
	require.Len(t, e.wiqls, 1)
	assert.Contains(t, e.wiqls[0], "[System.WorkItemType] = 'Task'")
	assert.Contains(t, e.wiqls[0], "[System.AssignedTo] = @Me")
	assert.Contains(t, e.wiqls[0], "[System.Id] IN (1, 3, 5, 2, 4)")
	assert.Equal(t, [][]float64{{3}}, e.batchIDs)
	// The parent is filtered out: the task is listed at the top level, still naming its parent.
	assert.Equal(t, "Proposed\t3\tTask\tNew\tWrite tests\tBob\t1\n", e.out.String())
}

func TestSprintView_FilterNoMatch(t *testing.T) {
	e := newSpViewEnv(t, true)
	e.stubIterations(t, "current", spViewIts)
	e.stubRels(spViewID, spViewRels)
	e.stubWIQL(t)
	require.NoError(t, e.run(t, "-a", "@me"))
	assert.Empty(t, e.out.String())
	assert.Equal(t, "no work items in Sprint 3 match your filters\n", e.errOut.String())
}

func TestSprintView_BadType(t *testing.T) {
	e := newSpViewEnv(t, true)
	e.stubIterations(t, "current", spViewIts)
	e.stubRels(spViewID, spViewRels)
	e.stubProcess(t)
	require.ErrorContains(t, e.run(t, "--type", "nope"), `unknown work item type "nope"`)
}

func TestSprintView_NamedIteration(t *testing.T) {
	for _, arg := range []string{"sprint 4", `My Proj\R1\Sprint 4`, "My Proj/R1/Sprint 4"} {
		t.Run(arg, func(t *testing.T) {
			e := newSpViewEnv(t, false)
			e.stubIterations(t, "", spViewAllIts)
			e.stubRels(spViewID2, `{"workItemRelations":[{"target":{"id":2}}]}`)
			e.stubProcess(t)
			e.stubBatch(t)
			require.NoError(t, e.run(t, arg))
			assert.Equal(t, "Proposed\t2\tUser Story\tNew\tLogin page\t\t\n", e.out.String())
		})
	}
}

func TestSprintView_UnknownIteration(t *testing.T) {
	e := newSpViewEnv(t, false)
	e.stubIterations(t, "", spViewAllIts)
	require.EqualError(t, e.run(t, "Sprint 9"), `team "Web Team" has no sprint "Sprint 9"; its sprints: Sprint 4, Sprint 3`)
}

func TestSprintView_NoCurrentSprint(t *testing.T) {
	e := newSpViewEnv(t, true)
	e.stubIterations(t, "current", `{"count":0,"value":[]}`)
	require.EqualError(t, e.run(t), `team "Web Team" has no current sprint; set sprint dates in the team settings`)
}

func TestSprintView_Empty(t *testing.T) {
	e := newSpViewEnv(t, true)
	e.stubIterations(t, "current", spViewIts)
	e.stubRels(spViewID, `{"workItemRelations":[]}`)
	require.NoError(t, e.run(t))
	assert.Empty(t, e.out.String())
	assert.Equal(t, "no work items in Sprint 3\n", e.errOut.String())

	e = newSpViewEnv(t, false)
	e.stubIterations(t, "current", spViewIts)
	e.stubRels(spViewID, `{"workItemRelations":[]}`)
	require.NoError(t, e.run(t, "--json", "id"))
	assert.JSONEq(t, `[]`, e.out.String())
}

func TestSprintView_Web(t *testing.T) {
	e := newSpViewEnv(t, true)
	e.stubIterations(t, "current", spViewIts)
	require.NoError(t, e.run(t, "-w"))
	want := "https://dev.azure.com/" + e.org + "/My%20Proj/_sprints/taskboard/Web%20Team/My%20Proj/R1%5CSprint%203"
	assert.Equal(t, []string{want}, e.browser.URLs)
	assert.Empty(t, e.out.String())
}

func TestSprintView_Errors(t *testing.T) {
	e := newSpViewEnv(t, true)
	e.stubIterations(t, "current", spViewIts)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/Web%20Team/_apis/work/teamsettings/iterations/"+spViewID+"/workitems"),
		httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "no access"}))
	require.ErrorContains(t, e.run(t), "no access")

	e = newSpViewEnv(t, true)
	e.stubIterations(t, "current", spViewIts)
	e.stubRels(spViewID, spViewRels)
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/wit/workitemsbatch"),
		httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "batch boom"}))
	require.ErrorContains(t, e.run(t), "batch boom")

	e = newSpViewEnv(t, true)
	e.stubIterations(t, "current", spViewIts)
	e.stubRels(spViewID, spViewRels)
	e.stubBatch(t)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypes"),
		httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "types boom"}))
	require.ErrorContains(t, e.run(t), "types boom")
}
