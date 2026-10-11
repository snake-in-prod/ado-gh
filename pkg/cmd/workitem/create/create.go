// Package create implements `ado workitem create`.
package create

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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

// maxURLLength is gh's limit on a URL handed to the browser.
const maxURLLength = 8192

// metadata are the extras "Add metadata" offers, in prompt order.
var metadata = []string{"Assignee", "Tags", "Area", "Iteration", "Priority", "Parent"}

type options struct {
	typ, title, assignee, area, iteration, parent, team string
	tags, fields                                        []string
	priority                                            int
	web                                                 bool
	body                                                cmdutil.BodyFlags
}

// NewCmdCreate returns the `workitem create` command.
func NewCmdCreate(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a work item",
		Long: `Create a work item in the project.

--type takes a type name of the project's process (any case) or an alias: bug, story,
task, feature or epic. --field sets any field by friendly or reference name.

On a terminal, a missing type or title is prompted for, the description is written in
your editor, and you can add metadata before submitting. Otherwise --type and --title
are required. The description is sent as Markdown.

With --web, the new work item form opens prefilled with the given values; --parent can't
be prefilled and is ignored.`,
		Example: `  ado workitem create --type bug -t "Crash on save" -a @me --iteration @current
  ado wi create --type story -t "Export to CSV" --tag export,ui --parent 42
  ado wi create --type task -t "Write docs" -f "Remaining Work=4" -w`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("priority") && (o.priority < 1 || o.priority > 4) {
				return cmdutil.FlagErrorf("--priority must be between 1 and 4")
			}
			if o.parent != "" {
				if _, _, err := shared.ParseRef(o.parent); err != nil {
					return cmdutil.FlagErrorf("invalid --parent: %v", err)
				}
			}
			if !f.IOStreams.CanPrompt() {
				var missing []string
				if o.typ == "" {
					missing = append(missing, "--type")
				}
				if o.title == "" && !o.web {
					missing = append(missing, "--title")
				}
				if len(missing) > 0 {
					return cmdutil.FlagErrorf("%s required when not running interactively", strings.Join(missing, " and "))
				}
			}
			return run(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&o.typ, "type", "", "Work item type: a type name or bug, story, task, feature, epic")
	fl.StringVarP(&o.title, "title", "t", "", "Title for the work item")
	cmdutil.AddBodyFlags(cmd, &o.body)
	fl.StringVarP(&o.assignee, "assignee", "a", "", `Assign to a user (email, name or "@me")`)
	fl.StringVar(&o.area, "area", "", "Area path")
	fl.StringVar(&o.iteration, "iteration", "", `Iteration path, or "@current" for the team's current sprint`)
	fl.StringSliceVarP(&o.tags, "tag", "l", nil, "Add tags, repeatable or comma-separated (alias --label)")
	fl.StringVar(&o.parent, "parent", "", "Parent work item ID")
	fl.IntVarP(&o.priority, "priority", "p", 0, "Priority from 1 (highest) to 4")
	fl.StringArrayVarP(&o.fields, "field", "f", nil, "Set a field: Name=Value (repeatable)")
	fl.StringVar(&o.team, "team", "", "Team for --iteration @current (default: config team, else the project's default team)")
	fl.BoolVarP(&o.web, "web", "w", false, "Open the new work item form in the browser, prefilled")
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
	proc := shared.NewProcess(c)
	interactive := ios.CanPrompt() && !o.web && (o.typ == "" || o.title == "")

	if o.typ == "" {
		types, err := proc.Types(ctx)
		if err != nil {
			return err
		}
		i, err := f.Prompter.Select("Type", types)
		if err != nil {
			return err
		}
		o.typ = types[i]
	}
	typ, err := proc.ResolveType(ctx, o.typ)
	if err != nil {
		return err
	}
	if o.title == "" && !o.web {
		if o.title, err = f.Prompter.Input("Title", ""); err != nil {
			return err
		}
		if strings.TrimSpace(o.title) == "" {
			return errors.New("a title is required")
		}
	}
	body, err := readBody(f, o, interactive)
	if err != nil {
		return err
	}
	if interactive {
		if err := whatsNext(f.Prompter, o); err != nil {
			return err
		}
	}

	resolver := &api.IdentityResolver{HTTP: hc, OrgURL: orgURL, Prompter: f.Prompter}
	ops, err := buildOps(ctx, f, c, resolver, project, typ, body, o)
	if err != nil {
		return err
	}
	if o.web {
		if o.parent != "" {
			fmt.Fprintln(ios.ErrOut, "! --parent is ignored with --web: the form can't prefill links")
		}
		u, err := webURL(project, typ, ops)
		if err != nil {
			return err
		}
		return cmdutil.OpenInBrowser(f, u)
	}
	if o.parent != "" {
		id, _, _ := shared.ParseRef(o.parent) // checked in RunE
		ops = append(ops, addOp("/relations/-", map[string]any{
			"rel": "System.LinkTypes.Hierarchy-Reverse", "url": orgURL + "_apis/wit/workItems/" + strconv.Itoa(id),
		}))
	}

	if ios.IsStdoutTTY() {
		fmt.Fprintf(ios.ErrOut, "Creating %s in %s\n\n", typ, project.Project)
	}
	wi, err := shared.SendWithHTMLFallback(ops, func(ops []webapi.JsonPatchOperation) (*api.WorkItem, error) {
		return c.CreateWorkItem(ctx, typ, ops)
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(ios.Out, adocontext.WorkItemURL(project, wi.ID))
	return nil
}

// readBody returns the description from -b, -F or -e; interactively the editor opens and an
// empty text means no description.
func readBody(f *cmdutil.Factory, o *options, interactive bool) (string, error) {
	switch {
	case o.body.Given() || o.body.Editor:
		return o.body.Read(f, "")
	case interactive:
		cfg, err := f.Config()
		if err != nil {
			return "", err
		}
		body, err := cmdutil.Edit(f.IOStreams, cmdutil.ResolveEditor(cfg), "")
		if strings.TrimSpace(body) == "" {
			body = ""
		}
		return body, err
	}
	return "", nil
}

// whatsNext asks to submit, add metadata (then asks again) or cancel.
func whatsNext(p *cmdutil.Prompter, o *options) error {
	for {
		i, err := p.Select("What's next?", []string{"Submit", "Add metadata", "Cancel"})
		switch {
		case err != nil:
			return err
		case i == 0:
			return nil
		case i == 2:
			return cmdutil.CancelError
		}
		if err := promptMetadata(p, o); err != nil {
			return err
		}
	}
}

// promptMetadata asks which metadata to add, then for each one its value, prefilled with the
// current one.
func promptMetadata(p *cmdutil.Prompter, o *options) error {
	picked, err := p.MultiSelect("What would you like to add?", nil, metadata)
	if err != nil {
		return err
	}
	for _, i := range picked {
		switch metadata[i] {
		case "Assignee":
			o.assignee, err = p.Input(`Assignee (email, name or "@me")`, o.assignee)
		case "Tags":
			var tags string
			if tags, err = p.Input("Tags (comma-separated)", strings.Join(o.tags, ",")); err == nil {
				o.tags = strings.Split(tags, ",")
			}
		case "Area":
			o.area, err = p.Input("Area path", o.area)
		case "Iteration":
			o.iteration, err = p.Input(`Iteration path (or "@current")`, o.iteration)
		case "Priority":
			err = promptPriority(p, o)
		case "Parent":
			if o.parent, err = p.Input("Parent work item ID", o.parent); err == nil && o.parent != "" {
				_, _, err = shared.ParseRef(o.parent)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func promptPriority(p *cmdutil.Prompter, o *options) error {
	current := ""
	if o.priority > 0 {
		current = strconv.Itoa(o.priority)
	}
	s, err := p.Input("Priority (1-4)", current)
	if err != nil || strings.TrimSpace(s) == "" {
		return err
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 4 {
		return fmt.Errorf("invalid priority %q: must be between 1 and 4", s)
	}
	o.priority = n
	return nil
}

// buildOps resolves the field values of o into JSON Patch ops for a typ work item. The parent
// link is left out: it can't be prefilled on the web form.
func buildOps(ctx context.Context, f *cmdutil.Factory, c *api.WorkItemClient, resolver *api.IdentityResolver,
	project adocontext.Repo, typ, body string, o *options,
) ([]webapi.JsonPatchOperation, error) {
	var ops []webapi.JsonPatchOperation
	set := func(field string, value any) {
		ops = append(ops, addOp("/fields/"+field, value))
	}
	if o.title != "" {
		set("System.Title", o.title)
	}
	if body != "" {
		ops = append(ops, shared.BodyOps("System.Description", body, nil, false)...)
	}
	identity := func(ctx context.Context, v string) (string, error) {
		u, err := resolver.ResolveUser(ctx, v)
		return u.FieldValue(), err
	}
	if o.assignee != "" {
		v, err := identity(ctx, o.assignee)
		if err != nil {
			return nil, err
		}
		set("System.AssignedTo", v)
	}
	if o.area != "" {
		p, err := shared.ResolvePath(ctx, c, "areas", o.area)
		if err != nil {
			return nil, err
		}
		set("System.AreaPath", p)
	}
	if o.iteration != "" {
		p, err := shared.ResolveIterationPath(ctx, c, o.iteration, func() (string, error) {
			cfg, err := f.Config()
			if err != nil {
				return "", err
			}
			return shared.ResolveTeam(ctx, c, cfg, project, o.team)
		})
		if err != nil {
			return nil, err
		}
		set("System.IterationPath", p)
	}
	var tags []string
	for _, t := range o.tags {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	if len(tags) > 0 {
		set("System.Tags", strings.Join(tags, "; "))
	}
	if o.priority > 0 {
		set("Microsoft.VSTS.Common.Priority", o.priority)
	}
	fields := &shared.FieldResolver{Client: c, Identity: identity}
	for _, arg := range o.fields {
		v, err := fields.Resolve(ctx, typ, arg)
		if err != nil {
			return nil, err
		}
		ops = append(ops, v.Op())
	}
	return ops, nil
}

// webURL is the new work item form for typ, prefilled with the field values of ops as
// [Ref.Name]=value query parameters (brackets literal, values with %20 for spaces).
// Formats, links and cleared fields have no prefill and are left out.
func webURL(project adocontext.Repo, typ string, ops []webapi.JsonPatchOperation) (string, error) {
	var q []string
	for _, op := range ops {
		ref, ok := strings.CutPrefix(*op.Path, "/fields/")
		if ok && op.Value != nil {
			q = append(q, "["+ref+"]="+adocontext.QueryEscape(fmt.Sprint(op.Value)))
		}
	}
	u := adocontext.ProjectURL(project) + "/_workitems/create/" + url.PathEscape(typ)
	if len(q) > 0 {
		u += "?" + strings.Join(q, "&")
	}
	if len(u) >= maxURLLength {
		return "", errors.New("cannot open in browser: maximum URL length exceeded")
	}
	return u, nil
}

func addOp(path string, value any) webapi.JsonPatchOperation {
	return webapi.JsonPatchOperation{Op: &webapi.OperationValues.Add, Path: &path, Value: value}
}
