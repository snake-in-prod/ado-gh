// Package list implements `ado release list`.
package list

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	relshared "github.com/snake-in-prod/ado-gh/pkg/cmd/release/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

var (
	statuses = []string{"active", "abandoned", "draft"}

	releaseFlags = []string{"status", "user", "branch"}

	// releaseJSONFields and defJSONFields are the public `--json` fields of a release and,
	// with --definitions, of a release definition.
	releaseJSONFields = []string{"id", "name", "definition", "status", "createdBy", "createdOn", "environments", "url"}
	defJSONFields     = []string{"id", "name", "path", "environments", "url"}
)

type options struct {
	status, user, branch string
	limit                int
	definitions          bool
	limitSet             bool
	exporter             *cmdutil.Exporter
}

// NewCmdList returns the `release list` command.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:     "list [<definition>]",
		Aliases: []string{"ls"},
		Short:   "List recent releases, or release definitions with --definitions",
		Long: `List the project's Classic Release releases, newest first, or those of <definition>
(an ID or a name).

--branch matches the branch of the release's primary artifact.

With --definitions, list the release definitions with their folders and environments.`,
		Example: `  ado release list -s active -u @me
  ado release list ado-live-release -b main -L 50
  ado release list -d
  ado release list --json id,name,environments --jq '.[].environments'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validate(cmd, o, args); err != nil {
				return err
			}
			project, err := f.BaseProject()
			if err != nil {
				return err
			}
			hc, err := authshared.HTTPClient(cmd.Context(), f, project.Host, project.Org)
			if err != nil {
				return err
			}
			c := api.NewReleaseClient(hc, adocontext.OrgURL(project.Host, project.Org), project.Project)
			if o.definitions {
				return runDefinitions(cmd.Context(), f, c, project, o)
			}
			var ref string
			if len(args) == 1 {
				ref = args[0]
			}
			return runReleases(cmd.Context(), f, c, project, o, ref)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&o.status, "status", "s", "", "Filter releases by status: {"+strings.Join(statuses, "|")+"}")
	fl.StringVarP(&o.user, "user", "u", "", `Filter releases by creator (email, name, GUID or "@me")`)
	fl.StringVarP(&o.branch, "branch", "b", "", "Filter releases by the branch of their primary artifact")
	fl.IntVarP(&o.limit, "limit", "L", 20, "Maximum number of releases to list")
	fl.BoolVarP(&o.definitions, "definitions", "d", false, "List release definitions instead of releases")
	cmdutil.AddJSONFlags(cmd, &o.exporter, slices.Concat(releaseJSONFields, []string{"path"}))
	return cmd
}

func validate(cmd *cobra.Command, o *options, args []string) error {
	if o.limit < 1 {
		return cmdutil.FlagErrorf("invalid value for --limit: %d", o.limit)
	}
	o.limitSet = cmd.Flags().Changed("limit")
	if o.definitions {
		for _, n := range releaseFlags {
			if cmd.Flags().Changed(n) {
				return cmdutil.FlagErrorf("--%s cannot be used with --definitions", n)
			}
		}
		if len(args) > 0 {
			return cmdutil.FlagErrorf("a <definition> argument cannot be used with --definitions")
		}
		return checkJSONFields(o.exporter, defJSONFields)
	}
	if o.status != "" && !slices.Contains(statuses, o.status) {
		return cmdutil.FlagErrorf("invalid value for --status: %q (use %s)", o.status, strings.Join(statuses, ", "))
	}
	return checkJSONFields(o.exporter, releaseJSONFields)
}

// checkJSONFields rejects --json fields of the other mode: AddJSONFlags accepts both sets.
func checkJSONFields(e *cmdutil.Exporter, allowed []string) error {
	if e == nil {
		return nil
	}
	for _, name := range e.Fields {
		if !slices.Contains(allowed, name) {
			return cmdutil.FlagErrorf("unknown JSON field %q; available fields: %s", name, strings.Join(allowed, ", "))
		}
	}
	return nil
}

func runReleases(ctx context.Context, f *cmdutil.Factory, c *api.ReleaseClient, project adocontext.Repo, o *options, ref string) error {
	expand := release.ReleaseExpandsValues.Environments
	args := release.GetReleasesArgs{Expand: &expand}
	if ref != "" {
		id, err := relshared.ResolveDefinition(ctx, c, ref)
		if err != nil {
			return err
		}
		args.DefinitionId = &id
	}
	if o.status != "" {
		s := release.ReleaseStatus(o.status)
		args.StatusFilter = &s
	}
	if o.branch != "" {
		b := o.branch
		if !strings.HasPrefix(b, "refs/") {
			b = "refs/heads/" + b
		}
		args.SourceBranchFilter = &b
	}
	if o.user != "" {
		resolver := &api.IdentityResolver{HTTP: c.HTTP, OrgURL: c.OrgURL, Prompter: f.Prompter}
		ids, err := resolver.Resolve(ctx, []string{o.user})
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			u := ids[0].String()
			args.CreatedBy = &u
		}
	}
	rels, err := c.Releases(ctx, args, o.limit)
	if err != nil {
		return err
	}

	ios := f.IOStreams
	if o.exporter != nil {
		out := make([]releaseJSON, len(rels))
		for i, r := range rels {
			out[i] = toReleaseJSON(project, r)
		}
		return o.exporter.Write(ios, out)
	}
	if len(rels) == 0 {
		fmt.Fprintln(ios.ErrOut, "no releases match your search")
		return nil
	}
	tty := ios.IsStdoutTTY()
	cs := ios.ColorScheme()
	tp := tableprinter.New(ios, "id", "name", "definition", "status", "environments", "created")
	now := time.Now()
	for _, r := range rels {
		tp.AddField(strconv.Itoa(prshared.Deref(r.Id)), tableprinter.WithColor(cs.Cyan))
		tp.AddField(tableprinter.OneLine(prshared.Deref(r.Name)))
		var def string
		if r.ReleaseDefinition != nil {
			def = prshared.Deref(r.ReleaseDefinition.Name)
		}
		tp.AddField(tableprinter.OneLine(def))
		tp.AddField(string(prshared.Deref(r.Status)))
		// On a TTY each environment is a colored status mark and its name; otherwise name:status.
		var envs []string
		for _, e := range prshared.Deref(r.Environments) {
			name, status := tableprinter.OneLine(prshared.Deref(e.Name)), string(prshared.Deref(e.Status))
			if tty {
				mark, color := shared.StatusMark(cs, status, "")
				envs = append(envs, color(mark)+" "+name)
			} else {
				envs = append(envs, name+":"+status)
			}
		}
		sep := ","
		if tty {
			sep = "  "
		}
		tp.AddField(strings.Join(envs, sep))
		var created time.Time
		if t := api.TimeOf(r.CreatedOn); t != nil {
			created = *t
		}
		tp.AddTimeField(now, created, cs.Gray)
		tp.EndRow()
	}
	return tp.Render()
}

// envJSON is a release environment in JSON output.
type envJSON struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// defEnvJSON is a release definition's environment in JSON output.
type defEnvJSON struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type defRefJSON struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type releaseJSON struct {
	ID           int              `json:"id"`
	Name         string           `json:"name"`
	Definition   *defRefJSON      `json:"definition"`
	Status       string           `json:"status"`
	CreatedBy    *shared.UserJSON `json:"createdBy"`
	CreatedOn    *time.Time       `json:"createdOn"`
	Environments []envJSON        `json:"environments"`
	URL          string           `json:"url"`
}

func toReleaseJSON(project adocontext.Repo, r release.Release) releaseJSON {
	id := prshared.Deref(r.Id)
	out := releaseJSON{
		ID: id, Name: prshared.Deref(r.Name), Status: string(prshared.Deref(r.Status)),
		CreatedOn: api.TimeOf(r.CreatedOn), Environments: []envJSON{},
		URL: adocontext.ProjectURL(project) + "/_releaseProgress?_a=release-pipeline-progress&releaseId=" + strconv.Itoa(id),
	}
	if d := r.ReleaseDefinition; d != nil {
		out.Definition = &defRefJSON{ID: prshared.Deref(d.Id), Name: prshared.Deref(d.Name), Path: prshared.Deref(d.Path)}
	}
	if u := r.CreatedBy; u != nil {
		out.CreatedBy = &shared.UserJSON{ID: prshared.Deref(u.Id), DisplayName: prshared.Deref(u.DisplayName), UniqueName: prshared.Deref(u.UniqueName)}
	}
	for _, e := range prshared.Deref(r.Environments) {
		out.Environments = append(out.Environments, envJSON{Name: prshared.Deref(e.Name), Status: string(prshared.Deref(e.Status))})
	}
	return out
}

type defJSON struct {
	ID           int          `json:"id"`
	Name         string       `json:"name"`
	Path         string       `json:"path"`
	Environments []defEnvJSON `json:"environments"`
	URL          string       `json:"url"`
}

func runDefinitions(ctx context.Context, f *cmdutil.Factory, c *api.ReleaseClient, project adocontext.Repo, o *options) error {
	expand := release.ReleaseDefinitionExpandsValues.Environments
	limit := 0
	// -L defaults to a release count; for definitions it applies only when given.
	if o.limitSet {
		limit = o.limit
	}
	defs, err := c.Definitions(ctx, release.GetReleaseDefinitionsArgs{Expand: &expand}, limit)
	if err != nil {
		return err
	}
	ios := f.IOStreams
	if o.exporter != nil {
		out := make([]defJSON, len(defs))
		for i, d := range defs {
			id := prshared.Deref(d.Id)
			out[i] = defJSON{ID: id, Name: prshared.Deref(d.Name), Path: prshared.Deref(d.Path), Environments: []defEnvJSON{},
				URL: adocontext.ProjectURL(project) + "/_release?definitionId=" + strconv.Itoa(id)}
			for _, e := range prshared.Deref(d.Environments) {
				out[i].Environments = append(out[i].Environments, defEnvJSON{ID: prshared.Deref(e.Id), Name: prshared.Deref(e.Name)})
			}
		}
		return o.exporter.Write(ios, out)
	}
	if len(defs) == 0 {
		fmt.Fprintln(ios.ErrOut, "no release definitions found")
		return nil
	}
	cs := ios.ColorScheme()
	tp := tableprinter.New(ios, "id", "name", "folder", "environments")
	for _, d := range defs {
		tp.AddField(strconv.Itoa(prshared.Deref(d.Id)), tableprinter.WithColor(cs.Cyan))
		tp.AddField(tableprinter.OneLine(prshared.Deref(d.Name)))
		tp.AddField(tableprinter.OneLine(prshared.Deref(d.Path)), tableprinter.WithColor(cs.Gray))
		var envs []string
		for _, e := range prshared.Deref(d.Environments) {
			envs = append(envs, tableprinter.OneLine(prshared.Deref(e.Name)))
		}
		tp.AddField(strings.Join(envs, ","))
		tp.EndRow()
	}
	return tp.Render()
}
