package view

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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

type wiViewEnv struct {
	org     string
	reg     *httpmock.Registry
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	ios     *iostreams.IOStreams
	browser *browsertest.Browser
}

// newWIViewEnv returns an env for its own org (the SDK caches discovery per base URL) with a
// fixed clock.
func newWIViewEnv(t *testing.T, tty bool) *wiViewEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	old := now
	now = func() time.Time { return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = old })
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	org := "wiview-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	e := &wiViewEnv{org: org, reg: &httpmock.Registry{}, out: out, errOut: errOut, ios: ios, browser: &browsertest.Browser{}}
	e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	return e
}

func (e *wiViewEnv) run(t *testing.T, args ...string) error {
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
	cmd := NewCmdView(f)
	cmd.SetArgs(args)
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

func (e *wiViewEnv) wiURL(id string) string {
	return "https://dev.azure.com/" + e.org + "/_apis/wit/workItems/" + id
}

// stubItem answers GET work item 42 (with relations) in project My Proj.
func (e *wiViewEnv) stubItem(t *testing.T) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitems/42"), func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "relations", r.URL.Query().Get("$expand"))
		return httpmock.JSONResponse(map[string]any{
			"id": 42, "rev": 3,
			"fields": map[string]any{
				"System.WorkItemType": "User Story", "System.Title": "Export to CSV", "System.State": "Active",
				"System.Reason":      "Implementation started",
				"System.AssignedTo":  map[string]any{"displayName": "Jane Doe", "uniqueName": "jane@example.com", "id": "j1"},
				"System.CreatedBy":   map[string]any{"displayName": "Bob", "uniqueName": "bob@example.com"},
				"System.ChangedBy":   map[string]any{"displayName": "Jane Doe", "uniqueName": "jane@example.com"},
				"System.CreatedDate": "2026-10-09T12:00:00Z", "System.ChangedDate": "2026-10-11T11:00:00Z",
				"System.AreaPath": `My Proj\Web`, "System.IterationPath": `My Proj\Sprint 1`,
				"System.Tags": "ui; export", "Microsoft.VSTS.Common.Priority": 2,
				"System.Description": "Some **bold** text",
			},
			"multilineFieldsFormat": map[string]any{"System.Description": "markdown"},
			"relations": []map[string]any{
				{"rel": "System.LinkTypes.Hierarchy-Reverse", "url": e.wiURL("7"), "attributes": map[string]any{"name": "Parent"}},
				{"rel": "System.LinkTypes.Hierarchy-Forward", "url": e.wiURL("43"), "attributes": map[string]any{"name": "Child"}},
				{"rel": "System.LinkTypes.Related", "url": e.wiURL("44"), "attributes": map[string]any{"name": "Related"}},
				{"rel": "System.LinkTypes.Dependency-Forward", "url": e.wiURL("45"), "attributes": map[string]any{"name": "Successor"}},
				{"rel": "ArtifactLink", "url": "vstfs:///Git/PullRequestId/p1%2Fr1%2F45", "attributes": map[string]any{"name": "Pull Request"}},
				{"rel": "ArtifactLink", "url": "vstfs:///Git/Commit/p1%2Fr1%2F0123456789abcdef0123456789abcdef01234567", "attributes": map[string]any{"name": "Fixed in Commit"}},
				{"rel": "ArtifactLink", "url": "vstfs:///Git/Ref/p1%2Fr1%2FGBfeature%2Fcsv", "attributes": map[string]any{"name": "Branch"}},
				{"rel": "Hyperlink", "url": "https://example.com/spec", "attributes": map[string]any{"comment": "spec"}},
				{"rel": "AttachedFile", "url": "https://dev.azure.com/x/_apis/wit/attachments/abc", "attributes": map[string]any{"name": "log.txt"}},
				{"rel": "ArtifactLink", "url": "vstfs:///Build/Build/9", "attributes": map[string]any{"name": "Build"}},
			},
		})(r)
	})
}

// stubLinked answers the batch fetch of the linked work items (45 is inaccessible).
func (e *wiViewEnv) stubLinked(t *testing.T) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/wit/workitemsbatch"), func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &body))
		assert.Equal(t, []any{float64(7), float64(43), float64(44), float64(45)}, body["ids"])
		assert.Equal(t, []any{"System.WorkItemType", "System.Title", "System.State"}, body["fields"])
		item := func(id int, typ, title, state string) map[string]any {
			return map[string]any{"id": id, "fields": map[string]any{"System.WorkItemType": typ, "System.Title": title, "System.State": state}}
		}
		return httpmock.JSONResponse(map[string]any{"count": 3, "value": []any{
			item(7, "Feature", "Reporting", "Active"), item(43, "Task", "Write exporter", "New"), item(44, "Bug", "CSV quoting", "Closed"), nil,
		}})(r)
	})
}

// stubComments answers two pages of comments of work item 42, oldest first.
func (e *wiViewEnv) stubComments(t *testing.T) {
	path := e.org + "/My%20Proj/_apis/wit/workitems/42/comments"
	e.reg.Register(httpmock.REST(http.MethodGet, path), func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "asc", r.URL.Query().Get("order"))
		assert.Empty(t, r.URL.Query().Get("continuationToken"))
		return httpmock.StringResponse(`{"continuationToken":"next","comments":[
			{"id":1,"text":"<div>First <b>html</b></div>","format":"html","createdBy":{"displayName":"Bob"},"createdDate":"2026-10-10T12:00:00Z","url":"u1"},
			{"id":2,"text":"gone","isDeleted":true,"createdBy":{"displayName":"Bob"}}]}`)(r)
	})
	e.reg.Register(httpmock.REST(http.MethodGet, path), func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "next", r.URL.Query().Get("continuationToken"))
		return httpmock.StringResponse(`{"comments":[
			{"id":3,"text":"Second *md*","format":"markdown","createdBy":{"displayName":"Jane Doe"},"createdDate":"2026-10-11T10:00:00Z","url":"u3"}]}`)(r)
	})
}

func (e *wiViewEnv) stubUpdates() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitems/42/updates"), httpmock.StringResponse(`{"count":2,"value":[
		{"id":1,"rev":1,"revisedBy":{"displayName":"Bob"},"revisedDate":"2026-10-11T11:00:00Z",
		 "fields":{"System.Title":{"newValue":"Export"},"System.State":{"newValue":"New"},"System.ChangedDate":{"newValue":"2026-10-09T12:00:00Z"}}},
		{"id":2,"rev":2,"revisedBy":{"displayName":"Jane Doe"},"revisedDate":"9999-01-01T00:00:00Z",
		 "fields":{"System.State":{"oldValue":"New","newValue":"Active"},"System.Rev":{"oldValue":1,"newValue":2},
		  "System.ChangedDate":{"oldValue":"2026-10-09T12:00:00Z","newValue":"2026-10-11T11:00:00Z"},
		  "System.AssignedTo":{"newValue":{"displayName":"Jane Doe"}}},
		 "relations":{"added":[{"rel":"System.LinkTypes.Related","url":"`+e.wiURL("44")+`","attributes":{"name":"Related"}}]}}]}`))
}

func TestWIViewTTY(t *testing.T) {
	e := newWIViewEnv(t, true)
	e.stubItem(t)
	e.stubLinked(t)

	require.NoError(t, e.run(t, "#42"))
	e.reg.Verify(t)
	out := e.out.String()
	for _, want := range []string{
		"Export to CSV #42\n",
		"User Story • Active (Implementation started) • opened by Bob about 2 days ago • changed by Jane Doe about 1 hour ago\n",
		"Assignee: Jane Doe\nArea: My Proj\\Web\nIteration: My Proj\\Sprint 1\nPriority: 2\nTags: ui, export\n",
		"Parent\n  #7 Feature: Reporting (Active)\n\n",
		"Children\n  #43 Task: Write exporter (New)\n\n",
		"Related\n  #44 Bug: CSV quoting (Closed)\n\n",
		"Other links\n  #45 [Successor]\n\n",
		"Pull requests\n  !45\n\n",
		"Commits\n  01234567\n\n",
		"Branches\n  feature/csv\n\n",
		"Hyperlinks\n  https://example.com/spec - spec\n\n",
		"Attachments\n  log.txt\n\n",
		"Other\n  vstfs:///Build/Build/9\n\n",
		"View this work item on Azure DevOps: https://dev.azure.com/" + e.org + "/My%20Proj/_workitems/edit/42\n",
	} {
		assert.Contains(t, out, want)
	}
	// Markdown is rendered with glamour (notty style without colour: indented, markers kept).
	assert.Contains(t, out, "\n  Some **bold** text ")
	assert.NotContains(t, out, "Comments")
	assert.NotContains(t, out, "History")
}

func TestWIViewRaw(t *testing.T) {
	e := newWIViewEnv(t, false)
	e.stubItem(t)
	e.stubLinked(t)

	require.NoError(t, e.run(t, "42"))
	e.reg.Verify(t)
	out := e.out.String()
	assert.True(t, strings.HasPrefix(out, "title:\tExport to CSV\ntype:\tUser Story\nstate:\tActive\nreason:\tImplementation started\n"+
		"assignee:\tJane Doe\narea:\tMy Proj\\Web\niteration:\tMy Proj\\Sprint 1\npriority:\t2\ntags:\tui, export\n"+
		"created by:\tBob\ncreated:\t2026-10-09T12:00:00Z\nchanged by:\tJane Doe\nchanged:\t2026-10-11T11:00:00Z\nid:\t42\n"), out)
	assert.Contains(t, out, "parent:\t#7 Feature: Reporting (Active)\nchild:\t#43 Task: Write exporter (New)\n")
	assert.Contains(t, out, "pullRequest:\t!45\n")
	assert.True(t, strings.HasSuffix(out, "--\nSome **bold** text\n"), out)
}

func TestWIViewHTMLDescriptionAndReproSteps(t *testing.T) {
	e := newWIViewEnv(t, true)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitems/5"), httpmock.JSONResponse(map[string]any{
		"id": 5, "fields": map[string]any{
			"System.WorkItemType": "Bug", "System.Title": "Crash", "System.State": "New",
			"Microsoft.VSTS.TCM.ReproSteps": "<div>Open</div><div>Save</div>",
		},
	}))

	require.NoError(t, e.run(t, "5"))
	e.reg.Verify(t)
	out := e.out.String()
	assert.Contains(t, out, "\nOpen\nSave\n\n")
	assert.Contains(t, out, "Assignee: not assigned\n")
	assert.NotContains(t, out, "Priority")
}

func TestWIViewNoDescription(t *testing.T) {
	e := newWIViewEnv(t, true)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitems/5"),
		httpmock.JSONResponse(map[string]any{"id": 5, "fields": map[string]any{"System.Title": "T"}}))

	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitems/5/comments"), httpmock.StringResponse(`{"comments":[]}`))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitems/5/updates"), httpmock.StringResponse(`{"value":[]}`))

	require.NoError(t, e.run(t, "5", "-c", "--history"))
	e.reg.Verify(t)
	assert.Contains(t, e.out.String(), "No description provided")
	assert.Contains(t, e.out.String(), "No comments")
}

func TestWIViewComments(t *testing.T) {
	e := newWIViewEnv(t, true)
	e.stubItem(t)
	e.stubLinked(t)
	e.stubComments(t)

	require.NoError(t, e.run(t, "42", "-c"))
	out := e.out.String()
	assert.Contains(t, out, "Comments (2)\n\nBob • about 1 day ago\n  First html\n\nJane Doe • about 2 hours ago\n")
	assert.Contains(t, out, "    Second *md* ")
	assert.NotContains(t, out, "gone")
	assert.Less(t, strings.Index(out, "First"), strings.Index(out, "Second"))
}

func TestWIViewCommentsRaw(t *testing.T) {
	e := newWIViewEnv(t, false)
	e.stubItem(t)
	e.stubLinked(t)
	e.stubComments(t)

	require.NoError(t, e.run(t, "42", "--comments"))
	assert.Contains(t, e.out.String(), "--\nauthor:\tBob\ncreated:\t2026-10-10T12:00:00Z\n--\n<div>First <b>html</b></div>\n"+
		"--\nauthor:\tJane Doe\ncreated:\t2026-10-11T10:00:00Z\n--\nSecond *md*\n")
}

func TestWIViewHistory(t *testing.T) {
	e := newWIViewEnv(t, true)
	e.stubItem(t)
	e.stubLinked(t)
	e.stubUpdates()

	require.NoError(t, e.run(t, "42", "--history"))
	e.reg.Verify(t)
	assert.Contains(t, e.out.String(), "History\n\nRev 1 • Bob • about 2 days ago\n  Created\n\n"+
		"Rev 2 • Jane Doe • about 1 hour ago\n  AssignedTo: (none) → Jane Doe\n  State: New → Active\n  + Related #44\n\n")
}

func TestWIViewHistoryRaw(t *testing.T) {
	e := newWIViewEnv(t, false)
	e.stubItem(t)
	e.stubLinked(t)
	e.stubUpdates()

	require.NoError(t, e.run(t, "42", "--history"))
	assert.Contains(t, e.out.String(), "--\nrev:\t2\nrevised by:\tJane Doe\nrevised:\t2026-10-11T11:00:00Z\n"+
		"AssignedTo: (none) → Jane Doe\nState: New → Active\n+ Related #44\n")
}

func TestWIViewJSON(t *testing.T) {
	e := newWIViewEnv(t, false)
	e.stubItem(t)
	e.stubLinked(t)

	// No comments or updates are fetched: no stubs are registered for them.
	require.NoError(t, e.run(t, "42", "--json", "id,type,title,state,reason,assignedTo,priority,parent,tags,url,description,relations"))
	e.reg.Verify(t)
	var got map[string]any
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &got))
	assert.Equal(t, float64(42), got["id"])
	assert.Equal(t, "User Story", got["type"])
	assert.Equal(t, "Implementation started", got["reason"])
	assert.Equal(t, map[string]any{"id": "j1", "displayName": "Jane Doe", "uniqueName": "jane@example.com"}, got["assignedTo"])
	assert.Equal(t, float64(2), got["priority"])
	assert.Equal(t, float64(7), got["parent"])
	assert.Equal(t, []any{"ui", "export"}, got["tags"])
	assert.Equal(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_workitems/edit/42", got["url"])
	assert.Equal(t, "Some **bold** text", got["description"])
	rels := got["relations"].([]any)
	require.Len(t, rels, 10)
	assert.Equal(t, map[string]any{
		"group": "parent", "rel": "System.LinkTypes.Hierarchy-Reverse", "name": "Parent", "url": e.wiURL("7"),
		"workItem": map[string]any{"id": float64(7), "type": "Feature", "title": "Reporting", "state": "Active"},
	}, rels[0])
	assert.Equal(t, map[string]any{
		"group": "pullRequest", "rel": "ArtifactLink", "name": "Pull Request", "url": "vstfs:///Git/PullRequestId/p1%2Fr1%2F45", "workItem": nil,
	}, rels[4])
}

func TestWIViewJSONLazy(t *testing.T) {
	e := newWIViewEnv(t, false)
	e.stubItem(t)
	e.stubComments(t)
	e.stubUpdates()

	// relations not requested: no batch call for linked titles.
	require.NoError(t, e.run(t, "42", "--json", "comments,history,fields"))
	e.reg.Verify(t)
	var got struct {
		Comments []map[string]any `json:"comments"`
		History  []map[string]any `json:"history"`
		Fields   map[string]any   `json:"fields"`
	}
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &got))
	require.Len(t, got.Comments, 2)
	assert.Equal(t, map[string]any{
		"id": float64(1), "author": map[string]any{"displayName": "Bob", "uniqueName": ""}, "body": "<div>First <b>html</b></div>",
		"format": "html", "createdDate": "2026-10-10T12:00:00Z", "url": "u1",
	}, got.Comments[0])
	require.Len(t, got.History, 2)
	assert.Equal(t, map[string]any{"oldValue": "New", "newValue": "Active"}, got.History[1]["fields"].(map[string]any)["System.State"])
	assert.Equal(t, "Export to CSV", got.Fields["System.Title"])
}

func TestWIViewWeb(t *testing.T) {
	e := newWIViewEnv(t, true)

	require.NoError(t, e.run(t, "42", "-w"))
	assert.Empty(t, e.reg.Requests)
	assert.Equal(t, []string{"https://dev.azure.com/" + e.org + "/My%20Proj/_workitems/edit/42"}, e.browser.URLs)
	assert.Contains(t, e.errOut.String(), "Opening https://dev.azure.com/"+e.org+"/My%20Proj/_workitems/edit/42 in your browser.")
}

func TestWIViewURLRef(t *testing.T) {
	e := newWIViewEnv(t, false)

	require.NoError(t, e.run(t, "https://dev.azure.com/other/Other%20Proj/_workitems/edit/9", "--web"))
	assert.Equal(t, []string{"https://dev.azure.com/other/Other%20Proj/_workitems/edit/9"}, e.browser.URLs)
}

func TestWIViewErrors(t *testing.T) {
	e := newWIViewEnv(t, false)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitems/404"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401232: Work item 404 does not exist", "typeKey": "WorkItemUnauthorizedAccessException"}))

	err := e.run(t, "404")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TF401232")

	err = e.run(t, "abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid work item reference "abc"`)

	err = e.run(t, "1", "--web", "--json", "id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot use `--web` with `--json`")
}
