// Package list implements `ado sprint list`.
package list

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/workitem/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// jsonFields are the public `--json` fields.
var jsonFields = []string{"id", "name", "path", "startDate", "finishDate", "timeFrame"}

var timeframes = []string{"past", "current", "future", "all"}

type options struct {
	timeframe, team string
	exporter        *cmdutil.Exporter
}

// NewCmdList returns the `sprint list` command.
func NewCmdList(f *cmdutil.Factory) *cobra.Command {
	o := &options{}
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the team's sprints",
		Long: `List the iterations (sprints) selected by the team, with their dates and time frame.

--team picks the team; the default is the "team" config value, else the project's default team.`,
		Example: `  ado sprint list
  ado sprint list --timeframe future --team "Web Team"
  ado sprint list --json name,startDate,finishDate`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !slices.Contains(timeframes, o.timeframe) {
				return cmdutil.FlagErrorf("invalid --timeframe %q: use past, current, future or all", o.timeframe)
			}
			return run(cmd.Context(), f, o)
		},
	}
	cmd.Flags().StringVar(&o.timeframe, "timeframe", "all", "Filter by time frame: {past|current|future|all}")
	cmd.Flags().StringVar(&o.team, "team", "", "Team `name` (default: config team, else the project's default team)")
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
	// The server only filters on `current`; past and future are filtered here.
	its, err := c.TeamIterations(ctx, team, o.timeframe == "current")
	if err != nil {
		return err
	}
	if o.timeframe != "all" {
		its = slices.DeleteFunc(its, func(it api.Iteration) bool { return it.TimeFrame != o.timeframe })
	}

	if o.exporter != nil {
		out := make([]sprintJSON, len(its))
		for i, it := range its {
			out[i] = sprintJSON{ID: it.ID, Name: it.Name, Path: it.Path, StartDate: datePtr(it.StartDate),
				FinishDate: datePtr(it.FinishDate), TimeFrame: it.TimeFrame}
		}
		return o.exporter.Write(ios, out)
	}
	if len(its) == 0 {
		fmt.Fprintf(ios.ErrOut, "no sprints found for team %s\n", team)
		return nil
	}
	tty := ios.IsStdoutTTY()
	cs := ios.ColorScheme()
	tp := tableprinter.New(ios, "name", "path", "start", "finish", "timeframe")
	for _, it := range its {
		tp.AddField(it.Name, tableprinter.WithColor(cs.Bold))
		tp.AddField(it.Path)
		tp.AddField(date(it.StartDate, tty))
		tp.AddField(date(it.FinishDate, tty))
		tp.AddField(it.TimeFrame, tableprinter.WithColor(cs.Gray))
		tp.EndRow()
	}
	return tp.Render()
}

// date formats an iteration date; a missing date is "-" on a terminal, empty otherwise.
func date(t time.Time, tty bool) string {
	switch {
	case !t.IsZero():
		return t.Format(time.DateOnly)
	case tty:
		return "-"
	}
	return ""
}

// datePtr returns nil for a missing date, so JSON has null.
func datePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

type sprintJSON struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Path       string     `json:"path"`
	StartDate  *time.Time `json:"startDate"`
	FinishDate *time.Time `json:"finishDate"`
	TimeFrame  string     `json:"timeFrame"`
}
