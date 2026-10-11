package create

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/browsertest"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/editortest"
	"github.com/snake-in-prod/ado-gh/internal/witfixtures"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

type wiCreateEnv struct {
	org     string
	reg     *httpmock.Registry
	ios     *iostreams.IOStreams
	in      *bytes.Buffer
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	browser *browsertest.Browser
	// ops holds the JSON Patch documents of create requests in order.
	ops [][]map[string]any
	// paths holds the unescaped paths of create requests.
	paths []string
}

// newWICreateEnv returns an env for its own org (the SDK caches discovery per base URL), with
// the location and Agile process metadata stubs registered.
func newWICreateEnv(t *testing.T, tty bool) *wiCreateEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "wicreate-" + strings.NewReplacer("/", "-", " ", "-", "@", "-").Replace(strings.ToLower(t.Name()))
	e := &wiCreateEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut, browser: &browsertest.Browser{}}
	e.reg.StubLocations(org, httpmock.WorkItemLocations...)
	return e
}

func (e *wiCreateEnv) stubProcess(t *testing.T) {
	t.Helper()
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypes"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "types"))))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypecategories"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "categories"))))
}

func (e *wiCreateEnv) stubMe() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"), httpmock.JSONResponse(map[string]any{
		"authenticatedUser": map[string]any{"id": "33333333-3333-3333-3333-333333333333", "providerDisplayName": "Me",
			"properties": map[string]any{"Account": map[string]any{"$value": "me@example.com"}}},
	}))
}

// stubNodes answers GET classificationnodes/{group} with root\child of My Proj.
func (e *wiCreateEnv) stubNodes(group, root, child string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/classificationnodes/"+group), httpmock.JSONResponse(map[string]any{
		"id": 1, "name": "My Proj", "path": `\My Proj\` + root,
		"children": []map[string]any{{"id": 2, "name": child, "path": `\My Proj\` + root + `\` + child}},
	}))
}

func (e *wiCreateEnv) stubFields(t *testing.T) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/fields"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Read(t, "fields.json"))))
}

// stubCreate answers the create request with work item 42 after recording it, or with the
// status and message when status is not 200.
func (e *wiCreateEnv) stubCreate(t *testing.T, status int, message string) {
	e.reg.Register(func(r *http.Request) bool {
		return r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/"+e.org+"/My Proj/_apis/wit/workitems/$")
	}, func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var ops []map[string]any
		require.NoError(t, json.Unmarshal(b, &ops))
		e.ops, e.paths = append(e.ops, ops), append(e.paths, r.URL.Path)
		if status != http.StatusOK {
			return httpmock.StatusJSONResponse(status, map[string]any{"message": message, "typeKey": "X"})(r)
		}
		return httpmock.JSONResponse(map[string]any{"id": 42, "rev": 1, "fields": map[string]any{}})(r)
	})
}

func (e *wiCreateEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	e.ios.In = wiCreateStdin{iotest.OneByteReader(e.in)}
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "web"}, nil
		},
		Browser:  e.browser,
		Prompter: &cmdutil.Prompter{IO: e.ios, Accessible: true},
	}
	cmd := NewCmdCreate(f)
	cmd.SetArgs(args)
	cmd.SetIn(e.in)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

// wiCreateStdin keeps stdin for the prompts: exec copies a non-file Stdin to the editor with
// io.Copy, which prefers WriteTo, so the fake editor reads nothing and the answers after it stay.
type wiCreateStdin struct{ io.Reader }

func (wiCreateStdin) WriteTo(io.Writer) (int64, error) { return 0, nil }

func (e *wiCreateEnv) itemURL() string {
	return "https://dev.azure.com/" + e.org + "/My%20Proj/_workitems/edit/42\n"
}

func wiCreateOp(op, path string, value any) map[string]any {
	m := map[string]any{"op": op, "path": path}
	if value != nil {
		m["value"] = value
	}
	return m
}

func TestWICreateAllFlags(t *testing.T) {
	e := newWICreateEnv(t, false)
	e.stubProcess(t)
	e.stubMe()
	e.stubNodes("areas", "Area", "Web")
	e.stubNodes("iterations", "Iteration", "Sprint 1")
	e.stubFields(t)
	e.stubCreate(t, http.StatusOK, "")

	require.NoError(t, e.run(t, "--type", "story", "-t", "Export", "-b", "Some **md**", "-a", "@me",
		"--area", "Web", "--iteration", "Sprint 1", "--tag", "ui, export", "--label", "csv", "--parent", "#7",
		"-p", "2", "-f", "Story Points=5", "-f", "Value Area="))
	e.reg.Verify(t)
	require.Len(t, e.ops, 1)
	assert.Equal(t, "/"+e.org+"/My Proj/_apis/wit/workitems/$User Story", e.paths[0])
	assert.Equal(t, []map[string]any{
		wiCreateOp("add", "/fields/System.Title", "Export"),
		wiCreateOp("add", "/fields/System.Description", "Some **md**"),
		wiCreateOp("add", "/multilineFieldsFormat/System.Description", "Markdown"),
		wiCreateOp("add", "/fields/System.AssignedTo", "me@example.com"),
		wiCreateOp("add", "/fields/System.AreaPath", `My Proj\Web`),
		wiCreateOp("add", "/fields/System.IterationPath", `My Proj\Sprint 1`),
		wiCreateOp("add", "/fields/System.Tags", "ui; export; csv"),
		wiCreateOp("add", "/fields/Microsoft.VSTS.Common.Priority", float64(2)),
		wiCreateOp("add", "/fields/Microsoft.VSTS.Scheduling.StoryPoints", float64(5)),
		wiCreateOp("remove", "/fields/Microsoft.VSTS.Common.ValueArea", nil),
		wiCreateOp("add", "/relations/-", map[string]any{
			"rel": "System.LinkTypes.Hierarchy-Reverse", "url": "https://dev.azure.com/" + e.org + "/_apis/wit/workItems/7",
		}),
	}, e.ops[0])
	assert.Equal(t, e.itemURL(), e.out.String())
	assert.Empty(t, e.errOut.String())
}

func TestWICreateIterationCurrent(t *testing.T) {
	e := newWICreateEnv(t, false)
	e.stubProcess(t)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/Web%20Team/_apis/work/teamsettings/iterations"),
		httpmock.StringResponse(`{"count":1,"value":[{"id":"a0000000-0000-0000-0000-000000000003","name":"Sprint 3","path":"My Proj\\Sprint 3"}]}`))
	e.stubCreate(t, http.StatusOK, "")

	require.NoError(t, e.run(t, "--type", "Bug", "-t", "T", "--iteration", "@current", "--team", "Web Team"))
	e.reg.Verify(t)
	assert.Contains(t, e.ops[0], wiCreateOp("add", "/fields/System.IterationPath", `My Proj\Sprint 3`))
}

func TestWICreateHTMLFallback(t *testing.T) {
	e := newWICreateEnv(t, false)
	e.stubProcess(t)
	e.stubCreate(t, http.StatusBadRequest, "Invalid path /multilineFieldsFormat/System.Description")
	e.stubCreate(t, http.StatusOK, "")

	require.NoError(t, e.run(t, "--type", "task", "-t", "T", "-b", "*hi*"))
	e.reg.Verify(t)
	require.Len(t, e.ops, 2)
	assert.Equal(t, []map[string]any{
		wiCreateOp("add", "/fields/System.Title", "T"),
		wiCreateOp("add", "/fields/System.Description", "<p><em>hi</em></p>\n"),
	}, e.ops[1])
}

func TestWICreateBodyFile(t *testing.T) {
	e := newWICreateEnv(t, false)
	e.stubProcess(t)
	e.stubCreate(t, http.StatusOK, "")
	e.in.WriteString("from stdin")

	require.NoError(t, e.run(t, "--type", "task", "-t", "T", "-F", "-"))
	assert.Contains(t, e.ops[0], wiCreateOp("add", "/fields/System.Description", "from stdin"))
}

func TestWICreateErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		stub    func(t *testing.T, e *wiCreateEnv)
		wantErr string
	}{
		{"missing type and title", nil, nil, "--type and --title required when not running interactively"},
		{"missing title", []string{"--type", "bug"}, nil, "--title required when not running interactively"},
		{"missing type with web", []string{"-w"}, nil, "--type required when not running interactively"},
		{"bad priority", []string{"--type", "bug", "-t", "T", "-p", "5"}, nil, "--priority must be between 1 and 4"},
		{"bad parent", []string{"--type", "bug", "-t", "T", "--parent", "x"}, nil, "invalid --parent"},
		{"unknown type", []string{"--type", "nope", "-t", "T"}, func(t *testing.T, e *wiCreateEnv) { e.stubProcess(t) },
			`unknown work item type "nope"`},
		{"unknown field", []string{"--type", "bug", "-t", "T", "-f", "Nope=1"}, func(t *testing.T, e *wiCreateEnv) {
			e.stubProcess(t)
			e.stubFields(t)
		}, `unknown field "Nope"`},
		{"unknown area", []string{"--type", "bug", "-t", "T", "--area", "Nope"}, func(t *testing.T, e *wiCreateEnv) {
			e.stubProcess(t)
			e.stubNodes("areas", "Area", "Web")
		}, `area path "Nope" not found`},
		{"create fails", []string{"--type", "bug", "-t", "T"}, func(t *testing.T, e *wiCreateEnv) {
			e.stubProcess(t)
			e.stubCreate(t, http.StatusBadRequest, "TF401320: Rule Error")
		}, "TF401320"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newWICreateEnv(t, false)
			if tt.stub != nil {
				tt.stub(t, e)
			}
			err := e.run(t, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.NotContains(t, e.out.String(), "_workitems/edit")
		})
	}
}

// TestWICreateEditorHelperProcess is not a test: it is the fake editor of editortest.Command.
func TestWICreateEditorHelperProcess(*testing.T) { editortest.HelperProcess() }

func TestWICreateTTYPrompts(t *testing.T) {
	e := newWICreateEnv(t, true)
	editor, _ := editortest.Command(t, "TestWICreateEditorHelperProcess", "Edited body")
	t.Setenv("ADO_EDITOR", editor)
	e.stubProcess(t)
	e.stubMe()
	e.stubCreate(t, http.StatusOK, "")
	// Type: the sorted usable Agile types are Bug, Epic, Feature, Issue, Task, User Story.
	// Then the title, "Add metadata" (2), pick Assignee (1) and Priority (5), answer them,
	// then "Submit" (1).
	e.in.WriteString("1\nPrompted title\n2\n1\n5\n0\n@me\n3\n1\n")

	require.NoError(t, e.run(t))
	e.reg.Verify(t)
	assert.Equal(t, "/"+e.org+"/My Proj/_apis/wit/workitems/$Bug", e.paths[0])
	assert.Equal(t, []map[string]any{
		wiCreateOp("add", "/fields/System.Title", "Prompted title"),
		wiCreateOp("add", "/fields/System.Description", "Edited body"),
		wiCreateOp("add", "/multilineFieldsFormat/System.Description", "Markdown"),
		wiCreateOp("add", "/fields/System.AssignedTo", "me@example.com"),
		wiCreateOp("add", "/fields/Microsoft.VSTS.Common.Priority", float64(3)),
	}, e.ops[0])
	assert.True(t, strings.HasSuffix(e.out.String(), "\n"+e.itemURL()), e.out.String())
	assert.Contains(t, e.errOut.String(), "Creating Bug in My Proj")
}

func TestWICreateTTYCancel(t *testing.T) {
	e := newWICreateEnv(t, true)
	editor, _ := editortest.Command(t, "TestWICreateEditorHelperProcess", "  \n")
	t.Setenv("ADO_EDITOR", editor)
	e.stubProcess(t)
	e.in.WriteString("Title\n3\n")
	require.ErrorIs(t, e.run(t, "--type", "bug"), cmdutil.CancelError)
	assert.Empty(t, e.ops)
}

func TestWICreateTTYFlagsSkipPrompts(t *testing.T) {
	e := newWICreateEnv(t, true)
	e.stubProcess(t)
	e.stubCreate(t, http.StatusOK, "")
	// Type and title given: no prompt and no editor.
	require.NoError(t, e.run(t, "--type", "bug", "-t", "T"))
	assert.Equal(t, []map[string]any{wiCreateOp("add", "/fields/System.Title", "T")}, e.ops[0])
}

func TestWICreateTTYEmptyTitle(t *testing.T) {
	e := newWICreateEnv(t, true)
	e.stubProcess(t)
	e.in.WriteString("\n")
	require.EqualError(t, e.run(t, "--type", "bug"), "a title is required")
}

func TestWICreateTTYBadPriority(t *testing.T) {
	e := newWICreateEnv(t, true)
	editor, _ := editortest.Command(t, "TestWICreateEditorHelperProcess", "")
	t.Setenv("ADO_EDITOR", editor)
	e.stubProcess(t)
	e.in.WriteString("T\n2\n5\n0\n9\n")
	require.EqualError(t, e.run(t, "--type", "bug"), `invalid priority "9": must be between 1 and 4`)
}

func TestWICreateTTYMetadata(t *testing.T) {
	e := newWICreateEnv(t, true)
	editor, _ := editortest.Command(t, "TestWICreateEditorHelperProcess", "")
	t.Setenv("ADO_EDITOR", editor)
	e.stubProcess(t)
	e.stubNodes("areas", "Area", "Web")
	e.stubNodes("iterations", "Iteration", "Sprint 1")
	e.stubCreate(t, http.StatusOK, "")
	// Add metadata: Tags (2), Area (3), Iteration (4), Parent (6); then Submit.
	e.in.WriteString("T\n2\n2\n3\n4\n6\n0\na,b\nWeb\nSprint 1\n9\n1\n")

	require.NoError(t, e.run(t, "--type", "bug"))
	e.reg.Verify(t)
	assert.Equal(t, []map[string]any{
		wiCreateOp("add", "/fields/System.Title", "T"),
		wiCreateOp("add", "/fields/System.AreaPath", `My Proj\Web`),
		wiCreateOp("add", "/fields/System.IterationPath", `My Proj\Sprint 1`),
		wiCreateOp("add", "/fields/System.Tags", "a; b"),
		wiCreateOp("add", "/relations/-", map[string]any{
			"rel": "System.LinkTypes.Hierarchy-Reverse", "url": "https://dev.azure.com/" + e.org + "/_apis/wit/workItems/9",
		}),
	}, e.ops[0])
}

func TestWICreateWeb(t *testing.T) {
	e := newWICreateEnv(t, true)
	e.stubProcess(t)
	e.stubMe()
	e.stubNodes("areas", "Area", "Web")
	e.stubNodes("iterations", "Iteration", `Sprint 1`)
	e.stubFields(t)

	require.NoError(t, e.run(t, "-w", "--type", "story", "-t", "Fix a+b & c#d", "-b", "Line 1\n**bold**",
		"-a", "@me", "--area", "Web", "--iteration", "Sprint 1", "-l", "x, y", "-p", "1",
		"-f", "Story Points=3", "-f", "Value Area=", "--parent", "7"))
	e.reg.Verify(t)
	require.Len(t, e.browser.URLs, 1)
	want := "https://dev.azure.com/" + e.org + "/My%20Proj/_workitems/create/User%20Story?" +
		"[System.Title]=Fix%20a%2Bb%20%26%20c%23d" +
		"&[System.Description]=Line%201%0A%2A%2Abold%2A%2A" +
		"&[System.AssignedTo]=me%40example.com" +
		"&[System.AreaPath]=My%20Proj%5CWeb" +
		"&[System.IterationPath]=My%20Proj%5CSprint%201" +
		"&[System.Tags]=x%3B%20y" +
		"&[Microsoft.VSTS.Common.Priority]=1" +
		"&[Microsoft.VSTS.Scheduling.StoryPoints]=3"
	assert.Equal(t, want, e.browser.URLs[0])
	assert.Contains(t, e.errOut.String(), "! --parent is ignored with --web")
	assert.Contains(t, e.errOut.String(), "Opening "+want)
	assert.Empty(t, e.ops, "nothing is created")
}

func TestWICreateWebTypeOnly(t *testing.T) {
	e := newWICreateEnv(t, false)
	e.stubProcess(t)
	require.NoError(t, e.run(t, "-w", "--type", "bug"))
	assert.Equal(t, []string{"https://dev.azure.com/" + e.org + "/My%20Proj/_workitems/create/Bug"}, e.browser.URLs)
}

func TestWICreateWebTooLong(t *testing.T) {
	e := newWICreateEnv(t, false)
	e.stubProcess(t)
	body := filepath.Join(t.TempDir(), "body.md")
	require.NoError(t, os.WriteFile(body, []byte(strings.Repeat("x", 8192)), 0o600))
	require.EqualError(t, e.run(t, "-w", "--type", "bug", "-F", body), "cannot open in browser: maximum URL length exceeded")
	assert.Empty(t, e.browser.URLs)
}
