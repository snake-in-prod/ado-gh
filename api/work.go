package api

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/work"
)

// Iteration is a team iteration (sprint).
type Iteration struct {
	ID   string
	Name string
	// Path is the iteration path, e.g. Proj\Release 1\Sprint 3.
	Path string
	// StartDate and FinishDate are date-only (UTC midnight); zero when the iteration has no dates.
	StartDate  time.Time
	FinishDate time.Time
	// TimeFrame is "past", "current" or "future".
	TimeFrame string
}

// DateRange is an inclusive range of dates (UTC midnight).
type DateRange struct {
	Start time.Time
	End   time.Time
}

// TeamIterations returns the iterations selected by team; current limits them to the current
// one, the only $timeframe the server supports.
func (c *WorkItemClient) TeamIterations(ctx context.Context, team string, current bool) ([]Iteration, error) {
	args := work.GetTeamIterationsArgs{Project: &c.Project, Team: &team}
	if current {
		tf := string(work.TimeFrameValues.Current)
		args.Timeframe = &tf
	}
	its, err := c.Work.GetTeamIterations(ctx, args)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Iteration, 0, len(deref(its)))
	for _, it := range deref(its) {
		a := deref(it.Attributes)
		var id string
		if it.Id != nil {
			id = it.Id.String()
		}
		out = append(out, Iteration{
			ID: id, Name: deref(it.Name), Path: deref(it.Path),
			StartDate: toTime(a.StartDate), FinishDate: toTime(a.FinishDate), TimeFrame: string(deref(a.TimeFrame)),
		})
	}
	return out, nil
}

// WorkingDays returns the weekdays team works on, from its team settings.
func (c *WorkItemClient) WorkingDays(ctx context.Context, team string) ([]time.Weekday, error) {
	s, err := c.Work.GetTeamSettings(ctx, work.GetTeamSettingsArgs{Project: &c.Project, Team: &team})
	if err != nil {
		return nil, mapErr(err)
	}
	var out []time.Weekday
	for _, name := range deref(s.WorkingDays) {
		for d := time.Sunday; d <= time.Saturday; d++ {
			if strings.EqualFold(name, d.String()) {
				out = append(out, d)
			}
		}
	}
	return out, nil
}

// TeamDaysOff returns team's days off in the iteration with ID iterationID.
func (c *WorkItemClient) TeamDaysOff(ctx context.Context, team, iterationID string) ([]DateRange, error) {
	id, err := uuid.Parse(iterationID)
	if err != nil {
		return nil, err
	}
	d, err := c.Work.GetTeamDaysOff(ctx, work.GetTeamDaysOffArgs{Project: &c.Project, Team: &team, IterationId: &id})
	if err != nil {
		return nil, mapErr(err)
	}
	var out []DateRange
	for _, r := range deref(d.DaysOff) {
		out = append(out, DateRange{Start: toTime(r.Start), End: toTime(r.End)})
	}
	return out, nil
}
