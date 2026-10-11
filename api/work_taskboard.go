package api

import (
	"cmp"
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/work"
)

// TaskboardColumn is a column of a team's customized sprint taskboard.
type TaskboardColumn struct {
	ID   string
	Name string
}

// TaskboardItem places a work item in a taskboard column.
type TaskboardItem struct {
	ID       int
	ColumnID string
	Column   string
}

// TaskboardColumns returns team's taskboard columns in board order. It is empty when the team
// never customized its taskboard, which then shows the task type's states.
func (c *WorkItemClient) TaskboardColumns(ctx context.Context, team string) ([]TaskboardColumn, error) {
	res, err := c.Work.GetColumns(ctx, work.GetColumnsArgs{Project: &c.Project, Team: &team})
	if err != nil {
		return nil, mapErr(err)
	}
	cols := slices.Clone(deref(res.Columns))
	slices.SortStableFunc(cols, func(a, b work.TaskboardColumn) int { return cmp.Compare(deref(a.Order), deref(b.Order)) })
	out := make([]TaskboardColumn, 0, len(cols))
	for _, col := range cols {
		out = append(out, TaskboardColumn{ID: uuidString(col.Id), Name: deref(col.Name)})
	}
	return out, nil
}

// TaskboardItems returns the column of each work item on team's taskboard of the iteration with
// ID iterationID. The server rejects it (400) when the taskboard is not customized.
func (c *WorkItemClient) TaskboardItems(ctx context.Context, team, iterationID string) ([]TaskboardItem, error) {
	id, err := uuid.Parse(iterationID)
	if err != nil {
		return nil, err
	}
	res, err := c.Work.GetWorkItemColumns(ctx, work.GetWorkItemColumnsArgs{Project: &c.Project, Team: &team, IterationId: &id})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]TaskboardItem, 0, len(deref(res)))
	for _, it := range deref(res) {
		out = append(out, TaskboardItem{ID: deref(it.WorkItemId), ColumnID: uuidString(it.ColumnId), Column: deref(it.Column)})
	}
	return out, nil
}

// IterationWorkItemIDs returns the IDs of the work items in team's iteration with ID
// iterationID (backlog items and their children), each once, in backlog order.
func (c *WorkItemClient) IterationWorkItemIDs(ctx context.Context, team, iterationID string) ([]int, error) {
	id, err := uuid.Parse(iterationID)
	if err != nil {
		return nil, err
	}
	res, err := c.Work.GetIterationWorkItems(ctx, work.GetIterationWorkItemsArgs{Project: &c.Project, Team: &team, IterationId: &id})
	if err != nil {
		return nil, mapErr(err)
	}
	var out []int
	for _, l := range deref(res.WorkItemRelations) {
		if l.Target != nil && l.Target.Id != nil && !slices.Contains(out, *l.Target.Id) {
			out = append(out, *l.Target.Id)
		}
	}
	return out, nil
}

func uuidString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
