package shared

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/snake-in-prod/ado-gh/pkg/markdown"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	fieldsPrefix = "/fields/"
	formatPrefix = "/multilineFieldsFormat/"
)

var gfm = goldmark.New(goldmark.WithExtensions(extension.GFM))

// convertMarkdown renders Markdown to HTML; a variable so tests can make it fail.
var convertMarkdown = gfm.Convert

// IsMarkdown reports whether field of wi is stored as Markdown. Field and format compare
// case-insensitively; a field missing from multilineFieldsFormat is HTML.
func IsMarkdown(wi *api.WorkItem, field string) bool {
	if wi == nil {
		return false
	}
	for k, v := range wi.MultilineFieldsFormat {
		if strings.EqualFold(k, field) {
			return strings.EqualFold(v, "markdown")
		}
	}
	return false
}

// BodyOps returns the JSON Patch ops writing the Markdown md to the multiline field. current
// is the work item being edited (nil when creating). The field keeps its format: a field
// stored as HTML with a value gets md converted to HTML, unless forceMarkdown switches it to
// Markdown (permanent for that work item and field). Otherwise md is sent as Markdown, the
// value op and the multilineFieldsFormat op together.
func BodyOps(field, md string, current *api.WorkItem, forceMarkdown bool) []webapi.JsonPatchOperation {
	if current != nil && !forceMarkdown && !IsMarkdown(current, field) {
		if v, ok := current.Fields[field]; ok && v != nil && v != "" {
			return []webapi.JsonPatchOperation{addOp(fieldsPrefix+field, MarkdownToHTML(md))}
		}
	}
	return []webapi.JsonPatchOperation{addOp(fieldsPrefix+field, md), addOp(formatPrefix+field, "Markdown")}
}

// SendWithHTMLFallback sends ops. If the organization rejects them with a 400 mentioning
// multilineFieldsFormat (no Markdown support), it retries once without the format ops, the
// Markdown values of their fields converted to HTML.
func SendWithHTMLFallback(ops []webapi.JsonPatchOperation, send func([]webapi.JsonPatchOperation) (*api.WorkItem, error)) (*api.WorkItem, error) {
	wi, err := send(ops)
	var he *api.HTTPError
	if !errors.As(err, &he) || he.StatusCode != http.StatusBadRequest ||
		!strings.Contains(strings.ToLower(he.Message), strings.ToLower("multilineFieldsFormat")) {
		return wi, err
	}
	markdownFields := map[string]bool{}
	for _, op := range ops {
		if f, ok := strings.CutPrefix(opPath(op), formatPrefix); ok {
			markdownFields[f] = true
		}
	}
	if len(markdownFields) == 0 {
		return wi, err
	}
	var htmlOps []webapi.JsonPatchOperation
	for _, op := range ops {
		p := opPath(op)
		if strings.HasPrefix(p, formatPrefix) {
			continue
		}
		if f, ok := strings.CutPrefix(p, fieldsPrefix); ok && markdownFields[f] {
			if s, ok := op.Value.(string); ok {
				op.Value = MarkdownToHTML(s)
			}
		}
		htmlOps = append(htmlOps, op)
	}
	return send(htmlOps)
}

// MarkdownToHTML converts GitHub-flavoured Markdown to HTML; raw HTML in md is dropped. If
// the conversion fails, md is returned HTML-escaped rather than as a partial body.
func MarkdownToHTML(md string) string {
	var b bytes.Buffer
	if err := convertMarkdown([]byte(md), &b); err != nil {
		return html.EscapeString(md)
	}
	return b.String()
}

// RenderBody returns a multiline field or comment for display: raw when stdout is not a TTY,
// else Markdown rendered with glamour, or HTML converted to plain text.
func RenderBody(ios *iostreams.IOStreams, text string, isMarkdown bool) (string, error) {
	switch {
	case !ios.IsStdoutTTY():
		return text, nil
	case isMarkdown:
		return markdown.Render(ios, text)
	default:
		return HTMLToText(text), nil
	}
}

func addOp(path string, value any) webapi.JsonPatchOperation {
	return webapi.JsonPatchOperation{Op: &webapi.OperationValues.Add, Path: &path, Value: value}
}

func opPath(op webapi.JsonPatchOperation) string {
	if op.Path == nil {
		return ""
	}
	return *op.Path
}

// HTMLToText converts work item HTML to plain text: block elements (Azure DevOps stores each
// line as a <div>) on their own lines, list items numbered or bulleted, links as
// "text <href>", table rows as tab-separated cells.
func HTMLToText(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return s
	}
	w := &textWriter{sep: true}
	w.walk(doc, false)
	return strings.TrimRight(w.b.String(), " \n")
}

type list struct {
	ordered bool
	n       int
}

// textWriter collapses whitespace and tracks line state while writing text.
type textWriter struct {
	b strings.Builder
	// nl is the number of newlines ending b.
	nl int
	// sep is set at the start of a line or after a list marker or cell tab: no space follows.
	sep bool
	// space is a collapsed space waiting for the next character.
	space bool
	// marker is set right after a list marker, so the item's first block stays on its line.
	marker bool
	lists  []list
}

func (w *textWriter) text(s string, pre bool) {
	for _, r := range s {
		switch {
		case pre && r == '\n':
			w.newline()
		case !pre && unicode.IsSpace(r):
			w.space = !w.sep
		default:
			if w.space {
				w.b.WriteByte(' ')
			}
			w.b.WriteRune(r)
			w.nl, w.sep, w.space, w.marker = 0, false, false, false
		}
	}
}

// raw writes a marker or separator that no space may follow.
func (w *textWriter) raw(s string) {
	w.b.WriteString(s)
	w.nl, w.sep, w.space = 0, true, false
}

func (w *textWriter) newline() {
	w.b.WriteByte('\n')
	w.nl++
	w.sep, w.space, w.marker = true, false, false
}

// ensure ends the current line with at least n newlines, unless nothing is written yet.
func (w *textWriter) ensure(n int) {
	if w.b.Len() == 0 || w.marker {
		return
	}
	for w.nl < n {
		w.newline()
	}
}

func (w *textWriter) children(n *html.Node, pre bool) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.walk(c, pre)
	}
}

func (w *textWriter) walk(n *html.Node, pre bool) {
	switch n.Type {
	case html.TextNode:
		w.text(n.Data, pre)
		return
	case html.ElementNode:
	default:
		w.children(n, pre)
		return
	}
	switch n.DataAtom {
	case atom.Head, atom.Script, atom.Style:
	case atom.Br:
		w.newline()
	case atom.P, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		w.ensure(2)
		w.children(n, pre)
		w.ensure(2)
	case atom.Pre:
		w.ensure(1)
		w.children(n, true)
		w.ensure(1)
	case atom.Ul, atom.Ol:
		w.ensure(1)
		w.lists = append(w.lists, list{ordered: n.DataAtom == atom.Ol})
		w.children(n, pre)
		w.lists = w.lists[:len(w.lists)-1]
		w.ensure(1)
	case atom.Li:
		w.marker = false
		w.ensure(1)
		marker := "- "
		if d := len(w.lists); d > 0 {
			l := &w.lists[d-1]
			if l.ordered {
				l.n++
				marker = strconv.Itoa(l.n) + ". "
			}
			marker = strings.Repeat("  ", d-1) + marker
		}
		w.raw(marker)
		w.marker = true
		w.children(n, pre)
		w.marker = false
		w.ensure(1)
	case atom.Tr:
		w.ensure(1)
		first := true
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.DataAtom == atom.Td || c.DataAtom == atom.Th {
				if !first {
					w.raw("\t")
					w.marker = true // a <div> in the cell stays on the row
				}
				first = false
			}
			w.walk(c, pre)
		}
		w.marker = false
		w.ensure(1)
	case atom.A:
		w.children(n, pre)
		href := attr(n, "href")
		switch text := strings.Join(strings.Fields(textContent(n)), " "); {
		case href == "" || href == text:
		case text == "":
			w.text("<"+href+">", false)
		default:
			w.text(" <"+href+">", false)
		}
	case atom.Div, atom.Table, atom.Blockquote, atom.Tbody, atom.Thead, atom.Section, atom.Hr:
		w.ensure(1)
		w.children(n, pre)
		w.ensure(1)
	default:
		w.children(n, pre)
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textContent(c))
	}
	return b.String()
}
