// Package view implements `ado sprint view`.
package view

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

// jsonFields are the public `--json` fields.
var jsonFields = []string{"id", "type", "title", "state", "stateCategory", "assignedTo", "parent", "url"}

// fetchFields are the fields fetched for each work item.
var fetchFields = []string{"System.WorkItemType", "System.Title", "System.State", "System.AssignedTo"}

// categories are the state categories in display order, with their headings.
var categories = []struct{ name, heading string }{
	{shared.CategoryProposed, "Proposed"},
	{shared.CategoryInProgress, "In Progress"},
	{shared.CategoryResolved, "Resolved"},
	{shared.CategoryCompleted, "Completed"},
	{shared.CategoryRemoved, "Removed"},
}

type options struct {
	iteration, team, assignee string
	types                     []string
	web                       bool
	exporter                  *cmdutil.Exporter
}

// NewCmdView returns the `sprint view` command.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "view [<iteration>]",
		Short: "Show the work items of a sprint",
		Long: `Show the work items of a sprint, the team's current sprint by default, grouped by the
state category of the top-level items, with child items nested under their parent.

<iteration> is a sprint name or path of the team, e.g. "Sprint 3" or "Proj\Release 1\Sprint 3".

--team picks the team; the default is the "team" config value, else the project's default team.`,
		Example: `  ado sprint view
  ado sprint view "Sprint 3" -a @me
  ado sprint view --type task --json id,title,state,parent`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				o.iteration = args[0]
			}
			return run(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&o.team, "team", "", "Team `name` (default: config team, else the project's default team)")
	fl.StringVarP(&o.assignee, "assignee", "a", "", `Filter by assignee (email, name, "@me" or "none")`)
	fl.StringSliceVar(&o.types, "type", nil, "Filter by type: a type name or bug, story, task, feature, epic")
	fl.BoolVarP(&o.web, "web", "w", false, "Open the sprint's taskboard in the browser")
	cmdutil.AddJSONFlags(cmd, &o.exporter, jsonFields)
	return cmd
}

// item is a sprint work item; parent is its parent in the sprint (0 if none).
type item struct {
	wi       api.WorkItem
	parent   int
	category string
	children []*item
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	ios := f.IOStreams
	project, err := f.BaseProject()
	if err != nil {
		return err
	}
	hc, err := authshared.HTTPClient(ctx, f, project.Host, project.Org)
	if err != nil {
		return err
	}
	orgURL := authshared.OrgURL(project.Host, project.Org)
	c := api.NewWorkItemClient(hc, orgURL, project.Project)
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	team, err := shared.ResolveTeam(ctx, c, cfg, project, o.team)
	if err != nil {
		return err
	}
	it, err := findIteration(ctx, c, team, o.iteration)
	if err != nil {
		return err
	}
	if o.web {
		return cmdutil.OpenInBrowser(f, adocontext.TaskboardURL(project, team, it.Path))
	}

	links, err := c.IterationWorkItems(ctx, team, it.ID)
	if err != nil {
		return err
	}
	parents := map[int]int{}
	var ids []int
	for _, l := range links {
		if !slices.Contains(ids, l.TargetID) {
			ids = append(ids, l.TargetID)
		}
		if l.SourceID != 0 && strings.HasSuffix(l.Rel, "Hierarchy-Forward") {
			parents[l.TargetID] = l.SourceID
		}
	}
	proc := shared.NewProcess(c)
	filtered := o.assignee != "" || len(o.types) > 0
	if len(ids) > 0 && filtered {
		resolver := &api.IdentityResolver{HTTP: hc, OrgURL: orgURL, Prompter: f.Prompter}
		if ids, err = filterIDs(ctx, c, proc, resolver, ids, o); err != nil {
			return err
		}
	}
	var wis []api.WorkItem
	if len(ids) > 0 {
		if wis, err = c.WorkItems(ctx, ids, fetchFields, false); err != nil {
			return err
		}
	}
	items := make([]*item, len(wis))
	for i, wi := range wis {
		cat, err := stateCategory(ctx, proc, wi)
		if err != nil {
			return err
		}
		items[i] = &item{wi: wi, parent: parents[wi.ID], category: cat}
	}

	if o.exporter != nil {
		out := make([]itemJSON, len(items))
		for i, it := range items {
			out[i] = toJSON(it, project)
		}
		return o.exporter.Write(ios, out)
	}
	if len(items) == 0 {
		msg := "no work items in " + it.Name
		if filtered {
			msg += " match your filters"
		}
		fmt.Fprintln(ios.ErrOut, msg)
		return nil
	}
	return printItems(ios, it, nest(items))
}

// findIteration returns team's current iteration when name is empty, else the team iteration
// with that name or path (any case, `/` or `\` separated).
func findIteration(ctx context.Context, c *api.WorkItemClient, team, name string) (api.Iteration, error) {
	its, err := c.TeamIterations(ctx, team, name == "")
	if err != nil {
		return api.Iteration{}, err
	}
	if name == "" {
		if len(its) == 0 {
			return api.Iteration{}, fmt.Errorf("team %q has no current sprint; set sprint dates in the team settings", team)
		}
		return its[0], nil
	}
	path := strings.ReplaceAll(name, "/", `\`)
	names := make([]string, len(its))
	for i, it := range its {
		if strings.EqualFold(it.Name, name) || strings.EqualFold(it.Path, path) {
			return it, nil
		}
		names[i] = it.Name
	}
	return api.Iteration{}, fmt.Errorf("team %q has no sprint %q; its sprints: %s", team, name, strings.Join(names, ", "))
}

// filterIDs keeps the ids matching --type and --assignee, through a WIQL query.
func filterIDs(ctx context.Context, c *api.WorkItemClient, proc *shared.Process, resolver *api.IdentityResolver, ids []int, o *options) ([]int, error) {
	filter := shared.WIQLFilter{IDs: ids}
	for _, t := range o.types {
		typ, err := proc.ResolveType(ctx, t)
		if err != nil {
			return nil, err
		}
		filter.Types = append(filter.Types, typ)
	}
	if o.assignee != "" {
		var err error
		if filter.Assignee, err = shared.ResolveIdentityFilter(ctx, resolver, o.assignee); err != nil {
			return nil, err
		}
	}
	wiql, err := shared.BuildWIQL(filter)
	if err != nil {
		return nil, err
	}
	res, err := c.QueryWIQL(ctx, wiql, "", len(ids))
	if err != nil {
		return nil, err
	}
	// Keep the sprint's order, not the query's.
	return slices.DeleteFunc(ids, func(id int) bool { return !slices.Contains(res.IDs, id) }), nil
}

// stateCategory returns the category of wi's state in its type's workflow ("" if unknown).
func stateCategory(ctx context.Context, proc *shared.Process, wi api.WorkItem) (string, error) {
	states, err := proc.States(ctx, str(wi.Fields, "System.WorkItemType"))
	if err != nil {
		return "", err
	}
	state := str(wi.Fields, "System.State")
	for _, s := range states {
		if strings.EqualFold(s.Name, state) {
			return s.Category, nil
		}
	}
	return "", nil
}

// nest attaches items to their parent when the parent is listed too and returns the
// top-level items, in sprint order.
func nest(items []*item) []*item {
	byID := map[int]*item{}
	for _, it := range items {
		byID[it.wi.ID] = it
	}
	var roots []*item
	for _, it := range items {
		if p, ok := byID[it.parent]; ok && p != it {
			p.children = append(p.children, it)
		} else {
			roots = append(roots, it)
		}
	}
	return roots
}

// printItems prints the top-level items grouped by state category, children indented under their
// parent on a TTY; off a TTY one TSV row per item: category, id, type, state, title,
// assignee, parent.
func printItems(ios *iostreams.IOStreams, it api.Iteration, roots []*item) error {
	tty := ios.IsStdoutTTY()
	cs := ios.ColorScheme()
	if tty {
		fmt.Fprintf(ios.Out, "%s · %s\n", cs.Bold(it.Name), cs.Gray(it.Path))
	}
	// Known categories in order, then any other (e.g. a state missing from the process).
	order := []string{}
	for _, c := range categories {
		order = append(order, c.name)
	}
	for _, r := range roots {
		if !slices.Contains(order, r.category) {
			order = append(order, r.category)
		}
	}
	var tp *tableprinter.TablePrinter
	if !tty {
		tp = tableprinter.New(ios)
	}
	for _, cat := range order {
		var group []*item
		for _, r := range roots {
			if r.category == cat {
				group = append(group, r)
			}
		}
		if len(group) == 0 {
			continue
		}
		if tty {
			fmt.Fprintf(ios.Out, "\n%s\n", cs.Bold(heading(cat)))
			tp = tableprinter.New(ios)
		}
		var row func(it *item, depth int)
		row = func(it *item, depth int) {
			fl := it.wi.Fields
			id := strconv.Itoa(it.wi.ID)
			if tty {
				id = strings.Repeat("  ", depth) + "#" + id
			} else {
				tp.AddField(cat)
			}
			tp.AddField(id, tableprinter.WithColor(cs.Green))
			tp.AddField(str(fl, "System.WorkItemType"))
			tp.AddField(str(fl, "System.State"))
			tp.AddField(str(fl, "System.Title"))
			tp.AddField(displayName(fl))
			if !tty {
				p := ""
				if it.parent != 0 {
					p = strconv.Itoa(it.parent)
				}
				tp.AddField(p)
			}
			tp.EndRow()
			for _, ch := range it.children {
				row(ch, depth+1)
			}
		}
		for _, r := range group {
			row(r, 0)
		}
		if tty {
			if err := tp.Render(); err != nil {
				return err
			}
		}
	}
	if tty {
		return nil
	}
	return tp.Render()
}

// heading returns the TTY heading of state category cat.
func heading(cat string) string {
	for _, c := range categories {
		if c.name == cat {
			return c.heading
		}
	}
	if cat == "" {
		return "Other"
	}
	return cat
}

type userJSON struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	UniqueName  string `json:"uniqueName"`
}

type itemJSON struct {
	ID            int       `json:"id"`
	Type          string    `json:"type"`
	Title         string    `json:"title"`
	State         string    `json:"state"`
	StateCategory string    `json:"stateCategory"`
	AssignedTo    *userJSON `json:"assignedTo"`
	Parent        *int      `json:"parent"`
	URL           string    `json:"url"`
}

func toJSON(it *item, project adocontext.Repo) itemJSON {
	fl := it.wi.Fields
	out := itemJSON{
		ID: it.wi.ID, Type: str(fl, "System.WorkItemType"), Title: str(fl, "System.Title"), State: str(fl, "System.State"),
		StateCategory: it.category, URL: adocontext.WorkItemURL(project, it.wi.ID),
	}
	if m, ok := fl["System.AssignedTo"].(map[string]any); ok {
		out.AssignedTo = &userJSON{ID: str(m, "id"), DisplayName: str(m, "displayName"), UniqueName: str(m, "uniqueName")}
	}
	if it.parent != 0 {
		out.Parent = &it.parent
	}
	return out
}

func displayName(fields map[string]any) string {
	m, _ := fields["System.AssignedTo"].(map[string]any)
	return str(m, "displayName")
}

func str(fields map[string]any, ref string) string {
	s, _ := fields[ref].(string)
	return s
}
