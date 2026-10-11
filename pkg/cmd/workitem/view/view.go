// Package view implements `ado workitem view`.
package view

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/text"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

var jsonFields = []string{
	"id", "type", "title", "state", "reason", "assignedTo", "createdBy", "createdDate", "changedDate",
	"areaPath", "iterationPath", "tags", "priority", "parent", "url", "fields",
	"description", "relations", "comments", "history",
}

// now is the clock relative times are computed from.
var now = time.Now

// Relation groups in display order.
const (
	groupParent     = "parent"
	groupChild      = "child"
	groupRelated    = "related"
	groupLink       = "link"
	groupPR         = "pullRequest"
	groupCommit     = "commit"
	groupBranch     = "branch"
	groupHyperlink  = "hyperlink"
	groupAttachment = "attachment"
	groupOther      = "other"
)

var groupTitles = []struct{ group, title string }{
	{groupParent, "Parent"}, {groupChild, "Children"}, {groupRelated, "Related"}, {groupLink, "Other links"},
	{groupPR, "Pull requests"}, {groupCommit, "Commits"}, {groupBranch, "Branches"},
	{groupHyperlink, "Hyperlinks"}, {groupAttachment, "Attachments"}, {groupOther, "Other"},
}

// historySkip are the fields every revision changes; the history leaves them out.
var historySkip = map[string]bool{
	"System.Rev": true, "System.Watermark": true, "System.ChangedDate": true, "System.ChangedBy": true,
	"System.AuthorizedDate": true, "System.AuthorizedAs": true, "System.RevisedDate": true, "System.PersonId": true,
	"System.CommentCount": true,
}

type options struct {
	ref                    string
	comments, history, web bool
	exporter               *cmdutil.Exporter
}

// NewCmdView returns the `workitem view` command.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "view <id> | <url>",
		Short: "View a work item",
		Long: `Display the type, title, state, assignee, paths, tags, description and links of a
work item. Linked work items are shown with their title and state.

With --comments the comments are shown (oldest first), with --history the field
change log. Markdown renders on a terminal; piped output is raw.`,
		Example: `  ado workitem view 123
  ado wi view 123 -c --history
  ado wi view 123 --json id,title,relations,comments`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.ref = args[0]
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().BoolVarP(&o.comments, "comments", "c", false, "View work item comments")
	cmd.Flags().BoolVar(&o.history, "history", false, "View the field change history")
	cmd.Flags().BoolVarP(&o.web, "web", "w", false, "Open the work item in the web browser")
	cmdutil.AddJSONFlags(cmd, &o.exporter, jsonFields)
	return cmd
}

type identityJSON struct {
	ID          string `json:"id,omitempty"`
	DisplayName string `json:"displayName"`
	UniqueName  string `json:"uniqueName"`
}

type linkedJSON struct {
	ID    int    `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	State string `json:"state"`
}

type relationJSON struct {
	// Group is parent, child, related, link, pullRequest, commit, branch, hyperlink,
	// attachment or other.
	Group string `json:"group"`
	Rel   string `json:"rel"`
	// Name is the link type's friendly name (attributes.name).
	Name     string      `json:"name"`
	URL      string      `json:"url"`
	Comment  string      `json:"comment,omitempty"`
	WorkItem *linkedJSON `json:"workItem"`
}

type commentJSON struct {
	ID          int          `json:"id"`
	Author      identityJSON `json:"author"`
	Body        string       `json:"body"`
	Format      string       `json:"format"`
	CreatedDate time.Time    `json:"createdDate"`
	URL         string       `json:"url"`
}

type changeJSON struct {
	OldValue any `json:"oldValue"`
	NewValue any `json:"newValue"`
}

type updateJSON struct {
	Rev              int                   `json:"rev"`
	RevisedBy        identityJSON          `json:"revisedBy"`
	RevisedDate      time.Time             `json:"revisedDate"`
	Fields           map[string]changeJSON `json:"fields"`
	RelationsAdded   []relationJSON        `json:"relationsAdded"`
	RelationsRemoved []relationJSON        `json:"relationsRemoved"`
}

// viewJSON is the `--json` shape of a viewed work item.
type viewJSON struct {
	ID            int            `json:"id"`
	Type          string         `json:"type"`
	Title         string         `json:"title"`
	State         string         `json:"state"`
	Reason        string         `json:"reason"`
	AssignedTo    *identityJSON  `json:"assignedTo"`
	CreatedBy     *identityJSON  `json:"createdBy"`
	CreatedDate   string         `json:"createdDate"`
	ChangedBy     *identityJSON  `json:"-"`
	ChangedDate   string         `json:"changedDate"`
	AreaPath      string         `json:"areaPath"`
	IterationPath string         `json:"iterationPath"`
	Tags          []string       `json:"tags"`
	Priority      *int           `json:"priority"`
	Parent        *int           `json:"parent"`
	URL           string         `json:"url"`
	Fields        map[string]any `json:"fields"`
	Description   string         `json:"description"`
	Relations     []relationJSON `json:"relations"`
	Comments      []commentJSON  `json:"comments"`
	History       []updateJSON   `json:"history"`

	// descMarkdown reports whether Description is Markdown.
	descMarkdown bool
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	id, project, err := shared.ParseRef(o.ref)
	if err != nil {
		return err
	}
	if project.Project == "" {
		if project, err = f.BaseProject(); err != nil {
			return err
		}
	}
	if o.web {
		return cmdutil.OpenInBrowser(f, adocontext.WorkItemURL(project, id))
	}
	hc, err := authshared.HTTPClient(ctx, f, project.Host, project.Org)
	if err != nil {
		return err
	}
	c := api.NewWorkItemClient(hc, authshared.OrgURL(project.Host, project.Org), project.Project)
	wi, err := c.WorkItem(ctx, id, true)
	if err != nil {
		return err
	}
	d := toJSON(wi, project)

	wants := func(field string) bool { return o.exporter != nil && slices.Contains(o.exporter.Fields, field) }
	if o.exporter == nil || wants("relations") {
		if err := linkTitles(ctx, c, d.Relations); err != nil {
			return err
		}
	}
	if wants("comments") || o.exporter == nil && o.comments {
		if d.Comments, err = comments(ctx, c, id); err != nil {
			return err
		}
	}
	if wants("history") || o.exporter == nil && o.history {
		updates, err := c.Updates(ctx, id)
		if err != nil {
			return err
		}
		d.History = historyJSON(updates)
	}

	ios := f.IOStreams
	switch {
	case o.exporter != nil:
		return o.exporter.Write(ios, d)
	case ios.IsStdoutTTY():
		return printTTY(ios, d, o)
	}
	printRaw(ios.Out, d, o)
	return nil
}

func toJSON(wi *api.WorkItem, project adocontext.Repo) viewJSON {
	str := func(ref string) string { s, _ := wi.Fields[ref].(string); return s }
	d := viewJSON{
		ID: wi.ID, Type: str("System.WorkItemType"), Title: str("System.Title"), State: str("System.State"),
		Reason: str("System.Reason"), AssignedTo: identity(wi.Fields["System.AssignedTo"]),
		CreatedBy: identity(wi.Fields["System.CreatedBy"]), CreatedDate: str("System.CreatedDate"),
		ChangedBy: identity(wi.Fields["System.ChangedBy"]), ChangedDate: str("System.ChangedDate"),
		AreaPath: str("System.AreaPath"), IterationPath: str("System.IterationPath"), Tags: []string{},
		URL: adocontext.WorkItemURL(project, wi.ID), Fields: wi.Fields, Relations: relationsJSON(wi.Relations),
		Description: str("System.Description"), descMarkdown: shared.IsMarkdown(wi, "System.Description"),
	}
	if d.Description == "" && str("Microsoft.VSTS.TCM.ReproSteps") != "" {
		// Bugs of the Agile and CMMI processes describe themselves in Repro Steps.
		d.Description, d.descMarkdown = str("Microsoft.VSTS.TCM.ReproSteps"), shared.IsMarkdown(wi, "Microsoft.VSTS.TCM.ReproSteps")
	}
	for t := range strings.SplitSeq(str("System.Tags"), ";") {
		if t = strings.TrimSpace(t); t != "" {
			d.Tags = append(d.Tags, t)
		}
	}
	if p, ok := wi.Fields["Microsoft.VSTS.Common.Priority"].(float64); ok {
		n := int(p)
		d.Priority = &n
	}
	for _, r := range d.Relations {
		if r.Group == groupParent && r.WorkItem != nil {
			d.Parent = &r.WorkItem.ID
		}
	}
	return d
}

// identity maps an identity field value (an object) to its JSON; nil when unset.
func identity(v any) *identityJSON {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	s := func(k string) string { s, _ := m[k].(string); return s }
	return &identityJSON{ID: s("id"), DisplayName: s("displayName"), UniqueName: s("uniqueName")}
}

func relationsJSON(rels []api.WorkItemRelation) []relationJSON {
	out := make([]relationJSON, 0, len(rels))
	for _, r := range rels {
		name, _ := r.Attributes["name"].(string)
		comment, _ := r.Attributes["comment"].(string)
		j := relationJSON{Rel: r.Rel, Name: name, URL: r.URL, Comment: comment, Group: groupOther}
		if id := workItemID(r.URL); id > 0 {
			j.WorkItem = &linkedJSON{ID: id}
		}
		lower := strings.ToLower(r.URL)
		switch {
		case r.Rel == "System.LinkTypes.Hierarchy-Reverse":
			j.Group = groupParent
		case r.Rel == "System.LinkTypes.Hierarchy-Forward":
			j.Group = groupChild
		case r.Rel == "System.LinkTypes.Related":
			j.Group = groupRelated
		case j.WorkItem != nil:
			j.Group = groupLink
		case r.Rel == "Hyperlink":
			j.Group = groupHyperlink
		case r.Rel == "AttachedFile":
			j.Group = groupAttachment
		case strings.HasPrefix(lower, "vstfs:///git/pullrequestid/"):
			j.Group = groupPR
		case strings.HasPrefix(lower, "vstfs:///git/commit/"):
			j.Group = groupCommit
		case strings.HasPrefix(lower, "vstfs:///git/ref/"):
			j.Group = groupBranch
		}
		out = append(out, j)
	}
	return out
}

// workItemID returns the ID of a work item REST URL (.../_apis/wit/workItems/ID), else 0.
func workItemID(u string) int {
	i := strings.LastIndex(strings.ToLower(u), "/_apis/wit/workitems/")
	if i < 0 {
		return 0
	}
	id, err := strconv.Atoi(u[i+len("/_apis/wit/workitems/"):])
	if err != nil {
		return 0
	}
	return id
}

// linkTitles fills in the type, title and state of the linked work items. Deleted or
// inaccessible items keep only their ID.
func linkTitles(ctx context.Context, c *api.WorkItemClient, rels []relationJSON) error {
	var ids []int
	for _, r := range rels {
		if r.WorkItem != nil && !slices.Contains(ids, r.WorkItem.ID) {
			ids = append(ids, r.WorkItem.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	items, err := c.WorkItems(ctx, ids, []string{"System.WorkItemType", "System.Title", "System.State"}, false)
	if err != nil {
		return err
	}
	byID := make(map[int]api.WorkItem, len(items))
	for _, wi := range items {
		byID[wi.ID] = wi
	}
	for _, r := range rels {
		if r.WorkItem == nil {
			continue
		}
		f := byID[r.WorkItem.ID].Fields
		r.WorkItem.Type, _ = f["System.WorkItemType"].(string)
		r.WorkItem.Title, _ = f["System.Title"].(string)
		r.WorkItem.State, _ = f["System.State"].(string)
	}
	return nil
}

// comments returns the work item's comments, oldest first, without deleted ones.
func comments(ctx context.Context, c *api.WorkItemClient, id int) ([]commentJSON, error) {
	out := []commentJSON{}
	for token := ""; ; {
		page, next, err := c.Comments(ctx, id, false, token)
		if err != nil {
			return nil, err
		}
		for _, cm := range page {
			if !cm.IsDeleted {
				out = append(out, commentJSON{
					ID: cm.ID, Author: identityOf(cm.CreatedBy), Body: cm.Text, Format: cm.Format, CreatedDate: cm.CreatedDate, URL: cm.URL,
				})
			}
		}
		if token = next; token == "" {
			return out, nil
		}
	}
}

func identityOf(r api.IdentityRef) identityJSON {
	return identityJSON{ID: r.ID, DisplayName: r.DisplayName, UniqueName: r.UniqueName}
}

// changedAt is when update u was made: its System.ChangedDate. RevisedDate is when the
// revision was superseded, which is 9999-01-01 for the latest one.
func changedAt(u api.WorkItemUpdate) time.Time {
	if ch, ok := u.Fields["System.ChangedDate"]; ok {
		if s, ok := ch.NewValue.(string); ok {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t
			}
		}
	}
	return u.RevisedDate
}

func historyJSON(updates []api.WorkItemUpdate) []updateJSON {
	out := make([]updateJSON, 0, len(updates))
	for _, u := range updates {
		j := updateJSON{
			Rev: u.Rev, RevisedBy: identityOf(u.RevisedBy), RevisedDate: changedAt(u), Fields: map[string]changeJSON{},
			RelationsAdded: relationsJSON(u.RelationsAdded), RelationsRemoved: relationsJSON(u.RelationsRemoved),
		}
		for k, ch := range u.Fields {
			j.Fields[k] = changeJSON{OldValue: ch.OldValue, NewValue: ch.NewValue}
		}
		out = append(out, j)
	}
	return out
}

func printTTY(ios *iostreams.IOStreams, d viewJSON, o *options) error {
	cs := ios.ColorScheme()
	w := ios.Out
	fmt.Fprintf(w, "%s %s\n", cs.Bold(d.Title), cs.Gray(fmt.Sprintf("#%d", d.ID)))
	state := d.State
	if d.Reason != "" {
		state += " (" + d.Reason + ")"
	}
	fmt.Fprintf(w, "%s • %s • opened by %s %s • changed by %s %s\n", d.Type, cs.Green(state),
		displayName(d.CreatedBy), ago(d.CreatedDate), displayName(d.ChangedBy), ago(d.ChangedDate))
	assignee := displayName(d.AssignedTo)
	if assignee == "" {
		assignee = "not assigned"
	}
	for _, kv := range [][2]string{
		{"Assignee", assignee}, {"Area", d.AreaPath}, {"Iteration", d.IterationPath},
		{"Priority", priority(d.Priority)}, {"Tags", strings.Join(d.Tags, ", ")},
	} {
		if kv[1] != "" {
			fmt.Fprintf(w, "%s %s\n", cs.Bold(kv[0]+":"), kv[1])
		}
	}

	desc := cs.Gray("No description provided")
	if strings.TrimSpace(d.Description) != "" {
		out, err := shared.RenderBody(ios, d.Description, d.descMarkdown)
		if err != nil {
			return err
		}
		desc = strings.Trim(out, "\n")
	}
	fmt.Fprintf(w, "\n%s\n\n", desc)

	for _, g := range groupTitles {
		var lines []string
		for _, r := range d.Relations {
			if r.Group == g.group {
				lines = append(lines, relationLine(r))
			}
		}
		if len(lines) > 0 {
			fmt.Fprintf(w, "%s\n  %s\n\n", cs.Bold(g.title), strings.Join(lines, "\n  "))
		}
	}

	if o.comments {
		if len(d.Comments) == 0 {
			fmt.Fprint(w, cs.Gray("No comments")+"\n\n")
		} else {
			fmt.Fprintf(w, "%s\n\n", cs.Bold(fmt.Sprintf("Comments (%d)", len(d.Comments))))
		}
		for _, c := range d.Comments {
			body, err := shared.RenderBody(ios, c.Body, strings.EqualFold(c.Format, "markdown"))
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "%s • %s\n%s\n\n", cs.Bold(c.Author.DisplayName), text.RelativeTimeAgo(now(), c.CreatedDate),
				indent(strings.Trim(body, "\n")))
		}
	}
	if o.history {
		fmt.Fprintf(w, "%s\n\n", cs.Bold("History"))
		for _, u := range d.History {
			fmt.Fprintf(w, "%s • %s • %s\n", cs.Bold(fmt.Sprintf("Rev %d", u.Rev)), u.RevisedBy.DisplayName,
				text.RelativeTimeAgo(now(), u.RevisedDate))
			fmt.Fprintln(w, indent(strings.Join(changeLines(u), "\n")))
			fmt.Fprintln(w)
		}
	}
	fmt.Fprintln(w, cs.Gray("View this work item on Azure DevOps: "+d.URL))
	return nil
}

func printRaw(w io.Writer, d viewJSON, o *options) {
	for _, kv := range [][2]string{
		{"title", d.Title}, {"type", d.Type}, {"state", d.State}, {"reason", d.Reason},
		{"assignee", displayName(d.AssignedTo)}, {"area", d.AreaPath}, {"iteration", d.IterationPath},
		{"priority", priority(d.Priority)}, {"tags", strings.Join(d.Tags, ", ")},
		{"created by", displayName(d.CreatedBy)}, {"created", d.CreatedDate},
		{"changed by", displayName(d.ChangedBy)}, {"changed", d.ChangedDate},
		{"id", strconv.Itoa(d.ID)}, {"url", d.URL},
	} {
		fmt.Fprintf(w, "%s:\t%s\n", kv[0], kv[1])
	}
	for _, r := range d.Relations {
		fmt.Fprintf(w, "%s:\t%s\n", r.Group, relationLine(r))
	}
	fmt.Fprintf(w, "--\n%s\n", d.Description)
	if o.comments {
		for _, c := range d.Comments {
			fmt.Fprintf(w, "--\nauthor:\t%s\ncreated:\t%s\n--\n%s\n", c.Author.DisplayName, c.CreatedDate.Format(time.RFC3339), c.Body)
		}
	}
	if o.history {
		for _, u := range d.History {
			fmt.Fprintf(w, "--\nrev:\t%d\nrevised by:\t%s\nrevised:\t%s\n%s\n", u.Rev, u.RevisedBy.DisplayName,
				u.RevisedDate.Format(time.RFC3339), strings.Join(changeLines(u), "\n"))
		}
	}
}

// relationLine describes a relation: a linked work item with its type, title and state, a PR,
// commit or branch by its number, short SHA or name, else its name or URL.
func relationLine(r relationJSON) string {
	var s string
	switch {
	case r.WorkItem != nil:
		s = fmt.Sprintf("#%d", r.WorkItem.ID)
		if r.WorkItem.Title != "" {
			s += fmt.Sprintf(" %s: %s (%s)", r.WorkItem.Type, r.WorkItem.Title, r.WorkItem.State)
		}
		if r.Group == groupLink {
			s += " [" + cmp.Or(r.Name, r.Rel) + "]"
		}
	case r.Group == groupPR:
		s = "!" + artifactID(r.URL)
	case r.Group == groupCommit:
		s = artifactID(r.URL)
		s = s[:min(len(s), 8)]
	case r.Group == groupBranch:
		s = strings.TrimPrefix(artifactID(r.URL), "GB")
	case r.Group == groupAttachment:
		s = cmp.Or(r.Name, r.URL)
	default:
		s = r.URL
	}
	if r.Comment != "" {
		s += " - " + r.Comment
	}
	return s
}

// artifactID is the ID part of a Git artifact URL (.../{project}%2F{repo}%2F{id}), unescaped:
// a branch's GB{name} is itself escaped, so it may hold more %2F.
func artifactID(u string) string {
	parts := strings.SplitN(u, "%2F", 3)
	id := parts[len(parts)-1]
	if s, err := url.PathUnescape(id); err == nil {
		return s
	}
	return id
}

// changeLines lists the changes of a revision: "Field: old → new" per changed field (minus the
// bookkeeping ones) and "+"/"-" lines for added and removed links.
func changeLines(u updateJSON) []string {
	if u.Rev == 1 {
		return []string{"Created"}
	}
	var keys, lines []string
	for k := range u.Fields {
		if !historySkip[k] {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		ch := u.Fields[k]
		lines = append(lines, fmt.Sprintf("%s: %s → %s", k[strings.LastIndex(k, ".")+1:], value(ch.OldValue), value(ch.NewValue)))
	}
	for _, r := range u.RelationsAdded {
		lines = append(lines, "+ "+cmp.Or(r.Name, r.Rel)+" "+relationLine(r))
	}
	for _, r := range u.RelationsRemoved {
		lines = append(lines, "- "+cmp.Or(r.Name, r.Rel)+" "+relationLine(r))
	}
	return lines
}

// value formats a history value on one line: an identity as its name, long text truncated.
func value(v any) string {
	switch x := v.(type) {
	case nil:
		return "(none)"
	case map[string]any:
		if n, ok := x["displayName"].(string); ok {
			return n
		}
	}
	return text.Truncate(60, strings.Join(strings.Fields(fmt.Sprint(v)), " "))
}

func displayName(i *identityJSON) string {
	if i == nil {
		return ""
	}
	return i.DisplayName
}

func priority(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}

func ago(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return text.RelativeTimeAgo(now(), t)
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}
