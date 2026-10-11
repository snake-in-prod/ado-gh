// Package edit implements `ado workitem edit`.
package edit

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// editFlags are the flags that change a work item; with none, edit prompts or fails.
var editFlags = []string{
	"title", "body", "body-file", "editor", "state", "assignee", "unassign", "area", "iteration",
	"priority", "add-tag", "remove-tag", "add-link", "remove-link", "field",
}

// pickable are the fields the interactive picker offers, in prompt order.
var pickable = []string{"Title", "Body", "State", "Assignee", "Area", "Iteration", "Priority", "Tags"}

type options struct {
	title, state, assignee, area, iteration, team string
	unassign, markdown, interactive               bool
	priority                                      int
	addTags, removeTags, addLinks, removeLinks    []string
	fields                                        []string
	body                                          cmdutil.BodyFlags
}

// NewCmdEdit returns the `workitem edit` command.
func NewCmdEdit(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "edit <id>...",
		Short: "Edit work items",
		Long: `Edit one or more work items. The same changes apply to every work item given.

--state takes a state of the work item's type (any case). --add-link and --remove-link
take <type>:<target>: a link type by name (parent, child, related, "Tested By", ...)
with a work item ID, pr:<number>, commit:<sha> or branch:<name> in the current
repository, or url:<URL> for a hyperlink. --field sets any field by friendly
or reference name; Name= clears it.

--body keeps the description's format: a description stored as HTML gets the Markdown
converted to HTML. --markdown switches it to Markdown, which can't be undone.

Each work item is updated in one request that fails if it changed since it was read.
On a terminal without flags, pick the fields to edit, prefilled with their values.`,
		Example: `  ado workitem edit 123 --state Active -a @me
  ado wi edit 123 124 --add-tag ui -f "Story Points=5"
  ado wi edit 123 --add-link parent:100 --remove-link related:7`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			if fl.Changed("priority") && (o.priority < 1 || o.priority > 4) {
				return cmdutil.FlagErrorf("--priority must be between 1 and 4")
			}
			if !slices.ContainsFunc(editFlags, fl.Changed) {
				if !f.IOStreams.CanPrompt() {
					return cmdutil.FlagErrorf("no changes requested; use one or more of: %s", flagList(fl))
				}
				if len(args) > 1 {
					return cmdutil.FlagErrorf("multiple work items cannot be edited interactively")
				}
				o.interactive = true
			}
			return run(cmd.Context(), f, o, args)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&o.title, "title", "t", "", "Set the title")
	cmdutil.AddBodyFlags(cmd, &o.body)
	fl.BoolVar(&o.markdown, "markdown", false, "Switch the description to Markdown if it is stored as HTML (permanent)")
	fl.StringVar(&o.state, "state", "", "Set the state")
	fl.StringVarP(&o.assignee, "assignee", "a", "", `Assign to a user (email, name or "@me")`)
	fl.BoolVar(&o.unassign, "unassign", false, "Remove the assignee")
	cmd.MarkFlagsMutuallyExclusive("assignee", "unassign")
	fl.StringVar(&o.area, "area", "", "Set the area path")
	fl.StringVar(&o.iteration, "iteration", "", `Set the iteration path, or "@current" for the team's current sprint`)
	fl.IntVarP(&o.priority, "priority", "p", 0, "Set the priority from 1 (highest) to 4")
	fl.StringSliceVar(&o.addTags, "add-tag", nil, "Add tags, repeatable or comma-separated (alias --add-label)")
	fl.StringSliceVar(&o.removeTags, "remove-tag", nil, "Remove tags, repeatable or comma-separated (alias --remove-label)")
	fl.StringArrayVar(&o.addLinks, "add-link", nil, "Add a link: <type>:<target> (repeatable)")
	fl.StringArrayVar(&o.removeLinks, "remove-link", nil, "Remove a link: <type>:<target> (repeatable)")
	fl.StringArrayVarP(&o.fields, "field", "f", nil, "Set a field: Name=Value; Name= clears it (repeatable)")
	fl.StringVar(&o.team, "team", "", "Team for --iteration @current (default: config team, else the project's default team)")
	fl.SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		switch name {
		case "add-label":
			name = "add-tag"
		case "remove-label":
			name = "remove-tag"
		}
		return pflag.NormalizedName(name)
	})
	return cmd
}

// flagList names editFlags as in the usage, e.g. "-t/--title, --state".
func flagList(fl *pflag.FlagSet) string {
	var names []string
	for _, n := range editFlags {
		if s := fl.Lookup(n).Shorthand; s != "" {
			names = append(names, "-"+s+"/--"+n)
		} else {
			names = append(names, "--"+n)
		}
	}
	return strings.Join(names, ", ")
}

// editor applies the changes of options to work items, with the values that don't depend on
// the work item resolved once.
type editor struct {
	f        *cmdutil.Factory
	o        *options
	c        *api.WorkItemClient
	proc     *shared.Process
	fields   *shared.FieldResolver
	resolver *api.IdentityResolver
	project  adocontext.Repo
	orgURL   string

	// body is the new description, read on the first work item; nil when unchanged.
	body                         *string
	assignee, areaPath, iterPath string
	addLinks, removeLinks        []shared.Link
}

func run(ctx context.Context, f *cmdutil.Factory, o *options, refs []string) error {
	ids, project, err := parseRefs(f, refs)
	if err != nil {
		return err
	}
	hc, err := authshared.HTTPClient(ctx, f, project.Host, project.Org)
	if err != nil {
		return err
	}
	orgURL := authshared.OrgURL(project.Host, project.Org)
	c := api.NewWorkItemClient(hc, orgURL, project.Project)
	e := &editor{
		f: f, o: o, c: c, proc: shared.NewProcess(c), project: project, orgURL: orgURL,
		resolver: &api.IdentityResolver{HTTP: hc, OrgURL: orgURL, Prompter: f.Prompter},
	}
	e.fields = &shared.FieldResolver{Client: c, Identity: e.identity}

	var first *api.WorkItem
	if o.interactive {
		if first, err = c.WorkItem(ctx, ids[0], true); err != nil {
			return err
		}
		if err := e.pick(ctx, first); err != nil {
			return err
		}
	}
	if err := e.prepare(ctx); err != nil {
		return err
	}

	ios := f.IOStreams
	var done, failed []string
	for i, id := range ids {
		wi := first
		if i > 0 {
			wi = nil
		}
		if err := e.edit(ctx, id, wi); err != nil {
			if len(ids) == 1 {
				return err
			}
			fmt.Fprintf(ios.ErrOut, "X failed to edit work item %d: %v\n", id, err)
			failed = append(failed, strconv.Itoa(id))
			continue
		}
		fmt.Fprintln(ios.Out, adocontext.WorkItemURL(project, id))
		done = append(done, strconv.Itoa(id))
	}
	if len(failed) > 0 {
		if len(done) == 0 {
			done = []string{"none"}
		}
		fmt.Fprintf(ios.ErrOut, "edited: %s; failed: %s\n", strings.Join(done, ", "), strings.Join(failed, ", "))
		return cmdutil.SilentError
	}
	return nil
}

// parseRefs parses the work item references; the project is the one URLs name, else the
// current one.
func parseRefs(f *cmdutil.Factory, refs []string) ([]int, adocontext.Repo, error) {
	var ids []int
	var project adocontext.Repo
	for _, ref := range refs {
		id, p, err := shared.ParseRef(ref)
		if err != nil {
			return nil, project, err
		}
		if p.Project != "" {
			if project.Project != "" && (!strings.EqualFold(p.Org, project.Org) || !strings.EqualFold(p.Project, project.Project)) {
				return nil, project, errors.New("work items must be in one project")
			}
			project = p
		}
		ids = append(ids, id)
	}
	if project.Project != "" {
		return ids, project, nil
	}
	project, err := f.BaseProject()
	return ids, project, err
}

func (e *editor) identity(ctx context.Context, v string) (string, error) {
	u, err := e.resolver.ResolveUser(ctx, v)
	return u.FieldValue(), err
}

// prepare resolves the values that are the same for every work item.
func (e *editor) prepare(ctx context.Context) error {
	o := e.o
	var err error
	if o.assignee != "" {
		if e.assignee, err = e.identity(ctx, o.assignee); err != nil {
			return err
		}
	}
	if o.area != "" {
		if e.areaPath, err = shared.ResolvePath(ctx, e.c, "areas", o.area); err != nil {
			return err
		}
	}
	if o.iteration != "" {
		e.iterPath, err = shared.ResolveIterationPath(ctx, e.c, o.iteration, func() (string, error) {
			cfg, err := e.f.Config()
			if err != nil {
				return "", err
			}
			return shared.ResolveTeam(ctx, e.c, cfg, e.project, o.team)
		})
		if err != nil {
			return err
		}
	}
	if len(o.addLinks)+len(o.removeLinks) == 0 {
		return nil
	}
	p := &shared.LinkParser{F: e.f, Client: e.c, OrgURL: e.orgURL}
	parse := func(args []string) ([]shared.Link, error) {
		var out []shared.Link
		for _, a := range args {
			l, err := p.ParseArg(ctx, a)
			if err != nil {
				return nil, err
			}
			out = append(out, l)
		}
		return out, nil
	}
	if e.addLinks, err = parse(o.addLinks); err != nil {
		return err
	}
	e.removeLinks, err = parse(o.removeLinks)
	return err
}

// edit updates work item id, read first unless wi is given. A work item the changes leave
// as it is isn't sent.
func (e *editor) edit(ctx context.Context, id int, wi *api.WorkItem) error {
	var err error
	if wi == nil {
		if wi, err = e.c.WorkItem(ctx, id, true); err != nil {
			return err
		}
	}
	ops, err := e.ops(ctx, id, wi)
	if err != nil || len(ops) == 1 {
		return err
	}
	_, err = shared.SendWithHTMLFallback(ops, func(ops []webapi.JsonPatchOperation) (*api.WorkItem, error) {
		return e.c.UpdateWorkItem(ctx, id, ops)
	})
	if api.IsRevConflict(err) {
		return fmt.Errorf("work item %d changed since read; retry", id)
	}
	return err
}

// ops returns the JSON Patch for wi, starting with a `test /rev` op so the update fails if
// wi changed since it was read.
func (e *editor) ops(ctx context.Context, id int, wi *api.WorkItem) ([]webapi.JsonPatchOperation, error) {
	o := e.o
	typ := str(wi, "System.WorkItemType")
	ops := []webapi.JsonPatchOperation{patchOp(webapi.OperationValues.Test, "/rev", wi.Rev)}
	set := func(field string, v any) {
		ops = append(ops, patchOp(webapi.OperationValues.Add, "/fields/"+field, v))
	}
	if o.title != "" {
		set("System.Title", o.title)
	}
	if e.body == nil && (o.body.Given() || o.body.Editor) {
		body, err := o.body.Read(e.f, currentBody(wi))
		if err != nil {
			return nil, err
		}
		e.body = &body
	}
	if e.body != nil {
		ops = append(ops, shared.BodyOps("System.Description", *e.body, wi, o.markdown)...)
	}
	if o.state != "" {
		s, err := e.resolveState(ctx, typ, o.state)
		if err != nil {
			return nil, err
		}
		set("System.State", s)
	}
	switch {
	case e.assignee != "":
		set("System.AssignedTo", e.assignee)
	case o.unassign:
		set("System.AssignedTo", "")
	}
	if e.areaPath != "" {
		set("System.AreaPath", e.areaPath)
	}
	if e.iterPath != "" {
		set("System.IterationPath", e.iterPath)
	}
	if o.priority > 0 {
		set("Microsoft.VSTS.Common.Priority", o.priority)
	}
	if op, ok := tagsOp(str(wi, "System.Tags"), o.addTags, o.removeTags); ok {
		ops = append(ops, op)
	}
	for _, arg := range o.fields {
		v, err := e.fields.Resolve(ctx, typ, arg)
		if err != nil {
			return nil, err
		}
		ops = append(ops, v.Op())
	}
	rels, err := e.relationOps(id, wi)
	return append(ops, rels...), err
}

// relationOps removes the matching relations, then adds the links wi doesn't have yet.
func (e *editor) relationOps(id int, wi *api.WorkItem) ([]webapi.JsonPatchOperation, error) {
	ops, err := shared.RemoveOps(id, wi.Relations, e.removeLinks)
	if err != nil {
		return nil, err
	}
	for _, l := range e.addLinks {
		if !slices.ContainsFunc(wi.Relations, l.Matches) {
			ops = append(ops, l.AddOp())
		}
	}
	return ops, nil
}

// resolveState returns the state of type typ named name (any case).
func (e *editor) resolveState(ctx context.Context, typ, name string) (string, error) {
	states, err := e.proc.States(ctx, typ)
	if err != nil {
		return "", err
	}
	var names []string
	for _, s := range states {
		if strings.EqualFold(s.Name, name) {
			return s.Name, nil
		}
		names = append(names, s.Name)
	}
	return "", fmt.Errorf("invalid state %q for %s; valid states: %s", name, typ, strings.Join(names, ", "))
}

// tagsOp computes the tag set client-side, as the server's `add` semantics on System.Tags
// are disputed: `add` when there were no tags, else `replace` (with "" when all are removed).
// ok is false when the set doesn't change.
func tagsOp(current string, add, remove []string) (webapi.JsonPatchOperation, bool) {
	old := splitTags(current)
	var tags []string
	for _, t := range old {
		if !containsFold(remove, t) {
			tags = append(tags, t)
		}
	}
	for _, t := range add {
		if t = strings.TrimSpace(t); t != "" && !containsFold(tags, t) {
			tags = append(tags, t)
		}
	}
	if slices.Equal(tags, old) {
		return webapi.JsonPatchOperation{}, false
	}
	op := webapi.OperationValues.Replace
	if len(old) == 0 {
		op = webapi.OperationValues.Add
	}
	return patchOp(op, "/fields/System.Tags", strings.Join(tags, "; ")), true
}

func splitTags(s string) []string {
	var out []string
	for t := range strings.SplitSeq(s, ";") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func containsFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(v string) bool { return strings.EqualFold(strings.TrimSpace(v), s) })
}

// pick asks which fields of wi to edit, then for each its new value, prefilled with the
// current one, and stores the answers in the options.
func (e *editor) pick(ctx context.Context, wi *api.WorkItem) error {
	p, o := e.f.Prompter, e.o
	picked, err := p.MultiSelect("What would you like to edit?", nil, pickable)
	if err != nil {
		return err
	}
	if len(picked) == 0 {
		return cmdutil.CancelError
	}
	for _, i := range picked {
		switch pickable[i] {
		case "Title":
			o.title, err = p.Input("Title", str(wi, "System.Title"))
		case "Body":
			err = e.pickBody(wi)
		case "State":
			err = e.pickState(ctx, wi)
		case "Assignee":
			current := assignee(wi)
			var v string
			if v, err = p.Input(`Assignee (email, name or "@me")`, current); err == nil && v != current {
				o.assignee = v
			}
		case "Area":
			o.area, err = p.Input("Area path", str(wi, "System.AreaPath"))
		case "Iteration":
			o.iteration, err = p.Input(`Iteration path (or "@current")`, str(wi, "System.IterationPath"))
		case "Priority":
			err = e.pickPriority(wi)
		case "Tags":
			old := splitTags(str(wi, "System.Tags"))
			var v string
			if v, err = p.Input("Tags (comma-separated)", strings.Join(old, ", ")); err == nil {
				o.addTags = strings.Split(v, ",")
				for _, t := range old {
					if !containsFold(o.addTags, t) {
						o.removeTags = append(o.removeTags, t)
					}
				}
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *editor) pickBody(wi *api.WorkItem) error {
	cfg, err := e.f.Config()
	if err != nil {
		return err
	}
	body, err := cmdutil.Edit(e.f.IOStreams, cmdutil.ResolveEditor(cfg), currentBody(wi))
	if err == nil && strings.TrimSpace(body) != "" {
		e.body = &body
	}
	return err
}

func (e *editor) pickState(ctx context.Context, wi *api.WorkItem) error {
	states, err := e.proc.States(ctx, str(wi, "System.WorkItemType"))
	if err != nil {
		return err
	}
	var names []string
	for _, s := range states {
		names = append(names, s.Name)
	}
	i, err := e.f.Prompter.Select("State (now "+str(wi, "System.State")+")", names)
	if err == nil {
		e.o.state = names[i]
	}
	return err
}

func (e *editor) pickPriority(wi *api.WorkItem) error {
	current := ""
	if p, ok := wi.Fields["Microsoft.VSTS.Common.Priority"].(float64); ok {
		current = strconv.Itoa(int(p))
	}
	s, err := e.f.Prompter.Input("Priority (1-4)", current)
	if err != nil || strings.TrimSpace(s) == "" {
		return err
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 4 {
		return fmt.Errorf("invalid priority %q: must be between 1 and 4", s)
	}
	e.o.priority = n
	return nil
}

// currentBody is the description to edit: Markdown as is, HTML as plain text.
func currentBody(wi *api.WorkItem) string {
	s := str(wi, "System.Description")
	if shared.IsMarkdown(wi, "System.Description") {
		return s
	}
	return shared.HTMLToText(s)
}

// assignee is the sign-in name (else display name) of wi's assignee; "" when unassigned.
func assignee(wi *api.WorkItem) string {
	m, _ := wi.Fields["System.AssignedTo"].(map[string]any)
	if s, _ := m["uniqueName"].(string); s != "" {
		return s
	}
	s, _ := m["displayName"].(string)
	return s
}

func str(wi *api.WorkItem, ref string) string {
	s, _ := wi.Fields[ref].(string)
	return s
}

func patchOp(op webapi.Operation, path string, value any) webapi.JsonPatchOperation {
	return webapi.JsonPatchOperation{Op: &op, Path: &path, Value: value}
}
