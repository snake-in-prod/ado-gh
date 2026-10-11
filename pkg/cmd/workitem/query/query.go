// Package query implements `ado workitem query`.
package query

import (
	"context"
	"fmt"
	"regexp"
	"slices"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// jsonFields are the public `--json` fields: the `wi list` fields, plus links for tree and
// one-hop queries.
var jsonFields = []string{
	"id", "type", "title", "state", "reason", "assignedTo", "createdBy", "createdDate", "changedDate",
	"areaPath", "iterationPath", "tags", "priority", "parent", "url", "fields", "links",
}

type options struct {
	wiqlFile, team string
	limit          int
	web            bool
	exporter       *cmdutil.Exporter
}

// NewCmdQuery returns the `workitem query` command.
func NewCmdQuery(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "query [<wiql> | <path> | <id>]",
		Short: "Run a WIQL query or a saved query",
		Long: `Run a WIQL query or a saved query.

Text starting with SELECT runs as WIQL. A GUID runs the saved query with that ID; any other
text is a saved query path, such as "Shared Queries/Team/Active Bugs" or "My Queries/Mine".
--wiql-file reads WIQL from a file, or from standard input with "-".

Flat queries print the query's columns. Tree and one-hop queries print linked work items
nested under their parents; --limit counts their top-level work items.

Macros such as @Me, @Today, @Project, @CurrentIteration and @TeamAreas are evaluated by Azure
DevOps; the team macros use --team (default: config team, else the project's default team).`,
		Example: `  ado workitem query "Shared Queries/Team/Active Bugs"
  ado wi query "SELECT [System.Id] FROM WorkItems WHERE [System.AssignedTo] = @Me"
  ado wi query --wiql-file - < bugs.wiql --json id,title`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.limit < 1 {
				return cmdutil.FlagErrorf("invalid value for --limit: %d", o.limit)
			}
			if (len(args) == 1) == (o.wiqlFile != "") {
				return cmdutil.FlagErrorf("give either a query argument or --wiql-file")
			}
			var arg string
			if len(args) == 1 {
				arg = args[0]
			}
			if o.web && (o.wiqlFile != "" || selectRE.MatchString(arg)) {
				return cmdutil.FlagErrorf("--web works only with saved queries")
			}
			return run(cmd.Context(), f, o, arg)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&o.wiqlFile, "wiql-file", "", `Read the WIQL query from file (use "-" for stdin)`)
	fl.StringVar(&o.team, "team", "", "Team for team macros (default: config team, else the project's default team)")
	fl.IntVarP(&o.limit, "limit", "L", 200, "Maximum number of results to fetch")
	fl.BoolVarP(&o.web, "web", "w", false, "Open the saved query in the browser")
	cmdutil.AddJSONFlags(cmd, &o.exporter, jsonFields)
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options, arg string) error {
	project, err := f.BaseProject()
	if err != nil {
		return err
	}
	hc, err := authshared.HTTPClient(ctx, f, project.Host, project.Org)
	if err != nil {
		return err
	}
	c := api.NewWorkItemClient(hc, authshared.OrgURL(project.Host, project.Org), project.Project)
	in, err := resolveInput(ctx, c, f.IOStreams.In, arg, o.wiqlFile)
	if err != nil {
		return err
	}
	if o.web {
		return cmdutil.OpenInBrowser(f, adocontext.ProjectURL(project)+"/_queries/query/"+in.Saved.ID)
	}
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	top := o.limit
	if isLinkQuery(in) {
		top = 0
	}
	res, err := runQuery(ctx, c, in, teamFor(cfg, project, o.team), top)
	if err != nil {
		return err
	}
	return render(ctx, f, c, project, o, res)
}

// linkRE matches WIQL of a tree or one-hop query.
var linkRE = regexp.MustCompile(`(?i)\bFROM\s+WorkItemLinks\b`)

// isLinkQuery reports whether in is a tree or one-hop query. Those run without $top, as the
// service doesn't say what it counts, and are trimmed to --limit roots instead.
func isLinkQuery(in *queryInput) bool {
	if in.Saved != nil {
		return in.Saved.QueryType == "tree" || in.Saved.QueryType == "oneHop"
	}
	return linkRE.MatchString(in.WIQL)
}

// render fetches the field values of res's work items (the query's columns, plus the known
// fields --json asks for) and prints them as JSON or a table.
func render(ctx context.Context, f *cmdutil.Factory, c *api.WorkItemClient, project adocontext.Repo, o *options, res *api.QueryResult) error {
	ios := f.IOStreams
	var flat, ids []int
	var roots []*node
	total := len(res.IDs)
	if isLinkResult(res) {
		total = len(res.Links)
		roots = buildTree(res)
		if len(roots) > o.limit {
			fmt.Fprintf(ios.ErrOut, "! showing the first %d of %d top-level work items; use -L to show more\n", o.limit, len(roots))
			roots = roots[:o.limit]
		}
		seen := map[int]bool{}
		walk(roots, func(n *node) {
			if !seen[n.id] {
				seen[n.id] = true
				ids = append(ids, n.id)
			}
		})
	} else {
		flat, ids = res.IDs, res.IDs
		if len(flat) >= o.limit {
			fmt.Fprintf(ios.ErrOut, "! results reached the limit of %d; use -L to fetch more\n", o.limit)
		}
	}
	if total >= maxResults {
		fmt.Fprintln(ios.ErrOut, "! Azure DevOps truncates query results at 20,000; narrow the query")
	}

	var want []string
	if o.exporter != nil {
		want = o.exporter.Fields
	}
	items := map[int]api.WorkItem{}
	if fields := fetchFields(res, want); len(fields) > 0 && len(ids) > 0 {
		list, err := c.WorkItems(ctx, ids, fields, false)
		if err != nil {
			return err
		}
		for _, wi := range list {
			items[wi.ID] = wi
		}
	}
	get := func(id int) api.WorkItem {
		wi := items[id]
		wi.ID = id
		return wi
	}

	if o.exporter != nil {
		out := []map[string]any{}
		for _, id := range flat {
			out = append(out, itemJSON(get(id), res, project, want))
		}
		var item func(n *node) map[string]any
		item = func(n *node) map[string]any {
			m := itemJSON(get(n.id), res, project, want)
			if slices.Contains(want, "links") {
				m["links"] = linksJSON(n.links, item)
			}
			return m
		}
		for _, r := range roots {
			out = append(out, item(r))
		}
		return o.exporter.Write(ios, out)
	}
	if len(ids) == 0 {
		fmt.Fprintln(ios.ErrOut, "no work items match the query")
		return nil
	}
	return printTable(ios, res, items, flat, roots)
}
