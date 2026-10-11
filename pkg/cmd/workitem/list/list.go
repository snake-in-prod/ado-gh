// Package list implements `ado workitem list`.
package list

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	// maxURLLength is gh's limit on a URL handed to the browser.
	maxURLLength = 8192
	// searchPageSize is the $top of a search page; its IDs fit one WIQL `IN` clause well under
	// the 32,000-character limit.
	searchPageSize = 1000
)

// jsonFields are the public `--json` fields.
var jsonFields = []string{
	"id", "type", "title", "state", "reason", "assignedTo", "createdBy", "createdDate", "changedDate",
	"areaPath", "iterationPath", "tags", "priority", "parent", "url", "fields",
}

// listFields are the fields fetched unless --json asks for `fields` (all of them).
var listFields = []string{
	"System.Id", "System.WorkItemType", "System.Title", "System.State", "System.Reason", "System.AssignedTo",
	"System.CreatedBy", "System.CreatedDate", "System.ChangedDate", "System.AreaPath", "System.IterationPath",
	"System.Tags", "Microsoft.VSTS.Common.Priority", "System.Parent",
}

type options struct {
	types, tags                                            []string
	state, assignee, author, area, iteration, search, team string
	limit                                                  int
	web                                                    bool
	exporter                                               *cmdutil.Exporter
}

// NewCmdList returns the `workitem list` command.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List work items in the project",
		Long: `List work items in the project, most recently changed first.

--state takes open (the default), closed, all or a state name; open and closed follow the
state categories of the project's process. --tag matches work items having all the tags.

--search runs Azure DevOps Work Item Search, which takes the portal search syntax (for
example "crash a:@me t:Bug"), then applies the other filters to the hits.`,
		Example: `  ado workitem list -a @me
  ado wi list --type bug -s closed --iteration @current
  ado wi list -S "crash on save" --json id,title`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.limit < 1 {
				return cmdutil.FlagErrorf("invalid value for --limit: %d", o.limit)
			}
			return run(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.StringSliceVar(&o.types, "type", nil, "Filter by type: a type name or bug, story, task, feature, epic")
	fl.StringVarP(&o.state, "state", "s", "open", "Filter by state: {open|closed|all|<state name>}")
	fl.StringVarP(&o.assignee, "assignee", "a", "", `Filter by assignee (email, name, "@me" or "none")`)
	fl.StringVarP(&o.author, "author", "A", "", `Filter by author (email, name or "@me")`)
	fl.StringVar(&o.area, "area", "", "Filter by area path, including sub-areas")
	fl.StringVar(&o.iteration, "iteration", "", `Filter by iteration path, including children, or "@current"`)
	fl.StringSliceVarP(&o.tags, "tag", "l", nil, "Filter by tags, all must match (alias --label)")
	fl.StringVarP(&o.search, "search", "S", "", "Search work items with Work Item Search syntax")
	fl.StringVar(&o.team, "team", "", "Team for --iteration @current (default: config team, else the project's default team)")
	fl.IntVarP(&o.limit, "limit", "L", 30, "Maximum number of work items to fetch")
	fl.BoolVarP(&o.web, "web", "w", false, "Open the query in the browser")
	cmd.MarkFlagsMutuallyExclusive("search", "web")
	cmdutil.AddJSONFlags(cmd, &o.exporter, jsonFields)
	fl.SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "label" {
			name = "tag"
		}
		return pflag.NormalizedName(name)
	})
	return cmd
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
	filter, err := buildFilter(ctx, f, c, &api.IdentityResolver{HTTP: hc, OrgURL: orgURL, Prompter: f.Prompter}, project, o)
	if err != nil {
		return err
	}

	if o.web {
		wiql, err := shared.BuildWIQL(filter)
		if err != nil {
			return err
		}
		u := adocontext.ProjectURL(project) + "/_queries/query/?wiql=" + adocontext.QueryEscape(wiql)
		if len(u) >= maxURLLength {
			fmt.Fprintln(ios.ErrOut, "! the query is too long for a URL; opening the Work Items hub without the filters")
			u = adocontext.ProjectURL(project) + "/_workitems"
		}
		return cmdutil.OpenInBrowser(f, u)
	}

	var ids []int
	if o.search != "" {
		s := &api.WorkItemSearcher{HTTP: hc, OrgURL: orgURL, Project: project.Project}
		ids, err = searchIDs(ctx, f, c, s, filter, o)
	} else {
		ids, err = queryIDs(ctx, c, filter, o.limit)
	}
	if err != nil {
		return err
	}
	var items []api.WorkItem
	if len(ids) > 0 {
		fields := listFields
		if o.exporter != nil && slices.Contains(o.exporter.Fields, "fields") {
			fields = nil
		}
		if items, err = c.WorkItems(ctx, ids, fields, false); err != nil {
			return err
		}
	}

	if o.exporter != nil {
		out := make([]itemJSON, len(items))
		for i, wi := range items {
			out[i] = toJSON(wi, project)
		}
		return o.exporter.Write(ios, out)
	}
	if len(items) == 0 {
		fmt.Fprintln(ios.ErrOut, "no work items match your search")
		return nil
	}
	return printTable(ios, project, items)
}

// buildFilter resolves the filter flags into a WIQL filter.
func buildFilter(ctx context.Context, f *cmdutil.Factory, c *api.WorkItemClient, resolver *api.IdentityResolver,
	project adocontext.Repo, o *options,
) (shared.WIQLFilter, error) {
	var filter shared.WIQLFilter
	proc := shared.NewProcess(c)
	for _, t := range o.types {
		typ, err := proc.ResolveType(ctx, t)
		if err != nil {
			return filter, err
		}
		filter.Types = append(filter.Types, typ)
	}
	var err error
	if filter.States, err = proc.ExpandStates(ctx, filter.Types, o.state); err != nil {
		return filter, err
	}
	if o.assignee != "" {
		if filter.Assignee, err = shared.ResolveIdentityFilter(ctx, resolver, o.assignee); err != nil {
			return filter, err
		}
	}
	if o.author != "" {
		if filter.Author, err = shared.ResolveIdentityFilter(ctx, resolver, o.author); err != nil {
			return filter, err
		}
	}
	if o.area != "" {
		if filter.AreaPath, err = shared.ResolvePath(ctx, c, "areas", o.area); err != nil {
			return filter, err
		}
	}
	if o.iteration != "" {
		filter.IterationPath, err = shared.ResolveIterationPath(ctx, c, o.iteration, func() (string, error) {
			cfg, err := f.Config()
			if err != nil {
				return "", err
			}
			return shared.ResolveTeam(ctx, c, cfg, project, o.team)
		})
		if err != nil {
			return filter, err
		}
	}
	for _, t := range o.tags {
		if t = strings.TrimSpace(t); t != "" {
			filter.Tags = append(filter.Tags, t)
		}
	}
	return filter, nil
}

func queryIDs(ctx context.Context, c *api.WorkItemClient, filter shared.WIQLFilter, limit int) ([]int, error) {
	wiql, err := shared.BuildWIQL(filter)
	if err != nil {
		return nil, err
	}
	res, err := c.QueryWIQL(ctx, wiql, "", limit)
	if err != nil {
		return nil, err
	}
	return res.IDs, nil
}

// searchIDs pages through the search hits, newest change first, and keeps those matching the
// other filters (WIQL `[System.Id] IN (page)`) until --limit are found or the hits run out.
// Order is exact within a page; the search index may lag recent changes across pages.
func searchIDs(ctx context.Context, f *cmdutil.Factory, c *api.WorkItemClient, s *api.WorkItemSearcher,
	filter shared.WIQLFilter, o *options,
) ([]int, error) {
	var ids []int
	warned := false
	for skip := 0; len(ids) < o.limit; {
		page, err := s.Search(ctx, o.search, skip, searchPageSize)
		if err != nil {
			return nil, err
		}
		if page.InfoCode != 0 && !warned {
			fmt.Fprintf(f.IOStreams.ErrOut, "! %s\n", infoMessage(page.InfoCode))
			warned = true
		}
		if len(page.IDs) > 0 {
			filter.IDs = page.IDs
			found, err := queryIDs(ctx, c, filter, o.limit-len(ids))
			if err != nil {
				return nil, err
			}
			ids = append(ids, found...)
		}
		skip += page.Results
		if page.Results < searchPageSize || skip >= page.Count {
			break
		}
	}
	return ids, nil
}

// infoMessage explains a Work Item Search info code.
func infoMessage(code int) string {
	switch code {
	case 1, 2, 6, 7:
		return "the search index is being built; results may be incomplete"
	case 8:
		return "work item search trimmed the results; results may be incomplete"
	case 11:
		return "some work items are not accessible to search; results may be incomplete"
	}
	return fmt.Sprintf("work item search returned info code %d; results may be incomplete", code)
}

func printTable(ios *iostreams.IOStreams, project adocontext.Repo, items []api.WorkItem) error {
	tty := ios.IsStdoutTTY()
	if tty {
		noun := "work items"
		if len(items) == 1 {
			noun = "work item"
		}
		fmt.Fprintf(ios.Out, "\nShowing %d %s in %s\n\n", len(items), noun, project.Project)
	}
	cs := ios.ColorScheme()
	tp := tableprinter.New(ios, "id", "type", "state", "title", "assignee", "tags", "updated")
	now := time.Now()
	for _, wi := range items {
		id := strconv.Itoa(wi.ID)
		if tty {
			id = "#" + id
		}
		tp.AddField(id, tableprinter.WithColor(cs.Green))
		tp.AddField(str(wi.Fields, "System.WorkItemType"))
		tp.AddField(str(wi.Fields, "System.State"))
		tp.AddField(str(wi.Fields, "System.Title"))
		var assignee string
		if u := user(wi.Fields, "System.AssignedTo"); u != nil {
			assignee = u.DisplayName
		}
		tp.AddField(assignee)
		tp.AddField(strings.Join(tags(wi.Fields), ", "), tableprinter.WithColor(cs.Gray))
		if changed, err := time.Parse(time.RFC3339Nano, str(wi.Fields, "System.ChangedDate")); err == nil {
			tp.AddTimeField(now, changed, cs.Gray)
		} else {
			tp.AddField("") // no usable date: leave the cell empty rather than show year 1
		}
		tp.EndRow()
	}
	return tp.Render()
}

type userJSON struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	UniqueName  string `json:"uniqueName"`
}

type itemJSON struct {
	ID            int            `json:"id"`
	Type          string         `json:"type"`
	Title         string         `json:"title"`
	State         string         `json:"state"`
	Reason        string         `json:"reason"`
	AssignedTo    *userJSON      `json:"assignedTo"`
	CreatedBy     *userJSON      `json:"createdBy"`
	CreatedDate   string         `json:"createdDate"`
	ChangedDate   string         `json:"changedDate"`
	AreaPath      string         `json:"areaPath"`
	IterationPath string         `json:"iterationPath"`
	Tags          []string       `json:"tags"`
	Priority      *int           `json:"priority"`
	Parent        *int           `json:"parent"`
	URL           string         `json:"url"`
	Fields        map[string]any `json:"fields"`
}

func toJSON(wi api.WorkItem, project adocontext.Repo) itemJSON {
	fl := wi.Fields
	return itemJSON{
		ID: wi.ID, Type: str(fl, "System.WorkItemType"), Title: str(fl, "System.Title"), State: str(fl, "System.State"),
		Reason: str(fl, "System.Reason"), AssignedTo: user(fl, "System.AssignedTo"), CreatedBy: user(fl, "System.CreatedBy"),
		CreatedDate: str(fl, "System.CreatedDate"), ChangedDate: str(fl, "System.ChangedDate"),
		AreaPath: str(fl, "System.AreaPath"), IterationPath: str(fl, "System.IterationPath"), Tags: tags(fl),
		Priority: num(fl, "Microsoft.VSTS.Common.Priority"), Parent: num(fl, "System.Parent"),
		URL: adocontext.WorkItemURL(project, wi.ID), Fields: fl,
	}
}

func str(fields map[string]any, ref string) string {
	s, _ := fields[ref].(string)
	return s
}

// num returns an integer field (decoded from JSON as float64), or nil when unset.
func num(fields map[string]any, ref string) *int {
	f, ok := fields[ref].(float64)
	if !ok {
		return nil
	}
	n := int(f)
	return &n
}

// user returns an identity field, or nil when unset.
func user(fields map[string]any, ref string) *userJSON {
	m, ok := fields[ref].(map[string]any)
	if !ok {
		return nil
	}
	return &userJSON{ID: str(m, "id"), DisplayName: str(m, "displayName"), UniqueName: str(m, "uniqueName")}
}

// tags splits System.Tags ("a; b") into its tags.
func tags(fields map[string]any) []string {
	out := []string{}
	for _, t := range strings.Split(str(fields, "System.Tags"), ";") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}
