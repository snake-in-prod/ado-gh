package restore

import (
	"bytes"
	"encoding/json"
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

type wiRestoreEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	out, errOut *bytes.Buffer
	// restores records the restored IDs in call order.
	restores []string
}

// newWIRestoreEnv returns an env for its own org (the SDK caches discovery per base URL).
func newWIRestoreEnv(t *testing.T, tty bool) *wiRestoreEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, _, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "wirestore-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &wiRestoreEnv{org: org, reg: &httpmock.Registry{}, ios: ios, out: out, errOut: errOut}
	e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	return e
}

func (e *wiRestoreEnv) run(t *testing.T, args ...string) error {
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
	}
	cmd := NewCmdRestore(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

// stubRestore answers the recycle bin PATCH of work item id in project, recording it.
func (e *wiRestoreEnv) stubRestore(t *testing.T, project string, id int, resp httpmock.Responder) {
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/"+project+"/_apis/wit/recyclebin/"+strconv.Itoa(id)),
		func(r *http.Request) (*http.Response, error) {
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, map[string]any{"isDeleted": false}, body)
			e.restores = append(e.restores, strconv.Itoa(id))
			return resp(r)
		})
}

func wiRestoreOK(id int, title string) httpmock.Responder {
	return httpmock.JSONResponse(map[string]any{"id": id, "name": title, "type": "Issue", "project": "My Proj"})
}

func wiRestoreNotFound(id int) httpmock.Responder {
	return httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{
		"message": "TF401232: Work item " + strconv.Itoa(id) + " does not exist, or you do not have permissions to read it.",
	})
}

func TestWIRestoreOne(t *testing.T) {
	e := newWIRestoreEnv(t, false)
	e.stubRestore(t, "My%20Proj", 123, wiRestoreOK(123, "Fix login"))

	require.NoError(t, e.run(t, "#123"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"123"}, e.restores)
	assert.Equal(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_workitems/edit/123\n", e.out.String())
	assert.Empty(t, e.errOut.String())
}

func TestWIRestoreTTY(t *testing.T) {
	e := newWIRestoreEnv(t, true)
	e.stubRestore(t, "My%20Proj", 123, wiRestoreOK(123, "Fix login"))

	require.NoError(t, e.run(t, "123"))
	assert.Contains(t, e.errOut.String(), "Restored work item 123 (Fix login)")
	assert.Contains(t, e.out.String(), "/_workitems/edit/123")
}

func TestWIRestoreURLProject(t *testing.T) {
	e := newWIRestoreEnv(t, false)
	e.stubRestore(t, "Other", 7, wiRestoreOK(7, "x"))

	require.NoError(t, e.run(t, "https://dev.azure.com/"+e.org+"/Other/_workitems/edit/7"))
	e.reg.Verify(t)
	assert.Equal(t, "https://dev.azure.com/"+e.org+"/Other/_workitems/edit/7\n", e.out.String())
}

func TestWIRestoreNotInRecycleBin(t *testing.T) {
	e := newWIRestoreEnv(t, false)
	e.stubRestore(t, "My%20Proj", 5, wiRestoreNotFound(5))

	err := e.run(t, "5")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "work item 5 is not in the recycle bin")
	assert.Empty(t, e.out.String())
}

func TestWIRestorePartialFailure(t *testing.T) {
	e := newWIRestoreEnv(t, false)
	e.stubRestore(t, "My%20Proj", 1, wiRestoreOK(1, "a"))
	e.stubRestore(t, "My%20Proj", 2, wiRestoreNotFound(2))
	e.stubRestore(t, "My%20Proj", 3, wiRestoreOK(3, "c"))

	err := e.run(t, "1", "2", "3")
	require.EqualError(t, err, "failed to restore work items 2; done: 1, 3")
	e.reg.Verify(t)
	assert.Equal(t, []string{"1", "2", "3"}, e.restores)
	assert.Contains(t, e.errOut.String(), "Failed to restore work item 2: work item 2 is not in the recycle bin")
	assert.Equal(t, 2, strings.Count(e.out.String(), "/_workitems/edit/"))
}

func TestWIRestoreAllFail(t *testing.T) {
	e := newWIRestoreEnv(t, false)
	e.stubRestore(t, "My%20Proj", 1, wiRestoreNotFound(1))
	e.stubRestore(t, "My%20Proj", 2, httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))

	err := e.run(t, "1", "2")
	require.EqualError(t, err, "failed to restore work items 1, 2")
	assert.Contains(t, e.errOut.String(), "boom")
}

func TestWIRestoreArgs(t *testing.T) {
	e := newWIRestoreEnv(t, false)
	require.Error(t, e.run(t))
	err := e.run(t, "abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid work item reference "abc"`)
	assert.Empty(t, e.restores)
}
