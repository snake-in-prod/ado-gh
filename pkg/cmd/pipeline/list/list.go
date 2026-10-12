// Package list implements `ado pipeline list`.
package list

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

const (
	// commitPageSize and maxCommitPages bound the scan for --commit, which the Build API
	// cannot filter on: at most 1000 runs are read.
	commitPageSize = 100
	maxCommitPages = 10
)

var (
	statuses = []string{"notStarted", "inProgress", "cancelling", "completed", "postponed", "all"}
	results  = []string{"succeeded", "partiallySucceeded", "failed", "canceled"}
	events   = []string{"manual", "individualCI", "batchedCI", "pullRequest", "schedule", "resourceTrigger"}

	runFlags = []string{"branch", "status", "result", "user", "commit", "event"}
	defFlags = []string{"folder", "match", "all"}
)

// defJSONFields are the public `--json` fields of a definition (--definitions).
var defJSONFields = []string{"id", "name", "path", "type", "repository", "queueStatus", "latestRun", "url"}

type options struct {
	branch, status, result, user, commit, event string
	folder, match                               string
	limit                                       int
	definitions, all, limitSet                  bool
	exporter                                    *cmdutil.Exporter
}

// NewCmdList returns the `pipeline list` command.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:     "list [<pipeline>]",
		Aliases: []string{"ls"},
		Short:   "List recent runs, or pipelines with --definitions",
		Long: `List the project's recent runs, newest queued first, or those of <pipeline> (an ID, a
name, a folder path such as \Folder\Name, or a URL).

--branch matches the run's source branch only: pull request validation runs build
refs/pull/N/merge, so list them with --event pullRequest. --commit is filtered client-side
over the newest 1000 matching runs; narrow it with <pipeline> or --branch.

With --definitions, list the pipelines themselves: enabled ones unless --all.`,
		Example: `  ado pipeline list -b main --result failed
  ado run list ci -u @me -L 50
  ado pipeline list -d --folder Infra --match 'deploy*'
  ado pipeline list --json id,result --jq '.[].id'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validate(cmd, o, args); err != nil {
				return err
			}
			if o.definitions {
				return runDefinitions(cmd.Context(), f, o)
			}
			var ref string
			if len(args) == 1 {
				ref = args[0]
			}
			return runRuns(cmd.Context(), f, o, ref)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&o.branch, "branch", "b", "", "Filter runs by source branch")
	fl.StringVarP(&o.status, "status", "s", "", "Filter runs by status: {"+strings.Join(statuses, "|")+"}")
	fl.StringVar(&o.result, "result", "", "Filter runs by result: {"+strings.Join(results, "|")+"}")
	fl.StringVarP(&o.user, "user", "u", "", `Filter runs by the user they were requested for (email, name, GUID or "@me")`)
	fl.StringVarP(&o.commit, "commit", "c", "", "Filter runs by commit SHA (or a prefix of it)")
	fl.StringVarP(&o.event, "event", "e", "", "Filter runs by trigger: {"+strings.Join(events, "|")+"}")
	fl.IntVarP(&o.limit, "limit", "L", 20, "Maximum number of runs to list")
	fl.BoolVarP(&o.definitions, "definitions", "d", false, "List pipelines instead of runs")
	fl.StringVar(&o.folder, "folder", "", "With --definitions: only pipelines in this folder or below it")
	fl.StringVar(&o.match, "match", "", "With --definitions: only pipelines whose name matches this glob")
	fl.BoolVarP(&o.all, "all", "a", false, "With --definitions: include disabled and paused pipelines")
	cmdutil.AddJSONFlags(cmd, &o.exporter, slices.Concat(shared.RunJSONFields, []string{"name", "path", "type", "repository", "queueStatus", "latestRun"}))
	return cmd
}

func validate(cmd *cobra.Command, o *options, args []string) error {
	changed := func(names []string) string {
		for _, n := range names {
			if cmd.Flags().Changed(n) {
				return n
			}
		}
		return ""
	}
	if o.limit < 1 {
		return cmdutil.FlagErrorf("invalid value for --limit: %d", o.limit)
	}
	o.limitSet = cmd.Flags().Changed("limit")
	if o.definitions {
		if n := changed(runFlags); n != "" {
			return cmdutil.FlagErrorf("--%s cannot be used with --definitions", n)
		}
		if len(args) > 0 {
			return cmdutil.FlagErrorf("a <pipeline> argument cannot be used with --definitions; use --match or --folder")
		}
		if o.match != "" {
			if _, err := path.Match(o.match, ""); err != nil {
				return cmdutil.FlagErrorf("invalid --match pattern %q", o.match)
			}
		}
		return checkJSONFields(o.exporter, defJSONFields)
	}
	if n := changed(defFlags); n != "" {
		return cmdutil.FlagErrorf("--%s requires --definitions", n)
	}
	for _, v := range []struct {
		flag, val string
		allowed   []string
	}{{"status", o.status, statuses}, {"result", o.result, results}, {"event", o.event, events}} {
		if v.val != "" && !slices.Contains(v.allowed, v.val) {
			return cmdutil.FlagErrorf("invalid value for --%s: %q (use %s)", v.flag, v.val, strings.Join(v.allowed, ", "))
		}
	}
	return checkJSONFields(o.exporter, shared.RunJSONFields)
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

func runRuns(ctx context.Context, f *cmdutil.Factory, o *options, ref string) error {
	project, err := f.BaseProject()
	if err != nil {
		return err
	}
	var pref shared.PipelineRef
	if ref != "" {
		if pref, err = shared.ParsePipelineRef(ref); err != nil {
			return err
		}
		if pref.Project.Project != "" {
			project = pref.Project
		}
	}
	c, err := shared.NewClient(ctx, f, project)
	if err != nil {
		return err
	}

	var args build.GetBuildsArgs
	if ref != "" {
		id, err := shared.ResolvePipeline(ctx, c, pref)
		if err != nil {
			return err
		}
		args.Definitions = &[]int{id}
	}
	if o.branch != "" {
		b := o.branch
		if !strings.HasPrefix(b, "refs/") {
			b = "refs/heads/" + b
		}
		args.BranchName = &b
	}
	if o.status != "" {
		s := build.BuildStatus(o.status)
		args.StatusFilter = &s
	}
	if o.result != "" {
		r := build.BuildResult(o.result)
		args.ResultFilter = &r
	}
	if o.event != "" {
		e := build.BuildReason(o.event)
		args.ReasonFilter = &e
	}
	if o.user != "" {
		resolver := &api.IdentityResolver{HTTP: c.HTTP, OrgURL: adocontext.OrgURL(project.Host, project.Org), Prompter: f.Prompter}
		ids, err := resolver.Resolve(ctx, []string{o.user})
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			u := ids[0].String()
			args.RequestedFor = &u
		}
	}

	runs, err := fetchRuns(ctx, f, c, args, o)
	if err != nil {
		return err
	}
	ios := f.IOStreams
	if o.exporter != nil {
		out := make([]shared.RunJSON, len(runs))
		for i, r := range runs {
			out[i] = shared.NewRunJSON(project, r)
		}
		return o.exporter.Write(ios, out)
	}
	if len(runs) == 0 {
		fmt.Fprintln(ios.ErrOut, "no runs match your search")
		return nil
	}
	return printRuns(f, runs)
}

// fetchRuns pages through the runs args matches until --limit are found. --commit is matched
// client-side, so it reads pages of commitPageSize, at most maxCommitPages of them, and warns
// when it stops before the runs or the limit ran out.
func fetchRuns(ctx context.Context, f *cmdutil.Factory, c *api.PipelineClient, args build.GetBuildsArgs, o *options) ([]build.Build, error) {
	commit := strings.ToLower(o.commit)
	var out []build.Build
	for page := 1; ; page++ {
		top := o.limit - len(out)
		if commit != "" {
			top = commitPageSize
		}
		args.Top = &top
		runs, token, err := c.Builds(ctx, args)
		if err != nil {
			return nil, err
		}
		for _, r := range runs {
			if commit != "" && !strings.HasPrefix(strings.ToLower(prshared.Deref(r.SourceVersion)), commit) {
				continue
			}
			if out = append(out, r); len(out) == o.limit {
				return out, nil
			}
		}
		if token == "" {
			return out, nil
		}
		if commit != "" && page == maxCommitPages {
			fmt.Fprintf(f.IOStreams.ErrOut, "! stopped after the newest %d runs; results may be incomplete (narrow the search with a pipeline or --branch)\n",
				maxCommitPages*commitPageSize)
			return out, nil
		}
		args.ContinuationToken = &token
	}
}

func printRuns(f *cmdutil.Factory, runs []build.Build) error {
	ios := f.IOStreams
	tty := ios.IsStdoutTTY()
	cs := ios.ColorScheme()
	tp := tableprinter.New(ios, "status", "number", "pipeline", "branch", "event", "id", "elapsed", "age")
	now := time.Now()
	for _, r := range runs {
		status, result := string(prshared.Deref(r.Status)), string(prshared.Deref(r.Result))
		if tty {
			mark, color := shared.StatusMark(cs, status, result)
			tp.AddField(mark, tableprinter.WithColor(color))
		} else {
			tp.AddField(status)
			tp.AddField(result)
		}
		tp.AddField(tableprinter.OneLine(prshared.Deref(r.BuildNumber)))
		var pipeline string
		if r.Definition != nil {
			pipeline = prshared.Deref(r.Definition.Name)
		}
		tp.AddField(tableprinter.OneLine(pipeline))
		tp.AddField(strings.TrimPrefix(prshared.Deref(r.SourceBranch), "refs/heads/"), tableprinter.WithColor(cs.Cyan))
		tp.AddField(string(prshared.Deref(r.Reason)))
		tp.AddField(strconv.Itoa(prshared.Deref(r.Id)), tableprinter.WithColor(cs.Cyan))
		tp.AddField(elapsed(now, r))
		var queued time.Time
		if r.QueueTime != nil {
			queued = r.QueueTime.Time
		}
		tp.AddTimeField(now, queued, cs.Gray)
		tp.EndRow()
	}
	return tp.Render()
}

// elapsed is how long r ran (so far, while running), empty before it started.
func elapsed(now time.Time, r build.Build) string {
	start := api.TimeOf(r.StartTime)
	if start == nil || start.IsZero() {
		return ""
	}
	end := now
	if fin := api.TimeOf(r.FinishTime); fin != nil && !fin.IsZero() {
		end = *fin
	}
	return end.Sub(*start).Round(time.Second).String()
}

type repoJSON struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	DefaultBranch string `json:"defaultBranch"`
}

type defJSON struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Type        string    `json:"type"`
	Repository  *repoJSON `json:"repository"`
	QueueStatus string    `json:"queueStatus"`
	// LatestRun is the latest completed run, as the table's last result column shows.
	LatestRun *shared.RunJSON `json:"latestRun"`
	URL       string          `json:"url"`
}

func runDefinitions(ctx context.Context, f *cmdutil.Factory, o *options) error {
	project, err := f.BaseProject()
	if err != nil {
		return err
	}
	c, err := shared.NewClient(ctx, f, project)
	if err != nil {
		return err
	}
	defs, err := c.DefinitionsFull(ctx)
	if err != nil {
		return err
	}
	kept := defs[:0]
	for _, d := range defs {
		ok, err := keepDefinition(o, d)
		if err != nil {
			return err
		}
		if ok {
			kept = append(kept, d)
		}
	}
	defs = kept
	slices.SortStableFunc(defs, func(a, b build.BuildDefinition) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(prshared.Deref(a.Path)), strings.ToLower(prshared.Deref(b.Path))),
			cmp.Compare(strings.ToLower(prshared.Deref(a.Name)), strings.ToLower(prshared.Deref(b.Name))))
	})
	// -L defaults to a run count; for pipelines it applies only when given.
	if o.limitSet && len(defs) > o.limit {
		defs = defs[:o.limit]
	}

	ios := f.IOStreams
	if o.exporter != nil {
		out := make([]defJSON, len(defs))
		for i, d := range defs {
			out[i] = toDefJSON(project, d)
		}
		return o.exporter.Write(ios, out)
	}
	if len(defs) == 0 {
		fmt.Fprintln(ios.ErrOut, "no pipelines match your search")
		return nil
	}
	tty := ios.IsStdoutTTY()
	cs := ios.ColorScheme()
	tp := tableprinter.New(ios, "id", "name", "folder", "type", "repository", "status", "last run")
	for _, d := range defs {
		tp.AddField(strconv.Itoa(prshared.Deref(d.Id)), tableprinter.WithColor(cs.Cyan))
		tp.AddField(tableprinter.OneLine(prshared.Deref(d.Name)))
		tp.AddField(tableprinter.OneLine(prshared.Deref(d.Path)), tableprinter.WithColor(cs.Gray))
		tp.AddField(shared.DefinitionType(d.Process))
		var repo string
		if d.Repository != nil {
			repo = prshared.Deref(d.Repository.Name)
		}
		tp.AddField(tableprinter.OneLine(repo))
		tp.AddField(queueStatus(d))
		var last string
		if b := d.LatestCompletedBuild; b != nil {
			status, result := string(prshared.Deref(b.Status)), string(prshared.Deref(b.Result))
			last = result
			if tty {
				mark, color := shared.StatusMark(cs, status, result)
				last = color(mark + " " + result)
			}
		}
		tp.AddField(last)
		tp.EndRow()
	}
	return tp.Render()
}

// keepDefinition reports whether d passes the --all, --folder and --match filters. A malformed
// --match pattern is an error, never a silent mismatch.
func keepDefinition(o *options, d build.BuildDefinition) (bool, error) {
	if !o.all && queueStatus(d) != string(build.DefinitionQueueStatusValues.Enabled) {
		return false, nil
	}
	if o.folder != "" {
		folder := strings.ToLower(shared.NormalizeFolder(o.folder))
		p := strings.ToLower(shared.NormalizeFolder(prshared.Deref(d.Path)))
		if p != folder && !strings.HasPrefix(p, strings.TrimSuffix(folder, `\`)+`\`) {
			return false, nil
		}
	}
	if o.match == "" {
		return true, nil
	}
	ok, err := path.Match(strings.ToLower(o.match), strings.ToLower(prshared.Deref(d.Name)))
	if err != nil {
		return false, cmdutil.FlagErrorf("invalid --match pattern %q", o.match)
	}
	return ok, nil
}

func toDefJSON(project adocontext.Repo, d build.BuildDefinition) defJSON {
	id := prshared.Deref(d.Id)
	out := defJSON{
		ID: id, Name: prshared.Deref(d.Name), Path: prshared.Deref(d.Path), Type: shared.DefinitionType(d.Process),
		QueueStatus: queueStatus(d), URL: adocontext.PipelineURL(project, id),
	}
	if r := d.Repository; r != nil {
		out.Repository = &repoJSON{ID: prshared.Deref(r.Id), Name: prshared.Deref(r.Name), Type: prshared.Deref(r.Type),
			DefaultBranch: prshared.Deref(r.DefaultBranch)}
	}
	if b := d.LatestCompletedBuild; b != nil {
		run := shared.NewRunJSON(project, *b)
		out.LatestRun = &run
	}
	return out
}

// queueStatus is d's queue status, enabled when the definition names none.
func queueStatus(d build.BuildDefinition) string {
	return string(cmp.Or(prshared.Deref(d.QueueStatus), build.DefinitionQueueStatusValues.Enabled))
}
