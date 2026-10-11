// Package board implements `ado board`.
package board

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/columns"
	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// recentDoneDays is how long a completed item stays on the board.
// ponytail: fixed window, add a flag if people need older Done cards.
const recentDoneDays = 30

// jsonFields are the public `--json` fields.
var jsonFields = []string{"name", "team", "columns"}

type options struct {
	team, assignee string
	web            bool
	exporter       *cmdutil.Exporter
}

// NewCmdBoard returns the `board` command.
func NewCmdBoard(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "board [<backlog>]",
		Short: "Show a team's Kanban board",
		Long: `Show a team's Kanban board for a backlog level: the requirement backlog by default
(Stories, Backlog items, Issues or Requirements, by process), or a level such as features or
epics, by name or category reference name.

Columns show their WIP limit (Active 3/5) and split columns their Doing and Done halves.
Completed items changed in the last 30 days are shown.

--team picks the team; the default is the "team" config value, else the project's default team.`,
		Example: `  ado board
  ado board features --team "Web Team"
  ado board -a @me --json columns
  ado board epics --web`,
		GroupID: "core",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			level := ""
			if len(args) > 0 {
				level = args[0]
			}
			return run(cmd.Context(), f, o, level)
		},
	}
	cmd.Flags().StringVar(&o.team, "team", "", "Team `name` (default: config team, else the project's default team)")
	cmd.Flags().StringVarP(&o.assignee, "assignee", "a", "", `Filter by assignee (email, name, "@me" or "none")`)
	cmd.Flags().BoolVarP(&o.web, "web", "w", false, "Open the board in the browser")
	cmdutil.AddJSONFlags(cmd, &o.exporter, jsonFields)
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options, levelArg string) error {
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
	backlogs, err := c.Backlogs(ctx, team)
	if err != nil {
		return err
	}
	level, err := pickLevel(backlogs, levelArg, team)
	if err != nil {
		return err
	}
	if o.web {
		return cmdutil.OpenInBrowser(f, adocontext.BoardURL(project, team, level.ID))
	}
	board, err := c.Board(ctx, team, level.Name)
	if err != nil {
		return err
	}
	var assignee shared.IdentityFilter
	if o.assignee != "" {
		r := &api.IdentityResolver{HTTP: hc, OrgURL: orgURL, Prompter: f.Prompter}
		if assignee, err = shared.ResolveIdentityFilter(ctx, r, o.assignee); err != nil {
			return err
		}
	}
	res, err := c.QueryWIQL(ctx, boardWIQL(project.Project, team, board, backlogs.OrderField, assignee), team, 0)
	if err != nil {
		return err
	}
	var items []api.WorkItem
	if len(res.IDs) > 0 {
		fields := []string{"System.Id", "System.WorkItemType", "System.Title", "System.State", "System.AssignedTo",
			board.ColumnField, board.DoneField}
		fields = slices.DeleteFunc(fields, func(s string) bool { return s == "" })
		if items, err = c.WorkItems(ctx, res.IDs, fields, false); err != nil {
			return err
		}
	}
	cols := place(board, items)

	if o.exporter != nil {
		return o.exporter.Write(ios, boardJSON{Name: level.Name, Team: team, Columns: cols})
	}
	out := ios.Out
	if !ios.IsStdoutTTY() {
		tp := tableprinter.New(ios)
		for _, col := range cols {
			for _, it := range col.Items {
				name := col.Name
				switch {
				case it.Done:
					name += "/Done"
				case col.IsSplit:
					name += "/Doing"
				}
				for _, v := range []string{name, strconv.Itoa(it.ID), it.Title, it.assigneeName()} {
					tp.AddField(tableprinter.OneLine(v))
				}
				tp.EndRow()
			}
		}
		return tp.Render()
	}
	cs := ios.ColorScheme()
	fmt.Fprintf(out, "%s\n\n", cs.Bold(level.Name+" · "+team))
	rcols := make([]columns.Column, len(cols))
	for i, col := range cols {
		rcols[i] = columns.Column{Name: col.Name, WIPLimit: col.WIPLimit, Split: col.IsSplit}
		for _, it := range col.Items {
			card := columns.Card{ID: it.ID, Title: tableprinter.OneLine(it.Title), Assignee: it.assigneeName()}
			if it.Done {
				rcols[i].Done = append(rcols[i].Done, card)
			} else {
				rcols[i].Cards = append(rcols[i].Cards, card)
			}
		}
	}
	columns.Render(out, cs, ios.TerminalWidth(), rcols)
	return nil
}

// pickLevel returns the backlog level named arg (name or ID, any case), the requirement level
// when arg is "".
func pickLevel(b *api.Backlogs, arg, team string) (api.BacklogLevel, error) {
	levels := append([]api.BacklogLevel{b.Requirement}, b.Portfolio...)
	i := 0
	if arg != "" {
		i = slices.IndexFunc(levels, func(l api.BacklogLevel) bool {
			return strings.EqualFold(l.Name, arg) || strings.EqualFold(l.ID, arg)
		})
	}
	if i < 0 {
		names := make([]string, len(levels))
		for j, l := range levels {
			names[j] = l.Name
		}
		return api.BacklogLevel{}, fmt.Errorf("unknown backlog %q; available: %s", arg, strings.Join(names, ", "))
	}
	if levels[i].Hidden {
		return api.BacklogLevel{}, fmt.Errorf("backlog %q is hidden for team %q; turn it on in the team's backlog settings", levels[i].Name, team)
	}
	return levels[i], nil
}

// boardWIQL returns the query for the cards of board: its work item types and mapped states in
// the team's areas, with completed items limited to the last recentDoneDays days. The types
// come from the board's state mappings, so bugs shown with requirements are included.
func boardWIQL(project, team string, b *api.Board, orderField string, assignee shared.IdentityFilter) string {
	var types, states, done []string
	for _, col := range b.Columns {
		for typ, state := range col.StateMappings {
			types, states = append(types, typ), append(states, state)
			if col.ColumnType == "outgoing" {
				done = append(done, state)
			}
		}
	}
	clauses := []string{
		"[System.TeamProject] = @project",
		"[System.WorkItemType] IN " + quoteList(types),
		"[System.AreaPath] = @TeamAreas(" + shared.QuoteWIQL("["+project+`]\`+team) + ")",
		"[System.State] IN " + quoteList(states),
	}
	if len(done) > 0 {
		clauses = append(clauses, fmt.Sprintf("([System.State] NOT IN %s OR [System.ChangedDate] >= @Today - %d)", quoteList(done), recentDoneDays))
	}
	if c := shared.IdentityClause("System.AssignedTo", assignee); c != "" {
		clauses = append(clauses, c)
	}
	order := "[System.Id] ASC"
	if orderField != "" && !strings.ContainsAny(orderField, "[]") {
		order = "[" + orderField + "] ASC, " + order
	}
	return "SELECT [System.Id] FROM WorkItems WHERE " + strings.Join(clauses, " AND ") + " ORDER BY " + order
}

// quoteList returns the sorted, deduplicated values as a WIQL list: ('a', 'b').
func quoteList(values []string) string {
	slices.Sort(values)
	values = slices.Compact(values)
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = shared.QuoteWIQL(v)
	}
	return "(" + strings.Join(quoted, ", ") + ")"
}

// place puts items in board's columns by the team's column field, falling back to the state
// mapping when the field is unset or names no column; items matching neither are dropped.
// System.BoardColumn is not used: another team's board can change it.
func place(b *api.Board, items []api.WorkItem) []columnJSON {
	cols := make([]columnJSON, len(b.Columns))
	for i, col := range b.Columns {
		cols[i] = columnJSON{Name: col.Name, IsSplit: col.IsSplit, WIPLimit: col.ItemLimit, Items: []itemJSON{}}
	}
	for _, wi := range items {
		typ, state := str(wi.Fields, "System.WorkItemType"), str(wi.Fields, "System.State")
		name := str(wi.Fields, b.ColumnField)
		i := slices.IndexFunc(b.Columns, func(c api.BoardColumn) bool { return name != "" && c.Name == name })
		if i < 0 {
			i = slices.IndexFunc(b.Columns, func(c api.BoardColumn) bool { return c.StateMappings[typ] == state })
		}
		if i < 0 {
			continue
		}
		done, _ := wi.Fields[b.DoneField].(bool)
		cols[i].Items = append(cols[i].Items, itemJSON{
			ID: wi.ID, Type: typ, Title: str(wi.Fields, "System.Title"), State: state,
			AssignedTo: user(wi.Fields, "System.AssignedTo"), Done: done && cols[i].IsSplit,
		})
	}
	return cols
}

func str(fields map[string]any, ref string) string {
	s, _ := fields[ref].(string)
	return s
}

// user returns an identity field, or nil when unset.
func user(fields map[string]any, ref string) *userJSON {
	m, ok := fields[ref].(map[string]any)
	if !ok {
		return nil
	}
	return &userJSON{ID: str(m, "id"), DisplayName: str(m, "displayName"), UniqueName: str(m, "uniqueName")}
}

type userJSON struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	UniqueName  string `json:"uniqueName"`
}

type itemJSON struct {
	ID         int       `json:"id"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	State      string    `json:"state"`
	AssignedTo *userJSON `json:"assignedTo"`
	// Done is set for an item in the Done half of a split column.
	Done bool `json:"done"`
}

func (it itemJSON) assigneeName() string {
	if it.AssignedTo == nil {
		return ""
	}
	return it.AssignedTo.DisplayName
}

type columnJSON struct {
	Name     string     `json:"name"`
	IsSplit  bool       `json:"isSplit"`
	WIPLimit int        `json:"wipLimit"`
	Items    []itemJSON `json:"items"`
}

type boardJSON struct {
	Name    string       `json:"name"`
	Team    string       `json:"team"`
	Columns []columnJSON `json:"columns"`
}
