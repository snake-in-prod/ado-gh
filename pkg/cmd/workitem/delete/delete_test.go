package delete

import (
	"bytes"
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

type wiDeleteEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	in          *bytes.Buffer
	out, errOut *bytes.Buffer
	// deletes records the DELETE calls as "id" or "id destroy".
	deletes []string
}

// newWIDeleteEnv returns an env for its own org (the SDK caches discovery per base URL).
func newWIDeleteEnv(t *testing.T, tty bool) *wiDeleteEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "widelete-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &wiDeleteEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut}
	e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	return e
}

func (e *wiDeleteEnv) run(t *testing.T, args ...string) error {
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
	cmd := NewCmdDelete(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

// stubDelete answers the DELETE of work item id with resp, recording it.
func (e *wiDeleteEnv) stubDelete(t *testing.T, id int, resp httpmock.Responder) {
	e.reg.Register(httpmock.REST(http.MethodDelete, e.org+"/My%20Proj/_apis/wit/workitems/"+strconv.Itoa(id)),
		func(r *http.Request) (*http.Response, error) {
			call := strconv.Itoa(id)
			if d := r.URL.Query().Get("destroy"); d != "" {
				assert.Equal(t, "true", d)
				call += " destroy"
			}
			e.deletes = append(e.deletes, call)
			return resp(r)
		})
}

// stubTitles answers the batch title lookup with the given id -> title items.
func (e *wiDeleteEnv) stubTitles(items ...map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodPost, e.org+"/My%20Proj/_apis/wit/workitemsbatch"),
		httpmock.JSONResponse(map[string]any{"count": len(items), "value": items}))
}

func wiDeleteItem(id int, title string) map[string]any {
	return map[string]any{"id": id, "rev": 1, "fields": map[string]any{"System.Title": title}}
}

func wiDeleteOK() httpmock.Responder {
	return httpmock.JSONResponse(map[string]any{"id": 1, "code": 200})
}

func TestWIDeleteSoftWithYes(t *testing.T) {
	e := newWIDeleteEnv(t, false)
	e.stubDelete(t, 123, wiDeleteOK())

	require.NoError(t, e.run(t, "123", "--yes"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"123"}, e.deletes)
	assert.Contains(t, e.errOut.String(), "Moved work item 123 to the recycle bin")
	assert.Contains(t, e.errOut.String(), "ado wi restore 123")
	assert.Empty(t, e.out.String())
}

func TestWIDeletePermanent(t *testing.T) {
	e := newWIDeleteEnv(t, false)
	e.stubDelete(t, 123, httpmock.StatusStringResponse(http.StatusNoContent, ""))

	require.NoError(t, e.run(t, "#123", "--permanent", "-y"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"123 destroy"}, e.deletes)
	assert.Contains(t, e.errOut.String(), "Permanently deleted work item 123")
	assert.NotContains(t, e.errOut.String(), "restore")
}

func TestWIDeleteNonTTYNeedsYes(t *testing.T) {
	e := newWIDeleteEnv(t, false)
	err := e.run(t, "123")
	require.EqualError(t, err, "--yes required when not running interactively")
	assert.Empty(t, e.deletes)
}

func TestWIDeleteConfirm(t *testing.T) {
	t.Run("lists IDs and titles", func(t *testing.T) {
		e := newWIDeleteEnv(t, true)
		e.in.WriteString("y\n")
		e.stubTitles(wiDeleteItem(123, "Fix login"), wiDeleteItem(124, "Add logout"))
		e.stubDelete(t, 123, wiDeleteOK())
		e.stubDelete(t, 124, wiDeleteOK())

		require.NoError(t, e.run(t, "123", "124"))
		e.reg.Verify(t)
		assert.Contains(t, e.out.String(), "Move 2 work items to the recycle bin?")
		assert.Contains(t, e.out.String(), "123 Fix login")
		assert.Contains(t, e.out.String(), "124 Add logout")
		assert.Equal(t, []string{"123", "124"}, e.deletes)
		assert.Contains(t, e.errOut.String(), "ado wi restore 123 124")
	})
	t.Run("permanent names irreversibility", func(t *testing.T) {
		e := newWIDeleteEnv(t, true)
		e.in.WriteString("y\n")
		e.stubTitles(wiDeleteItem(123, "Fix login"))
		e.stubDelete(t, 123, wiDeleteOK())

		require.NoError(t, e.run(t, "123", "--permanent"))
		assert.Contains(t, e.out.String(), "Permanently delete work item 123? This cannot be undone.")
		assert.Equal(t, []string{"123 destroy"}, e.deletes)
	})
	t.Run("declined", func(t *testing.T) {
		e := newWIDeleteEnv(t, true)
		e.in.WriteString("n\n")
		e.stubTitles(wiDeleteItem(123, "Fix login"))

		require.ErrorIs(t, e.run(t, "123"), cmdutil.CancelError)
		assert.Empty(t, e.deletes)
	})
	t.Run("-y skips", func(t *testing.T) {
		e := newWIDeleteEnv(t, true)
		e.stubDelete(t, 123, wiDeleteOK())

		require.NoError(t, e.run(t, "123", "-y"))
		e.reg.Verify(t)
		assert.Empty(t, e.out.String())
		assert.Equal(t, []string{"123"}, e.deletes)
	})
}

func TestWIDeleteForbiddenHint(t *testing.T) {
	forbidden := httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "TF237111: denied"})
	t.Run("soft", func(t *testing.T) {
		e := newWIDeleteEnv(t, false)
		e.stubDelete(t, 123, forbidden)
		err := e.run(t, "123", "-y")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "TF237111")
		assert.Contains(t, err.Error(), `"Delete and restore work items"`)
		assert.Contains(t, err.Error(), `"Permanently delete work items"`)
	})
	t.Run("permanent", func(t *testing.T) {
		e := newWIDeleteEnv(t, false)
		e.stubDelete(t, 123, forbidden)
		err := e.run(t, "123", "--permanent", "-y")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"Permanently delete work items"`)
		assert.NotContains(t, err.Error(), `"Delete and restore work items"`)
	})
}

func TestWIDeletePartialFailure(t *testing.T) {
	e := newWIDeleteEnv(t, false)
	e.stubDelete(t, 1, wiDeleteOK())
	e.stubDelete(t, 2, httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401232: Work item 2 does not exist"}))
	e.stubDelete(t, 3, wiDeleteOK())

	err := e.run(t, "1", "2", "3", "-y")
	require.EqualError(t, err, "failed to delete work items 2; done: 1, 3")
	e.reg.Verify(t)
	assert.Equal(t, []string{"1", "2", "3"}, e.deletes)
	assert.Contains(t, e.errOut.String(), "Failed to delete work item 2: TF401232")
	assert.Contains(t, e.errOut.String(), "ado wi restore 1 3")
}

func TestWIDeleteAllFail(t *testing.T) {
	e := newWIDeleteEnv(t, false)
	gone := httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "gone"})
	e.stubDelete(t, 1, gone)
	e.stubDelete(t, 2, gone)

	require.EqualError(t, e.run(t, "1", "2", "-y"), "failed to delete work items 1, 2")
	assert.NotContains(t, e.errOut.String(), "restore")
}

func TestWIDeleteSingleFailureReturnsError(t *testing.T) {
	e := newWIDeleteEnv(t, false)
	e.stubDelete(t, 2, httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401232: nope"}))

	err := e.run(t, "2", "-y")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TF401232: nope")
}

func TestWIDeleteURLRef(t *testing.T) {
	e := newWIDeleteEnv(t, false)
	e.stubDelete(t, 9, wiDeleteOK())
	url := "https://dev.azure.com/" + e.org + "/My%20Proj/_workitems/edit/9"

	require.NoError(t, e.run(t, url, "-y"))
	e.reg.Verify(t)
	assert.Equal(t, []string{"9"}, e.deletes)
	assert.Contains(t, e.errOut.String(), "ado wi restore "+url)
}

func TestWIDeleteInvalidRef(t *testing.T) {
	e := newWIDeleteEnv(t, false)
	err := e.run(t, "abc", "-y")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid work item reference "abc"`)
	assert.Empty(t, e.deletes)
}
