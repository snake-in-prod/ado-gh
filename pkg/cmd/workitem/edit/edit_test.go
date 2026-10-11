package edit

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
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

type wiEditEnv struct {
	org    string
	reg    *httpmock.Registry
	ios    *iostreams.IOStreams
	in     *bytes.Buffer
	out    *bytes.Buffer
	errOut *bytes.Buffer
	// ops maps a work item ID to the JSON Patch documents sent to it, in order.
	ops map[int][][]map[string]any
}

// newWIEditEnv returns an env for its own org (the SDK caches discovery per base URL).
func newWIEditEnv(t *testing.T, tty bool) *wiEditEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	org := "wiedit-" + strings.NewReplacer("/", "-", " ", "-", "@", "-", ":", "-").Replace(strings.ToLower(t.Name()))
	e := &wiEditEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut, ops: map[int][][]map[string]any{}}
	e.reg.StubLocations(org, slices.Concat(httpmock.WorkItemLocations, httpmock.GitPRLocations)...)
	return e
}

func (e *wiEditEnv) wiURL(id int) string {
	return "https://dev.azure.com/" + e.org + "/_apis/wit/workItems/" + strconv.Itoa(id)
}

func (e *wiEditEnv) webURL(id int) string {
	return "https://dev.azure.com/" + e.org + "/My%20Proj/_workitems/edit/" + strconv.Itoa(id)
}

// stubItem answers GET work item id (rev 3, a User Story) with fields merged over the defaults,
// the multiline formats and relations.
func (e *wiEditEnv) stubItem(t *testing.T, id int, fields map[string]any, formats map[string]any, rels []map[string]any) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitems/"+strconv.Itoa(id)), func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "relations", r.URL.Query().Get("$expand"))
		f := map[string]any{"System.WorkItemType": "User Story", "System.Title": "Old", "System.State": "New"}
		for k, v := range fields {
			f[k] = v
		}
		return httpmock.JSONResponse(map[string]any{"id": id, "rev": 3, "fields": f, "multilineFieldsFormat": formats, "relations": rels})(r)
	})
}

// stubUpdate answers PATCH work item id after recording the patch, with status and message
// (and typeKey) when status is not 200.
func (e *wiEditEnv) stubUpdate(t *testing.T, id, status int, typeKey, message string) {
	e.reg.Register(httpmock.REST(http.MethodPatch, e.org+"/My%20Proj/_apis/wit/workitems/"+strconv.Itoa(id)), func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var ops []map[string]any
		require.NoError(t, json.Unmarshal(b, &ops))
		e.ops[id] = append(e.ops[id], ops)
		if status != http.StatusOK {
			return httpmock.StatusJSONResponse(status, map[string]any{"message": message, "typeKey": typeKey})(r)
		}
		return httpmock.JSONResponse(map[string]any{"id": id, "rev": 4, "fields": map[string]any{}})(r)
	})
}

func (e *wiEditEnv) stubProcess(t *testing.T) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypes"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "types"))))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypecategories"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, "agile", "categories"))))
}

func (e *wiEditEnv) stubRelationTypes(t *testing.T) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/wit/workitemrelationtypes"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Read(t, "live_relationtypes.json"))))
}

func (e *wiEditEnv) stubFields(t *testing.T) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/fields"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Read(t, "fields.json"))))
}

func (e *wiEditEnv) stubMe() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/connectionData"), httpmock.JSONResponse(map[string]any{
		"authenticatedUser": map[string]any{"id": "33333333-3333-3333-3333-333333333333", "providerDisplayName": "Me",
			"properties": map[string]any{"Account": map[string]any{"$value": "me@example.com"}}},
	}))
}

// stubNodes answers GET classificationnodes/{group} with root\child of My Proj.
func (e *wiEditEnv) stubNodes(group, root, child string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/classificationnodes/"+group), httpmock.JSONResponse(map[string]any{
		"id": 1, "name": "My Proj", "path": `\My Proj\` + root,
		"children": []map[string]any{{"id": 2, "name": child, "path": `\My Proj\` + root + `\` + child}},
	}))
}

func (e *wiEditEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	e.ios.In = wiEditStdin{iotest.OneByteReader(e.in)}
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "web"}, nil
		},
		Prompter: &cmdutil.Prompter{IO: e.ios, Accessible: true},
	}
	cmd := NewCmdEdit(f)
	cmd.SilenceUsage = true // as under the root command
	cmd.SetArgs(args)
	cmd.SetIn(e.in)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

// wiEditStdin keeps stdin for the prompts after the fake editor ran (see the create tests).
type wiEditStdin struct{ io.Reader }

func (wiEditStdin) WriteTo(io.Writer) (int64, error) { return 0, nil }

func wiEditOp(op, path string, value any) map[string]any {
	m := map[string]any{"op": op, "path": path}
	if value != nil {
		m["value"] = value
	}
	return m
}

var wiEditRevTest = wiEditOp("test", "/rev", float64(3))

func TestWIEditAllFlags(t *testing.T) {
	e := newWIEditEnv(t, false)
	e.stubItem(t, 123, map[string]any{"System.Tags": "a; B", "System.Description": "<div>old</div>"}, nil, []map[string]any{
		{"rel": "System.LinkTypes.Related", "url": e.wiURL(5)},
		{"rel": "System.LinkTypes.Hierarchy-Reverse", "url": e.wiURL(7)},
		{"rel": "Hyperlink", "url": "https://x.test/spec"},
	})
	e.stubProcess(t)
	e.stubMe()
	e.stubNodes("areas", "Area", "Web")
	e.stubNodes("iterations", "Iteration", "Sprint 1")
	e.stubFields(t)
	e.stubRelationTypes(t)
	e.stubUpdate(t, 123, http.StatusOK, "", "")

	require.NoError(t, e.run(t, "123", "-t", "New title", "-b", "*md*", "--state", "active", "-a", "@me",
		"--area", "Web", "--iteration", "Sprint 1", "-p", "2", "--add-tag", "c,a", "--remove-label", "b",
		"--add-link", "child:9", "--add-link", "parent:7", "--remove-link", "related:5", "--remove-link", "url:https://x.test/spec",
		"-f", "Story Points=5", "-f", "Value Area="))
	e.reg.Verify(t)
	require.Len(t, e.ops[123], 1)
	assert.Equal(t, []map[string]any{
		wiEditRevTest,
		wiEditOp("add", "/fields/System.Title", "New title"),
		wiEditOp("add", "/fields/System.Description", "<p><em>md</em></p>\n"),
		wiEditOp("add", "/fields/System.State", "Active"),
		wiEditOp("add", "/fields/System.AssignedTo", "me@example.com"),
		wiEditOp("add", "/fields/System.AreaPath", `My Proj\Web`),
		wiEditOp("add", "/fields/System.IterationPath", `My Proj\Sprint 1`),
		wiEditOp("add", "/fields/Microsoft.VSTS.Common.Priority", float64(2)),
		wiEditOp("replace", "/fields/System.Tags", "a; c"),
		wiEditOp("add", "/fields/Microsoft.VSTS.Scheduling.StoryPoints", float64(5)),
		wiEditOp("remove", "/fields/Microsoft.VSTS.Common.ValueArea", nil),
		wiEditOp("remove", "/relations/2", nil),
		wiEditOp("remove", "/relations/0", nil),
		wiEditOp("add", "/relations/-", map[string]any{"rel": "System.LinkTypes.Hierarchy-Forward", "url": e.wiURL(9)}),
	}, e.ops[123][0], "the existing parent link is not added again")
	assert.Equal(t, e.webURL(123)+"\n", e.out.String())
	assert.Empty(t, e.errOut.String())
}

func TestWIEditTags(t *testing.T) {
	tests := []struct {
		name, current string
		args          []string
		want          map[string]any
	}{
		{"add when none", "", []string{"--add-tag", "x, y"}, wiEditOp("add", "/fields/System.Tags", "x; y")},
		{"replace with the joined set", "a; b", []string{"--add-tag", "A,c", "--remove-tag", "b"}, wiEditOp("replace", "/fields/System.Tags", "a; c")},
		{"replace empty when all removed", "a; b", []string{"--remove-tag", "A,B"}, wiEditOp("replace", "/fields/System.Tags", "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newWIEditEnv(t, false)
			e.stubItem(t, 1, map[string]any{"System.Tags": tt.current}, nil, nil)
			e.stubUpdate(t, 1, http.StatusOK, "", "")
			require.NoError(t, e.run(t, append([]string{"1"}, tt.args...)...))
			assert.Equal(t, []map[string]any{wiEditRevTest, tt.want}, e.ops[1][0])
		})
	}
}

func TestWIEditTagsUnchangedSendsNothing(t *testing.T) {
	e := newWIEditEnv(t, false)
	e.stubItem(t, 1, map[string]any{"System.Tags": "a"}, nil, nil)
	require.NoError(t, e.run(t, "1", "--add-tag", "A", "--remove-tag", "zzz"))
	assert.Empty(t, e.ops)
	assert.Equal(t, e.webURL(1)+"\n", e.out.String())
}

func TestWIEditBodyFormat(t *testing.T) {
	tests := []struct {
		name    string
		fields  map[string]any
		formats map[string]any
		args    []string
		want    []map[string]any
	}{
		{"html field keeps html", map[string]any{"System.Description": "<div>x</div>"}, nil, nil,
			[]map[string]any{wiEditOp("add", "/fields/System.Description", "<p><strong>b</strong></p>\n")}},
		{"markdown forces the switch", map[string]any{"System.Description": "<div>x</div>"}, nil, []string{"--markdown"},
			[]map[string]any{wiEditOp("add", "/fields/System.Description", "**b**"), wiEditOp("add", "/multilineFieldsFormat/System.Description", "Markdown")}},
		{"markdown field stays markdown", map[string]any{"System.Description": "x"}, map[string]any{"System.Description": "markdown"}, nil,
			[]map[string]any{wiEditOp("add", "/fields/System.Description", "**b**"), wiEditOp("add", "/multilineFieldsFormat/System.Description", "Markdown")}},
		{"empty field becomes markdown", nil, nil, nil,
			[]map[string]any{wiEditOp("add", "/fields/System.Description", "**b**"), wiEditOp("add", "/multilineFieldsFormat/System.Description", "Markdown")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newWIEditEnv(t, false)
			e.stubProcess(t)
			e.stubItem(t, 1, tt.fields, tt.formats, nil)
			e.stubUpdate(t, 1, http.StatusOK, "", "")
			require.NoError(t, e.run(t, append([]string{"1", "-b", "**b**"}, tt.args...)...))
			assert.Equal(t, append([]map[string]any{wiEditRevTest}, tt.want...), e.ops[1][0])
		})
	}
}

// stubProcessOf serves process's types and categories, and its Bug type's fields.
func (e *wiEditEnv) stubProcessOf(t *testing.T, process string) {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypes"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, process, "types"))))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypecategories"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Process(t, process, "categories"))))
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitemtypes/Bug/fields"),
		httpmock.JSONResponse(json.RawMessage(witfixtures.Read(t, process+"_type_bug_fields.json"))))
}

func TestWIEditBugBodyGoesToReproSteps(t *testing.T) {
	const repro = "Microsoft.VSTS.TCM.ReproSteps"
	tests := []struct {
		name, process string
		fields        map[string]any
		args          []string
		want          []map[string]any
	}{
		{"agile html repro steps keep html", "agile", map[string]any{repro: "<div>old</div>"}, nil,
			[]map[string]any{wiEditOp("add", "/fields/"+repro, "<p><strong>b</strong></p>\n")}},
		{"scrum empty repro steps become markdown", "scrum", nil, nil,
			[]map[string]any{wiEditOp("add", "/fields/"+repro, "**b**"), wiEditOp("add", "/multilineFieldsFormat/"+repro, "Markdown")}},
		{"scrum --markdown switches", "scrum", map[string]any{repro: "<div>old</div>"}, []string{"--markdown"},
			[]map[string]any{wiEditOp("add", "/fields/"+repro, "**b**"), wiEditOp("add", "/multilineFieldsFormat/"+repro, "Markdown")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newWIEditEnv(t, false)
			e.stubProcessOf(t, tt.process)
			fields := map[string]any{"System.WorkItemType": "Bug"}
			for k, v := range tt.fields {
				fields[k] = v
			}
			e.stubItem(t, 1, fields, nil, nil)
			e.stubUpdate(t, 1, http.StatusOK, "", "")
			require.NoError(t, e.run(t, append([]string{"1", "-b", "**b**"}, tt.args...)...))
			e.reg.Verify(t)
			assert.Equal(t, append([]map[string]any{wiEditRevTest}, tt.want...), e.ops[1][0])
		})
	}
}

func TestWIEditBugEditorShowsReproSteps(t *testing.T) {
	e := newWIEditEnv(t, true)
	editor, seen := editortest.Command(t, "TestWIEditEditorHelperProcess", "Edited")
	t.Setenv("ADO_EDITOR", editor)
	e.stubProcessOf(t, "agile")
	e.stubItem(t, 1, map[string]any{"System.WorkItemType": "Bug", "Microsoft.VSTS.TCM.ReproSteps": "<div>1. open</div>"}, nil, nil)
	e.stubUpdate(t, 1, http.StatusOK, "", "")
	require.NoError(t, e.run(t, "1", "-e"))
	assert.Equal(t, "1. open", wiEditSeen(t, seen))
	assert.Equal(t, []map[string]any{wiEditRevTest, wiEditOp("add", "/fields/Microsoft.VSTS.TCM.ReproSteps", "<p>Edited</p>\n")}, e.ops[1][0])
}

func TestWIEditHTMLFieldFlag(t *testing.T) {
	const ac = "Microsoft.VSTS.Common.AcceptanceCriteria"
	tests := []struct {
		name    string
		fields  map[string]any
		formats map[string]any
		args    []string
		want    []map[string]any
	}{
		{"html value keeps html", map[string]any{ac: "<div>old</div>"}, nil, nil,
			[]map[string]any{wiEditOp("add", "/fields/"+ac, "<ul>\n<li>done</li>\n</ul>\n")}},
		{"empty value becomes markdown", nil, nil, nil,
			[]map[string]any{wiEditOp("add", "/fields/"+ac, "- done"), wiEditOp("add", "/multilineFieldsFormat/"+ac, "Markdown")}},
		{"markdown value stays markdown", map[string]any{ac: "x"}, map[string]any{ac: "Markdown"}, nil,
			[]map[string]any{wiEditOp("add", "/fields/"+ac, "- done"), wiEditOp("add", "/multilineFieldsFormat/"+ac, "Markdown")}},
		{"--markdown switches", map[string]any{ac: "<div>old</div>"}, nil, []string{"--markdown"},
			[]map[string]any{wiEditOp("add", "/fields/"+ac, "- done"), wiEditOp("add", "/multilineFieldsFormat/"+ac, "Markdown")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newWIEditEnv(t, false)
			e.stubFields(t)
			e.stubItem(t, 1, tt.fields, tt.formats, nil)
			e.stubUpdate(t, 1, http.StatusOK, "", "")
			require.NoError(t, e.run(t, append([]string{"1", "-f", "Acceptance Criteria=- done"}, tt.args...)...))
			assert.Equal(t, append([]map[string]any{wiEditRevTest}, tt.want...), e.ops[1][0])
		})
	}
}

func TestWIEditHTMLFieldFlagFallback(t *testing.T) {
	e := newWIEditEnv(t, false)
	e.stubFields(t)
	e.stubItem(t, 1, nil, nil, nil)
	e.stubUpdate(t, 1, http.StatusBadRequest, "X", "Invalid path /multilineFieldsFormat/Microsoft.VSTS.Common.AcceptanceCriteria")
	e.stubUpdate(t, 1, http.StatusOK, "", "")
	require.NoError(t, e.run(t, "1", "-f", "Acceptance Criteria=*hi*"))
	require.Len(t, e.ops[1], 2)
	assert.Equal(t, []map[string]any{wiEditRevTest, wiEditOp("add", "/fields/Microsoft.VSTS.Common.AcceptanceCriteria", "<p><em>hi</em></p>\n")}, e.ops[1][1])
}

func TestWIEditHTMLFallback(t *testing.T) {
	e := newWIEditEnv(t, false)
	e.stubProcess(t)
	e.stubItem(t, 1, nil, nil, nil)
	e.stubUpdate(t, 1, http.StatusBadRequest, "X", "Invalid path /multilineFieldsFormat/System.Description")
	e.stubUpdate(t, 1, http.StatusOK, "", "")
	require.NoError(t, e.run(t, "1", "-b", "*hi*"))
	require.Len(t, e.ops[1], 2)
	assert.Equal(t, []map[string]any{wiEditRevTest, wiEditOp("add", "/fields/System.Description", "<p><em>hi</em></p>\n")}, e.ops[1][1])
}

func TestWIEditRevConflict(t *testing.T) {
	e := newWIEditEnv(t, false)
	e.stubItem(t, 123, nil, nil, nil)
	e.stubUpdate(t, 123, http.StatusBadRequest, "TestPatchOperationFailedException",
		"VS403351: Test Operation for path /rev failed, value 4 was not equal to test value 3.")
	require.EqualError(t, e.run(t, "123", "-t", "T"), "work item 123 changed since read; retry")
	assert.Empty(t, e.out.String())
}

func TestWIEditUnassignAndURLRef(t *testing.T) {
	e := newWIEditEnv(t, false)
	e.stubItem(t, 5, nil, nil, nil)
	e.stubUpdate(t, 5, http.StatusOK, "", "")
	require.NoError(t, e.run(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_workitems/edit/5", "--unassign"))
	assert.Equal(t, []map[string]any{wiEditRevTest, wiEditOp("add", "/fields/System.AssignedTo", "")}, e.ops[5][0])
}

func TestWIEditMultipleIDs(t *testing.T) {
	e := newWIEditEnv(t, false)
	e.stubFields(t)
	for _, id := range []int{1, 2} {
		e.stubItem(t, id, nil, nil, nil)
		e.stubUpdate(t, id, http.StatusOK, "", "")
	}
	require.NoError(t, e.run(t, "1", "#2", "--add-tag", "x", "-f", "Story Points=5"))
	e.reg.Verify(t)
	for _, id := range []int{1, 2} {
		assert.Equal(t, []map[string]any{
			wiEditRevTest,
			wiEditOp("add", "/fields/System.Tags", "x"),
			wiEditOp("add", "/fields/Microsoft.VSTS.Scheduling.StoryPoints", float64(5)),
		}, e.ops[id][0])
	}
	assert.Equal(t, e.webURL(1)+"\n"+e.webURL(2)+"\n", e.out.String())
}

func TestWIEditPartialFailure(t *testing.T) {
	e := newWIEditEnv(t, false)
	e.stubItem(t, 1, nil, nil, nil)
	e.stubUpdate(t, 1, http.StatusOK, "", "")
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/wit/workitems/2"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401232: Work item 2 does not exist"}))
	e.stubItem(t, 3, nil, nil, nil)
	e.stubUpdate(t, 3, http.StatusBadRequest, "TestPatchOperationFailedException", "VS403351")

	err := e.run(t, "1", "2", "3", "-t", "T")
	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Equal(t, e.webURL(1)+"\n", e.out.String())
	assert.Contains(t, e.errOut.String(), "failed to edit work item 2: TF401232")
	assert.Contains(t, e.errOut.String(), "failed to edit work item 3: work item 3 changed since read; retry")
	assert.Contains(t, e.errOut.String(), "edited: 1; failed: 2, 3")
}

func TestWIEditArtifactLinks(t *testing.T) {
	e := newWIEditEnv(t, false)
	const ids = "11111111-1111-1111-1111-111111111111%2F22222222-2222-2222-2222-222222222222%2F"
	sha := "0123456789abcdef0123456789abcdef01234567"
	e.stubItem(t, 1, nil, nil, []map[string]any{
		{"rel": "ArtifactLink", "url": "vstfs:///Git/Commit/" + ids + sha, "attributes": map[string]any{"name": "Fixed in Commit"}},
	})
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web"), httpmock.JSONResponse(map[string]any{
		"id": "22222222-2222-2222-2222-222222222222", "project": map[string]any{"id": "11111111-1111-1111-1111-111111111111"},
	}))
	e.stubUpdate(t, 1, http.StatusOK, "", "")

	require.NoError(t, e.run(t, "1", "--add-link", "branch:feature/x", "--remove-link", "commit:"+sha))
	e.reg.Verify(t)
	assert.Equal(t, []map[string]any{
		wiEditRevTest,
		wiEditOp("remove", "/relations/0", nil),
		wiEditOp("add", "/relations/-", map[string]any{"rel": "ArtifactLink", "url": "vstfs:///Git/Ref/" + ids + "GBfeature%2Fx",
			"attributes": map[string]any{"name": "Branch"}}),
	}, e.ops[1][0])
}

func TestWIEditLinkErrors(t *testing.T) {
	tests := []struct {
		name, arg, wantErr string
	}{
		{"remove missing link", "--remove-link=related:6", "work item 1 has no System.LinkTypes.Related:6 link"},
		{"unknown type", "--add-link=nope:6", `unknown link type "nope"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newWIEditEnv(t, false)
			e.stubRelationTypes(t)
			e.stubItem(t, 1, nil, nil, []map[string]any{{"rel": "System.LinkTypes.Related", "url": e.wiURL(5)}})
			err := e.run(t, "1", tt.arg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, e.ops)
		})
	}
}

func TestWIEditErrors(t *testing.T) {
	tests := []struct {
		name    string
		tty     bool
		args    []string
		stub    func(t *testing.T, e *wiEditEnv)
		wantErr string
	}{
		{"no flags non-tty", false, []string{"1"}, nil, "no changes requested; use one or more of: -t/--title"},
		{"no ids", false, []string{"-t", "T"}, nil, "requires at least 1 arg"},
		{"bad ref", false, []string{"x", "-t", "T"}, nil, "invalid work item reference"},
		{"bad priority", false, []string{"1", "-p", "0"}, nil, "--priority must be between 1 and 4"},
		{"assignee and unassign", false, []string{"1", "-a", "x", "--unassign"}, nil, "if any flags in the group [assignee unassign] are set none of the others can be"},
		{"interactive with several ids", true, []string{"1", "2"}, nil, "multiple work items cannot be edited interactively"},
		{"mixed projects", false, []string{"https://dev.azure.com/o/P1/_workitems/edit/1", "https://dev.azure.com/o/P2/_workitems/edit/2", "-t", "T"}, nil,
			"work items must be in one project"},
		{"bad state", false, []string{"1", "--state", "Nope"}, func(t *testing.T, e *wiEditEnv) {
			e.stubItem(t, 1, nil, nil, nil)
			e.stubProcess(t)
		}, `invalid state "Nope" for User Story; valid states: New, Active, Resolved, Closed, Removed`},
		{"unknown area", false, []string{"1", "--area", "Nope"}, func(_ *testing.T, e *wiEditEnv) {
			e.stubNodes("areas", "Area", "Web")
		}, `area path "Nope" not found`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newWIEditEnv(t, tt.tty)
			if tt.stub != nil {
				tt.stub(t, e)
			}
			err := e.run(t, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, e.ops)
		})
	}
}

// TestWIEditEditorHelperProcess is not a test: it is the fake editor of editortest.Command.
func TestWIEditEditorHelperProcess(*testing.T) { editortest.HelperProcess() }

// wiEditSeen returns the text the fake editor was opened with.
func wiEditSeen(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

func TestWIEditEditorFlag(t *testing.T) {
	e := newWIEditEnv(t, true)
	editor, seen := editortest.Command(t, "TestWIEditEditorHelperProcess", "Edited")
	t.Setenv("ADO_EDITOR", editor)
	e.stubProcess(t)
	e.stubItem(t, 1, map[string]any{"System.Description": "current **md**"}, map[string]any{"System.Description": "Markdown"}, nil)
	e.stubUpdate(t, 1, http.StatusOK, "", "")
	require.NoError(t, e.run(t, "1", "-e"))
	assert.Equal(t, "current **md**", wiEditSeen(t, seen))
	assert.Equal(t, []map[string]any{
		wiEditRevTest,
		wiEditOp("add", "/fields/System.Description", "Edited"),
		wiEditOp("add", "/multilineFieldsFormat/System.Description", "Markdown"),
	}, e.ops[1][0])
}

func TestWIEditTTYPicker(t *testing.T) {
	e := newWIEditEnv(t, true)
	editor, seen := editortest.Command(t, "TestWIEditEditorHelperProcess", "New body")
	t.Setenv("ADO_EDITOR", editor)
	e.stubItem(t, 1, map[string]any{
		"System.Tags": "a; b", "System.Description": "<div>old</div>", "Microsoft.VSTS.Common.Priority": 2,
		"System.AreaPath": `My Proj`, "System.IterationPath": `My Proj`,
		"System.AssignedTo": map[string]any{"displayName": "Jane", "uniqueName": "jane@example.com"},
	}, nil, nil)
	e.stubProcess(t)
	e.stubNodes("areas", "Area", "Web")
	e.stubNodes("iterations", "Iteration", "Sprint 1")
	e.stubUpdate(t, 1, http.StatusOK, "", "")
	// Pick every option (Title 1 .. Tags 8), then: title, body (editor), state Active (2),
	// assignee kept (empty answer), area, iteration, priority, tags.
	e.in.WriteString("1\n2\n3\n4\n5\n6\n7\n8\n0\nPicked\n2\n\nWeb\nSprint 1\n4\nb, c\n")

	require.NoError(t, e.run(t, "1"))
	e.reg.Verify(t)
	assert.Equal(t, "old", wiEditSeen(t, seen))
	assert.Equal(t, []map[string]any{
		wiEditRevTest,
		wiEditOp("add", "/fields/System.Title", "Picked"),
		wiEditOp("add", "/fields/System.Description", "<p>New body</p>\n"),
		wiEditOp("add", "/fields/System.State", "Active"),
		wiEditOp("add", "/fields/System.AreaPath", `My Proj\Web`),
		wiEditOp("add", "/fields/System.IterationPath", `My Proj\Sprint 1`),
		wiEditOp("add", "/fields/Microsoft.VSTS.Common.Priority", float64(4)),
		wiEditOp("replace", "/fields/System.Tags", "b; c"),
	}, e.ops[1][0])
	assert.True(t, strings.HasSuffix(e.out.String(), e.webURL(1)+"\n"), e.out.String())
}

func TestWIEditTTYPickerNothing(t *testing.T) {
	e := newWIEditEnv(t, true)
	e.stubItem(t, 1, nil, nil, nil)
	e.in.WriteString("0\n")
	require.ErrorIs(t, e.run(t, "1"), cmdutil.CancelError)
	assert.Empty(t, e.ops)
}

func TestWIEditTTYPickerBadPriority(t *testing.T) {
	e := newWIEditEnv(t, true)
	e.stubItem(t, 1, nil, nil, nil)
	e.in.WriteString("7\n0\n9\n")
	require.EqualError(t, e.run(t, "1"), `invalid priority "9": must be between 1 and 4`)
}
