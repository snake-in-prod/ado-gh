package assign

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
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

type wiAssignEnv struct {
	org    string
	reg    *httpmock.Registry
	ios    *iostreams.IOStreams
	out    *bytes.Buffer
	errOut *bytes.Buffer
	// ops holds the JSON Patch document of each update, by work item ID.
	ops map[int][]map[string]any
}

func newWIAssignEnv(t *testing.T, tty bool) *wiAssignEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "wiassign-" + strings.NewReplacer("/", "-", " ", "-").Replace(strings.ToLower(t.Name()))
	e := &wiAssignEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut, ops: map[int][]map[string]any{}}
	e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	return e
}

func (e *wiAssignEnv) stubMe() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"), httpmock.JSONResponse(map[string]any{
		"authenticatedUser": map[string]any{"id": "33333333-3333-3333-3333-333333333333", "providerDisplayName": "Me",
			"properties": map[string]any{"Account": map[string]any{"$value": "me@example.com"}}},
	}))
}

// stubUpdate answers the PATCH of work item id after recording its ops, or with the status
// when not 200.
func (e *wiAssignEnv) stubUpdate(t *testing.T, id, status int) {
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/My%20Proj/_apis/wit/workitems/"+strconv.Itoa(id)),
		func(r *http.Request) (*http.Response, error) {
			b, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			var ops []map[string]any
			require.NoError(t, json.Unmarshal(b, &ops))
			e.ops[id] = ops
			if status != http.StatusOK {
				return httpmock.StatusJSONResponse(status, map[string]any{"message": "TF401232: Work item " + strconv.Itoa(id) + " does not exist"})(r)
			}
			return httpmock.JSONResponse(map[string]any{"id": id, "rev": 2, "fields": map[string]any{}})(r)
		})
}

func (e *wiAssignEnv) run(t *testing.T, args ...string) error {
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
		Prompter: &cmdutil.Prompter{IO: e.ios, Accessible: true},
	}
	cmd := NewCmdAssign(f)
	cmd.SetArgs(args)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

func wiAssignOp(value string) []map[string]any {
	return []map[string]any{{"op": "add", "path": "/fields/System.AssignedTo", "value": value}}
}

func TestWIAssignDefaultMe(t *testing.T) {
	e := newWIAssignEnv(t, true)
	e.stubMe()
	e.stubUpdate(t, 42, http.StatusOK)

	require.NoError(t, e.run(t, "42"))
	e.reg.Verify(t)
	assert.Equal(t, wiAssignOp("me@example.com"), e.ops[42])
	assert.Equal(t, "✓ Assigned #42 to Me\n", e.errOut.String())
	assert.Empty(t, e.out.String())
}

func TestWIAssignToMultiple(t *testing.T) {
	e := newWIAssignEnv(t, false)
	e.reg.StubIMS(e.org)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/Identities"), httpmock.JSONResponse(map[string]any{
		"count": 1, "value": []map[string]any{{"id": "44444444-4444-4444-4444-444444444444", "providerDisplayName": "Jane Doe",
			"properties": map[string]any{"Account": map[string]any{"$value": "jane@example.com"}}}},
	}))
	e.stubUpdate(t, 1, http.StatusOK)
	e.stubUpdate(t, 2, http.StatusOK)

	require.NoError(t, e.run(t, "1", "#2", "--to", "jane@example.com"))
	e.reg.Verify(t)
	assert.Equal(t, wiAssignOp("jane@example.com"), e.ops[1])
	assert.Equal(t, wiAssignOp("jane@example.com"), e.ops[2])
	assert.Empty(t, e.errOut.String())
	assert.Empty(t, e.out.String())
}

func TestWIAssignUnassign(t *testing.T) {
	e := newWIAssignEnv(t, true)
	e.stubUpdate(t, 7, http.StatusOK)

	require.NoError(t, e.run(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_workitems/edit/7", "--unassign"))
	e.reg.Verify(t)
	assert.Equal(t, []map[string]any{{"op": "remove", "path": "/fields/System.AssignedTo"}}, e.ops[7])
	assert.Equal(t, "✓ Unassigned #7\n", e.errOut.String())
}

func TestWIAssignPartialFailure(t *testing.T) {
	e := newWIAssignEnv(t, true)
	e.stubMe()
	e.stubUpdate(t, 1, http.StatusNotFound)
	e.stubUpdate(t, 2, http.StatusOK)

	err := e.run(t, "1", "2")
	require.ErrorIs(t, err, cmdutil.SilentError)
	e.reg.Verify(t)
	assert.Contains(t, e.errOut.String(), "X Failed to update #1: ")
	assert.Contains(t, e.errOut.String(), "TF401232")
	assert.Contains(t, e.errOut.String(), "✓ Assigned #2 to Me\n")
}

func TestWIAssignArgErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no ids", nil, "requires at least 1 arg"},
		{"bad ref", []string{"abc"}, `invalid work item reference "abc"`},
		{"to and unassign", []string{"1", "--to", "x", "--unassign"}, "none of the others can be"},
		{"mixed projects", []string{"1", "https://dev.azure.com/other/P/_workitems/edit/2"}, "all work items must be in the same project"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newWIAssignEnv(t, false)
			err := e.run(t, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
