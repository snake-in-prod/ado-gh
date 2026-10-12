// Package logs implements `ado pipeline logs`.
package logs

import (
	"context"
	"fmt"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pipeline/shared"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// followInterval is how often --follow polls the run.
const followInterval = 3 * time.Second

type options struct {
	run    string
	filter shared.LogFilter
	output string
	follow bool
}

// NewCmdLogs returns the `pipeline logs` command.
func NewCmdLogs(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "logs [<run>]",
		Short: "Print or save the task logs of a run",
		Long: `Print the logs of a run's completed tasks, each line prefixed with its job and task
names, or write them to <dir>/<stage>/<job>/<NN>_<task>.log with --output.

<run> is a run ID or URL. Without it, you are asked to pick one of the recent runs of
the current repository's pipeline on the current branch (or of the project).

With --follow, an in-progress run is polled every 3 seconds and each task's log is
printed once the task completes, until the run completes. Azure DevOps serves the logs
of completed tasks only, so logs are not streamed line by line.`,
		Example: `  ado pipeline logs 812
  ado pipeline logs 812 --failed
  ado run logs 812 --job Build --task "go test"
  ado pipeline logs 812 -o out/
  ado pipeline logs 812 --follow`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				o.run = args[0]
			}
			return runLogs(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&o.filter.Job, "job", "", "Only the logs of the job with this name or ID")
	fl.StringVar(&o.filter.Task, "task", "", "Only the logs of tasks with this name or ID")
	fl.BoolVar(&o.filter.Failed, "failed", false, "Only the logs of failed tasks")
	fl.StringVarP(&o.output, "output", "o", "", "Write the logs to files under this directory")
	fl.BoolVarP(&o.follow, "follow", "f", false, "Print task logs as tasks complete until the run completes")
	return cmd
}

func runLogs(ctx context.Context, f *cmdutil.Factory, o *options) error {
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
	if o.run == "" {
		if id, err = shared.SelectRepoRun(ctx, f, c, project); err != nil {
			return err
		}
	}

	ios := f.IOStreams
	open := shared.BuildLogOpener(c, id)
	n := 0
	emit := func(logs []shared.TaskLog) error {
		if len(logs) == 0 {
			return nil
		}
		n += len(logs)
		if o.output != "" {
			return shared.WriteLogs(ctx, o.output, open, logs)
		}
		return shared.PrintLogs(ctx, ios.Out, open, logs)
	}
	if o.follow {
		err = follow(ctx, c, id, o.filter, emit)
	} else {
		var nodes []*shared.Node
		if nodes, err = timelineNodes(ctx, c, id); err == nil {
			err = emit(shared.TaskLogs(nodes, o.filter))
		}
	}
	if err != nil {
		return err
	}
	switch {
	case n == 0:
		fmt.Fprintln(ios.ErrOut, "no task logs found")
	case o.output != "":
		fmt.Fprintf(ios.ErrOut, "%s Wrote %d logs to %s\n", ios.ColorScheme().Green("✓"), n, o.output)
	}
	return nil
}

func timelineNodes(ctx context.Context, c *api.PipelineClient, id int) ([]*shared.Node, error) {
	tl, err := c.Timeline(ctx, id)
	if err != nil || tl == nil || tl.Records == nil {
		return nil, err
	}
	return shared.NewTimeline(*tl.Records).Nodes, nil
}

// follow emits the logs of run id's tasks as they complete, polling every followInterval until
// the run completes. The run's status is read before its timeline, so the timeline read after
// completion is final and its remaining logs are all emitted.
func follow(ctx context.Context, c *api.PipelineClient, id int, filter shared.LogFilter, emit func([]shared.TaskLog) error) error {
	seen := map[string]bool{}
	ticker := time.NewTicker(followInterval)
	defer ticker.Stop()
	for {
		run, err := c.BuildByID(ctx, id)
		if err != nil {
			return err
		}
		done := prshared.Deref(run.Status) == build.BuildStatusValues.Completed
		nodes, err := timelineNodes(ctx, c, id)
		if err != nil {
			return err
		}
		var held map[string]bool
		if !done {
			held = heldTasks(nodes)
		}
		var ready []shared.TaskLog
		for _, l := range shared.TaskLogs(nodes, filter) {
			if !seen[l.Task.ID] && !held[l.Task.ID] {
				seen[l.Task.ID] = true
				ready = append(ready, l)
			}
		}
		if err := emit(ready); err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// heldTasks returns the IDs of the tasks whose logs wait, so each job's logs come out in order:
// those after a task of the same job whose log may still come. The agent attaches a task's log
// just after marking it completed, so a completed task can briefly have none; once its job has
// a log (the job's logger ends after its tasks'), or for a skipped task, none will come.
func heldTasks(nodes []*shared.Node) map[string]bool {
	held := map[string]bool{}
	var walk func([]*shared.Node)
	walk = func(nodes []*shared.Node) {
		for _, n := range nodes {
			if n.Type != "Job" {
				walk(n.Children)
				continue
			}
			waiting := false
			for _, t := range n.Children {
				held[t.ID] = waiting
				waiting = waiting || t.State != "completed" || t.LogID == nil && t.Result != "skipped" && n.LogID == nil
			}
		}
	}
	walk(nodes)
	return held
}
