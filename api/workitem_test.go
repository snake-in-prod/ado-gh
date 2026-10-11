package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWITClient returns a WorkItemClient for project "proj" of org; every test needs its own
// org because the SDK caches location discovery per base URL.
func newWITClient(t *testing.T, org string) (*WorkItemClient, *httpmock.Registry) {
	t.Helper()
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.WorkItemLocations...)
	t.Cleanup(func() { reg.Verify(t) })
	return NewWorkItemClient(NewHTTPClient(ClientOptions{Transport: reg}), "https://dev.azure.com/"+org, "proj"), reg
}

func witJSONBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
	return body
}

func TestNewWorkItemClientClients(t *testing.T) {
	c := NewWorkItemClient(http.DefaultClient, "https://dev.azure.com/o", "proj")
	assert.NotNil(t, c.Wit)
	assert.NotNil(t, c.Work)
	assert.NotNil(t, c.Core)
	assert.NotNil(t, c.SDK)
}

func TestWorkItemClient_WorkItem(t *testing.T) {
	org := "wigetorg"
	c, reg := newWITClient(t, org)
	var expand string
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitems/5"), func(r *http.Request) (*http.Response, error) {
		expand = r.URL.Query().Get("$expand")
		return httpmock.StringResponse(`{
			"id": 5, "rev": 3, "url": "https://dev.azure.com/o/_apis/wit/workItems/5",
			"fields": {"System.Title": "Crash", "System.AssignedTo": {"displayName": "Ann"}},
			"relations": [{"rel": "System.LinkTypes.Hierarchy-Reverse", "url": "https://dev.azure.com/o/_apis/wit/workItems/1", "attributes": {"isLocked": false}}],
			"multilineFieldsFormat": {"System.Description": "Markdown"}
		}`)(r)
	})
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitems/6"), httpmock.StringResponse(`{"id": 6, "rev": 1, "fields": {}}`))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitems/7"), httpmock.StatusJSONResponse(http.StatusNotFound,
		map[string]any{"message": "TF401232: Work item 7 does not exist, or you do not have permissions to read it.", "typeKey": "WorkItemUnauthorizedAccessException"}))
	ctx := context.Background()

	wi, err := c.WorkItem(ctx, 5, true)
	require.NoError(t, err)
	assert.Equal(t, "relations", expand)
	assert.Equal(t, &WorkItem{
		ID: 5, Rev: 3, URL: "https://dev.azure.com/o/_apis/wit/workItems/5",
		Fields: map[string]any{"System.Title": "Crash", "System.AssignedTo": map[string]any{"displayName": "Ann"}},
		Relations: []WorkItemRelation{{
			Rel: "System.LinkTypes.Hierarchy-Reverse", URL: "https://dev.azure.com/o/_apis/wit/workItems/1",
			Attributes: map[string]any{"isLocked": false},
		}},
		MultilineFieldsFormat: map[string]string{"System.Description": "Markdown"},
	}, wi)

	wi, err = c.WorkItem(ctx, 6, false)
	require.NoError(t, err)
	assert.Nil(t, wi.MultilineFieldsFormat, "missing key decodes as nil (all HTML)")

	_, err = c.WorkItem(ctx, 7, false)
	assert.True(t, IsNotFound(err))
	assert.ErrorContains(t, err, "TF401232")
}

func TestWorkItemClient_WorkItems(t *testing.T) {
	org := "wibatchorg"
	c, reg := newWITClient(t, org)
	ids := make([]int, 450)
	for i := range ids {
		ids[i] = 1000 - i // WIQL order: not sorted ascending
	}
	var bodies []map[string]any
	for range 3 {
		reg.Register(httpmock.REST(http.MethodPost, org+"/proj/_apis/wit/workitemsbatch"), func(r *http.Request) (*http.Response, error) {
			body := witJSONBody(t, r)
			bodies = append(bodies, body)
			// Answer in reverse order with a null entry for an omitted (deleted) id.
			var items []any
			req := body["ids"].([]any)
			for i := len(req) - 1; i >= 0; i-- {
				id := int(req[i].(float64))
				if id == 990 {
					items = append(items, nil)
					continue
				}
				items = append(items, map[string]any{"id": id, "rev": 1, "fields": map[string]any{"System.Title": fmt.Sprint("t", id)}})
			}
			return httpmock.JSONResponse(map[string]any{"count": len(items), "value": items})(r)
		})
	}

	got, err := c.WorkItems(context.Background(), ids, []string{"System.Id", "System.Title"}, false)
	require.NoError(t, err)
	require.Len(t, bodies, 3)
	for i, n := range []int{200, 200, 50} {
		assert.Len(t, bodies[i]["ids"], n)
		assert.Equal(t, "omit", bodies[i]["errorPolicy"])
		assert.Equal(t, []any{"System.Id", "System.Title"}, bodies[i]["fields"])
		assert.NotContains(t, bodies[i], "$expand")
	}
	require.Len(t, got, 449, "the omitted item is skipped")
	want := []int{}
	for _, id := range ids {
		if id != 990 {
			want = append(want, id)
		}
	}
	gotIDs := make([]int, len(got))
	for i, wi := range got {
		gotIDs[i] = wi.ID
	}
	assert.Equal(t, want, gotIDs, "WIQL order restored")
	assert.Equal(t, "t1000", got[0].Fields["System.Title"])
}

func TestWorkItemClient_WorkItemsExpand(t *testing.T) {
	org := "wibatchexpandorg"
	c, reg := newWITClient(t, org)
	var body map[string]any
	reg.Register(httpmock.REST(http.MethodPost, org+"/proj/_apis/wit/workitemsbatch"), func(r *http.Request) (*http.Response, error) {
		body = witJSONBody(t, r)
		return httpmock.StringResponse(`{"count":1,"value":[{"id":1,"rev":2,"relations":[{"rel":"Hyperlink","url":"https://x"}]}]}`)(r)
	})
	got, err := c.WorkItems(context.Background(), []int{1}, []string{"System.Title"}, true)
	require.NoError(t, err)
	assert.Equal(t, "relations", body["$expand"])
	assert.NotContains(t, body, "fields", "fields is never sent together with $expand")
	assert.Equal(t, []WorkItem{{ID: 1, Rev: 2, Relations: []WorkItemRelation{{Rel: "Hyperlink", URL: "https://x"}}}}, got)

	got, err = c.WorkItems(context.Background(), nil, nil, false)
	require.NoError(t, err)
	assert.Empty(t, got, "no ids, no request")
}

func TestWorkItemClient_WorkItemsError(t *testing.T) {
	org := "wibatcherrorg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodPost, org+"/proj/_apis/wit/workitemsbatch"),
		httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "VS403474: bad field"}))
	_, err := c.WorkItems(context.Background(), []int{1}, []string{"Nope"}, false)
	var he *HTTPError
	require.ErrorAs(t, err, &he)
	assert.Equal(t, http.StatusBadRequest, he.StatusCode)
}

func TestWorkItemClient_QueryWIQL(t *testing.T) {
	org := "wiwiqlorg"
	c, reg := newWITClient(t, org)
	var top string
	var body map[string]any
	reg.Register(httpmock.REST(http.MethodPost, org+"/proj/_apis/wit/wiql"), func(r *http.Request) (*http.Response, error) {
		top, body = r.URL.Query().Get("$top"), witJSONBody(t, r)
		return httpmock.StringResponse(`{"queryType":"flat","columns":[{"referenceName":"System.Id"},{"referenceName":"System.Title"}],
			"workItems":[{"id":3},{"id":1}]}`)(r)
	})
	var teamTop string
	reg.Register(httpmock.REST(http.MethodPost, org+"/proj/Web%20Team/_apis/wit/wiql"), func(r *http.Request) (*http.Response, error) {
		teamTop = r.URL.Query().Get("$top")
		return httpmock.StringResponse(`{"queryType":"oneHop","workItemRelations":[{"target":{"id":2}},{"rel":"System.LinkTypes.Related","source":{"id":2},"target":{"id":4}}]}`)(r)
	})
	reg.Register(httpmock.REST(http.MethodPost, org+"/proj/_apis/wit/wiql"), httpmock.StatusJSONResponse(http.StatusBadRequest,
		map[string]any{"message": "VS402337: The number of work items returned exceeds the size limit of 20000. Change the query to return fewer items."}))
	ctx := context.Background()

	res, err := c.QueryWIQL(ctx, "SELECT [System.Id] FROM WorkItems", "", 30)
	require.NoError(t, err)
	assert.Equal(t, "30", top, "$top is always sent")
	assert.Equal(t, map[string]any{"query": "SELECT [System.Id] FROM WorkItems"}, body)
	assert.Equal(t, &QueryResult{QueryType: "flat", Columns: []string{"System.Id", "System.Title"}, IDs: []int{3, 1}}, res)

	res, err = c.QueryWIQL(ctx, "SELECT [System.Id] FROM WorkItemLinks", "Web Team", 200)
	require.NoError(t, err)
	assert.Equal(t, "200", teamTop)
	assert.Equal(t, &QueryResult{QueryType: "oneHop", Links: []WorkItemLink{
		{TargetID: 2}, {Rel: "System.LinkTypes.Related", SourceID: 2, TargetID: 4},
	}}, res)

	_, err = c.QueryWIQL(ctx, "SELECT [System.Id] FROM WorkItems", "", 30)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than 20,000 work items")
	assert.Contains(t, err.Error(), "narrow the filters")
	assert.Contains(t, err.Error(), "-L")
}

func TestWorkItemClient_QueryByID(t *testing.T) {
	org := "wiqueryidorg"
	c, reg := newWITClient(t, org)
	id := "6f0c0000-0000-0000-0000-000000000002"
	var top string
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/T/_apis/wit/wiql/"+id), func(r *http.Request) (*http.Response, error) {
		top = r.URL.Query().Get("$top")
		return httpmock.StringResponse(`{"queryType":"flat","workItems":[{"id":8}]}`)(r)
	})
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/wiql/"+id), httpmock.StatusJSONResponse(http.StatusBadRequest,
		map[string]any{"message": "VS402337: too many"}))
	ctx := context.Background()

	res, err := c.QueryByID(ctx, id, "T", 200)
	require.NoError(t, err)
	assert.Equal(t, "200", top)
	assert.Equal(t, []int{8}, res.IDs)

	_, err = c.QueryByID(ctx, id, "", 200)
	assert.ErrorContains(t, err, "narrow the filters")
	_, err = c.QueryByID(ctx, "not-a-guid", "", 200)
	assert.ErrorContains(t, err, "invalid query ID")
}

func TestWorkItemClient_Queries(t *testing.T) {
	org := "wiqueriesorg"
	c, reg := newWITClient(t, org)
	var q, rootQ map[string][]string
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/queries/Shared%20Queries%2FBugs"), func(r *http.Request) (*http.Response, error) {
		q = r.URL.Query()
		return httpmock.StringResponse(`{"id":"6f0c0000-0000-0000-0000-000000000003","name":"Bugs","path":"Shared Queries/Bugs",
			"isFolder":false,"isPublic":true,"queryType":"flat","wiql":"SELECT [System.Id] FROM WorkItems"}`)(r)
	})
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/queries"), func(r *http.Request) (*http.Response, error) {
		rootQ = r.URL.Query()
		return httpmock.StringResponse(`{"count":1,"value":[{"id":"6f0c0000-0000-0000-0000-000000000004","name":"Shared Queries",
			"path":"Shared Queries","isFolder":true,"isPublic":true,"hasChildren":true,
			"children":[{"id":"6f0c0000-0000-0000-0000-000000000003","name":"Bugs","path":"Shared Queries/Bugs","queryType":"flat"}]}]}`)(r)
	})
	ctx := context.Background()

	got, err := c.Query(ctx, "Shared Queries/Bugs", 0, true)
	require.NoError(t, err)
	assert.Equal(t, "wiql", q["$expand"][0])
	assert.NotContains(t, q, "$depth")
	assert.Equal(t, &QueryItem{
		ID: "6f0c0000-0000-0000-0000-000000000003", Name: "Bugs", Path: "Shared Queries/Bugs", IsPublic: true,
		QueryType: "flat", Wiql: "SELECT [System.Id] FROM WorkItems",
	}, got)

	roots, err := c.Queries(ctx, 2)
	require.NoError(t, err)
	assert.Equal(t, "2", rootQ["$depth"][0])
	assert.Equal(t, []QueryItem{{
		ID: "6f0c0000-0000-0000-0000-000000000004", Name: "Shared Queries", Path: "Shared Queries",
		IsFolder: true, IsPublic: true, HasChildren: true,
		Children: []QueryItem{{ID: "6f0c0000-0000-0000-0000-000000000003", Name: "Bugs", Path: "Shared Queries/Bugs", QueryType: "flat"}},
	}}, roots)
}

func TestWorkItemClient_CreateUpdateDelete(t *testing.T) {
	org := "wiwriteorg"
	c, reg := newWITClient(t, org)
	var created, updated []map[string]any
	reg.Register(httpmock.REST(http.MethodPost, org+"/proj/_apis/wit/workitems/$User%20Story"), func(r *http.Request) (*http.Response, error) {
		created = wiPatchBody(t, r)
		return httpmock.StringResponse(`{"id":9,"rev":1,"fields":{"System.Title":"T"},"url":"https://x/9"}`)(r)
	})
	reg.Register(httpmock.REST(http.MethodPatch, org+"/proj/_apis/wit/workitems/9"), func(r *http.Request) (*http.Response, error) {
		updated = wiPatchBody(t, r)
		return httpmock.StringResponse(`{"id":9,"rev":2,"fields":{"System.Title":"U"}}`)(r)
	})
	reg.Register(httpmock.REST(http.MethodPatch, org+"/proj/_apis/wit/workitems/10"), httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{
		"message": "VS403351: Test Operation for path /rev failed, value 7 was not equal to test value 6.", "typeKey": "TestPatchOperationFailedException",
	}))
	var soft, hard string
	reg.Register(httpmock.REST(http.MethodDelete, org+"/proj/_apis/wit/workitems/9"), func(r *http.Request) (*http.Response, error) {
		soft = r.URL.Query().Get("destroy")
		return httpmock.StringResponse(`{"id":9}`)(r)
	})
	reg.Register(httpmock.REST(http.MethodDelete, org+"/proj/_apis/wit/workitems/9"), func(r *http.Request) (*http.Response, error) {
		hard = r.URL.Query().Get("destroy")
		return httpmock.StringResponse(`{"id":9}`)(r)
	})
	ctx := context.Background()
	add, title := webapi.OperationValues.Add, "/fields/System.Title"
	ops := []webapi.JsonPatchOperation{{Op: &add, Path: &title, Value: "T"}}

	wi, err := c.CreateWorkItem(ctx, "User Story", ops)
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"op": "add", "path": "/fields/System.Title", "value": "T"}}, created)
	assert.Equal(t, &WorkItem{ID: 9, Rev: 1, Fields: map[string]any{"System.Title": "T"}, URL: "https://x/9"}, wi)

	wi, err = c.UpdateWorkItem(ctx, 9, ops)
	require.NoError(t, err)
	assert.Len(t, updated, 1)
	assert.Equal(t, 2, wi.Rev)

	_, err = c.UpdateWorkItem(ctx, 10, ops)
	assert.True(t, IsRevConflict(err))

	require.NoError(t, c.DeleteWorkItem(ctx, 9, false))
	require.NoError(t, c.DeleteWorkItem(ctx, 9, true))
	assert.Equal(t, "", soft)
	assert.Equal(t, "true", hard)
}

func TestWorkItemClient_Comments(t *testing.T) {
	org := "wicommentsorg"
	c, reg := newWITClient(t, org)
	var listQ, addQ, updQ map[string][]string
	var addBody, updBody map[string]any
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitems/5/comments"), func(r *http.Request) (*http.Response, error) {
		listQ = r.URL.Query()
		return httpmock.StringResponse(`{"totalCount":2,"count":1,"continuationToken":"next","comments":[{
			"id":11,"version":2,"text":"**hi**","format":"markdown","isDeleted":false,"url":"https://x/c/11",
			"createdBy":{"id":"6f0c0000-0000-0000-0000-00000000000a","displayName":"Ann","uniqueName":"ann@example.com"},
			"createdDate":"2026-10-01T10:00:00Z","modifiedDate":"2026-10-02T10:00:00Z"}]}`)(r)
	})
	reg.Register(httpmock.REST(http.MethodPost, org+"/proj/_apis/wit/workitems/5/comments"), func(r *http.Request) (*http.Response, error) {
		addQ, addBody = r.URL.Query(), witJSONBody(t, r)
		return httpmock.StringResponse(`{"id":12,"text":"x","format":"markdown"}`)(r)
	})
	reg.Register(httpmock.REST(http.MethodPatch, org+"/proj/_apis/wit/workitems/5/comments/12"), func(r *http.Request) (*http.Response, error) {
		updQ, updBody = r.URL.Query(), witJSONBody(t, r)
		return httpmock.StringResponse(`{"id":12,"text":"<p>y</p>","format":"html","version":2}`)(r)
	})
	reg.Register(httpmock.REST(http.MethodDelete, org+"/proj/_apis/wit/workitems/5/comments/12"), httpmock.StringResponse(`{"id":12,"isDeleted":true}`))
	ctx := context.Background()

	comments, token, err := c.Comments(ctx, 5, true, "tok")
	require.NoError(t, err)
	assert.Equal(t, "desc", listQ["order"][0])
	assert.Equal(t, "tok", listQ["continuationToken"][0])
	assert.Equal(t, "next", token)
	assert.Equal(t, []WorkItemComment{{
		ID: 11, Version: 2, Text: "**hi**", Format: "markdown", URL: "https://x/c/11",
		CreatedBy:   IdentityRef{ID: "6f0c0000-0000-0000-0000-00000000000a", DisplayName: "Ann", UniqueName: "ann@example.com"},
		CreatedDate: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC), ModifiedDate: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC),
	}}, comments)

	added, err := c.AddComment(ctx, 5, "x", true)
	require.NoError(t, err)
	assert.Equal(t, "markdown", addQ["format"][0])
	assert.Equal(t, map[string]any{"text": "x"}, addBody)
	assert.Equal(t, 12, added.ID)

	upd, err := c.UpdateComment(ctx, 5, 12, "<p>y</p>", false)
	require.NoError(t, err)
	assert.Equal(t, "html", updQ["format"][0])
	assert.Equal(t, map[string]any{"text": "<p>y</p>"}, updBody)
	assert.Equal(t, "html", upd.Format)

	require.NoError(t, c.DeleteComment(ctx, 5, 12))
}

func TestWorkItemClient_CommentsAscending(t *testing.T) {
	org := "wicommentsascorg"
	c, reg := newWITClient(t, org)
	var q map[string][]string
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitems/5/comments"), func(r *http.Request) (*http.Response, error) {
		q = r.URL.Query()
		return httpmock.StringResponse(`{"comments":[]}`)(r)
	})
	comments, token, err := c.Comments(context.Background(), 5, false, "")
	require.NoError(t, err)
	assert.Equal(t, "asc", q["order"][0])
	assert.NotContains(t, q, "continuationToken")
	assert.Empty(t, comments)
	assert.Empty(t, token)
}

func TestWorkItemClient_Updates(t *testing.T) {
	org := "wiupdatesorg"
	c, reg := newWITClient(t, org)
	page := func(from, n int) httpmock.Responder {
		items := make([]map[string]any, n)
		for i := range items {
			items[i] = map[string]any{"id": from + i, "rev": from + i}
		}
		return httpmock.JSONResponse(map[string]any{"count": n, "value": items})
	}
	var skips []string
	for _, p := range []httpmock.Responder{page(1, 200), page(201, 1)} {
		reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitems/5/updates"), func(r *http.Request) (*http.Response, error) {
			skips = append(skips, r.URL.Query().Get("$skip"))
			return p(r)
		})
	}
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitems/6/updates"), httpmock.StringResponse(`{"count":1,"value":[{
		"id":1,"rev":2,"revisedBy":{"id":"6f0c0000-0000-0000-0000-00000000000b","displayName":"Bob","uniqueName":"bob@example.com"},
		"revisedDate":"2026-10-03T08:00:00Z",
		"fields":{"System.State":{"oldValue":"New","newValue":"Active"}},
		"relations":{"added":[{"rel":"Hyperlink","url":"https://a"}],"removed":[{"rel":"Hyperlink","url":"https://b"}]}}]}`))
	ctx := context.Background()

	got, err := c.Updates(ctx, 5)
	require.NoError(t, err)
	assert.Len(t, got, 201)
	assert.Equal(t, []string{"0", "200"}, skips)

	got, err = c.Updates(ctx, 6)
	require.NoError(t, err)
	assert.Equal(t, []WorkItemUpdate{{
		ID: 1, Rev: 2, RevisedBy: IdentityRef{ID: "6f0c0000-0000-0000-0000-00000000000b", DisplayName: "Bob", UniqueName: "bob@example.com"},
		RevisedDate:      time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC),
		Fields:           map[string]FieldChange{"System.State": {OldValue: "New", NewValue: "Active"}},
		RelationsAdded:   []WorkItemRelation{{Rel: "Hyperlink", URL: "https://a"}},
		RelationsRemoved: []WorkItemRelation{{Rel: "Hyperlink", URL: "https://b"}},
	}}, got)
}

func TestWorkItemClient_RelationTypes(t *testing.T) {
	org := "wireltypesorg"
	c, reg := newWITClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/wit/workitemrelationtypes"), httpmock.StringResponse(`{"count":2,"value":[
		{"name":"Parent","referenceName":"System.LinkTypes.Hierarchy-Reverse","attributes":{"usage":"workItemLink","enabled":true}},
		{"name":"Hyperlink","referenceName":"Hyperlink","attributes":{"usage":"resourceLink","enabled":false}}]}`))
	got, err := c.RelationTypes(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []RelationType{
		{Name: "Parent", ReferenceName: "System.LinkTypes.Hierarchy-Reverse", Usage: "workItemLink", Enabled: true},
		{Name: "Hyperlink", ReferenceName: "Hyperlink", Usage: "resourceLink"},
	}, got)
}

func TestWorkItemClient_ClassificationNode(t *testing.T) {
	org := "winodesorg"
	c, reg := newWITClient(t, org)
	var depth string
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/classificationnodes/iterations/Sprint%201"), func(r *http.Request) (*http.Response, error) {
		depth = r.URL.Query().Get("$depth")
		return httpmock.StringResponse(`{"id":3,"name":"Sprint 1","path":"\\proj\\Iteration\\Sprint 1","hasChildren":true,
			"children":[{"id":4,"name":"Week 1","path":"\\proj\\Iteration\\Sprint 1\\Week 1"}]}`)(r)
	})
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/classificationnodes/areas"), httpmock.StringResponse(`{"id":1,"name":"proj","path":"\\proj\\Area"}`))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/classificationnodes/areas/Nope"), httpmock.StatusJSONResponse(http.StatusNotFound,
		map[string]any{"message": "VS402485: The node name Nope does not exist."}))
	ctx := context.Background()

	got, err := c.ClassificationNode(ctx, "iterations", "Sprint 1", 1)
	require.NoError(t, err)
	assert.Equal(t, "1", depth)
	assert.Equal(t, &ClassificationNode{
		ID: 3, Name: "Sprint 1", Path: `\proj\Iteration\Sprint 1`, HasChildren: true,
		Children: []ClassificationNode{{ID: 4, Name: "Week 1", Path: `\proj\Iteration\Sprint 1\Week 1`}},
	}, got)

	got, err = c.ClassificationNode(ctx, "areas", "", 0)
	require.NoError(t, err)
	assert.Equal(t, "proj", got.Name)

	_, err = c.ClassificationNode(ctx, "areas", "Nope", 0)
	assert.True(t, IsNotFound(err))
}

func TestMapErrTypeKey(t *testing.T) {
	status, msg, key := http.StatusBadRequest, "VS403351: Test Operation failed", "TestPatchOperationFailedException"
	err := mapErr(azuredevops.WrappedError{StatusCode: &status, Message: &msg, TypeKey: &key})
	assert.Equal(t, &HTTPError{StatusCode: status, Message: msg, TypeKey: key}, err)
}

func TestIsRevConflict(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"type key", &HTTPError{StatusCode: 400, TypeKey: "TestPatchOperationFailedException", Message: "x"}, true},
		{"VS403351 message", &HTTPError{StatusCode: 400, Message: "VS403351: Test Operation for path /rev failed"}, true},
		{"412 status", &HTTPError{StatusCode: http.StatusPreconditionFailed}, true},
		{"wrapped", fmt.Errorf("edit 5: %w", &HTTPError{StatusCode: 400, TypeKey: "TestPatchOperationFailedException"}), true},
		{"rule error", &HTTPError{StatusCode: 400, TypeKey: "RuleValidationException", Message: "TF401320: Rule Error"}, false},
		{"not found", &HTTPError{StatusCode: 404}, false},
		{"other error", errors.New("VS403351"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsRevConflict(tt.err))
		})
	}
}

func TestWorkItemClient_Errors(t *testing.T) {
	org := "wierrorsorg"
	c, reg := newWITClient(t, org)
	ctx := context.Background()
	calls := map[string]func() error{
		"WorkItem":       func() error { _, err := c.WorkItem(ctx, 1, false); return err },
		"CreateWorkItem": func() error { _, err := c.CreateWorkItem(ctx, "Bug", nil); return err },
		"DeleteWorkItem": func() error { return c.DeleteWorkItem(ctx, 1, false) },
		"Comments":       func() error { _, _, err := c.Comments(ctx, 1, false, ""); return err },
		"AddComment":     func() error { _, err := c.AddComment(ctx, 1, "x", true); return err },
		"UpdateComment":  func() error { _, err := c.UpdateComment(ctx, 1, 2, "x", true); return err },
		"Updates":        func() error { _, err := c.Updates(ctx, 1); return err },
		"RelationTypes":  func() error { _, err := c.RelationTypes(ctx); return err },
		"Query":          func() error { _, err := c.Query(ctx, "x", 1, false); return err },
		"Queries":        func() error { _, err := c.Queries(ctx, 0); return err },
		"ClassificationNode": func() error {
			_, err := c.ClassificationNode(ctx, "areas", "", 0)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			reg.Register(func(r *http.Request) bool { return r.Method != http.MethodOptions },
				httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
			var he *HTTPError
			require.ErrorAs(t, call(), &he)
			assert.Equal(t, http.StatusInternalServerError, he.StatusCode)
		})
	}
}
