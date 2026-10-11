package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/witfixtures"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var processTestOrgSeq atomic.Int32

// processTestClient returns a work item client for project "proj" of a fresh org (the SDK
// caches location discovery per base URL) and its registry, verified at cleanup.
func processTestClient(t *testing.T) (*api.WorkItemClient, *httpmock.Registry, string) {
	t.Helper()
	org := fmt.Sprintf("processorg%d", processTestOrgSeq.Add(1))
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.WorkItemLocations...)
	t.Cleanup(func() { reg.Verify(t) })
	return api.NewWorkItemClient(api.NewHTTPClient(api.ClientOptions{Transport: reg}), "https://dev.azure.com/"+org, "proj"), reg, org
}

// processTestStub serves the types and categories responses once each.
func processTestStub(reg *httpmock.Registry, org string, types, categories []byte) {
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes"), httpmock.JSONResponse(json.RawMessage(types)))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypecategories"), httpmock.JSONResponse(json.RawMessage(categories)))
}

// processTestFixture returns a Process serving the fixtures of process.
func processTestFixture(t *testing.T, process string) (*Process, *httpmock.Registry, string) {
	t.Helper()
	c, reg, org := processTestClient(t)
	processTestStub(reg, org, witfixtures.Process(t, process, "types"), witfixtures.Process(t, process, "categories"))
	return NewProcess(c), reg, org
}

func processTestStatesPath(org, typ string) string {
	return org + "/proj/_apis/wit/workitemtypes/" + url.PathEscape(typ) + "/states"
}

func TestProcessResolveType(t *testing.T) {
	tests := []struct {
		process, input, want, wantErr string
	}{
		{"agile", "Bug", "Bug", ""},
		{"agile", "user STORY", "User Story", ""},
		{"agile", "bug", "Bug", ""},
		{"agile", "story", "User Story", ""},
		{"agile", "task", "Task", ""},
		{"agile", "feature", "Feature", ""},
		{"agile", "Epic", "Epic", ""},
		{"agile", "issue", "Issue", ""},
		{"agile", "Test Case", "", `unknown work item type "Test Case"; valid types: Bug, Epic, Feature, Issue, Task, User Story`},
		{"agile", "shared steps", "", `unknown work item type "shared steps"`},
		{"agile", "nope", "", `unknown work item type "nope"; valid types: Bug, Epic, Feature, Issue, Task, User Story`},
		{"scrum", "story", "Product Backlog Item", ""},
		{"scrum", "impediment", "Impediment", ""},
		{"basic", "bug", "Issue", ""},
		{"basic", "story", "Issue", ""},
		{"basic", "epic", "Epic", ""},
		{"basic", "feature", "", `unknown work item type "feature"; valid types: Epic, Issue, Task`},
		{"cmmi", "story", "Requirement", ""},
		{"cmmi", "change request", "Change Request", ""},
		{"cmmi", "bug", "Bug", ""},
		{"inherited", "story", "User Story", ""},
		{"inherited", "customer request", "Customer Request", ""},
		{"inherited", "bug", "Bug", ""},
		{"inherited", "Issue", "", `unknown work item type "Issue"; valid types: Bug, Customer Request, Epic, Feature, Task, User Story`},
	}
	for _, tt := range tests {
		t.Run(tt.process+"/"+tt.input, func(t *testing.T) {
			p, _, _ := processTestFixture(t, tt.process)
			got, err := p.ResolveType(context.Background(), tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestProcessTypesExcludeHiddenAndDisabled(t *testing.T) {
	p, _, _ := processTestFixture(t, "inherited")
	types, err := p.Types(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"Bug", "Customer Request", "Epic", "Feature", "Task", "User Story"}, types)
}

// TestProcessAliasFallbacks covers an inherited process where the alias's category default is
// disabled (another member is used) and one with no Requirement category (the alias's usual
// type name is used), with types listed without states.
func TestProcessAliasFallbacks(t *testing.T) {
	c, reg, org := processTestClient(t)
	processTestStub(reg, org, []byte(`{"count": 3, "value": [
		{"name": "Old Bug", "isDisabled": true},
		{"name": "Defect"},
		{"name": "User Story"}
	]}`), []byte(`{"count": 1, "value": [
		{"referenceName": "Microsoft.BugCategory", "defaultWorkItemType": {"name": "Old Bug"},
		 "workItemTypes": [{"name": "Old Bug"}, {"name": "Defect"}]}
	]}`))
	p := NewProcess(c)
	ctx := context.Background()

	got, err := p.ResolveType(ctx, "bug")
	require.NoError(t, err)
	assert.Equal(t, "Defect", got)

	got, err = p.ResolveType(ctx, "STORY")
	require.NoError(t, err)
	assert.Equal(t, "User Story", got)

	_, err = p.ResolveType(ctx, "old bug")
	assert.EqualError(t, err, `unknown work item type "old bug"; valid types: Defect, User Story`)
}

func TestProcessExpandStates(t *testing.T) {
	tests := []struct {
		name, process string
		types         []string
		state         string
		want          []string
		wantErr       string
	}{
		{"agile open", "agile", nil, "open", []string{"New", "Active", "Resolved"}, ""},
		{"agile closed", "agile", nil, "Closed", []string{"Closed", "Removed"}, ""},
		{"agile bug open", "agile", []string{"Bug"}, "open", []string{"New", "Active", "Resolved"}, ""},
		{"agile bug closed", "agile", []string{"Bug"}, "closed", []string{"Closed"}, ""},
		{"scrum open", "scrum", nil, "OPEN", []string{"New", "Approved", "Committed", "In Progress", "Open", "To Do"}, ""},
		{"scrum closed", "scrum", nil, "closed", []string{"Done", "Removed", "Closed"}, ""},
		{"basic open", "basic", nil, "open", []string{"To Do", "Doing"}, ""},
		{"basic closed", "basic", nil, "closed", []string{"Done"}, ""},
		{"cmmi requirement open", "cmmi", []string{"Requirement"}, "open", []string{"Proposed", "Active", "Resolved"}, ""},
		{"inherited bug open", "inherited", []string{"Bug"}, "open", []string{"New", "Triaged", "Active", "Resolved"}, ""},
		{"state name any case", "agile", nil, "active", []string{"Active"}, ""},
		{"state name per type", "scrum", []string{"Task"}, "to do", []string{"To Do"}, ""},
		{"unknown state", "basic", nil, "Active", nil, `unknown state "Active"; use open, closed, all or a state of Epic, Issue, Task`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _, _ := processTestFixture(t, tt.process)
			got, err := p.ExpandStates(context.Background(), tt.types, tt.state)
			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestProcessExpandStatesAll(t *testing.T) {
	// No client: "all" needs no metadata.
	got, err := NewProcess(nil).ExpandStates(context.Background(), nil, "ALL")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestProcessCloseState(t *testing.T) {
	tests := []struct {
		process, typ, category, want, wantErr string
	}{
		{"agile", "User Story", CategoryCompleted, "Closed", ""},
		{"agile", "Bug", CategoryCompleted, "Closed", ""},
		{"scrum", "Product Backlog Item", CategoryCompleted, "Done", ""},
		{"basic", "Issue", CategoryCompleted, "Done", ""},
		{"cmmi", "Requirement", CategoryCompleted, "Closed", ""},
		{"agile", "Test Case", CategoryCompleted, "Closed", ""},
		{"inherited", "Issue", CategoryCompleted, "Closed", ""},
		{"inherited", "customer request", CategoryCompleted, "Closed", ""},

		{"agile", "Bug", CategoryResolved, "Resolved", ""},
		{"cmmi", "Bug", CategoryResolved, "Resolved", ""},
		{"cmmi", "Review", CategoryResolved, "Resolved", ""},
		{"cmmi", "Requirement", CategoryResolved, "Resolved", ""},
		{"agile", "User Story", CategoryResolved, "Resolved", ""},
		{"agile", "Task", CategoryResolved, "", `work item type "Task" has no Resolved state; pick one with --state: New, Active, Closed, Removed`},
		{"scrum", "Product Backlog Item", CategoryResolved, "", `work item type "Product Backlog Item" has no Resolved state`},
		{"basic", "Issue", CategoryResolved, "", `work item type "Issue" has no Resolved state; pick one with --state: To Do, Doing, Done`},

		{"agile", "User Story", CategoryRemoved, "Removed", ""},
		{"scrum", "Bug", CategoryRemoved, "Removed", ""},
		{"agile", "Bug", CategoryRemoved, "", `work item type "Bug" has no Removed state; pick one with --state: New, Active, Resolved, Closed`},
		{"basic", "Issue", CategoryRemoved, "", `work item type "Issue" has no Removed state`},
		{"cmmi", "Requirement", CategoryRemoved, "", `work item type "Requirement" has no Removed state`},
		{"cmmi", "Bug", CategoryRemoved, "", `work item type "Bug" has no Removed state`},
	}
	for _, tt := range tests {
		t.Run(tt.process+"/"+tt.typ+"/"+tt.category, func(t *testing.T) {
			p, _, _ := processTestFixture(t, tt.process)
			got, err := p.CloseState(context.Background(), tt.typ, tt.category)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestProcessReopenState(t *testing.T) {
	tests := []struct {
		process, typ, want string
	}{
		{"agile", "Bug", "New"},
		{"agile", "Issue", "Active"},
		{"scrum", "Impediment", "Open"},
		{"scrum", "Task", "To Do"},
		{"basic", "Issue", "To Do"},
		{"cmmi", "Review", "Active"},
		{"cmmi", "Requirement", "Proposed"},
		{"inherited", "Bug", "New"},
	}
	for _, tt := range tests {
		t.Run(tt.process+"/"+tt.typ, func(t *testing.T) {
			p, _, _ := processTestFixture(t, tt.process)
			got, err := p.ReopenState(context.Background(), tt.typ)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestProcessReopenStateNone(t *testing.T) {
	c, reg, org := processTestClient(t)
	processTestStub(reg, org, []byte(`{"count": 1, "value": [
		{"name": "Note", "states": [{"name": "Done", "category": "Completed"}]}
	]}`), []byte(`{"count": 0, "value": []}`))
	_, err := NewProcess(c).ReopenState(context.Background(), "Note")
	assert.EqualError(t, err, `work item type "Note" has no Proposed state; pick one with --state: Done`)
}

// TestProcessFetchedOnce: every stub answers once, so a second fetch would fail.
func TestProcessFetchedOnce(t *testing.T) {
	p, reg, _ := processTestFixture(t, "agile")
	ctx := context.Background()
	for range 2 {
		_, err := p.ResolveType(ctx, "bug")
		require.NoError(t, err)
		_, err = p.Types(ctx)
		require.NoError(t, err)
		_, err = p.ExpandStates(ctx, nil, "open")
		require.NoError(t, err)
		_, err = p.CloseState(ctx, "Bug", CategoryResolved)
		require.NoError(t, err)
		_, err = p.ReopenState(ctx, "Issue")
		require.NoError(t, err)
	}
	assert.Len(t, reg.Requests, 3, "OPTIONS, types and categories only")
}

// TestProcessStatesFallback: a types list without states costs one /states call per type, once.
func TestProcessStatesFallback(t *testing.T) {
	var types struct {
		Count int              `json:"count"`
		Value []map[string]any `json:"value"`
	}
	require.NoError(t, json.Unmarshal(witfixtures.Process(t, "agile", "types"), &types))
	for _, typ := range types.Value {
		delete(typ, "states")
	}
	noStates, err := json.Marshal(types)
	require.NoError(t, err)

	c, reg, org := processTestClient(t)
	processTestStub(reg, org, noStates, witfixtures.Process(t, "agile", "categories"))
	for _, typ := range []string{"Bug", "Epic", "Feature", "Issue", "Task", "User Story", "Shared Steps"} {
		body := []byte(`{"count": 2, "value": [{"name": "New", "category": "Proposed"}, {"name": "Closed", "category": "Completed"}]}`)
		if typ != "Shared Steps" {
			body = witfixtures.TypeStates(t, "agile", typ)
		}
		reg.Register(httpmock.REST(http.MethodGet, processTestStatesPath(org, typ)), httpmock.JSONResponse(json.RawMessage(body)))
	}
	p := NewProcess(c)
	ctx := context.Background()

	for range 2 {
		open, err := p.ExpandStates(ctx, nil, "open")
		require.NoError(t, err)
		assert.Equal(t, []string{"New", "Active", "Resolved"}, open)
		got, err := p.ReopenState(ctx, "Issue")
		require.NoError(t, err)
		assert.Equal(t, "Active", got)
		// Not in the types list at all (project-only Hidden type).
		got, err = p.CloseState(ctx, "Shared Steps", CategoryCompleted)
		require.NoError(t, err)
		assert.Equal(t, "Closed", got)
	}
	statesCalls := 0
	for _, r := range reg.Requests {
		if strings.HasSuffix(r.URL.Path, "/states") {
			statesCalls++
		}
	}
	assert.Equal(t, 7, statesCalls)
}

func TestProcessErrors(t *testing.T) {
	ctx := context.Background()
	notFound := httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF200016: The following project does not exist: proj."})

	t.Run("types", func(t *testing.T) {
		c, reg, org := processTestClient(t)
		reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes"), notFound)
		p := NewProcess(c)
		_, err := p.ResolveType(ctx, "bug")
		assert.ErrorContains(t, err, "TF200016")
	})
	t.Run("categories", func(t *testing.T) {
		c, reg, org := processTestClient(t)
		reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes"), httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "types"))))
		reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypecategories"), notFound)
		p := NewProcess(c)
		_, err := p.Types(ctx)
		assert.ErrorContains(t, err, "TF200016")
		// Not loaded: each call fetches the types again, and finds no stub left.
		const noStub = "no registered HTTP stubs matched GET"
		_, err = p.States(ctx, "Bug")
		assert.ErrorContains(t, err, noStub)
		_, err = p.ExpandStates(ctx, nil, "open")
		assert.ErrorContains(t, err, noStub)
		_, err = p.CloseState(ctx, "Bug", CategoryCompleted)
		assert.ErrorContains(t, err, noStub)
		_, err = p.ReopenState(ctx, "Bug")
		assert.ErrorContains(t, err, noStub)
	})
	t.Run("states", func(t *testing.T) {
		c, reg, org := processTestClient(t)
		processTestStub(reg, org, []byte(`{"count": 1, "value": [{"name": "Bug"}]}`), []byte(`{"count": 0, "value": []}`))
		reg.Register(httpmock.REST(http.MethodGet, processTestStatesPath(org, "Bug")), notFound)
		_, err := NewProcess(c).ExpandStates(ctx, nil, "open")
		assert.ErrorContains(t, err, "TF200016")
	})
}

func TestProcessBodyField(t *testing.T) {
	tests := []struct {
		process, typ, fieldsFixture, want string
	}{
		{"agile", "Bug", "agile_type_bug_fields.json", ReproStepsField},
		{"scrum", "bug", "scrum_type_bug_fields.json", ReproStepsField},
		{"basic", "Issue", "live_basic_type_issue_fields.json", DescriptionField}, // in the Bug category, no Repro Steps
		{"agile", "User Story", "", DescriptionField},                             // not a bug: no fields lookup
		{"scrum", "Product Backlog Item", "", DescriptionField},
	}
	for _, tt := range tests {
		t.Run(tt.process+"/"+tt.typ, func(t *testing.T) {
			p, reg, org := processTestFixture(t, tt.process)
			if tt.fieldsFixture != "" {
				reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes/"+url.PathEscape(tt.typ)+"/fields"),
					httpmock.JSONResponse(json.RawMessage(witfixtures.Read(t, tt.fieldsFixture))))
			}
			for range 2 { // the second call is served from the cache
				got, err := p.BodyField(context.Background(), tt.typ)
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestProcessBodyFieldErrors(t *testing.T) {
	p, reg, org := processTestFixture(t, "agile")
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/wit/workitemtypes/Bug/fields"),
		httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
	_, err := p.BodyField(context.Background(), "Bug")
	assert.ErrorContains(t, err, "boom")

	c, reg2, org2 := processTestClient(t)
	reg2.Register(httpmock.REST(http.MethodGet, org2+"/proj/_apis/wit/workitemtypes"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF200016: no project"}))
	_, err = NewProcess(c).BodyField(context.Background(), "Bug")
	assert.ErrorContains(t, err, "TF200016")
}
