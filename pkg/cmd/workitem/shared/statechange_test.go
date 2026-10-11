package shared

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
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

// stateTestEnv runs StateChange against project "My Proj" of an org named after the test (the
// SDK caches location discovery per base URL).
type stateTestEnv struct {
	org    string
	reg    *httpmock.Registry
	ios    *iostreams.IOStreams
	errOut *bytes.Buffer
	f      *cmdutil.Factory
	// patches holds the JSON Patch documents sent, keyed by work item ID, in order.
	patches map[int][][]map[string]any
	// comments holds the comment requests sent: text and format query value.
	comments []map[string]string
}

func newStateTestEnv(t *testing.T, tty bool) *stateTestEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, _, errOut := iostreams.Test()
	ios.SetStdoutTTY(tty)
	org := "statechange-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &stateTestEnv{org: org, reg: &httpmock.Registry{}, ios: ios, errOut: errOut, patches: map[int][][]map[string]any{}}
	e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	t.Cleanup(func() { e.reg.Verify(t) })
	cfg, err := config.Load()
	require.NoError(t, err)
	e.f = &cmdutil.Factory{
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: org, Project: "My Proj", Name: "web"}, nil
		},
		Prompter: &cmdutil.Prompter{IO: ios, Accessible: true},
	}
	return e
}

func (e *stateTestEnv) path(rest string) string { return e.org + "/My%20Proj/_apis/wit/" + rest }

func (e *stateTestEnv) stubProcess(t *testing.T, process string) {
	t.Helper()
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("workitemtypes")),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, process, "types"))))
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("workitemtypecategories")),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, process, "categories"))))
}

func (e *stateTestEnv) stubItem(id int, typ, state string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("workitems/"+strconv.Itoa(id))),
		httpmock.JSONResponse(map[string]any{"id": id, "rev": 3, "fields": map[string]any{
			"System.WorkItemType": typ, "System.State": state, "System.Title": "Item " + strconv.Itoa(id),
		}}))
}

// stubPatch records the patch of work item id and answers it, with status, typeKey and
// message when status is not 200.
func (e *stateTestEnv) stubPatch(t *testing.T, id, status int, typeKey, message string) {
	e.reg.Register(httpmock.REST(http.MethodPatch, e.path("workitems/"+strconv.Itoa(id))),
		func(r *http.Request) (*http.Response, error) {
			b, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			var ops []map[string]any
			require.NoError(t, json.Unmarshal(b, &ops))
			e.patches[id] = append(e.patches[id], ops)
			if status != http.StatusOK {
				return httpmock.StatusJSONResponse(status, map[string]any{"message": message, "typeKey": typeKey})(r)
			}
			return httpmock.JSONResponse(map[string]any{"id": id, "rev": 4, "fields": map[string]any{}})(r)
		})
}

func (e *stateTestEnv) stubReasons(typ string, values ...string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("workitemtypes/"+url.PathEscape(typ)+"/fields/System.Reason")),
		httpmock.JSONResponse(map[string]any{"referenceName": "System.Reason", "allowedValues": values}))
}

func (e *stateTestEnv) stubComment(t *testing.T, id, status int) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.path("workitems/"+strconv.Itoa(id)+"/comments")),
		func(r *http.Request) (*http.Response, error) {
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			e.comments = append(e.comments, map[string]string{"text": body["text"], "format": r.URL.Query().Get("format")})
			if status != http.StatusOK {
				return httpmock.StatusJSONResponse(status, map[string]any{"message": "comments are off"})(r)
			}
			return httpmock.JSONResponse(map[string]any{"id": 1, "text": body["text"]})(r)
		})
}

func (e *stateTestEnv) run(s StateChange) error { return s.Run(context.Background(), e.f) }

func stateTestOp(op, path string, value any) map[string]any {
	return map[string]any{"op": op, "path": path, "value": value}
}

// stateTestOps is the patch moving a rev 3 item to state, plus extra ops.
func stateTestOps(state string, extra ...map[string]any) []map[string]any {
	return append([]map[string]any{
		stateTestOp("test", "/rev", float64(3)),
		stateTestOp("add", "/fields/System.State", state),
	}, extra...)
}

func TestStateChangeTargetPerProcess(t *testing.T) {
	tests := []struct {
		name, process, typ, from string
		category, state          string
		want, wantErr            string
	}{
		{name: "agile default", process: "agile", typ: "User Story", from: "Active", want: "Closed"},
		{name: "scrum default", process: "scrum", typ: "Product Backlog Item", from: "Committed", want: "Done"},
		{name: "basic default", process: "basic", typ: "Issue", from: "Doing", want: "Done"},
		{name: "cmmi default", process: "cmmi", typ: "Requirement", from: "Active", want: "Closed"},
		{name: "inherited custom type", process: "inherited", typ: "Customer Request", from: "In Review", want: "Closed"},
		{name: "agile bug resolve by category", process: "agile", typ: "Bug", from: "Active", category: CategoryResolved, want: "Resolved"},
		{name: "cmmi requirement resolve by name", process: "cmmi", typ: "Requirement", from: "Active", category: CategoryResolved, want: "Resolved"},
		{name: "basic resolve errors", process: "basic", typ: "Issue", from: "Doing", category: CategoryResolved,
			wantErr: `work item type "Issue" has no Resolved state; pick one with --state: To Do, Doing, Done`},
		{name: "agile story removed", process: "agile", typ: "User Story", from: "New", category: CategoryRemoved, want: "Removed"},
		{name: "scrum task removed", process: "scrum", typ: "Task", from: "In Progress", category: CategoryRemoved, want: "Removed"},
		{name: "agile bug removed errors", process: "agile", typ: "Bug", from: "Active", category: CategoryRemoved,
			wantErr: `work item type "Bug" has no Removed state`},
		{name: "basic removed errors", process: "basic", typ: "Issue", from: "Doing", category: CategoryRemoved,
			wantErr: `work item type "Issue" has no Removed state`},
		{name: "cmmi removed errors", process: "cmmi", typ: "Bug", from: "Active", category: CategoryRemoved,
			wantErr: `work item type "Bug" has no Removed state`},
		{name: "state overrides category", process: "agile", typ: "Bug", from: "Active", category: CategoryResolved, state: "closed", want: "Closed"},
		{name: "unknown state", process: "basic", typ: "Issue", from: "Doing", state: "Gone",
			wantErr: `work item type "Issue" has no state "Gone"; valid states: To Do, Doing, Done`},
		{name: "removed item can be completed", process: "agile", typ: "User Story", from: "Removed", want: "Closed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newStateTestEnv(t, false)
			e.stubProcess(t, tt.process)
			e.stubItem(7, tt.typ, tt.from)
			if tt.wantErr == "" {
				e.stubPatch(t, 7, http.StatusOK, "", "")
			}
			category := tt.category
			if category == "" {
				category = CategoryCompleted
			}
			err := e.run(StateChange{Refs: []string{"7"}, Category: category, State: tt.state})
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Empty(t, e.patches)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, [][]map[string]any{stateTestOps(tt.want)}, e.patches[7])
			assert.Empty(t, e.errOut.String())
		})
	}
}

func TestStateChangeReopenTargetPerProcess(t *testing.T) {
	tests := []struct {
		name, process, typ, from, want string
	}{
		{"agile story", "agile", "User Story", "Closed", "New"},
		{"agile issue has no proposed state", "agile", "Issue", "Closed", "Active"},
		{"scrum impediment has no proposed state", "scrum", "Impediment", "Closed", "Open"},
		{"scrum bug", "scrum", "Bug", "Removed", "New"},
		{"basic issue", "basic", "Issue", "Done", "To Do"},
		{"cmmi bug resolved", "cmmi", "Bug", "Resolved", "Proposed"},
		{"inherited bug", "inherited", "Bug", "Closed", "New"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newStateTestEnv(t, true)
			e.stubProcess(t, tt.process)
			e.stubItem(7, tt.typ, tt.from)
			e.stubPatch(t, 7, http.StatusOK, "", "")

			require.NoError(t, e.run(StateChange{Refs: []string{"7"}, Category: CategoryProposed}))
			assert.Equal(t, [][]map[string]any{stateTestOps(tt.want)}, e.patches[7])
			assert.Equal(t, "✓ Reopened work item 7 (Item 7) as "+tt.want+"\n", e.errOut.String())
		})
	}
}

func TestStateChangeCloseTTYOutput(t *testing.T) {
	e := newStateTestEnv(t, true)
	e.stubProcess(t, "basic")
	e.stubItem(7, "Issue", "To Do")
	e.stubPatch(t, 7, http.StatusOK, "", "")

	require.NoError(t, e.run(StateChange{Refs: []string{"#7"}, Category: CategoryCompleted}))
	assert.Equal(t, "✓ Closed work item 7 (Item 7) as Done\n", e.errOut.String())
}

func TestStateChangeAlreadyInState(t *testing.T) {
	tests := []struct {
		name, process, typ, from string
		category, state          string
	}{
		{name: "completed", process: "basic", typ: "Issue", from: "Done", category: CategoryCompleted},
		{name: "resolved by name", process: "cmmi", typ: "Requirement", from: "Resolved", category: CategoryResolved},
		{name: "explicit state", process: "agile", typ: "Bug", from: "Active", category: CategoryCompleted, state: "active"},
		{name: "reopen proposed", process: "agile", typ: "User Story", from: "New", category: CategoryProposed},
		{name: "reopen in progress", process: "agile", typ: "User Story", from: "Active", category: CategoryProposed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newStateTestEnv(t, false)
			e.stubProcess(t, tt.process)
			e.stubItem(7, tt.typ, tt.from)

			require.NoError(t, e.run(StateChange{Refs: []string{"7"}, Category: tt.category, State: tt.state, Comment: "done"}))
			assert.Empty(t, e.patches)
			assert.Equal(t, "! Work item 7 (Item 7) is already "+tt.from+"\n", e.errOut.String())
		})
	}
}

func TestStateChangeReason(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubProcess(t, "agile")
	e.stubItem(7, "Bug", "Active")
	e.stubItem(8, "Bug", "Active")
	e.stubReasons("Bug", "Fixed", "Duplicate", "As Designed")
	e.stubPatch(t, 7, http.StatusOK, "", "")
	e.stubPatch(t, 8, http.StatusOK, "", "")

	// The allowed values are fetched once per type.
	require.NoError(t, e.run(StateChange{Refs: []string{"7", "8"}, Category: CategoryResolved, Reason: "duplicate"}))
	want := [][]map[string]any{stateTestOps("Resolved", stateTestOp("add", "/fields/System.Reason", "Duplicate"))}
	assert.Equal(t, want, e.patches[7])
	assert.Equal(t, want, e.patches[8])
}

func TestStateChangeReasonNoAllowedValues(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubProcess(t, "basic")
	e.stubItem(7, "Issue", "Doing")
	e.stubReasons("Issue")
	e.stubPatch(t, 7, http.StatusOK, "", "")

	// No allowed values to check against: the reason is sent as given.
	require.NoError(t, e.run(StateChange{Refs: []string{"7"}, Category: CategoryCompleted, Reason: "whatever"}))
	assert.Equal(t, [][]map[string]any{stateTestOps("Done", stateTestOp("add", "/fields/System.Reason", "whatever"))}, e.patches[7])
}

func TestStateChangeReasonNotAllowed(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubProcess(t, "basic")
	e.stubItem(7, "Issue", "Doing")
	e.stubReasons("Issue", "Completed", "Cut", "Deferred", "Obsolete")

	err := e.run(StateChange{Refs: []string{"7"}, Category: CategoryCompleted, Reason: "Bogus"})
	require.EqualError(t, err, `invalid reason "Bogus" for Issue; allowed reasons: Completed, Cut, Deferred, Obsolete`)
	assert.Empty(t, e.patches)
}

func TestStateChangeReasonFetchFails(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubProcess(t, "basic")
	e.stubItem(7, "Issue", "Doing")
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("workitemtypes/Issue/fields/System.Reason")),
		httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))

	err := e.run(StateChange{Refs: []string{"7"}, Category: CategoryCompleted, Reason: "Cut"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "denied")
	assert.Empty(t, e.patches)
}

func TestStateChangeReasonRejectedByServer(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubProcess(t, "basic")
	e.stubItem(7, "Issue", "Doing")
	e.stubReasons("Issue", "Completed", "Cut", "Started")
	msg := "TF401320: Rule Error for field Reason. Error code: Required, HasValues, LimitedToValues, AllowsOldValue, InvalidEmpty."
	e.stubPatch(t, 7, http.StatusBadRequest, "RuleValidationException", msg)

	err := e.run(StateChange{Refs: []string{"7"}, Category: CategoryCompleted, Reason: "started"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), msg)
	assert.Equal(t, [][]map[string]any{stateTestOps("Done", stateTestOp("add", "/fields/System.Reason", "Started"))}, e.patches[7])
}

func TestStateChangeRuleErrorHint(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubProcess(t, "cmmi")
	e.stubItem(7, "Bug", "Active")
	msg := "TF401320: Rule Error for field Resolved Reason. Error code: Required, HasValues, LimitedToValues, InvalidEmpty."
	e.stubPatch(t, 7, http.StatusBadRequest, "RuleValidationException", msg)

	err := e.run(StateChange{Refs: []string{"7"}, Category: CategoryResolved})
	require.EqualError(t, err, msg+" (HTTP 400)\nset the field with -f \"Resolved Reason=<value>\"")
}

func TestStateChangeField(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubProcess(t, "agile")
	e.stubItem(7, "User Story", "Active")
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("fields")),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Read(t, "fields.json"))))
	e.stubPatch(t, 7, http.StatusOK, "", "")

	require.NoError(t, e.run(StateChange{Refs: []string{"7"}, Category: CategoryCompleted, Fields: []string{"Story Points=5"}}))
	assert.Equal(t, [][]map[string]any{stateTestOps("Closed",
		stateTestOp("add", "/fields/Microsoft.VSTS.Scheduling.StoryPoints", float64(5)))}, e.patches[7])
}

func TestStateChangeFieldInvalid(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubProcess(t, "agile")
	e.stubItem(7, "User Story", "Active")

	err := e.run(StateChange{Refs: []string{"7"}, Category: CategoryCompleted, Fields: []string{"nope"}})
	require.EqualError(t, err, `invalid field "nope": expected Name=Value`)
	assert.Empty(t, e.patches)
}

func TestStateChangeComment(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubProcess(t, "agile")
	e.stubItem(7, "User Story", "Closed")
	e.stubPatch(t, 7, http.StatusOK, "", "")

	require.NoError(t, e.run(StateChange{Refs: []string{"7"}, Category: CategoryProposed, State: "active", Comment: "Done in **!45**"}))
	assert.Equal(t, [][]map[string]any{stateTestOps("Active",
		stateTestOp("add", "/fields/System.History", "Done in **!45**"),
		stateTestOp("add", "/multilineFieldsFormat/System.History", "Markdown"),
	)}, e.patches[7])
}

func TestStateChangeCommentFallback(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubProcess(t, "basic")
	e.stubItem(7, "Issue", "Doing")
	e.stubPatch(t, 7, http.StatusBadRequest, "X", "The type changed without a value: /multilineFieldsFormat/System.History")
	e.stubPatch(t, 7, http.StatusOK, "", "")
	e.stubComment(t, 7, http.StatusOK)

	require.NoError(t, e.run(StateChange{Refs: []string{"7"}, Category: CategoryCompleted, Comment: "*done*"}))
	require.Len(t, e.patches[7], 2)
	assert.Equal(t, stateTestOps("Done"), e.patches[7][1])
	assert.Equal(t, []map[string]string{{"text": "*done*", "format": "markdown"}}, e.comments)
}

func TestStateChangeCommentFallbackCommentFails(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubProcess(t, "basic")
	e.stubItem(7, "Issue", "Doing")
	e.stubPatch(t, 7, http.StatusBadRequest, "X", "Invalid path /multilineFieldsFormat/System.History")
	e.stubPatch(t, 7, http.StatusOK, "", "")
	e.stubComment(t, 7, http.StatusBadRequest)

	err := e.run(StateChange{Refs: []string{"7"}, Category: CategoryCompleted, Comment: "done"})
	require.EqualError(t, err, "work item 7 is now Done, but adding the comment failed: comments are off (HTTP 400)")
}

func TestStateChangeRevConflict(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubProcess(t, "basic")
	e.stubItem(7, "Issue", "Doing")
	e.stubPatch(t, 7, http.StatusBadRequest, "TestPatchOperationFailedException", "VS403351: Test operation failed")

	require.EqualError(t, e.run(StateChange{Refs: []string{"7"}, Category: CategoryCompleted}), "work item 7 changed since read; retry")
}

func TestStateChangeMultipleIDs(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubProcess(t, "basic")
	e.stubItem(7, "Issue", "Doing")
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("workitems/8")),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401232: Work item 8 does not exist"}))
	e.stubItem(9, "Epic", "To Do")
	e.stubPatch(t, 7, http.StatusOK, "", "")
	e.stubPatch(t, 9, http.StatusOK, "", "")

	// The URL ref names the same project in another case: the project is set up once.
	err := e.run(StateChange{Refs: []string{"7", "8", "https://dev.azure.com/" + e.org + "/my%20proj/_workitems/edit/9"}, Category: CategoryCompleted})
	require.EqualError(t, err, "failed to close work items 8; done: 7, 9")
	assert.Equal(t, "X Failed to close work item 8: TF401232: Work item 8 does not exist (HTTP 404)\n", e.errOut.String())
	assert.Equal(t, [][]map[string]any{stateTestOps("Done")}, e.patches[7])
	assert.Equal(t, [][]map[string]any{stateTestOps("Done")}, e.patches[9])
}

func TestStateChangeAllFail(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubProcess(t, "basic")
	e.stubItem(7, "Issue", "Done")
	e.stubItem(8, "Issue", "Done")
	e.stubPatch(t, 7, http.StatusForbidden, "X", "denied")
	e.stubPatch(t, 8, http.StatusForbidden, "X", "denied")

	require.EqualError(t, e.run(StateChange{Refs: []string{"7", "8"}, Category: CategoryProposed}), "failed to reopen work items 7, 8")
}

func TestStateChangeProcessFails(t *testing.T) {
	e := newStateTestEnv(t, false)
	e.stubItem(7, "Issue", "Doing")
	e.reg.Register(httpmock.REST(http.MethodGet, e.path("workitemtypes")),
		httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "no process"}))

	err := e.run(StateChange{Refs: []string{"7"}, Category: CategoryCompleted})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no process")
}

// newStateTestEnvNoAPI is a stateTestEnv whose run must not reach the API, so the
// location stubs are not required.
func newStateTestEnvNoAPI(t *testing.T) *stateTestEnv {
	e := newStateTestEnv(t, false)
	e.reg = &httpmock.Registry{}
	return e
}

func TestStateChangeProjectErrors(t *testing.T) {
	t.Run("invalid ref", func(t *testing.T) {
		e := newStateTestEnvNoAPI(t)
		err := e.run(StateChange{Refs: []string{"7", "abc"}, Category: CategoryCompleted})
		require.EqualError(t, err, `invalid work item reference "abc": expected an ID, #ID or a work item URL`)
		assert.Empty(t, e.reg.Requests)
	})
	t.Run("no base project", func(t *testing.T) {
		e := newStateTestEnvNoAPI(t)
		e.f.BaseRepo = func() (adocontext.Repo, error) { return adocontext.Repo{}, errors.New("not in a repository") }
		err := e.run(StateChange{Refs: []string{"7"}, Category: CategoryCompleted})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not in a repository")
		assert.Empty(t, e.reg.Requests)
	})
	t.Run("no http client", func(t *testing.T) {
		e := newStateTestEnvNoAPI(t)
		e.f.HttpClient = func() (*http.Client, error) { return nil, errors.New("no transport") }
		require.EqualError(t, e.run(StateChange{Refs: []string{"7"}, Category: CategoryCompleted}), "no transport")
	})
}
