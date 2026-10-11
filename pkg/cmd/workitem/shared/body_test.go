package shared

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const descField = "System.Description"

// bodyOpsJSON renders ops the way the API receives them.
func bodyOpsJSON(t *testing.T, ops []webapi.JsonPatchOperation) []map[string]any {
	t.Helper()
	b, err := json.Marshal(ops)
	require.NoError(t, err)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

func TestIsMarkdown(t *testing.T) {
	for _, tc := range []struct {
		name    string
		formats map[string]string
		want    bool
	}{
		{"Markdown", map[string]string{descField: "Markdown"}, true},
		{"lowercase", map[string]string{descField: "markdown"}, true},
		{"key case", map[string]string{"system.description": "MARKDOWN"}, true},
		{"Html", map[string]string{descField: "Html"}, false},
		{"missing key", map[string]string{"Microsoft.VSTS.TCM.ReproSteps": "Markdown"}, false},
		{"nil map", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsMarkdown(&api.WorkItem{MultilineFieldsFormat: tc.formats}, descField))
		})
	}
	assert.False(t, IsMarkdown(nil, descField))
}

func TestBodyOps(t *testing.T) {
	md := "**bold**"
	markdownOps := []map[string]any{
		{"op": "add", "path": "/fields/System.Description", "value": md},
		{"op": "add", "path": "/multilineFieldsFormat/System.Description", "value": "Markdown"},
	}
	htmlOps := []map[string]any{
		{"op": "add", "path": "/fields/System.Description", "value": "<p><strong>bold</strong></p>\n"},
	}
	htmlItem := &api.WorkItem{Fields: map[string]any{descField: "<div>old</div>"}}
	for _, tc := range []struct {
		name    string
		current *api.WorkItem
		force   bool
		want    []map[string]any
	}{
		{"create", nil, false, markdownOps},
		{"Markdown field", &api.WorkItem{Fields: map[string]any{descField: "old"}, MultilineFieldsFormat: map[string]string{descField: "markdown"}}, false, markdownOps},
		{"empty field", &api.WorkItem{Fields: map[string]any{}}, false, markdownOps},
		{"empty string field", &api.WorkItem{Fields: map[string]any{descField: ""}}, false, markdownOps},
		{"HTML field keeps HTML", htmlItem, false, htmlOps},
		{"HTML field forced to Markdown", htmlItem, true, markdownOps},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, bodyOpsJSON(t, BodyOps(descField, md, tc.current, tc.force)))
		})
	}
}

func TestMarkdownToHTML_GFM(t *testing.T) {
	assert.Contains(t, MarkdownToHTML("| a | b |\n|---|---|\n| 1 | 2 |\n"), "<table>")
}

func TestSendWithHTMLFallback(t *testing.T) {
	ops := append(BodyOps(descField, "*hi*", nil, false), BodyOps("System.Title", "T", &api.WorkItem{Fields: map[string]any{"System.Title": "<b>x</b>"}}, false)...)
	formatErr := &api.HTTPError{StatusCode: 400, Message: "Invalid path /multilineFieldsFormat/System.Description"}

	t.Run("success sends once with both ops", func(t *testing.T) {
		var sent [][]webapi.JsonPatchOperation
		wi, err := SendWithHTMLFallback(ops, func(o []webapi.JsonPatchOperation) (*api.WorkItem, error) {
			sent = append(sent, o)
			return &api.WorkItem{ID: 1}, nil
		})
		require.NoError(t, err)
		assert.Equal(t, 1, wi.ID)
		require.Len(t, sent, 1)
		assert.Len(t, sent[0], 3)
	})

	t.Run("format 400 retries once as HTML", func(t *testing.T) {
		var sent [][]webapi.JsonPatchOperation
		wi, err := SendWithHTMLFallback(ops, func(o []webapi.JsonPatchOperation) (*api.WorkItem, error) {
			sent = append(sent, o)
			if len(sent) == 1 {
				return nil, formatErr
			}
			return &api.WorkItem{ID: 2}, nil
		})
		require.NoError(t, err)
		assert.Equal(t, 2, wi.ID)
		require.Len(t, sent, 2)
		assert.Equal(t, []map[string]any{
			{"op": "add", "path": "/fields/System.Description", "value": "<p><em>hi</em></p>\n"},
			{"op": "add", "path": "/fields/System.Title", "value": "<p>T</p>\n"},
		}, bodyOpsJSON(t, sent[1]))
		assert.Len(t, sent[0], 3, "original ops untouched")
	})

	t.Run("retry failure is returned without a third try", func(t *testing.T) {
		calls := 0
		_, err := SendWithHTMLFallback(ops, func([]webapi.JsonPatchOperation) (*api.WorkItem, error) {
			calls++
			return nil, formatErr
		})
		assert.Equal(t, formatErr, err)
		assert.Equal(t, 2, calls)
	})

	for _, tc := range []struct {
		name string
		ops  []webapi.JsonPatchOperation
		err  error
	}{
		{"other 400", ops, &api.HTTPError{StatusCode: 400, Message: "TF401320: Rule Error for field Title"}},
		{"non-400 mentioning the op", ops, &api.HTTPError{StatusCode: 500, Message: "multilineFieldsFormat"}},
		{"plain error", ops, errors.New("multilineFieldsFormat")},
		{"no format op", BodyOps("System.Title", "T", &api.WorkItem{Fields: map[string]any{"System.Title": "x"}}, false), formatErr},
	} {
		t.Run(tc.name+" is not retried", func(t *testing.T) {
			calls := 0
			_, err := SendWithHTMLFallback(tc.ops, func([]webapi.JsonPatchOperation) (*api.WorkItem, error) {
				calls++
				return nil, tc.err
			})
			assert.Equal(t, tc.err, err)
			assert.Equal(t, 1, calls)
		})
	}
}

func TestHTMLToText(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"div lines", "<div>first</div><div>second</div><div><br></div><div>after blank</div>", "first\nsecond\n\nafter blank"},
		{"br", "a<br>b<br/>c", "a\nb\nc"},
		{"whitespace collapsed", "<div>  a \n  b  </div>", "a b"},
		{"entities", "<div>a &amp; b&nbsp;c &lt;x&gt;</div>", "a & b c <x>"},
		{"paragraphs", "<p>one</p><p>two</p>", "one\n\ntwo"},
		{"heading", "<h1>Title</h1><div>body</div>", "Title\n\nbody"},
		{"ordered list", "<ol><li>a</li><li>b</li><li><div>c</div></li></ol>", "1. a\n2. b\n3. c"},
		{"unordered nested", "<ul><li>a<ul><li>b</li></ul></li><li>c</li></ul>", "- a\n  - b\n- c"},
		{"ordered restarts", "<ol><li>a</li></ol><ol><li>b</li></ol>", "1. a\n1. b"},
		{"link", `<div>see <a href="https://x.test/a">docs</a>.</div>`, "see docs <https://x.test/a>."},
		{"bare link", `<a href="https://x.test/a">https://x.test/a</a>`, "https://x.test/a"},
		{"empty link", `<a href="https://x.test/a"></a>`, "<https://x.test/a>"},
		{"table", "<table><tr><th>Name</th><th>Value</th></tr>\n<tr><td>a</td> <td><div>1</div></td></tr></table><div>end</div>", "Name\tValue\na\t1\nend"},
		{"pre", "<pre>x := 1\n  y</pre>", "x := 1\n  y"},
		{"script dropped", "<div>a<script>alert(1)</script><style>p{}</style></div>", "a"},
		{"inline", "<div><b>bold</b> and <i>it</i></div>", "bold and it"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, HTMLToText(tc.in))
		})
	}
}

func TestRenderBody(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	ios.SetColorEnabled(false)

	out, err := RenderBody(ios, "# Title\n\n**bold**", true)
	require.NoError(t, err)
	assert.Equal(t, "# Title\n\n**bold**", out, "piped Markdown stays raw")
	out, err = RenderBody(ios, "<div>a</div>", false)
	require.NoError(t, err)
	assert.Equal(t, "<div>a</div>", out, "piped HTML stays raw")

	ios.SetStdoutTTY(true)
	out, err = RenderBody(ios, "# Title\n\n**bold**", true)
	require.NoError(t, err)
	assert.Contains(t, out, "  # Title", "glamour renders Markdown on a TTY")
	out, err = RenderBody(ios, "<div>a</div><div>b</div>", false)
	require.NoError(t, err)
	assert.Equal(t, "a\nb", out)

	t.Setenv("GLAMOUR_STYLE", "/nonexistent/style.json")
	ios.SetColorEnabled(true)
	_, err = RenderBody(ios, "x", true)
	assert.Error(t, err)
}
