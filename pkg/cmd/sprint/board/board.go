// Package board implements `ado sprint board`.
package board

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/columns"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// jsonFields are the public `--json` fields of a column.
var jsonFields = []string{"name", "items"}

// cardFields are the fields fetched for every card.
var cardFields = []string{"System.Title", "System.AssignedTo", "System.Parent", "System.State", "System.WorkItemType"}

type options struct {
	team      string
	iteration string
	exporter  *cmdutil.Exporter
}

// NewCmdBoard returns the `sprint board` command.
func NewCmdBoard(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "board [<iteration>]",
		Short: "Show a sprint's taskboard",
		Long: `Show the taskboard of a sprint, the current one by default: its columns side by side
(stacked when the terminal is too narrow) with cards "#id title (assignee)".

<iteration> is a sprint name or path from "ado sprint list". A team that never customized its
taskboard gets one column per state of its task type, as the web taskboard shows.

Without a terminal, prints one line per card: column, id, title, assignee and parent id,
separated by tabs.

--team picks the team; the default is the "team" config value, else the project's default team.`,
		Example: `  ado sprint board
  ado sprint board "Sprint 3" --team "Web Team"
  ado sprint board --json name,items --jq '.[] | {name, n: (.items | length)}'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				o.iteration = args[0]
			}
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().StringVar(&o.team, "team", "", "Team `name` (default: config team, else the project's default team)")
	cmdutil.AddJSONFlags(cmd, &o.exporter, jsonFields)
	return cmd
}

// column is a taskboard column and its cards.
type column struct {
	name  string
	items []api.WorkItem
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
	c := api.NewWorkItemClient(hc, authshared.OrgURL(project.Host, project.Org), project.Project)
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
	cols, err := taskboard(ctx, c, team, it.ID)
	if err != nil {
		return err
	}

	if o.exporter != nil {
		out := make([]columnJSON, len(cols))
		for i, col := range cols {
			out[i] = columnJSON{Name: col.name, Items: []cardJSON{}}
			for _, wi := range col.items {
				out[i].Items = append(out[i].Items, cardJSON{ID: wi.ID, Title: str(wi.Fields, "System.Title"),
					Type: str(wi.Fields, "System.WorkItemType"), State: str(wi.Fields, "System.State"),
					AssignedTo: user(wi.Fields), Parent: parent(wi.Fields)})
			}
		}
		return o.exporter.Write(ios, out)
	}
	if !ios.IsStdoutTTY() {
		for _, col := range cols {
			for _, wi := range col.items {
				var p string
				if n := parent(wi.Fields); n != nil {
					p = strconv.Itoa(*n)
				}
				fmt.Fprintf(ios.Out, "%s\t%d\t%s\t%s\t%s\n", col.name, wi.ID, str(wi.Fields, "System.Title"), assignee(wi.Fields), p)
			}
		}
		return nil
	}
	cs := ios.ColorScheme()
	fmt.Fprintf(ios.Out, "%s %s\n\n", cs.Bold(it.Name), cs.Gray(it.Path))
	view := make([]columns.Column, len(cols))
	for i, col := range cols {
		view[i].Name = col.name
		for _, wi := range col.items {
			view[i].Cards = append(view[i].Cards, columns.Card{ID: wi.ID, Title: str(wi.Fields, "System.Title"), Assignee: assignee(wi.Fields)})
		}
	}
	columns.Render(ios.Out, cs, ios.TerminalWidth(), view)
	return nil
}

// findIteration returns team's current iteration when arg is "", else the team iteration
// named arg or at path arg (any case, `\` or `/` separated, optional leading `\`).
func findIteration(ctx context.Context, c *api.WorkItemClient, team, arg string) (api.Iteration, error) {
	its, err := c.TeamIterations(ctx, team, arg == "")
	if err != nil {
		return api.Iteration{}, err
	}
	if arg == "" {
		if len(its) == 0 {
			return api.Iteration{}, fmt.Errorf("team %q has no current sprint; set sprint dates in the team settings", team)
		}
		return its[0], nil
	}
	path := strings.TrimPrefix(strings.ReplaceAll(arg, "/", `\`), `\`)
	for _, it := range its {
		if strings.EqualFold(it.Name, arg) || strings.EqualFold(it.Path, path) {
			return it, nil
		}
	}
	return api.Iteration{}, fmt.Errorf("sprint %q is not one of team %q's iterations; see ado sprint list", arg, team)
}

// taskboard returns team's taskboard columns for the iteration with ID iterationID, with
// their cards in board order.
func taskboard(ctx context.Context, c *api.WorkItemClient, team, iterationID string) ([]column, error) {
	tcs, err := c.TaskboardColumns(ctx, team)
	if err != nil {
		return nil, err
	}
	if len(tcs) == 0 {
		return defaultTaskboard(ctx, c, team, iterationID)
	}
	placed, err := c.TaskboardItems(ctx, team, iterationID)
	if err != nil {
		return nil, err
	}
	ids := make([]int, len(placed))
	for i, p := range placed {
		ids[i] = p.ID
	}
	items, err := c.WorkItems(ctx, ids, cardFields, false)
	if err != nil {
		return nil, err
	}
	cols := make([]column, len(tcs))
	for i, tc := range tcs {
		cols[i].name = tc.Name
	}
	for _, p := range placed {
		i := slices.IndexFunc(tcs, func(tc api.TaskboardColumn) bool {
			return p.ColumnID != "" && strings.EqualFold(tc.ID, p.ColumnID) || p.ColumnID == "" && strings.EqualFold(tc.Name, p.Column)
		})
		j := slices.IndexFunc(items, func(wi api.WorkItem) bool { return wi.ID == p.ID })
		if i >= 0 && j >= 0 {
			cols[i].items = append(cols[i].items, items[j])
		}
	}
	return cols, nil
}

// defaultTaskboard is the taskboard of a team that never customized it (the server then has no
// columns and refuses taskboardworkitems): one column per state of the task type but Removed,
// holding the iteration's tasks in that state.
// ponytail: tasks only; bugs managed as tasks (team setting bugsBehavior=asTasks) are left out.
func defaultTaskboard(ctx context.Context, c *api.WorkItemClient, team, iterationID string) ([]column, error) {
	p := shared.NewProcess(c)
	typ, err := p.ResolveType(ctx, "task")
	if err != nil {
		return nil, err
	}
	states, err := p.States(ctx, typ)
	if err != nil {
		return nil, err
	}
	ids, err := c.IterationWorkItemIDs(ctx, team, iterationID)
	if err != nil {
		return nil, err
	}
	items, err := c.WorkItems(ctx, ids, cardFields, false)
	if err != nil {
		return nil, err
	}
	var cols []column
	for _, s := range states {
		if s.Category == shared.CategoryRemoved {
			continue
		}
		col := column{name: s.Name}
		for _, wi := range items {
			if strings.EqualFold(str(wi.Fields, "System.WorkItemType"), typ) && strings.EqualFold(str(wi.Fields, "System.State"), s.Name) {
				col.items = append(col.items, wi)
			}
		}
		cols = append(cols, col)
	}
	return cols, nil
}

type userJSON struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	UniqueName  string `json:"uniqueName"`
}

type cardJSON struct {
	ID         int       `json:"id"`
	Title      string    `json:"title"`
	Type       string    `json:"type"`
	State      string    `json:"state"`
	AssignedTo *userJSON `json:"assignedTo"`
	Parent     *int      `json:"parent"`
}

type columnJSON struct {
	Name  string     `json:"name"`
	Items []cardJSON `json:"items"`
}

func str(fields map[string]any, ref string) string {
	s, _ := fields[ref].(string)
	return s
}

// user returns System.AssignedTo, or nil when unassigned.
func user(fields map[string]any) *userJSON {
	m, ok := fields["System.AssignedTo"].(map[string]any)
	if !ok {
		return nil
	}
	return &userJSON{ID: str(m, "id"), DisplayName: str(m, "displayName"), UniqueName: str(m, "uniqueName")}
}

func assignee(fields map[string]any) string {
	if u := user(fields); u != nil {
		return u.DisplayName
	}
	return ""
}

// parent returns System.Parent (decoded from JSON as float64), or nil when unset.
func parent(fields map[string]any) *int {
	f, ok := fields["System.Parent"].(float64)
	if !ok {
		return nil
	}
	n := int(f)
	return &n
}
