package link

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

type wiLinkEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	in          *bytes.Buffer
	out, errOut *bytes.Buffer
	// ops are the JSON Patch documents sent, in order.
	ops [][]map[string]any
}

// newWILinkEnv returns an env for its own org (the SDK caches discovery per base URL).
func newWILinkEnv(t *testing.T, tty bool) *wiLinkEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "wilink-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &wiLinkEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut}
	e.reg.StubLocations(org, slices.Concat(httpmock.WorkItemLocations, httpmock.GitPRLocations)...)
	e.reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/wit/workitemrelationtypes"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Read(t, "live_relationtypes.json"))))
	return e
}

func (e *wiLinkEnv) run(t *testing.T, args ...string) error {
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
	cmd := NewCmdLink(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

func (e *wiLinkEnv) wiURL(id int) string {
	return "https://dev.azure.com/" + e.org + "/_apis/wit/workItems/" + strconv.Itoa(id)
}

// stubItem answers GET work item id (rev 3) with rels.
func (e *wiLinkEnv) stubItem(t *testing.T, id int, rels ...map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitems/"+strconv.Itoa(id)), func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "relations", r.URL.Query().Get("$expand"))
		return httpmock.JSONResponse(map[string]any{"id": id, "rev": 3, "fields": map[string]any{}, "relations": rels})(r)
	})
}

// stubUpdate answers one PATCH of work item id after recording it: rev 4 on success, else
// status with message.
func (e *wiLinkEnv) stubUpdate(t *testing.T, id, status int, message string) {
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

func wiLinkOp(op, path string, value any) map[string]any {
	m := map[string]any{"op": op, "path": path}
	if value != nil {
		m["value"] = value
	}
	return m
}

func (e *wiLinkEnv) wiLinkAdd(rel string, id int) map[string]any {
	return wiLinkOp("add", "/relations/-", map[string]any{"rel": rel, "url": e.wiURL(id)})
}

func TestWILinkAddsRelations(t *testing.T) {
	e := newWILinkEnv(t, false)
	e.stubItem(t, 123, map[string]any{"rel": "System.LinkTypes.Related", "url": e.wiURL(5)})
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/45"), httpmock.JSONResponse(map[string]any{
		"pullRequestId": 45, "repository": map[string]any{"id": "22222222-2222-2222-2222-222222222222", "project": map[string]any{"id": "11111111-1111-1111-1111-111111111111"}},
	}))
	for range 5 {
		e.stubUpdate(t, 123, http.StatusOK, "")
	}

	require.NoError(t, e.run(t, "#123", "--parent", "100", "--child", "9", "--related", "5", "--link", "Predecessor=12",
		"--pr", "45", "--url", "https://x.test/spec"))
	e.reg.Verify(t)
	assert.Equal(t, [][]map[string]any{
		{e.wiLinkAdd("System.LinkTypes.Hierarchy-Reverse", 100)},
		{e.wiLinkAdd("System.LinkTypes.Hierarchy-Forward", 9)},
		{e.wiLinkAdd("System.LinkTypes.Dependency-Reverse", 12)},
		{wiLinkOp("add", "/relations/-", map[string]any{"rel": "ArtifactLink",
			"url":        "vstfs:///Git/PullRequestId/11111111-1111-1111-1111-111111111111%2F22222222-2222-2222-2222-222222222222%2F45",
			"attributes": map[string]any{"name": "Pull Request"}})},
		{wiLinkOp("add", "/relations/-", map[string]any{"rel": "Hyperlink", "url": "https://x.test/spec"})},
	}, e.ops, "the existing related link is not sent")
	assert.Equal(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_workitems/edit/123\n", e.out.String())
	assert.Contains(t, e.errOut.String(), "work item 123 already has the System.LinkTypes.Related:5 link")
}

func TestWILinkReplaceParent(t *testing.T) {
	stubParent := func(t *testing.T, e *wiLinkEnv) {
		e.stubItem(t, 123,
			map[string]any{"rel": "System.LinkTypes.Related", "url": e.wiURL(5)},
			map[string]any{"rel": "System.LinkTypes.Hierarchy-Reverse", "url": e.wiURL(7)})
	}
	replace := func(e *wiLinkEnv) []map[string]any {
		return []map[string]any{
			wiLinkOp("test", "/rev", float64(3)),
			wiLinkOp("remove", "/relations/1", nil),
			e.wiLinkAdd("System.LinkTypes.Hierarchy-Reverse", 100),
		}
	}

	t.Run("--yes replaces in one patch", func(t *testing.T) {
		e := newWILinkEnv(t, false)
		stubParent(t, e)
		e.stubUpdate(t, 123, http.StatusOK, "")
		require.NoError(t, e.run(t, "123", "--parent", "100", "--yes"))
		e.reg.Verify(t)
		assert.Equal(t, [][]map[string]any{replace(e)}, e.ops)
	})

	t.Run("confirmed on TTY", func(t *testing.T) {
		e := newWILinkEnv(t, true)
		e.in.WriteString("y\n")
		stubParent(t, e)
		e.stubUpdate(t, 123, http.StatusOK, "")
		require.NoError(t, e.run(t, "123", "--link", "parent=100"))
		e.reg.Verify(t)
		assert.Contains(t, e.out.String(), "Replace parent #7 of work item 123 with #100?")
		assert.Equal(t, [][]map[string]any{replace(e)}, e.ops)
	})

	t.Run("declined on TTY", func(t *testing.T) {
		e := newWILinkEnv(t, true)
		e.in.WriteString("n\n")
		stubParent(t, e)
		err := e.run(t, "123", "--parent", "100", "--child", "9")
		assert.ErrorIs(t, err, cmdutil.CancelError)
		assert.Empty(t, e.ops, "nothing is linked when the replacement is declined")
	})

	t.Run("non-TTY needs --yes", func(t *testing.T) {
		e := newWILinkEnv(t, false)
		stubParent(t, e)
		err := e.run(t, "123", "--parent", "100")
		assert.EqualError(t, err, "work item 123 already has parent #7; pass --yes to replace it")
		assert.Empty(t, e.ops)
	})

	t.Run("same parent is a no-op", func(t *testing.T) {
		e := newWILinkEnv(t, false)
		stubParent(t, e)
		require.NoError(t, e.run(t, "123", "--parent", "7"))
		assert.Empty(t, e.ops)
	})

	t.Run("rev conflict", func(t *testing.T) {
		e := newWILinkEnv(t, false)
		stubParent(t, e)
		e.stubUpdate(t, 123, http.StatusBadRequest, "VS403351: Test Operation for path /rev failed")
		err := e.run(t, "123", "--parent", "100", "-y")
		assert.EqualError(t, err, "work item 123 changed since read; retry")
	})
}

func TestWILinkNoAutoReplaceForDuplicate(t *testing.T) {
	e := newWILinkEnv(t, false)
	e.stubItem(t, 123, map[string]any{"rel": "System.LinkTypes.Duplicate-Reverse", "url": e.wiURL(7)})
	e.stubUpdate(t, 123, http.StatusBadRequest, "TF201036: only one Duplicate Of link")
	err := e.run(t, "123", "--link", "Duplicate Of=8")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TF201036")
	assert.Equal(t, [][]map[string]any{{e.wiLinkAdd("System.LinkTypes.Duplicate-Reverse", 8)}}, e.ops, "only an add, no remove")
}

func TestWILinkServerErrors(t *testing.T) {
	t.Run("TF201035 is success", func(t *testing.T) {
		e := newWILinkEnv(t, false)
		e.stubItem(t, 123)
		e.stubUpdate(t, 123, http.StatusBadRequest, "TF201035: Relation already exists.")
		require.NoError(t, e.run(t, "123", "--related", "5"))
		assert.Contains(t, e.out.String(), "/_workitems/edit/123")
	})

	t.Run("TF201036 on a child names the fix", func(t *testing.T) {
		e := newWILinkEnv(t, false)
		e.stubItem(t, 123)
		e.stubUpdate(t, 123, http.StatusBadRequest, "TF201036: You cannot add a Child link between work items 123 and 9.")
		err := e.run(t, "123", "--child", "9")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "work item 9 already has a parent; replace it with: ado wi link 9 --parent 123")
	})

	t.Run("TF201036 on a parent", func(t *testing.T) {
		e := newWILinkEnv(t, false)
		e.stubItem(t, 123)
		e.stubUpdate(t, 123, http.StatusBadRequest, "TF201036: You cannot add a Child link.")
		err := e.run(t, "123", "--parent", "100")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "a work item can have only one parent")
	})

	t.Run("partial failure summary", func(t *testing.T) {
		e := newWILinkEnv(t, false)
		e.stubItem(t, 123)
		e.stubUpdate(t, 123, http.StatusOK, "")
		e.stubUpdate(t, 123, http.StatusNotFound, "TF401232: Work item 6 does not exist")
		err := e.run(t, "123", "--related", "5", "--related", "6")
		assert.ErrorIs(t, err, cmdutil.SilentError)
		assert.Contains(t, e.errOut.String(), "X failed to add System.LinkTypes.Related:6: TF401232")
		assert.Contains(t, e.errOut.String(), "added: System.LinkTypes.Related:5; failed: System.LinkTypes.Related:6")
		assert.Empty(t, e.out.String())
	})
}

func TestWILinkErrors(t *testing.T) {
	t.Run("no link flags", func(t *testing.T) {
		e := newWILinkEnv(t, false)
		err := e.run(t, "123")
		assert.ErrorContains(t, err, "specify a link")
	})
	t.Run("bad ref", func(t *testing.T) {
		e := newWILinkEnv(t, false)
		err := e.run(t, "abc", "--related", "5")
		assert.ErrorContains(t, err, "invalid work item reference")
	})
	t.Run("work item not found", func(t *testing.T) {
		e := newWILinkEnv(t, false)
		e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitems/123"),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401232: Work item 123 does not exist"}))
		err := e.run(t, "123", "--related", "5")
		assert.ErrorContains(t, err, "TF401232")
	})
}
