package api

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
)

// CurrentIterationPath returns the path of team's current iteration, e.g. Proj\Release 1\Sprint 3.
func (c *WorkItemClient) CurrentIterationPath(ctx context.Context, team string) (string, error) {
	its, err := c.TeamIterations(ctx, team, true)
	if err != nil {
		return "", err
	}
	for _, it := range its {
		if it.Path != "" {
			return it.Path, nil
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
