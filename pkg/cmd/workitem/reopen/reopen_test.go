package reopen

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

// The reopen logic is tested in shared (statechange_test.go); these tests cover the flags.

// wiReopenRun runs `reopen args` against an org with agile User Story 7 in state Closed when
// stub is true, plus the field and reason stubs when fields is true, and returns the patches
// sent and the error.
func wiReopenRun(t *testing.T, stub, fields bool, args ...string) ([][]map[string]any, error) {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, _, _ := iostreams.Test()
	org := "wireopen-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	reg := &httpmock.Registry{}
	var patches [][]map[string]any
	if stub {
		reg.StubLocations(org, httpmock.WorkItemLocations...)
		reg.Register(httpmock.REST(http.MethodGet, org+"/My%20Proj/_apis/wit/workitemtypes"),
			httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "types"))))
		reg.Register(httpmock.REST(http.MethodGet, org+"/My%20Proj/_apis/wit/workitemtypecategories"),
			httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "categories"))))
		reg.Register(httpmock.REST(http.MethodGet, org+"/My%20Proj/_apis/wit/workitems/7"), httpmock.JSONResponse(map[string]any{
			"id": 7, "rev": 3, "fields": map[string]any{"System.WorkItemType": "User Story", "System.State": "Closed", "System.Title": "T"},
		}))
		reg.Register(httpmock.REST(http.MethodPatch, org+"/My%20Proj/_apis/wit/workitems/7"), func(r *http.Request) (*http.Response, error) {
			b, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			var ops []map[string]any
			require.NoError(t, json.Unmarshal(b, &ops))
			patches = append(patches, ops)
			return httpmock.JSONResponse(map[string]any{"id": 7, "rev": 4})(r)
		})
	}
	if fields {
		reg.Register(httpmock.REST(http.MethodGet, org+"/My%20Proj/_apis/wit/workitemtypes/User%20Story/fields/System.Reason"),
			httpmock.JSONResponse(map[string]any{"referenceName": "System.Reason", "allowedValues": []string{"Reactivated"}}))
		reg.Register(httpmock.REST(http.MethodGet, org+"/My%20Proj/_apis/wit/fields"),
			httpmock.JSONResponse(json.RawMessage(witfixtures.Read(t, "fields.json"))))
	}
	cfg, err := config.Load()
	require.NoError(t, err)
	f := &cmdutil.Factory{
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: org, Project: "My Proj", Name: "web"}, nil
		},
		Prompter: &cmdutil.Prompter{IO: ios, Accessible: true},
	}
	cmd := NewCmdReopen(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err = cmd.Execute()
	reg.Verify(t)
	return patches, err
}

func wiReopenOps(state string, extra ...map[string]any) [][]map[string]any {
	return [][]map[string]any{append([]map[string]any{
		{"op": "test", "path": "/rev", "value": float64(3)},
		{"op": "add", "path": "/fields/System.State", "value": state},
	}, extra...)}
}

func TestWIReopenDefault(t *testing.T) {
	patches, err := wiReopenRun(t, true, false, "7")
	require.NoError(t, err)
	assert.Equal(t, wiReopenOps("New"), patches)
}

func TestWIReopenFlags(t *testing.T) {
	patches, err := wiReopenRun(t, true, true, "7", "--state", "active", "-r", "reactivated", "-c", "again", "-f", "Story Points=5")
	require.NoError(t, err)
	assert.Equal(t, wiReopenOps("Active",
		map[string]any{"op": "add", "path": "/fields/System.Reason", "value": "Reactivated"},
		map[string]any{"op": "add", "path": "/fields/Microsoft.VSTS.Scheduling.StoryPoints", "value": float64(5)},
		map[string]any{"op": "add", "path": "/fields/System.History", "value": "again"},
		map[string]any{"op": "add", "path": "/multilineFieldsFormat/System.History", "value": "Markdown"},
	), patches)
}

func TestWIReopenArgs(t *testing.T) {
	_, err := wiReopenRun(t, false, false)
	assert.EqualError(t, err, "requires at least 1 arg(s), only received 0")
}
