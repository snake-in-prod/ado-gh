// Package checks implements `ado pr checks`.
package checks

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

type options struct {
	ref                            string
	required, web, watch, failFast bool
	interval                       int
	exporter                       *cmdutil.Exporter
}

// NewCmdChecks returns the `pr checks` command.
func NewCmdChecks(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "checks [<number> | <url> | <branch>]",
		Short: "Show the checks of a pull request",
		Long: `Show the branch policy evaluations (build validation, reviewers, work item linking,
comment resolution, status policies...) and the latest status per context of a pull request.
Without an argument, the pull request of the current branch is used.

Only build and status policies and pull request statuses decide the exit code; reviewer,
work item, comment and other policies are listed only. An expired build counts as pending:
--watch stops with a warning when only expired checks are left, as they may never requeue.

Exit codes: 0 when the counted checks pass or are skipped (or none are reported), 1 when
any failed, 8 when none failed but some are pending. --json output keeps these exit codes.`,
		Example: `  ado pr checks 123
  ado pr checks --required --watch --fail-fast
  ado pr checks 123 --json name,bucket,link`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case o.failFast && !o.watch:
				return cmdutil.FlagErrorf("cannot use `--fail-fast` flag without `--watch` flag")
			case cmd.Flags().Changed("interval") && !o.watch:
				return cmdutil.FlagErrorf("cannot use `--interval` flag without `--watch` flag")
			case o.interval < 1:
				return cmdutil.FlagErrorf("invalid value for --interval: %d", o.interval)
			case o.watch && o.exporter != nil:
				return cmdutil.FlagErrorf("cannot use `--watch` with `--json` flag")
			}
			if len(args) > 0 {
				o.ref = args[0]
			}
			return run(cmd.Context(), f, o)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&o.required, "required", false, "Only show checks that are required")
	fl.BoolVarP(&o.web, "web", "w", false, "Open the pull request in the web browser")
	fl.BoolVar(&o.watch, "watch", false, "Watch checks until they finish")
	fl.BoolVar(&o.failFast, "fail-fast", false, "Exit watch mode on the first check failure")
	fl.IntVarP(&o.interval, "interval", "i", 10, "Refresh interval in seconds in watch mode")
	cmdutil.AddJSONFlags(cmd, &o.exporter, shared.CheckJSONFields)
	return cmd
}

func run(ctx context.Context, f *cmdutil.Factory, o *options) error {
	pr, c, repo, err := shared.Find(ctx, f, o.ref)
	if err != nil {
		return err
	}
	ios := f.IOStreams
	repoURL := shared.RepoURL(repo)
	prURL := shared.PRURL(repoURL, *pr)

	if o.web {
		if ios.IsStdoutTTY() {
			fmt.Fprintf(ios.ErrOut, "Opening %s in your browser.\n", prURL)
		}
		return f.Browser.Browse(prURL)
	}

	fetch := func() ([]shared.Check, shared.CheckCounts, error) {
		checks, err := shared.Checks(ctx, c, pr, repoURL)
		if err != nil {
			return nil, shared.CheckCounts{}, err
		}
		if o.required {
			checks = slices.DeleteFunc(checks, func(ch shared.Check) bool { return !ch.IsRequired })
		}
		return checks, shared.TallyChecks(checks), nil
	}
	checks, counts, err := fetch()
	if err != nil {
		return err
	}
	if o.exporter != nil {
		if err := o.exporter.Write(ios, checks); err != nil {
			return err
		}
		return exitError(counts)
	}
	if len(checks) == 0 {
		msg := "no checks reported"
		if o.required {
			msg = "no required checks reported"
		}
		fmt.Fprintln(ios.ErrOut, msg)
		return nil
	}

	tty := ios.IsStdoutTTY()
	for {
		expired := counts.Pending > 0 && onlyExpired(checks)
		done := !o.watch || counts.Pending == 0 || o.failFast && counts.Fail > 0 || expired
		// Watching on a TTY redraws every round; otherwise only the final state is printed.
		if o.watch && tty {
			ios.RefreshScreen()
			if !done {
				fmt.Fprintf(ios.Out, "%s\n\n", ios.ColorScheme().Bold(fmt.Sprintf("Refreshing checks status every %d seconds. Press Ctrl+C to quit.", o.interval)))
			}
		}
		if done || tty {
			if err := printChecks(ios, checks, counts); err != nil {
				return err
			}
		}
		if done {
			if o.watch && expired {
				fmt.Fprintf(ios.ErrOut, "%s Only expired checks are pending; requeue them on the pull request: %s\n", ios.ColorScheme().Yellow("!"), prURL)
			}
			return exitError(counts)
		}
		time.Sleep(time.Duration(o.interval) * time.Second)
		if checks, counts, err = fetch(); err != nil {
			return err
		}
	}
}

// exitError maps the counts to gh's exit codes: 1 when any failed, else 8 when any is pending.
func exitError(n shared.CheckCounts) error {
	switch {
	case n.Fail > 0:
		return cmdutil.SilentError
	case n.Pending > 0:
		return cmdutil.PendingError
	}
	return nil
}

// onlyExpired reports whether every pending counted check is an expired build.
func onlyExpired(checks []shared.Check) bool {
	for _, ch := range checks {
		if ch.Gating() && ch.Bucket == "pending" && ch.State != "expired" {
			return false
		}
	}
	return true
}

var bucketOrder = map[string]int{"fail": 0, "pending": 1, "pass": 2, "skipping": 3}

func printChecks(ios *iostreams.IOStreams, checks []shared.Check, n shared.CheckCounts) error {
	cs := ios.ColorScheme()
	tty := ios.IsStdoutTTY()
	if tty {
		title := "All checks were successful"
		switch {
		case n.Fail > 0:
			title = "Some checks were not successful"
		case n.Pending > 0:
			title = "Some checks are still pending"
		}
		fmt.Fprintf(ios.Out, "%s\n%d failing, %d successful, %d skipped, and %d pending checks\n\n",
			cs.Bold(title), n.Fail, n.Pass, n.Skipping, n.Pending)
	}

	sorted := slices.Clone(checks)
	slices.SortStableFunc(sorted, func(a, b shared.Check) int { return cmp.Compare(bucketOrder[a.Bucket], bucketOrder[b.Bucket]) })
	marks := map[string]struct {
		mark  string
		color func(string) string
	}{"pass": {"✓", cs.Green}, "fail": {"X", cs.Red}, "pending": {"*", cs.Yellow}, "skipping": {"-", cs.Gray}}

	tp := tableprinter.New(ios, "", "name", "required", "state", "elapsed", "link")
	for _, ch := range sorted {
		required := "optional"
		if ch.IsRequired {
			required = "required"
		}
		took := elapsed(ch)
		if tty {
			m := marks[ch.Bucket]
			tp.AddField(m.mark, tableprinter.WithColor(m.color))
			tp.AddField(ch.Name)
		} else {
			tp.AddField(ch.Name)
			tp.AddField(ch.Bucket)
			if took == "" {
				took = "0"
			}
		}
		tp.AddField(required)
		tp.AddField(ch.State)
		tp.AddField(took)
		tp.AddField(ch.Link)
		tp.EndRow()
	}
	return tp.Render()
}

// elapsed is the run time of a completed check, "" when unknown.
func elapsed(ch shared.Check) string {
	if ch.StartedAt == nil || ch.CompletedAt == nil || !ch.CompletedAt.After(*ch.StartedAt) {
		return ""
	}
	return ch.CompletedAt.Sub(*ch.StartedAt).Round(time.Second).String()
}
