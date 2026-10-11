package list

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	wiListOrder = " ORDER BY [System.ChangedDate] DESC, [System.Id] DESC"
	// wiListOpen is the --state open clause of the Agile fixture.
	wiListOpen = "[System.State] IN ('New', 'Active', 'Resolved')"
)

type wiListEnv struct {
	org     string
	reg     *httpmock.Registry
	ios     *iostreams.IOStreams
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	browser *browsertest.Browser
	// wiqls and tops are the WIQL queries and $top values sent, in order.
	wiqls []string
	tops  []string
	// batches are the workitemsbatch request bodies, in order.
	batches []map[string]any
	// searches are the search request bodies, in order.
	searches []map[string]any
}

// newWIListEnv returns an env for its own org (the SDK caches discovery per base URL) with the
// location and Agile process metadata stubs registered.
func newWIListEnv(t *testing.T, tty bool) *wiListEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "wilist-" + strings.NewReplacer("/", "-", " ", "-", "@", "-", ",", "-").Replace(strings.ToLower(t.Name()))
	e := &wiListEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut, browser: &browsertest.Browser{}}
	t.Cleanup(func() { e.reg.Verify(t) })
	e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	e.reg.Register(httpmock.REST(http.MethodGet, org+"/My%20Proj/_apis/wit/workitemtypes"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "types"))))
	e.reg.Register(httpmock.REST(http.MethodGet, org+"/My%20Proj/_apis/wit/workitemtypecategories"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "categories"))))
	return e
}

// stubWIQL answers the next WIQL query with ids after recording it.
func (e *wiListEnv) stubWIQL(t *testing.T, ids ...int) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/wit/wiql"), func(r *http.Request) (*http.Response, error) {
		var body struct{ Query string }
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		e.wiqls, e.tops = append(e.wiqls, body.Query), append(e.tops, r.URL.Query().Get("$top"))
		items := []map[string]any{}
		for _, id := range ids {
			items = append(items, map[string]any{"id": id})
		}
		return httpmock.JSONResponse(map[string]any{"queryType": "flat", "workItems": items})(r)
	})
}

// stubBatch answers the next workitemsbatch request with items after recording it.
func (e *wiListEnv) stubBatch(t *testing.T, items ...map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/wit/workitemsbatch"), func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		e.batches = append(e.batches, body)
		return httpmock.JSONResponse(map[string]any{"count": len(items), "value": items})(r)
	})
}

// stubSearchPage answers the next search request with hits for ids and count after recording it.
func (e *wiListEnv) stubSearchPage(t *testing.T, count, infoCode int, ids ...string) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/search/workitemsearchresults"), func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		e.searches = append(e.searches, body)
		results := []map[string]any{}
		for _, id := range ids {
			results = append(results, map[string]any{"fields": map[string]string{"system.id": id}})
		}
		return httpmock.JSONResponse(map[string]any{"count": count, "infoCode": infoCode, "results": results})(r)
	})
}

// stubNodes answers GET classificationnodes/{group} with root\child of My Proj.
func (e *wiListEnv) stubNodes(group, root, child string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/classificationnodes/"+group), httpmock.JSONResponse(map[string]any{
		"id": 1, "name": "My Proj", "path": `\My Proj\` + root,
		"children": []map[string]any{{"id": 2, "name": child, "path": `\My Proj\` + root + `\` + child}},
	}))
}

func (e *wiListEnv) run(t *testing.T, args ...string) error {
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
		Browser:  e.browser,
		Prompter: &cmdutil.Prompter{IO: e.ios, Accessible: true},
	}
	cmd := NewCmdList(f)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

func wiListIdentity(name, email string) map[string]any {
	return map[string]any{"displayName": name, "uniqueName": email, "id": "a0000000-0000-0000-0000-000000000001",
		"imageUrl": "https://x/avatar", "_links": map[string]any{}}
}

func wiListItem(id int, title string) map[string]any {
	return map[string]any{"id": id, "rev": 3, "fields": map[string]any{
		"System.Id": id, "System.WorkItemType": "Bug", "System.Title": title, "System.State": "Active",
		"System.Reason": "Approved", "System.AssignedTo": wiListIdentity("Ann Lee", "ann@contoso.com"),
		"System.CreatedBy":   wiListIdentity("Bob Roe", "bob@contoso.com"),
		"System.CreatedDate": "2026-10-01T10:00:00Z", "System.ChangedDate": "2026-10-09T10:00:00.5Z",
		"System.AreaPath": "My Proj", "System.IterationPath": `My Proj\Sprint 1`, "System.Tags": "ui; urgent",
		"Microsoft.VSTS.Common.Priority": 2, "System.Parent": 7,
	}}
}

func TestListDefaults(t *testing.T) {
	e := newWIListEnv(t, false)
	e.stubWIQL(t, 2, 1)
	e.stubBatch(t, wiListItem(1, "Older"), wiListItem(2, "Newer"))
	require.NoError(t, e.run(t))

	assert.Equal(t, []string{"SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = @project" +
		" AND [System.State] IN ('New', 'Active', 'Resolved')" + wiListOrder}, e.wiqls, "default --state open, newest change first")
	assert.Equal(t, []string{"30"}, e.tops, "default --limit 30")
	require.Len(t, e.batches, 1)
	assert.Equal(t, []any{float64(2), float64(1)}, e.batches[0]["ids"])
	assert.Contains(t, e.batches[0]["fields"], "System.Title", "only the listed fields are fetched")
	assert.Equal(t, "omit", e.batches[0]["errorPolicy"])
	assert.Equal(t, "2\tBug\tActive\tNewer\tAnn Lee\tui, urgent\t2026-10-09T10:00:00Z\n"+
		"1\tBug\tActive\tOlder\tAnn Lee\tui, urgent\t2026-10-09T10:00:00Z\n", e.out.String(), "TSV in WIQL order, RFC3339 times")
	assert.Empty(t, e.errOut.String())
}

func TestListFilters(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		where string
		setup func(*wiListEnv)
	}{
		{"type and closed", []string{"--type", "bug", "-s", "closed"},
			"[System.WorkItemType] = 'Bug' AND [System.State] = 'Closed'", nil},
		{"several types, all states", []string{"--type", "bug,task", "-s", "all"},
			"[System.WorkItemType] IN ('Bug', 'Task')", nil},
		{"state name", []string{"-s", "resolved"}, "[System.State] = 'Resolved'", nil},
		{"assignee me, author none", []string{"-a", "@me", "-A", "none"},
			wiListOpen + " AND [System.AssignedTo] = @Me AND [System.CreatedBy] = ''", nil},
		{"tags all must match", []string{"--tag", "x", "-l", "y,O'Brien"},
			wiListOpen + " AND [System.Tags] CONTAINS 'x' AND [System.Tags] CONTAINS 'y' AND [System.Tags] CONTAINS 'O''Brien'", nil},
		{"area and iteration", []string{"--area", "web", "--iteration", "Sprint 1"},
			wiListOpen + ` AND [System.AreaPath] UNDER 'My Proj\Web' AND [System.IterationPath] UNDER 'My Proj\Sprint 1'`, func(e *wiListEnv) {
				e.stubNodes("areas", "Area", "Web")
				e.stubNodes("iterations", "Iteration", "Sprint 1")
			}},
		{"current iteration", []string{"--iteration", "@current", "--team", "Web Team"},
			wiListOpen + ` AND [System.IterationPath] UNDER 'My Proj\Sprint 3'`, func(e *wiListEnv) {
				e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/Web%20Team/_apis/work/teamsettings/iterations"),
					httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{{"path": `My Proj\Sprint 3`}}}))
			}},
		{"author by name", []string{"-A", "Ann Lee"}, wiListOpen + " AND [System.CreatedBy] = 'ann@contoso.com'", func(e *wiListEnv) {
			e.reg.StubIMS(e.org)
			e.reg.Register(func(r *http.Request) bool {
				return r.URL.Host == "vssps.dev.azure.com" && r.URL.Query().Get("filterValue") == "Ann Lee"
			}, httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{{
				"id": "a0000000-0000-0000-0000-000000000004", "providerDisplayName": "Ann Lee",
				"properties": map[string]any{"Account": map[string]any{"$type": "System.String", "$value": "ann@contoso.com"}},
			}}}))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newWIListEnv(t, false)
			if tt.setup != nil {
				tt.setup(e)
			}
			e.stubWIQL(t)
			require.NoError(t, e.run(t, append(tt.args, "-L", "5")...))
			require.Len(t, e.wiqls, 1)
			assert.Equal(t, "SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = @project AND "+tt.where+wiListOrder, e.wiqls[0])
			assert.Equal(t, []string{"5"}, e.tops)
		})
	}
}

func TestListFlagErrors(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"-L", "0"}, "invalid value for --limit: 0"},
		{[]string{"-S", "x", "-w"}, "none of the others can be"},
	} {
		e := newWIListEnv(t, false)
		e.reg = &httpmock.Registry{} // no requests
		err := e.run(t, tt.args...)
		require.Error(t, err)
		assert.Contains(t, err.Error(), tt.want)
	}
}

func TestListQueryTooLarge(t *testing.T) {
	e := newWIListEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/wit/wiql"), httpmock.StatusJSONResponse(http.StatusBadRequest,
		map[string]any{"message": "VS402337: The number of work items returned exceeds the size limit of 20000."}))
	err := e.run(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "narrow the filters")
}

func TestListUnknownType(t *testing.T) {
	e := newWIListEnv(t, false)
	err := e.run(t, "--type", "widget")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown work item type "widget"`)
}

func TestListTTY(t *testing.T) {
	e := newWIListEnv(t, true)
	e.stubWIQL(t, 2)
	item := wiListItem(2, "Crash on save")
	delete(item["fields"].(map[string]any), "System.AssignedTo")
	e.stubBatch(t, item)
	require.NoError(t, e.run(t))
	out := e.out.String()
	assert.Contains(t, out, "Showing 1 work item in My Proj")
	assert.Regexp(t, `ID\s+TYPE\s+STATE\s+TITLE\s+ASSIGNEE\s+TAGS\s+UPDATED`, out)
	assert.Regexp(t, `#2\s+Bug\s+Active\s+Crash on save\s+ui, urgent\s+.*ago`, out)
}

func TestListEmpty(t *testing.T) {
	for _, tty := range []bool{true, false} {
		t.Run(strconv.FormatBool(tty), func(t *testing.T) {
			e := newWIListEnv(t, tty)
			e.stubWIQL(t)
			require.NoError(t, e.run(t))
			assert.Empty(t, e.out.String())
			assert.Equal(t, "no work items match your search\n", e.errOut.String())
		})
	}
}

func TestListJSON(t *testing.T) {
	e := newWIListEnv(t, false)
	e.stubWIQL(t, 2)
	e.stubBatch(t, wiListItem(2, "Crash"))
	require.NoError(t, e.run(t, "--json", strings.Join(jsonFields, ",")))
	assert.Nil(t, e.batches[0]["fields"], "--json fields fetches every field")

	var got []map[string]any
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &got))
	require.Len(t, got, 1)
	raw := got[0]["fields"].(map[string]any)
	assert.Equal(t, "Crash", raw["System.Title"])
	delete(got[0], "fields")
	ann := map[string]any{"id": "a0000000-0000-0000-0000-000000000001", "displayName": "Ann Lee", "uniqueName": "ann@contoso.com"}
	bob := map[string]any{"id": "a0000000-0000-0000-0000-000000000001", "displayName": "Bob Roe", "uniqueName": "bob@contoso.com"}
	assert.Equal(t, map[string]any{
		"id": float64(2), "type": "Bug", "title": "Crash", "state": "Active", "reason": "Approved",
		"assignedTo": ann, "createdBy": bob, "createdDate": "2026-10-01T10:00:00Z", "changedDate": "2026-10-09T10:00:00.5Z",
		"areaPath": "My Proj", "iterationPath": `My Proj\Sprint 1`, "tags": []any{"ui", "urgent"},
		"priority": float64(2), "parent": float64(7),
		"url": "https://dev.azure.com/" + e.org + "/My%20Proj/_workitems/edit/2",
	}, got[0])
}

func TestListJSONEmptyValues(t *testing.T) {
	e := newWIListEnv(t, false)
	e.stubWIQL(t, 2)
	e.stubBatch(t, map[string]any{"id": 2, "fields": map[string]any{"System.Title": "Bare"}})
	require.NoError(t, e.run(t, "--json", "id,assignedTo,tags,priority,parent"))
	assert.JSONEq(t, `[{"id":2,"assignedTo":null,"tags":[],"priority":null,"parent":null}]`, e.out.String())
	assert.NotNil(t, e.batches[0]["fields"], "without the fields field only the listed fields are fetched")
}

func TestListJSONEmpty(t *testing.T) {
	e := newWIListEnv(t, false)
	e.stubWIQL(t)
	require.NoError(t, e.run(t, "--json", "id"))
	assert.Equal(t, "[]\n", e.out.String())
	assert.Empty(t, e.errOut.String())
}

// wiListHitIDs returns n search hit IDs starting at from, as strings.
func wiListHitIDs(from, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = strconv.Itoa(from + i)
	}
	return out
}

func TestListSearch(t *testing.T) {
	e := newWIListEnv(t, false)
	e.reg.StubSearch(e.org)
	// Page 1: 1000 hits of which the WIQL keeps 1; page 2: a full page again, the WIQL keeps 2
	// and reaches --limit 3, so no third page is read.
	e.stubSearchPage(t, 5000, 0, wiListHitIDs(1, 1000)...)
	e.stubWIQL(t, 10)
	e.stubSearchPage(t, 5000, 0, append(wiListHitIDs(1001, 999), "not-a-number")...)
	e.stubWIQL(t, 1500, 1200)
	e.stubBatch(t, wiListItem(10, "a"), wiListItem(1500, "b"), wiListItem(1200, "c"))
	require.NoError(t, e.run(t, "-S", "crash a:@me", "-a", "@me", "-L", "3"))

	require.Len(t, e.searches, 2)
	assert.Equal(t, map[string]any{
		"searchText": "crash a:@me", "$skip": float64(0), "$top": float64(1000),
		"filters":  map[string]any{"System.TeamProject": []any{"My Proj"}},
		"$orderBy": []any{map[string]any{"field": "system.changeddate", "sortOrder": "DESC"}},
	}, e.searches[0])
	assert.Equal(t, float64(1000), e.searches[1]["$skip"], "$skip moves by the number of results")

	require.Len(t, e.wiqls, 2)
	prefix := "SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = @project" +
		" AND [System.State] IN ('New', 'Active', 'Resolved') AND [System.AssignedTo] = @Me AND [System.Id] IN ("
	assert.Equal(t, prefix+strings.Join(wiListHitIDs(1, 1000), ", ")+")"+wiListOrder, e.wiqls[0])
	assert.Equal(t, prefix+strings.Join(wiListHitIDs(1001, 999), ", ")+")"+wiListOrder, e.wiqls[1], "unparsable IDs are skipped")
	assert.Equal(t, []string{"3", "2"}, e.tops, "$top is what is left to find")
	assert.Equal(t, []any{float64(10), float64(1500), float64(1200)}, e.batches[0]["ids"])
	assert.Empty(t, e.errOut.String())
}

func TestListSearchStops(t *testing.T) {
	t.Run("short page", func(t *testing.T) {
		e := newWIListEnv(t, false)
		e.reg.StubSearch(e.org)
		e.stubSearchPage(t, 5000, 0, "4", "5")
		e.stubWIQL(t, 4)
		e.stubBatch(t, wiListItem(4, "a"))
		require.NoError(t, e.run(t, "-S", "x"))
		assert.Len(t, e.searches, 1)
		assert.Equal(t, "SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = @project AND "+wiListOpen+
			" AND [System.Id] IN (4, 5)"+wiListOrder, e.wiqls[0])
	})
	t.Run("count reached", func(t *testing.T) {
		e := newWIListEnv(t, false)
		e.reg.StubSearch(e.org)
		e.stubSearchPage(t, 1000, 0, wiListHitIDs(1, 1000)...)
		e.stubWIQL(t)
		require.NoError(t, e.run(t, "-S", "x"))
		assert.Len(t, e.searches, 1)
		assert.Equal(t, "no work items match your search\n", e.errOut.String())
	})
	t.Run("no hits", func(t *testing.T) {
		e := newWIListEnv(t, false)
		e.reg.StubSearch(e.org)
		e.stubSearchPage(t, 0, 0)
		require.NoError(t, e.run(t, "-S", "x"))
		assert.Empty(t, e.wiqls, "no WIQL without hits")
		assert.Equal(t, "no work items match your search\n", e.errOut.String())
	})
}

func TestListSearchInfoCode(t *testing.T) {
	for code, want := range map[int]string{
		1:  "the search index is being built",
		8:  "work item search trimmed the results",
		11: "some work items are not accessible",
		3:  "work item search returned info code 3",
	} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			e := newWIListEnv(t, false)
			e.reg.StubSearch(e.org)
			e.stubSearchPage(t, 1, code, "4")
			e.stubWIQL(t, 4)
			e.stubBatch(t, wiListItem(4, "a"))
			require.NoError(t, e.run(t, "-S", "x"))
			assert.Contains(t, e.errOut.String(), "! "+want)
			assert.Equal(t, 1, strings.Count(e.errOut.String(), "!"))
		})
	}
}

func TestListSearchUnavailable(t *testing.T) {
	e := newWIListEnv(t, false)
	e.reg.StubResourceAreas(e.org) // on-prem: no search area
	err := e.run(t, "-S", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "work item search not available")
}

func TestListWeb(t *testing.T) {
	e := newWIListEnv(t, false)
	require.NoError(t, e.run(t, "-w", "--type", "bug", "-l", "a b+c"))
	wiql := "SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = @project AND [System.WorkItemType] = 'Bug'" +
		" AND [System.State] IN ('New', 'Active', 'Resolved') AND [System.Tags] CONTAINS 'a b+c'" + wiListOrder
	want := "https://dev.azure.com/" + e.org + "/My%20Proj/_queries/query/?wiql=" + adocontext.QueryEscape(wiql)
	assert.Equal(t, want, e.browser.URLs[0])
	assert.Contains(t, want, "%20b%2Bc", "spaces as %20, + escaped")
	u, err := url.Parse(e.browser.URLs[0])
	require.NoError(t, err)
	assert.Equal(t, wiql, u.Query().Get("wiql"))
	assert.Empty(t, e.wiqls, "nothing is queried")
}

func TestListWebTooLong(t *testing.T) {
	e := newWIListEnv(t, false)
	var tags []string
	for i := range 300 {
		tags = append(tags, fmt.Sprintf("tag-number-%03d", i))
	}
	require.NoError(t, e.run(t, "-w", "-l", strings.Join(tags, ",")))
	assert.Equal(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_workitems", e.browser.URLs[0])
	assert.Contains(t, e.errOut.String(), "too long")
}
