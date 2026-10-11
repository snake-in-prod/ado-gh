package query

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// maxResults is the service's cap on query results; more are silently truncated.
const maxResults = 20000

const idRef = "System.Id"

// knownFields maps the `wi list` JSON names to reference names. Other query columns go under
// `fields`, keyed by reference name.
var knownFields = map[string]string{
	"type": "System.WorkItemType", "title": "System.Title", "state": "System.State", "reason": "System.Reason",
	"assignedTo": "System.AssignedTo", "createdBy": "System.CreatedBy", "createdDate": "System.CreatedDate",
	"changedDate": "System.ChangedDate", "areaPath": "System.AreaPath", "iterationPath": "System.IterationPath",
	"tags": "System.Tags", "priority": "Microsoft.VSTS.Common.Priority", "parent": "System.Parent",
}

// node is a work item of a link query result with its linked items.
type node struct {
	id    int
	rel   string
	depth int
	links []*node
}

// isLinkResult reports whether res is a tree or one-hop result.
func isLinkResult(res *api.QueryResult) bool {
	return res.QueryType == "tree" || res.QueryType == "oneHop"
}

// buildTree rebuilds a link result: rows without a source are roots, the others are grouped by
// source. One-hop results stop at the first level; an ancestor guard stops cycles.
func buildTree(res *api.QueryResult) []*node {
	bySource := map[int][]api.WorkItemLink{}
	for _, l := range res.Links {
		bySource[l.SourceID] = append(bySource[l.SourceID], l)
	}
	maxDepth := len(res.Links)
	if res.QueryType == "oneHop" {
		maxDepth = 1
	}
	var build func(l api.WorkItemLink, depth int, ancestors []int) *node
	build = func(l api.WorkItemLink, depth int, ancestors []int) *node {
		n := &node{id: l.TargetID, rel: l.Rel, depth: depth}
		if depth >= maxDepth || slices.Contains(ancestors, l.TargetID) {
			return n
		}
		for _, c := range bySource[l.TargetID] {
			n.links = append(n.links, build(c, depth+1, append(ancestors, l.TargetID)))
		}
		return n
	}
	var roots []*node
	for _, l := range bySource[0] {
		roots = append(roots, build(l, 0, nil))
	}
	return roots
}

// walk calls fn for n and its descendants, depth first.
func walk(nodes []*node, fn func(*node)) {
	for _, n := range nodes {
		fn(n)
		walk(n.links, fn)
	}
}

// columns returns the query's columns, with an ID column first when the query has none.
func columns(res *api.QueryResult) []api.QueryColumn {
	if slices.ContainsFunc(res.Columns, func(c api.QueryColumn) bool { return c.ReferenceName == idRef }) {
		return res.Columns
	}
	return append([]api.QueryColumn{{ReferenceName: idRef, Name: "ID"}}, res.Columns...)
}

// fetchFields returns the reference names to fetch: the query's columns and the refs of the
// requested known JSON fields, without System.Id (every item has its ID).
func fetchFields(res *api.QueryResult, jsonFields []string) []string {
	var out []string
	add := func(ref string) {
		if ref != "" && ref != idRef && !slices.Contains(out, ref) {
			out = append(out, ref)
		}
	}
	for _, c := range res.Columns {
		add(c.ReferenceName)
	}
	for _, f := range jsonFields {
		add(knownFields[f])
	}
	return out
}

// itemJSON returns the requested JSON fields of wi: known fields under their `wi list` names,
// the other query columns under `fields`.
func itemJSON(wi api.WorkItem, res *api.QueryResult, project adocontext.Repo, want []string) map[string]any {
	out := make(map[string]any, len(want))
	for _, f := range want {
		switch f {
		case "id":
			out[f] = wi.ID
		case "url":
			out[f] = adocontext.WorkItemURL(project, wi.ID)
		case "links":
		case "fields":
			other := map[string]any{}
			for _, c := range res.Columns {
				if !slices.Contains(knownRefs, c.ReferenceName) {
					other[c.ReferenceName] = wi.Fields[c.ReferenceName]
				}
			}
			out[f] = other
		case "assignedTo", "createdBy":
			out[f] = user(wi.Fields[knownFields[f]])
		case "tags":
			out[f] = tags(wi.Fields)
		case "priority", "parent":
			out[f] = wi.Fields[knownFields[f]]
		default:
			s, _ := wi.Fields[knownFields[f]].(string)
			out[f] = s
		}
	}
	return out
}

// knownRefs are System.Id and the reference names of knownFields.
var knownRefs = func() []string {
	refs := []string{idRef}
	for _, r := range knownFields {
		refs = append(refs, r)
	}
	return refs
}()

// linksJSON returns nodes as `[{rel, item}]`, item carrying its own links when requested.
func linksJSON(nodes []*node, item func(*node) map[string]any) []map[string]any {
	out := make([]map[string]any, len(nodes))
	for i, n := range nodes {
		out[i] = map[string]any{"rel": n.rel, "item": item(n)}
	}
	return out
}

// user returns an identity field in the `wi list` shape, or nil when unset.
func user(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return map[string]any{"id": m["id"], "displayName": m["displayName"], "uniqueName": m["uniqueName"]}
}

// tags splits System.Tags ("a; b") into its tags.
func tags(fields map[string]any) []string {
	s, _ := fields["System.Tags"].(string)
	out := []string{}
	for _, t := range strings.Split(s, ";") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// cell formats a field value for the table: identities by display name.
func cell(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case map[string]any:
		if s, ok := v["displayName"].(string); ok {
			return s
		}
	}
	return fmt.Sprint(v)
}

// printTable prints rows (each an item and, for link results, its node) in the query's
// columns. On a TTY link results are indented by depth, with a LINK column for one-hop; off a
// TTY they get leading parent ID and link type columns instead.
func printTable(ios *iostreams.IOStreams, res *api.QueryResult, items map[int]api.WorkItem, flat []int, roots []*node) error {
	tty := ios.IsStdoutTTY()
	cols := columns(res)
	link := isLinkResult(res)
	oneHop := res.QueryType == "oneHop"
	var headers []string
	if link && !tty {
		headers = append(headers, "parent", "link")
	}
	for i, c := range cols {
		headers = append(headers, c.Name)
		if i == 0 && link && tty && oneHop {
			headers = append(headers, "link")
		}
	}
	cs := ios.ColorScheme()
	tp := tableprinter.New(ios, headers...)
	row := func(id int, n *node, parent int) {
		wi := items[id]
		if link && !tty {
			p := ""
			if parent != 0 {
				p = strconv.Itoa(parent)
			}
			tp.AddField(p)
			tp.AddField(n.rel)
		}
		for i, c := range cols {
			s := cell(wi.Fields[c.ReferenceName])
			color := func(s string) string { return s }
			if c.ReferenceName == idRef {
				s = strconv.Itoa(id)
				if tty {
					s = "#" + s
				}
				color = cs.Green
			}
			if i == 0 && n != nil && tty {
				s = strings.Repeat("  ", n.depth) + s
			}
			tp.AddField(s, tableprinter.WithColor(color))
			if i == 0 && link && tty && oneHop {
				tp.AddField(strings.TrimPrefix(n.rel, "System.LinkTypes."), tableprinter.WithColor(cs.Gray))
			}
		}
		tp.EndRow()
	}
	for _, id := range flat {
		row(id, nil, 0)
	}
	var visit func(nodes []*node, parent int)
	visit = func(nodes []*node, parent int) {
		for _, n := range nodes {
			row(n.id, n, parent)
			visit(n.links, n.id)
		}
	}
	visit(roots, 0)
	return tp.Render()
}
