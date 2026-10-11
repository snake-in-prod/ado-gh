package close

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
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

// The close logic is tested in shared (statechange_test.go); these tests cover the flags.

type wiCloseEnv struct {
	org string
	reg *httpmock.Registry
	ios *iostreams.IOStreams
	// patches holds the JSON Patch documents sent to work item 7.
	patches [][]map[string]any
}

func newWICloseEnv(t *testing.T) *wiCloseEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, _, _ := iostreams.Test()
	org := "wiclose-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &wiCloseEnv{org: org, reg: &httpmock.Registry{}, ios: ios}
	e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	return e
}

// stubItem serves an agile work item 7 of type typ in state Active and records its patch.
func (e *wiCloseEnv) stubItem(t *testing.T, typ string) {
	t.Helper()
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypes"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "types"))))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypecategories"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "categories"))))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitems/7"),
		httpmock.JSONResponse(map[string]any{"id": 7, "rev": 3, "fields": map[string]any{
			"System.WorkItemType": typ, "System.State": "Active", "System.Title": "Item 7",
		}}))
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/My%20Proj/_apis/wit/workitems/7"),
		func(r *http.Request) (*http.Response, error) {
			b, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			var ops []map[string]any
			require.NoError(t, json.Unmarshal(b, &ops))
			e.patches = append(e.patches, ops)
			return httpmock.JSONResponse(map[string]any{"id": 7, "rev": 4, "fields": map[string]any{}})(r)
		})
}

func (e *wiCloseEnv) run(t *testing.T, args ...string) error {
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
	cmd := NewCmdClose(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	return cmd.Execute()
}

func wiCloseOp(op, path string, value any) map[string]any {
	return map[string]any{"op": op, "path": path, "value": value}
}

// wiCloseOps is the patch moving a rev 3 item to state, plus extra ops.
func wiCloseOps(state string, extra ...map[string]any) [][]map[string]any {
	return [][]map[string]any{append([]map[string]any{
		wiCloseOp("test", "/rev", float64(3)),
		wiCloseOp("add", "/fields/System.State", state),
	}, extra...)}
}

func TestWICloseCategoryFlags(t *testing.T) {
	tests := []struct {
		name, typ string
		args      []string
		want      string
	}{
		{"default completes", "User Story", nil, "Closed"},
		{"resolve", "Bug", []string{"--resolve"}, "Resolved"},
		{"removed", "User Story", []string{"--removed"}, "Removed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newWICloseEnv(t)
			e.stubItem(t, tt.typ)
			require.NoError(t, e.run(t, append([]string{"7"}, tt.args...)...))
			e.reg.Verify(t)
			assert.Equal(t, wiCloseOps(tt.want), e.patches)
		})
	}
}

func TestWICloseValueFlags(t *testing.T) {
	e := newWICloseEnv(t)
	e.stubItem(t, "Bug")
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypes/Bug/fields/System.Reason"),
		httpmock.JSONResponse(map[string]any{"referenceName": "System.Reason", "allowedValues": []string{"Fixed"}}))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/fields"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Read(t, "fields.json"))))

	require.NoError(t, e.run(t, "7", "--resolve", "--state", "closed", "-r", "fixed", "-c", "done", "-f", "Story Points=5"))
	e.reg.Verify(t)
	assert.Equal(t, wiCloseOps("Closed",
		wiCloseOp("add", "/fields/System.Reason", "Fixed"),
		wiCloseOp("add", "/fields/Microsoft.VSTS.Scheduling.StoryPoints", float64(5)),
		wiCloseOp("add", "/fields/System.History", "done"),
		wiCloseOp("add", "/multilineFieldsFormat/System.History", "Markdown"),
	), e.patches)
}

func TestWICloseArgs(t *testing.T) {
	e := newWICloseEnv(t)
	assert.Error(t, e.run(t))
	assert.EqualError(t, e.run(t, "7", "--resolve", "--removed"),
		"if any flags in the group [resolve removed] are set none of the others can be; [removed resolve] were all set")
	assert.EqualError(t, e.run(t, "abc"), `invalid work item reference "abc": expected an ID, #ID or a work item URL`)
	assert.Empty(t, e.reg.Requests)
}
