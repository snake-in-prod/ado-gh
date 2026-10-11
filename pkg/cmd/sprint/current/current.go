// Package current implements `ado sprint current`.
package current

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// now is the clock the working days left are counted from.
var now = time.Now

// jsonFields are the public `--json` fields.
var jsonFields = []string{"id", "name", "path", "startDate", "finishDate", "timeFrame", "workingDaysLeft"}

type options struct {
	team     string
	web      bool
	exporter *cmdutil.Exporter
}

// NewCmdCurrent returns the `sprint current` command.
func NewCmdCurrent(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:   "current",
		Short: "Show the team's current sprint",
		Long: `Show the team's current sprint: its name, path, dates and the working days left.

Working days left count today through the finish date, on the team's working days, minus the
team's days off in the sprint.

--team picks the team; the default is the "team" config value, else the project's default team.`,
		Example: `  ado sprint current
  ado sprint current --team "Web Team" --json name,workingDaysLeft
  ado sprint current --web`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().StringVar(&o.team, "team", "", "Team `name` (default: config team, else the project's default team)")
	cmd.Flags().BoolVarP(&o.web, "web", "w", false, "Open the sprint's taskboard in the browser")
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
	cfg, err := f.Config()
	if err != nil {
		return err
	}
	team, err := shared.ResolveTeam(ctx, c, cfg, project, o.team)
	if err != nil {
		return err
	}
	it, err := shared.FindIteration(ctx, c, team, "")
	if err != nil {
		return err
	}
	if o.web {
		return cmdutil.OpenInBrowser(f, adocontext.TaskboardURL(project, team, it.Path))
	}

	var left *int
	if dated := !it.StartDate.IsZero() && !it.FinishDate.IsZero(); dated {
		weekdays, err := c.WorkingDays(ctx, team)
		if err != nil {
			return err
		}
		off, err := c.TeamDaysOff(ctx, team, it.ID)
		if err != nil {
			return err
		}
		n := workingDaysLeft(now(), it, weekdays, off)
		left = &n
	}

	if o.exporter != nil {
		return o.exporter.Write(ios, sprintJSON{ID: it.ID, Name: it.Name, Path: it.Path, StartDate: datePtr(it.StartDate),
			FinishDate: datePtr(it.FinishDate), TimeFrame: it.TimeFrame, WorkingDaysLeft: left})
	}
	out := ios.Out
	if !ios.IsStdoutTTY() {
		var start, finish, days string
		if left != nil {
			start, finish, days = it.StartDate.Format(time.DateOnly), it.FinishDate.Format(time.DateOnly), strconv.Itoa(*left)
		}
		_, err := fmt.Fprintf(out, "name:\t%s\npath:\t%s\nstart:\t%s\nfinish:\t%s\nworking days left:\t%s\n", it.Name, it.Path, start, finish, days)
		return err
	}
	cs := ios.ColorScheme()
	fmt.Fprintf(out, "%s\n%s\n", cs.Bold(it.Name), cs.Gray(it.Path))
	if left == nil {
		fmt.Fprintln(out, "Dates not set; set them in the team settings to count working days left")
		return nil
	}
	noun := "working days"
	if *left == 1 {
		noun = "working day"
	}
	fmt.Fprintf(out, "%s to %s · %d %s left\n", it.StartDate.Format(time.DateOnly), it.FinishDate.Format(time.DateOnly), *left, noun)
	return nil
}

// workingDaysLeft counts the days from today (now's calendar date) through it's finish date,
// not before its start, that fall on weekdays and outside the days off.
func workingDaysLeft(now time.Time, it api.Iteration, weekdays []time.Weekday, off []api.DateRange) int {
	y, m, d := now.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	if day.Before(it.StartDate) {
		day = it.StartDate
	}
	n := 0
	for ; !day.After(it.FinishDate); day = day.AddDate(0, 0, 1) {
		isOff := slices.ContainsFunc(off, func(r api.DateRange) bool { return !day.Before(r.Start) && !day.After(r.End) })
		if slices.Contains(weekdays, day.Weekday()) && !isOff {
			n++
		}
	}
	return n
}

// datePtr returns nil for a missing date, so JSON has null.
func datePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

type sprintJSON struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Path            string     `json:"path"`
	StartDate       *time.Time `json:"startDate"`
	FinishDate      *time.Time `json:"finishDate"`
	TimeFrame       string     `json:"timeFrame"`
	WorkingDaysLeft *int       `json:"workingDaysLeft"`
}
