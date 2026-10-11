package api

import (
	"context"
	"slices"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/work"
)

// BacklogLevel is a team backlog level; its Kanban board has the same name.
type BacklogLevel struct {
	// ID is the level's category reference name, e.g. Microsoft.RequirementCategory.
	ID string
	// Name is the plural level name, e.g. Stories.
	Name string
	// Type is "requirement" or "portfolio".
	Type string
	// Hidden is set when the team turned the level off.
	Hidden bool
}

// Backlogs is a team's backlog configuration.
type Backlogs struct {
	Requirement BacklogLevel
	Portfolio   []BacklogLevel
	// OrderField is the reference name of the backlog order field, e.g.
	// Microsoft.VSTS.Common.StackRank; "" when unknown.
	OrderField string
}

// Board is a team's Kanban board for one backlog level.
type Board struct {
	Columns []BoardColumn
	// ColumnField and DoneField are the reference names of the team-specific fields holding a
	// work item's column on this board and whether it is in the Done half of a split column.
	ColumnField string
	DoneField   string
}

// BoardColumn is a Kanban board column.
type BoardColumn struct {
	Name string
	// ColumnType is "incoming", "inProgress" or "outgoing".
	ColumnType string
	// ItemLimit is the WIP limit; 0 means none.
	ItemLimit int
	IsSplit   bool
	// StateMappings maps a work item type to its state in this column.
	StateMappings map[string]string
}

// Backlogs returns team's backlog levels and order field.
func (c *WorkItemClient) Backlogs(ctx context.Context, team string) (*Backlogs, error) {
	cfg, err := c.Work.GetBacklogConfigurations(ctx, work.GetBacklogConfigurationsArgs{Project: &c.Project, Team: &team})
	if err != nil {
		return nil, mapErr(err)
	}
	hidden := deref(cfg.HiddenBacklogs)
	level := func(l work.BacklogLevelConfiguration) BacklogLevel {
		id := deref(l.Id)
		return BacklogLevel{ID: id, Name: deref(l.Name), Type: string(deref(l.Type)),
			Hidden: deref(l.IsHidden) || slices.Contains(hidden, id)}
	}
	out := &Backlogs{Requirement: level(deref(cfg.RequirementBacklog)),
		OrderField: deref(deref(cfg.BacklogFields).TypeFields)["Order"]}
	for _, l := range deref(cfg.PortfolioBacklogs) {
		out.Portfolio = append(out.Portfolio, level(l))
	}
	return out, nil
}

// Board returns team's board named name (a backlog level name such as Stories).
func (c *WorkItemClient) Board(ctx context.Context, team, name string) (*Board, error) {
	b, err := c.Work.GetBoard(ctx, work.GetBoardArgs{Project: &c.Project, Team: &team, Id: &name})
	if err != nil {
		return nil, mapErr(err)
	}
	fields := deref(b.Fields)
	out := &Board{
		ColumnField: deref(deref(fields.ColumnField).ReferenceName),
		DoneField:   deref(deref(fields.DoneField).ReferenceName),
	}
	for _, col := range deref(b.Columns) {
		out.Columns = append(out.Columns, BoardColumn{
			Name: deref(col.Name), ColumnType: string(deref(col.ColumnType)), ItemLimit: deref(col.ItemLimit),
			IsSplit: deref(col.IsSplit), StateMappings: deref(col.StateMappings),
		})
	}
	return out, nil
}
