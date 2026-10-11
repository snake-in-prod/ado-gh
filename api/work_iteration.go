package api

import (
	"context"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/work"
)

// IterationWorkItems returns the work items of team's iteration iterationID as links, IDs
// only: SourceID 0 for a top-level item, else a Hierarchy-Forward link from its parent.
func (c *WorkItemClient) IterationWorkItems(ctx context.Context, team, iterationID string) ([]WorkItemLink, error) {
	id, err := uuid.Parse(iterationID)
	if err != nil {
		return nil, err
	}
	res, err := c.Work.GetIterationWorkItems(ctx, work.GetIterationWorkItemsArgs{Project: &c.Project, Team: &team, IterationId: &id})
	if err != nil {
		return nil, mapErr(err)
	}
	var out []WorkItemLink
	for _, l := range deref(res.WorkItemRelations) {
		out = append(out, WorkItemLink{Rel: deref(l.Rel), SourceID: deref(deref(l.Source).Id), TargetID: deref(deref(l.Target).Id)})
	}
	return out, nil
}
