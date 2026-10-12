// Package watch implements `ado pipeline watch`.
package watch

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

type options struct {
	run        string
	interval   int
	exitStatus bool
}

// NewCmdWatch returns the `pipeline watch` command.
func NewCmdWatch(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "watch [<run>]",
		Short: "Watch a run until it completes",
		Long: `Watch a run until it completes, then print its summary.

<run> is a run ID or URL. Without it, you are asked to pick one of the project's
in-progress runs.

On a terminal, the stages and jobs are redrawn every --interval seconds; otherwise a
line is printed per stage or job state change. Pressing Ctrl+C stops watching; the run
goes on.

The exit status is 0 if the run succeeded, 1 if it failed or partially succeeded, and
2 if it was canceled or watching was stopped. --exit-status is accepted for gh
compatibility and changes nothing.`,
		Example: `  ado pipeline watch 812
  ado run watch 812 -i 10 && echo "run succeeded"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.interval < 1 {
				return cmdutil.FlagErrorf("--interval must be at least 1 second")
			}
			if len(args) == 1 {
				o.run = args[0]
			}
			return runWatch(cmd.Context(), f, o)
		},
	}
	cmd.Flags().IntVarP(&o.interval, "interval", "i", 3, "Refresh interval in seconds")
	cmd.Flags().BoolVar(&o.exitStatus, "exit-status", false, "No-op: the exit status always reflects the run's result")
	return cmd
}

func runWatch(ctx context.Context, f *cmdutil.Factory, o *options) error {
	project, err := f.BaseProject()
	if err != nil {
		return err
	}
	var id int
	if o.run != "" {
		var p adocontext.Repo
		if id, p, err = shared.ParseRunRef(o.run); err != nil {
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
		status := build.BuildStatusValues.InProgress
		if id, err = shared.SelectRun(ctx, f, c, build.GetBuildsArgs{StatusFilter: &status}); err != nil {
			return err
		}
	}
	ios := f.IOStreams
	w := shared.BuildWatch(c, id, time.Duration(o.interval)*time.Second, func(b *build.Build, tl shared.Timeline) {
		printSummary(ios, shared.NewRunJSON(project, *b), tl)
	})
	w.Header = fmt.Sprintf("Run %d", id)
	return w.Run(ctx, ios)
}

// printSummary prints the completed run d: its result, stages or jobs, issues and URL.
func printSummary(ios *iostreams.IOStreams, d shared.RunJSON, tl shared.Timeline) {
	cs := ios.ColorScheme()
	w := ios.Out
	var pipeline string
	if d.Pipeline != nil {
		pipeline = d.Pipeline.Name
	}
	mark, color := shared.StatusMark(cs, d.Status, d.Result)
	fmt.Fprintf(w, "%s %s %s\n", color(mark), cs.Bold(pipeline), d.Number+cs.Gray(fmt.Sprintf(" · %s · ID %d", cmp.Or(d.Result, d.Status), d.ID)))
	if len(tl.Nodes) > 0 {
		title := "JOBS"
		if tl.Nodes[0].Type == "Stage" {
			title = "STAGES"
		}
		fmt.Fprintf(w, "\n%s\n", cs.Bold(title))
		shared.PrintTree(w, cs, tl.Nodes, false)
	}
	if anns := shared.Annotations(tl.Nodes); len(anns) > 0 {
		fmt.Fprintf(w, "\n%s\n", cs.Bold("ANNOTATIONS"))
		shared.PrintAnnotations(w, cs, anns)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, cs.Gray("View this run on Azure DevOps: "+d.URL))
}
