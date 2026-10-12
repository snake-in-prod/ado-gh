// Package view implements `ado pipeline view`.
package view

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/text"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

var jsonFields = append(slices.Clone(shared.RunJSONFields), "stages", "pendingApprovals")

type options struct {
	ref, job                                 string
	verbose, log, logFailed, exitStatus, web bool
	exporter                                 *cmdutil.Exporter
}

// NewCmdView returns the `pipeline view` command.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "view [<run>]",
		Short: "View a run",
		Long: `Display a run's summary and its stages and jobs, with the issues they reported
(Annotations) and the approvals and checks they wait for (Waiting). Failed jobs always
expand to their failed tasks; --verbose shows every task.

<run> is a run ID or URL. Without it, you pick one of the recent runs: those of the
current repository's pipeline on the current branch when one resolves, otherwise the
project's.

--log and --log-failed print task logs, each line prefixed with its job and task;
--job narrows them to one job (a name or ID). pendingApprovals in --json lists every
pending check; approvals have type Checkpoint.Approval.`,
		Example: `  ado pipeline view 812
  ado run view 812 -v
  ado pipeline view 812 --log-failed
  ado pipeline view 812 --log --job build
  ado pipeline view 812 --json stages,pendingApprovals`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.job != "" && !o.log && !o.logFailed {
				return cmdutil.FlagErrorf("--job requires --log or --log-failed")
			}
			if len(args) > 0 {
				o.ref = args[0]
			}
			return run(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.BoolVarP(&o.verbose, "verbose", "v", false, "Show every task, not only failed ones")
	fl.BoolVar(&o.log, "log", false, "Print the logs of all tasks")
	fl.BoolVar(&o.logFailed, "log-failed", false, "Print the logs of failed tasks")
	fl.StringVar(&o.job, "job", "", "With --log or --log-failed: only this job's logs (name or ID)")
	fl.BoolVar(&o.exitStatus, "exit-status", false, "Exit with status 1 if the run failed, 2 if it was canceled")
	fl.BoolVarP(&o.web, "web", "w", false, "Open the run in the web browser")
	cmdutil.AddJSONFlags(cmd, &o.exporter, jsonFields)
	cmd.MarkFlagsMutuallyExclusive("web", "json", "log", "log-failed")
	return cmd
}

// viewJSON is the `--json` shape of a viewed run.
type viewJSON struct {
	shared.RunJSON
	Stages           []*shared.Node   `json:"stages"`
	PendingApprovals []shared.Pending `json:"pendingApprovals"`
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	project, err := f.BaseProject()
	if err != nil {
		return err
	}
	var id int
	if o.ref != "" {
		var p adocontext.Repo
		if id, p, err = shared.ParseRunRef(o.ref); err != nil {
			return err
		}
		if p.Project != "" {
			project = p
		}
	}
	c, err := shared.NewClient(ctx, f, project)
	if err != nil {
		return err
	}
	if id == 0 {
		if id, err = shared.SelectRepoRun(ctx, f, c, project); err != nil {
			return err
		}
	}
	if o.web {
		return cmdutil.OpenInBrowser(f, adocontext.RunURL(project, id))
	}

	b, err := c.BuildByID(ctx, id)
	if err != nil {
		return err
	}
	data := viewJSON{RunJSON: shared.NewRunJSON(project, *b), Stages: []*shared.Node{}, PendingApprovals: []shared.Pending{}}
	wants := func(field string) bool { return o.exporter != nil && slices.Contains(o.exporter.Fields, field) }
	if o.exporter == nil || wants("stages") || wants("pendingApprovals") {
		tl, err := c.Timeline(ctx, id)
		if err != nil {
			return err
		}
		var records []build.TimelineRecord
		if tl != nil {
			records = prshared.Deref(tl.Records)
		}
		t := shared.NewTimeline(records)
		data.Stages = append(data.Stages, t.Nodes...)
		data.PendingApprovals = append(data.PendingApprovals, t.Pending...)
	}

	ios := f.IOStreams
	switch {
	case o.exporter != nil:
		err = o.exporter.Write(ios, data)
	case o.log || o.logFailed:
		err = printLogs(ctx, ios.Out, c, id, data.Stages, o)
	default:
		printView(ios, *b, data, o.verbose)
	}
	if err != nil || !o.exitStatus || data.Status != string(build.BuildStatusValues.Completed) {
		return err
	}
	return shared.ResultExitError(data.Result)
}

func printLogs(ctx context.Context, w io.Writer, c *api.PipelineClient, id int, nodes []*shared.Node, o *options) error {
	logs := shared.TaskLogs(nodes, shared.LogFilter{Job: o.job, Failed: o.logFailed})
	if len(logs) == 0 {
		what := "task"
		if o.logFailed {
			what = "failed task"
		}
		if o.job != "" {
			return fmt.Errorf("no %s logs found for job %q in run %d", what, o.job, id)
		}
		return fmt.Errorf("no %s logs found in run %d", what, id)
	}
	return shared.PrintLogs(ctx, w, shared.BuildLogOpener(c, id), logs)
}

func printView(ios *iostreams.IOStreams, b build.Build, d viewJSON, verbose bool) {
	cs := ios.ColorScheme()
	w := ios.Out
	tty := ios.IsStdoutTTY()
	var pipeline string
	if d.Pipeline != nil {
		pipeline = d.Pipeline.Name
	}
	branch := strings.TrimPrefix(d.SourceBranch, "refs/heads/")
	pr := triggeringPR(b)
	var duration string
	if d.StartTime != nil && d.FinishTime != nil {
		duration = d.FinishTime.Sub(*d.StartTime).Round(time.Second).String()
	}

	if tty {
		mark, color := shared.StatusMark(cs, d.Status, d.Result)
		fmt.Fprintf(w, "%s %s %s %s\n", color(mark), cs.Bold(pipeline), d.Number,
			cs.Gray(fmt.Sprintf("· %s · ID %d", cmp.Or(d.Result, d.Status), d.ID)))
		parts := []string{cs.Cyan(branch)}
		if d.SourceVersion != "" {
			parts = append(parts, shortSHA(d.SourceVersion))
		}
		if d.Reason != "" {
			parts = append(parts, "triggered via "+d.Reason)
		}
		if pr != "" {
			parts = append(parts, "PR !"+pr)
		}
		fmt.Fprintln(w, strings.Join(parts, " · "))
		if by := requested(d); by != "" {
			fmt.Fprintln(w, by)
		}
		var times []string
		for _, t := range []struct {
			label string
			at    *time.Time
		}{{"queued", d.QueueTime}, {"started", d.StartTime}, {"finished", d.FinishTime}} {
			if t.at != nil {
				times = append(times, t.label+" "+text.RelativeTimeAgo(time.Now(), *t.at))
			}
		}
		if duration != "" {
			times = append(times, "took "+duration)
		}
		if len(times) > 0 {
			s := strings.Join(times, " · ")
			fmt.Fprintln(w, cs.Gray(strings.ToUpper(s[:1])+s[1:]))
		}
	} else {
		for _, kv := range [][2]string{
			{"pipeline", pipeline}, {"number", d.Number}, {"id", strconv.Itoa(d.ID)},
			{"status", d.Status}, {"result", d.Result}, {"branch", branch}, {"commit", d.SourceVersion},
			{"event", d.Reason}, {"pr", pr}, {"requested by", userName(d.RequestedBy)},
			{"requested for", userName(d.RequestedFor)}, {"queued", rfc3339(d.QueueTime)},
			{"started", rfc3339(d.StartTime)}, {"finished", rfc3339(d.FinishTime)},
			{"duration", duration}, {"url", d.URL},
		} {
			fmt.Fprintf(w, "%s:\t%s\n", kv[0], kv[1])
		}
	}

	if len(d.Stages) > 0 {
		title := "JOBS"
		if d.Stages[0].Type == "Stage" {
			title = "STAGES"
		}
		fmt.Fprintf(w, "\n%s\n", cs.Bold(title))
		shared.PrintTree(w, cs, d.Stages, verbose)
	}
	if anns := shared.Annotations(d.Stages); len(anns) > 0 {
		fmt.Fprintf(w, "\n%s\n", cs.Bold("ANNOTATIONS"))
		shared.PrintAnnotations(w, cs, anns)
	}
	if len(d.PendingApprovals) > 0 {
		fmt.Fprintf(w, "\n%s\n", cs.Bold("WAITING"))
		shared.PrintWaiting(w, cs, d.PendingApprovals)
	}
	if !tty {
		return
	}
	fmt.Fprintln(w)
	if d.Result == string(build.BuildResultValues.Failed) || d.Result == string(build.BuildResultValues.PartiallySucceeded) {
		fmt.Fprintf(w, "To see what failed, try: ado pipeline view %d --log-failed\n", d.ID)
	}
	fmt.Fprintln(w, cs.Gray("View this run on Azure DevOps: "+d.URL))
}

// triggeringPR is the number of the pull request that triggered b, from its trigger info or
// its refs/pull/N/merge source branch; "" when none did.
func triggeringPR(b build.Build) string {
	if n := prshared.Deref(b.TriggerInfo)["pr.number"]; n != "" {
		return n
	}
	if n, ok := strings.CutPrefix(prshared.Deref(b.SourceBranch), "refs/pull/"); ok {
		if n, ok := strings.CutSuffix(n, "/merge"); ok {
			return n
		}
	}
	return ""
}

// requested is "Requested by X" with " for Y" when the run was requested for someone else.
func requested(d viewJSON) string {
	by, forName := userName(d.RequestedBy), userName(d.RequestedFor)
	switch {
	case by == "" && forName == "":
		return ""
	case by == "":
		return "Requested for " + forName
	case forName == "" || forName == by:
		return "Requested by " + by
	}
	return "Requested by " + by + " for " + forName
}

func userName(u *shared.UserJSON) string {
	if u == nil {
		return ""
	}
	return cmp.Or(u.DisplayName, u.UniqueName)
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func rfc3339(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
