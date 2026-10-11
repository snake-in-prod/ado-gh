// Package querylist implements `ado workitem query list`.
package querylist

import (
	"context"
	"fmt"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// fetchDepth is the $depth of each queries request; the TFS-era docs cap it at 2.
const fetchDepth = 2

// jsonFields are the public `--json` fields.
var jsonFields = []string{"id", "name", "path", "isFolder", "queryType", "isPublic"}

type options struct {
	folder   string
	exporter *cmdutil.Exporter
}

// NewCmdList returns the `workitem query list` command.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List saved queries",
		Long: `List the saved query tree of the project (My Queries and Shared Queries) with paths and IDs.

--folder lists only the queries and folders under a folder path such as "Shared Queries/Team".`,
		Example: `  ado workitem query list
  ado wi query list --folder "Shared Queries/Team" --json id,path`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().StringVar(&o.folder, "folder", "", "List only the queries under a folder `path`")
	cmdutil.AddJSONFlags(cmd, &o.exporter, jsonFields)
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
	c := api.NewWorkItemClient(hc, authshared.OrgURL(project.Host, project.Org), project.Project)

	var roots []api.QueryItem
	if o.folder == "" {
		if roots, err = c.Queries(ctx, fetchDepth); err != nil {
			return err
		}
	} else {
		folder, err := c.Query(ctx, o.folder, fetchDepth, false)
		if err != nil {
			return err
		}
		if !folder.IsFolder {
			return fmt.Errorf("%q is a query, not a folder", o.folder)
		}
		roots = folder.Children
	}
	var rows []row
	if err := walk(ctx, c, roots, 0, &rows); err != nil {
		return err
	}

	if o.exporter != nil {
		out := make([]queryJSON, len(rows))
		for i, r := range rows {
			q := r.item
			out[i] = queryJSON{ID: q.ID, Name: q.Name, Path: q.Path, IsFolder: q.IsFolder, QueryType: q.QueryType, IsPublic: q.IsPublic}
		}
		return o.exporter.Write(ios, out)
	}
	if len(rows) == 0 {
		fmt.Fprintln(ios.ErrOut, "no saved queries found")
		return nil
	}
	tty := ios.IsStdoutTTY()
	cs := ios.ColorScheme()
	tp := tableprinter.New(ios, "name", "type", "id")
	for _, r := range rows {
		q := r.item
		name, typ := q.Path, q.QueryType
		if tty {
			name = strings.Repeat("  ", r.depth) + q.Name
		}
		if q.IsFolder {
			typ = "folder"
			if tty {
				name += "/"
			}
		}
		tp.AddField(name)
		tp.AddField(typ, tableprinter.WithColor(cs.Gray))
		tp.AddField(q.ID, tableprinter.WithColor(cs.Gray))
		tp.EndRow()
	}
	return tp.Render()
}

type row struct {
	item  api.QueryItem
	depth int
}

// walk appends items and their descendants depth-first to rows, fetching by ID the children
// of folders below the requested $depth (hasChildren without children).
func walk(ctx context.Context, c *api.WorkItemClient, items []api.QueryItem, depth int, rows *[]row) error {
	for _, q := range items {
		*rows = append(*rows, row{q, depth})
		children := q.Children
		if q.IsFolder && q.HasChildren && children == nil {
			full, err := c.Query(ctx, q.ID, fetchDepth, false)
			if err != nil {
				return err
			}
			children = full.Children
		}
		if err := walk(ctx, c, children, depth+1, rows); err != nil {
			return err
		}
	}
	return nil
}

type queryJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsFolder  bool   `json:"isFolder"`
	QueryType string `json:"queryType"`
	IsPublic  bool   `json:"isPublic"`
}
