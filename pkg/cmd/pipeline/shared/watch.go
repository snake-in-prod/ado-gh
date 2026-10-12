package shared

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/snake-in-prod/ado-gh/api"
	prshared "github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// spinFrames are the marks of in-progress nodes, one frame per redraw.
var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Watch polls a run's tree until it completes. It holds no SDK types, so `pipeline watch`
// (see BuildWatch) and `release watch` share it.
type Watch struct {
	Interval time.Duration
	// Header is printed above the tree on each TTY redraw.
	Header string
	// Poll reads the run's tree; it's the only request made per tick.
	Poll func(context.Context) (Timeline, error)
	// Fetch reads the run once Poll's tree has completed (or is empty) and reports whether
	// the run itself has completed; until it has, polling goes on.
	Fetch func(context.Context) (bool, error)
	// Done, when set, tells from Poll's tree alone whether the run has completed, in place of
	// the every-root-completed check and Fetch (`release watch` stops on environments never
	// deployed).
	Done func(Timeline) bool
	// Summary prints the completed run and returns the exit error (see ResultExitError).
	Summary func(Timeline) error
}

// Run polls every Interval until the run completes, then prints its summary and returns its
// exit error. On a TTY it redraws the tree each tick, with a spinner on running nodes and a line
// per pending check; otherwise it prints a line per stage/job state change and per new pending
// check. A run already completed gets its summary at once. Ctrl-C or a canceled ctx stops
// watching (the run goes on) with CancelError.
//
// Run calls signal.NotifyContext: tests running it in a synctest bubble must first call
// signal.Notify once outside any bubble (see watch_test.go), or the bubble deadlocks.
func (w *Watch) Run(ctx context.Context, ios *iostreams.IOStreams) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	tty := ios.IsStdoutTTY()
	seen := map[string]string{}
	stopped := func(err error) error {
		if ctx.Err() == nil {
			return err
		}
		fmt.Fprintln(ios.ErrOut, "Stopped watching; nothing was canceled.")
		return cmdutil.CancelError
	}
	for frame := 0; ; frame++ {
		tl, err := w.Poll(ctx)
		if err != nil {
			return stopped(err)
		}
		done := false
		// ponytail: only the timeline says when to read the run, so a completed run whose
		// records stay open is watched until Ctrl-C; read the run every N ticks if that shows up.
		if w.Done != nil {
			done = w.Done(tl)
		} else if treeDone(tl.Nodes) {
			if done, err = w.Fetch(ctx); err != nil {
				return stopped(err)
			}
		}
		switch {
		case done && frame == 0:
		case tty && done:
			ios.RefreshScreen()
		case tty:
			ios.RefreshScreen()
			cs := ios.ColorScheme()
			fmt.Fprintf(ios.Out, "%s\n\n", cs.Bold(fmt.Sprintf("Refreshing status every %d seconds. Press Ctrl+C to quit.", int(w.Interval/time.Second))))
			if w.Header != "" {
				fmt.Fprintf(ios.Out, "%s\n\n", w.Header)
			}
			printTree(ios.Out, cs, tl.Nodes, false, "", cs.Yellow(spinFrames[frame%len(spinFrames)]))
			if len(tl.Pending) > 0 {
				fmt.Fprintln(ios.Out)
				PrintWaiting(ios.Out, cs, tl.Pending)
			}
		default:
			printTransitions(ios.Out, ios.ColorScheme(), tl, seen)
			if done {
				fmt.Fprintln(ios.Out)
			}
		}
		if done {
			return w.Summary(tl)
		}
		select {
		case <-ctx.Done():
			return stopped(ctx.Err())
		case <-ticker.C:
		}
	}
}

// treeDone reports whether every root node has completed, or there are none yet (a run that
// never started has no timeline).
func treeDone(nodes []*Node) bool {
	for _, n := range nodes {
		if n.State != string(build.TimelineRecordStateValues.Completed) {
			return false
		}
	}
	return true
}

// printTransitions prints the stages, jobs and pending checks of tl whose state differs from
// the one in seen, keyed by their path, and records the new ones.
func printTransitions(w io.Writer, cs *iostreams.ColorScheme, tl Timeline, seen map[string]string) {
	var walk func(nodes []*Node, path string)
	walk = func(nodes []*Node, path string) {
		for _, n := range nodes {
			if n.Type == "Task" {
				continue
			}
			p := n.Name
			if path != "" {
				p = path + " / " + n.Name
			}
			label := cmp.Or(n.Result, n.State)
			if n.Attempt > 1 {
				label += fmt.Sprintf(" (attempt %d)", n.Attempt)
			}
			if seen[p] != label {
				seen[p] = label
				mark, color := StatusMark(cs, n.State, n.Result)
				fmt.Fprintf(w, "%s %s: %s\n", color(mark), p, label)
			}
			walk(n.Children, p)
		}
	}
	walk(tl.Nodes, "")
	for _, p := range tl.Pending {
		key := "pending " + p.ID
		if seen[key] == "" {
			seen[key] = p.State
			PrintWaiting(w, cs, []Pending{p})
		}
	}
}

// BuildWatch returns a Watch over run id: each tick reads only its timeline, and once that has
// completed the build is read, summary prints it with the last timeline, and its result picks
// the exit error.
func BuildWatch(c *api.PipelineClient, id int, interval time.Duration, summary func(*build.Build, Timeline)) *Watch {
	var b *build.Build
	return &Watch{
		Interval: interval,
		Poll: func(ctx context.Context) (Timeline, error) {
			tl, err := c.Timeline(ctx, id)
			if err != nil || tl == nil || tl.Records == nil {
				return Timeline{}, err
			}
			return NewTimeline(*tl.Records), nil
		},
		Fetch: func(ctx context.Context) (bool, error) {
			var err error
			if b, err = c.BuildByID(ctx, id); err != nil {
				return false, err
			}
			return prshared.Deref(b.Status) == build.BuildStatusValues.Completed, nil
		},
		Summary: func(tl Timeline) error {
			summary(b, tl)
			return ResultExitError(string(prshared.Deref(b.Result)))
		},
	}
}
