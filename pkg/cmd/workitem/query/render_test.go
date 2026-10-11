package query

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
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	wiRenderFlatWIQL = "SELECT [System.Id], [System.Title], [System.AssignedTo], [Custom.Risk] FROM WorkItems"
	wiRenderTreeWIQL = "SELECT [System.Id], [System.Title] FROM WorkItemLinks WHERE [System.Links.LinkType] = 'System.LinkTypes.Hierarchy-Forward' MODE (Recursive)"
	wiRenderHier     = "System.LinkTypes.Hierarchy-Forward"
)

// wiRenderEnv is a wiQueryInputEnv that also records browser URLs, $top presence and batch
// requests.
type wiRenderEnv struct {
	*wiQueryInputEnv
	browser *browsertest.Browser
	// hasTop records, per WIQL run, whether $top was sent; tops holds its values.
	hasTop []bool
	// batchFields are the fields of each workitemsbatch request.
	batchFields [][]string
}

func newWIRenderEnv(t *testing.T, tty bool) *wiRenderEnv {
	t.Helper()
	e := &wiRenderEnv{wiQueryInputEnv: newWIQueryInputEnv(t, true), browser: &browsertest.Browser{}}
	e.ios.SetStdoutTTY(tty)
	return e
}

func (e *wiRenderEnv) errOut() string { return e.ios.ErrOut.(*bytes.Buffer).String() }

func (e *wiRenderEnv) record(r *http.Request) {
	e.hasTop = append(e.hasTop, r.URL.Query().Has("$top"))
	e.tops = append(e.tops, r.URL.Query().Get("$top"))
}

// stubResult answers the WIQL POST with result.
func (e *wiRenderEnv) stubResult(result map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/wit/wiql"), func(r *http.Request) (*http.Response, error) {
		e.record(r)
		return httpmock.JSONResponse(result)(r)
	})
}

// stubBatch answers a workitemsbatch request with items after recording its fields.
func (e *wiRenderEnv) stubBatch(t *testing.T, items ...map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/wit/workitemsbatch"), func(r *http.Request) (*http.Response, error) {
		var body struct{ Fields []string }
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		e.batchFields = append(e.batchFields, body.Fields)
		return httpmock.JSONResponse(map[string]any{"count": len(items), "value": items})(r)
	})
}

func (e *wiRenderEnv) exec(args ...string) error {
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Browser:    e.browser,
		Config:     func() (cmdutil.Config, error) { return e.cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "web"}, nil
		},
	}
	cmd := NewCmdQuery(f)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

func wiRenderItem(id int, fields map[string]any) map[string]any {
	return map[string]any{"id": id, "rev": 1, "fields": fields}
}

func wiRenderFlat(ids ...int) map[string]any {
	items := []map[string]any{}
	for _, id := range ids {
		items = append(items, map[string]any{"id": id})
	}
	return map[string]any{"queryType": "flat", "queryResultType": "workItem", "workItems": items, "columns": []map[string]any{
		{"referenceName": "System.Id", "name": "ID"}, {"referenceName": "System.Title", "name": "Title"},
		{"referenceName": "System.AssignedTo", "name": "Assigned To"}, {"referenceName": "Custom.Risk", "name": "Risk"},
	}}
}

// wiRenderLinks returns a link result of queryType with rows [rel, source, target].
func wiRenderLinks(queryType string, rows ...[3]any) map[string]any {
	rels := []map[string]any{}
	for _, r := range rows {
		m := map[string]any{"target": map[string]any{"id": r[2]}}
		if r[1].(int) != 0 {
			m["rel"], m["source"] = r[0], map[string]any{"id": r[1]}
		}
		rels = append(rels, m)
	}
	return map[string]any{"queryType": queryType, "queryResultType": "workItemLink", "workItemRelations": rels,
		"columns": []map[string]any{{"referenceName": "System.Id", "name": "ID"}, {"referenceName": "System.Title", "name": "Title"}}}
}

// wiRenderTitles returns batch items titled "T<id>".
func wiRenderTitles(ids ...int) []map[string]any {
	var out []map[string]any
	for _, id := range ids {
		out = append(out, wiRenderItem(id, map[string]any{"System.Title": "T" + strconv.Itoa(id)}))
	}
	return out
}

var wiRenderAda = map[string]any{"displayName": "Ada Lovelace", "uniqueName": "ada@example.com", "id": "u1", "imageUrl": "x"}

func TestWIQueryRenderFlatTTY(t *testing.T) {
	e := newWIRenderEnv(t, true)
	e.stubResult(wiRenderFlat(3, 1))
	e.stubBatch(t, wiRenderItem(1, map[string]any{"System.Title": "Second", "Custom.Risk": 2.5}),
		wiRenderItem(3, map[string]any{"System.Title": "First", "System.AssignedTo": wiRenderAda, "Custom.Risk": "High"}))
	require.NoError(t, e.exec(wiRenderFlatWIQL, "-L", "5"))
	assert.Equal(t, []string{"5"}, e.tops, "flat --limit is $top")
	assert.Equal(t, [][]string{{"System.Title", "System.AssignedTo", "Custom.Risk"}}, e.batchFields)
	lines := strings.Split(strings.TrimRight(e.out.String(), "\n"), "\n")
	require.Len(t, lines, 3)
	assert.Regexp(t, `^ID\s+TITLE\s+ASSIGNED TO\s+RISK$`, lines[0])
	assert.Regexp(t, `^#3\s+First\s+Ada Lovelace\s+High$`, lines[1], "query order kept, identities by name")
	assert.Regexp(t, `^#1\s+Second\s+2.5$`, lines[2])
	assert.Empty(t, e.errOut())
}

func TestWIQueryRenderFlatTSV(t *testing.T) {
	e := newWIRenderEnv(t, false)
	e.stubResult(wiRenderFlat(3, 1))
	e.stubBatch(t, wiRenderItem(3, map[string]any{"System.Title": "First", "System.AssignedTo": wiRenderAda}))
	require.NoError(t, e.exec(wiRenderFlatWIQL, "-L", "2"))
	assert.Equal(t, "3\tFirst\tAda Lovelace\t\n1\t\t\t\n", e.out.String(), "omitted items keep their ID")
	assert.Contains(t, e.errOut(), "results reached the limit of 2; use -L to fetch more")
}

func TestWIQueryRenderFlatJSON(t *testing.T) {
	e := newWIRenderEnv(t, false)
	e.stubResult(wiRenderFlat(3))
	e.stubBatch(t, wiRenderItem(3, map[string]any{"System.Title": "First", "System.AssignedTo": wiRenderAda,
		"Custom.Risk": "High", "System.State": "Active", "System.Tags": "a; b"}))
	require.NoError(t, e.exec(wiRenderFlatWIQL, "--json", "id,title,state,assignedTo,tags,priority,url,fields,links"))
	assert.Equal(t, [][]string{{"System.Title", "System.AssignedTo", "Custom.Risk", "System.State",
		"System.Tags", "Microsoft.VSTS.Common.Priority"}}, e.batchFields, "requested known fields fetched too")
	assert.JSONEq(t, `[{"id":3,"title":"First","state":"Active",
		"assignedTo":{"id":"u1","displayName":"Ada Lovelace","uniqueName":"ada@example.com"},
		"tags":["a","b"],"priority":null,"url":"https://dev.azure.com/`+e.org+`/My%20Proj/_workitems/edit/3",
		"fields":{"Custom.Risk":"High"},"links":null}]`, e.out.String())
}

func TestWIQueryRenderIDOnly(t *testing.T) {
	e := newWIRenderEnv(t, false)
	res := wiRenderFlat(4)
	res["columns"] = []map[string]any{{"referenceName": "System.Id", "name": "ID"}}
	e.stubResult(res)
	require.NoError(t, e.exec("SELECT [System.Id] FROM WorkItems"))
	assert.Equal(t, "4\n", e.out.String(), "no batch call for an ID-only query")
}

func TestWIQueryRenderEmpty(t *testing.T) {
	t.Run("table", func(t *testing.T) {
		e := newWIRenderEnv(t, true)
		e.stubResult(wiRenderFlat())
		require.NoError(t, e.exec(wiRenderFlatWIQL))
		assert.Empty(t, e.out.String())
		assert.Contains(t, e.errOut(), "no work items match the query")
	})
	t.Run("json", func(t *testing.T) {
		e := newWIRenderEnv(t, false)
		e.stubResult(wiRenderLinks("tree"))
		require.NoError(t, e.exec(wiRenderTreeWIQL, "--json", "id"))
		assert.JSONEq(t, `[]`, e.out.String())
	})
}

// wiRenderTreeRows: 7 → 8 → 10, 7 → 9, and a second root 20.
var wiRenderTreeRows = [][3]any{
	{"", 0, 7}, {wiRenderHier, 7, 8}, {wiRenderHier, 8, 10}, {wiRenderHier, 7, 9}, {"", 0, 20},
}

func TestWIQueryRenderTreeTTY(t *testing.T) {
	e := newWIRenderEnv(t, true)
	e.stubResult(wiRenderLinks("tree", wiRenderTreeRows...))
	e.stubBatch(t, wiRenderTitles(7, 8, 9, 10, 20)...)
	require.NoError(t, e.exec(wiRenderTreeWIQL))
	assert.Equal(t, []bool{false}, e.hasTop, "link queries send no $top")
	lines := strings.Split(strings.TrimRight(e.out.String(), "\n"), "\n")
	require.Len(t, lines, 6)
	assert.Regexp(t, `^ID\s+TITLE$`, lines[0])
	assert.Regexp(t, `^#7\s+T7$`, lines[1])
	assert.Regexp(t, `^  #8\s+T8$`, lines[2])
	assert.Regexp(t, `^    #10\s+T10$`, lines[3])
	assert.Regexp(t, `^  #9\s+T9$`, lines[4])
	assert.Regexp(t, `^#20\s+T20$`, lines[5])
}

func TestWIQueryRenderTreeLimit(t *testing.T) {
	e := newWIRenderEnv(t, false)
	e.stubResult(wiRenderLinks("tree", wiRenderTreeRows...))
	e.stubBatch(t, wiRenderTitles(7, 8, 10, 9)...)
	require.NoError(t, e.exec(wiRenderTreeWIQL, "-L", "1"))
	assert.Equal(t, "\t\t7\tT7\n7\t"+wiRenderHier+"\t8\tT8\n8\t"+wiRenderHier+"\t10\tT10\n7\t"+wiRenderHier+"\t9\tT9\n",
		e.out.String(), "TSV: parent and link columns, trimmed to 1 root")
	assert.Contains(t, e.errOut(), "showing the first 1 of 2 top-level work items")
}

func TestWIQueryRenderOneHop(t *testing.T) {
	rows := [][3]any{{"", 0, 7}, {"System.LinkTypes.Related", 7, 8}, {"", 0, 8}, {"System.LinkTypes.Related", 8, 7}}
	t.Run("tty", func(t *testing.T) {
		e := newWIRenderEnv(t, true)
		e.stubResult(wiRenderLinks("oneHop", rows...))
		e.stubBatch(t, wiRenderTitles(7, 8)...)
		require.NoError(t, e.exec("SELECT [System.Id], [System.Title] FROM WorkItemLinks MODE (MayContain)"))
		lines := strings.Split(strings.TrimRight(e.out.String(), "\n"), "\n")
		require.Len(t, lines, 5, "one hop only")
		assert.Regexp(t, `^ID\s+LINK\s+TITLE$`, lines[0])
		assert.Regexp(t, `^#7\s+T7$`, lines[1])
		assert.Regexp(t, `^  #8\s+Related\s+T8$`, lines[2])
		assert.Regexp(t, `^#8\s+T8$`, lines[3])
		assert.Regexp(t, `^  #7\s+Related\s+T7$`, lines[4])
	})
	t.Run("json", func(t *testing.T) {
		e := newWIRenderEnv(t, false)
		e.stubResult(wiRenderLinks("oneHop", rows[:2]...))
		e.stubBatch(t, wiRenderTitles(7, 8)...)
		require.NoError(t, e.exec("SELECT [System.Id], [System.Title] FROM WorkItemLinks", "--json", "id,title,links"))
		assert.JSONEq(t, `[{"id":7,"title":"T7","links":[
			{"rel":"System.LinkTypes.Related","item":{"id":8,"title":"T8","links":[]}}]}]`, e.out.String())
	})
}

func TestWIQueryRenderTreeJSONNested(t *testing.T) {
	e := newWIRenderEnv(t, false)
	e.stubResult(wiRenderLinks("tree", wiRenderTreeRows[:3]...))
	e.stubBatch(t, wiRenderTitles(7, 8, 10)...)
	require.NoError(t, e.exec(wiRenderTreeWIQL, "--json", "id,links"))
	assert.JSONEq(t, `[{"id":7,"links":[{"rel":"`+wiRenderHier+`","item":{"id":8,"links":[
		{"rel":"`+wiRenderHier+`","item":{"id":10,"links":[]}}]}}]}]`, e.out.String())
}

func TestWIQueryRenderSavedTree(t *testing.T) {
	e := newWIRenderEnv(t, false)
	saved := wiQueryInputSaved("Shared Queries/Tree")
	saved["queryType"] = "tree"
	e.stubSaved(wiQueryInputID, saved)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/wiql/"+wiQueryInputID), func(r *http.Request) (*http.Response, error) {
		e.record(r)
		return httpmock.JSONResponse(wiRenderLinks("tree", wiRenderTreeRows[:2]...))(r)
	})
	e.stubBatch(t, wiRenderTitles(7, 8)...)
	require.NoError(t, e.exec(wiQueryInputID))
	assert.Equal(t, []bool{false}, e.hasTop)
	assert.Equal(t, "\t\t7\tT7\n7\t"+wiRenderHier+"\t8\tT8\n", e.out.String())
}

func TestWIQueryRender20000(t *testing.T) {
	e := newWIRenderEnv(t, false)
	ids := make([]int, maxResults)
	for i := range ids {
		ids[i] = i + 1
	}
	res := wiRenderFlat(ids...)
	res["columns"] = []map[string]any{{"referenceName": "System.Id", "name": "ID"}}
	e.stubResult(res)
	require.NoError(t, e.exec("SELECT [System.Id] FROM WorkItems", "-L", "25000"))
	assert.Contains(t, e.errOut(), "truncates query results at 20,000")
	assert.NotContains(t, e.errOut(), "limit of")
}

func TestWIQueryRenderWeb(t *testing.T) {
	t.Run("saved", func(t *testing.T) {
		e := newWIRenderEnv(t, true)
		e.stubSaved("Shared%20Queries%2FBugs", wiQueryInputSaved("Shared Queries/Bugs"))
		require.NoError(t, e.exec("Shared Queries/Bugs", "-w"))
		assert.Equal(t, []string{"https://dev.azure.com/" + e.org + "/My%20Proj/_queries/query/" + wiQueryInputID}, e.browser.URLs)
		assert.Empty(t, e.out.String())
	})
	for name, args := range map[string][]string{
		"wiql":      {"SELECT [System.Id] FROM WorkItems", "-w"},
		"wiql file": {"--wiql-file", "-", "--web"},
	} {
		t.Run(name, func(t *testing.T) {
			e := &wiRenderEnv{wiQueryInputEnv: newWIQueryInputEnv(t, false), browser: &browsertest.Browser{}}
			err := e.exec(args...)
			var fe *cmdutil.FlagError
			require.ErrorAs(t, err, &fe)
			assert.Contains(t, err.Error(), "--web works only with saved queries")
			assert.Empty(t, e.browser.URLs)
		})
	}
}

func TestWIQueryRenderBuildTreeCycle(t *testing.T) {
	roots := buildTree(&api.QueryResult{QueryType: "tree", Links: []api.WorkItemLink{
		{TargetID: 1}, {Rel: wiRenderHier, SourceID: 1, TargetID: 2}, {Rel: wiRenderHier, SourceID: 2, TargetID: 1},
	}})
	var ids []int
	walk(roots, func(n *node) { ids = append(ids, n.id) })
	assert.Equal(t, []int{1, 2, 1}, ids, "the cycle stops at the repeated ancestor")
}

func TestWIQueryRenderCell(t *testing.T) {
	assert.Equal(t, "true", cell(true))
	assert.Equal(t, "3", cell(3.0))
	assert.Equal(t, "map[x:1]", cell(map[string]any{"x": 1}))
}
