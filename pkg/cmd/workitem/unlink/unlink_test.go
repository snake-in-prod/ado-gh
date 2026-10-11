package unlink

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
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

type wiUnlinkEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	in          *bytes.Buffer
	out, errOut *bytes.Buffer
	// ops are the JSON Patch documents sent, in order.
	ops [][]map[string]any
}

// newWIUnlinkEnv returns an env for its own org (the SDK caches discovery per base URL).
func newWIUnlinkEnv(t *testing.T, tty bool) *wiUnlinkEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "wiunlink-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &wiUnlinkEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut}
	e.reg.StubLocations(org, slices.Concat(httpmock.WorkItemLocations, httpmock.GitPRLocations)...)
	e.reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/wit/workitemrelationtypes"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Read(t, "live_relationtypes.json"))))
	return e
}

func (e *wiUnlinkEnv) run(t *testing.T, args ...string) error {
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
	cmd := NewCmdUnlink(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

func (e *wiUnlinkEnv) wiURL(id int) string {
	return "https://dev.azure.com/" + e.org + "/_apis/wit/workItems/" + strconv.Itoa(id)
}

// stubItem answers GET work item id (rev 3) with rels.
func (e *wiUnlinkEnv) stubItem(t *testing.T, id int, rels ...map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitems/"+strconv.Itoa(id)), func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "relations", r.URL.Query().Get("$expand"))
		return httpmock.JSONResponse(map[string]any{"id": id, "rev": 3, "fields": map[string]any{}, "relations": rels})(r)
	})
}

// stubUpdate answers one PATCH of work item id after recording it: rev 4 on success, else
// status with message.
func (e *wiUnlinkEnv) stubUpdate(t *testing.T, id, status int, message string) {
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/My%20Proj/_apis/wit/workitems/"+strconv.Itoa(id)), func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var ops []map[string]any
		require.NoError(t, json.Unmarshal(b, &ops))
		e.ops = append(e.ops, ops)
		if status != http.StatusOK {
			return httpmock.StatusJSONResponse(status, map[string]any{"message": message})(r)
		}
		return httpmock.JSONResponse(map[string]any{"id": id, "rev": 4, "fields": map[string]any{}})(r)
	})
}

func wiUnlinkOp(op, path string, value any) map[string]any {
	m := map[string]any{"op": op, "path": path}
	if value != nil {
		m["value"] = value
	}
	return m
}

func TestWIUnlinkRemovesInOnePatch(t *testing.T) {
	e := newWIUnlinkEnv(t, false)
	const ids = "11111111-1111-1111-1111-111111111111%2F22222222-2222-2222-2222-222222222222%2F"
	e.stubItem(t, 123,
		// Another host form and casing than the URLs ado builds: matched by rel + ID suffix.
		map[string]any{"rel": "System.LinkTypes.Hierarchy-Reverse", "url": "https://" + e.org + ".visualstudio.com/_apis/wit/WorkItems/100"},
		map[string]any{"rel": "System.LinkTypes.Related", "url": e.wiURL(5)},
		map[string]any{"rel": "ArtifactLink", "url": "vstfs:///Git/Ref/" + ids + "GBfeature%2Fx", "attributes": map[string]any{"name": "Branch"}},
		map[string]any{"rel": "Hyperlink", "url": "https://x.test/spec"},
	)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web"), httpmock.JSONResponse(map[string]any{
		"id": "22222222-2222-2222-2222-222222222222", "project": map[string]any{"id": "11111111-1111-1111-1111-111111111111"},
	}))
	e.stubUpdate(t, 123, http.StatusOK, "")

	require.NoError(t, e.run(t, "123", "--parent", "100", "--branch", "feature/x", "--url", "https://x.test/spec"))
	e.reg.Verify(t)
	assert.Equal(t, [][]map[string]any{{
		wiUnlinkOp("test", "/rev", float64(3)),
		wiUnlinkOp("remove", "/relations/3", nil),
		wiUnlinkOp("remove", "/relations/2", nil),
		wiUnlinkOp("remove", "/relations/0", nil),
	}}, e.ops)
	assert.Equal(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_workitems/edit/123\n", e.out.String())
}

func TestWIUnlinkErrors(t *testing.T) {
	t.Run("no match", func(t *testing.T) {
		e := newWIUnlinkEnv(t, false)
		e.stubItem(t, 123, map[string]any{"rel": "System.LinkTypes.Related", "url": e.wiURL(5)})
		err := e.run(t, "123", "--child", "5")
		assert.EqualError(t, err, "work item 123 has no System.LinkTypes.Hierarchy-Forward:5 link")
		assert.Empty(t, e.ops)
	})
	t.Run("rev conflict", func(t *testing.T) {
		e := newWIUnlinkEnv(t, false)
		e.stubItem(t, 123, map[string]any{"rel": "System.LinkTypes.Related", "url": e.wiURL(5)})
		e.stubUpdate(t, 123, http.StatusBadRequest, "VS403351: Test Operation for path /rev failed")
		err := e.run(t, "123", "--related", "5")
		assert.EqualError(t, err, "work item 123 changed since read; retry")
	})
	t.Run("server error", func(t *testing.T) {
		e := newWIUnlinkEnv(t, false)
		e.stubItem(t, 123, map[string]any{"rel": "System.LinkTypes.Related", "url": e.wiURL(5)})
		e.stubUpdate(t, 123, http.StatusForbidden, "denied")
		err := e.run(t, "123", "--related", "5")
		assert.ErrorContains(t, err, "denied")
	})
	t.Run("no link flags", func(t *testing.T) {
		e := newWIUnlinkEnv(t, false)
		assert.ErrorContains(t, e.run(t, "123"), "specify a link")
	})
	t.Run("bad ref", func(t *testing.T) {
		e := newWIUnlinkEnv(t, false)
		assert.ErrorContains(t, e.run(t, "abc", "--related", "5"), "invalid work item reference")
	})
	t.Run("work item not found", func(t *testing.T) {
		e := newWIUnlinkEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitems/123"),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401232: Work item 123 does not exist"}))
		assert.ErrorContains(t, e.run(t, "123", "--related", "5"), "TF401232")
	})
}
