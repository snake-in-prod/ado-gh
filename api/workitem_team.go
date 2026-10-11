package api

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/work"
)

// CurrentIterationPath returns the path of team's current iteration, e.g. Proj\Release 1\Sprint 3.
func (c *WorkItemClient) CurrentIterationPath(ctx context.Context, team string) (string, error) {
	current := "current"
	its, err := c.Work.GetTeamIterations(ctx, work.GetTeamIterationsArgs{Project: &c.Project, Team: &team, Timeframe: &current})
	if err != nil {
		return "", mapErr(err)
	}
	for _, it := range deref(its) {
		if p := deref(it.Path); p != "" {
			return p, nil
		}
	}
	return "", fmt.Errorf("team %q has no current iteration; check its sprint dates in the team settings", team)
}

// DefaultTeam returns the name of the project's default team.
func (c *WorkItemClient) DefaultTeam(ctx context.Context) (string, error) {
	p, err := c.Core.GetProject(ctx, core.GetProjectArgs{ProjectId: &c.Project})
	if err != nil {
		return "", mapErr(err)
	}
	if name := deref(deref(p.DefaultTeam).Name); name != "" {
		return name, nil
	}
	return "", fmt.Errorf("project %q has no default team; use --team", c.Project)
}
