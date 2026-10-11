package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/workitemtracking"
)

// workItemTypesLocation is the WIT workItemTypes resource. WorkItemTypes reads it through
// SDK.Send because the SDK model types fields[].allowedValues as strings, which identity
// objects would break.
var workItemTypesLocation = uuid.MustParse("7c8d7a76-4a09-43e8-b5df-bd792f4ac6aa")

// WorkItemType is a work item type of the project's process.
type WorkItemType struct {
	Name          string `json:"name"`
	ReferenceName string `json:"referenceName"`
	IsDisabled    bool   `json:"isDisabled"`
	// States are in workflow order; nil when the types list leaves them out, then use
	// WorkItemTypeStates.
	States []WorkItemState `json:"states"`
}

// WorkItemState is a workflow state and its category: Proposed, InProgress, Resolved,
// Completed or Removed.
type WorkItemState struct {
	Name     string `json:"name"`
	Category string `json:"category"`
}

// WorkItemTypeCategory is a work item type category such as Microsoft.BugCategory.
type WorkItemTypeCategory struct {
	ReferenceName string
	// DefaultType is the name of the category's default work item type.
	DefaultType string
	// Types are the names of the category's work item types.
	Types []string
}

// WorkItemTypes returns the project's work item types, including disabled ones.
func (c *WorkItemClient) WorkItemTypes(ctx context.Context) ([]WorkItemType, error) {
	resp, err := c.SDK.Send(ctx, http.MethodGet, workItemTypesLocation, "7.1-preview.2",
		map[string]string{"project": c.Project}, nil, nil, "", "application/json", nil)
	if err != nil {
		return nil, mapErr(err)
	}
	defer resp.Body.Close()
	var types []WorkItemType
	if err := c.SDK.UnmarshalCollectionBody(resp, &types); err != nil {
		return nil, err
	}
	return types, nil
}

// WorkItemTypeStates returns the states of work item type typ in workflow order.
func (c *WorkItemClient) WorkItemTypeStates(ctx context.Context, typ string) ([]WorkItemState, error) {
	states, err := c.Wit.GetWorkItemTypeStates(ctx, workitemtracking.GetWorkItemTypeStatesArgs{Project: &c.Project, Type: &typ})
	if err != nil {
		return nil, mapErr(err)
	}
	out := []WorkItemState{}
	for _, s := range deref(states) {
		out = append(out, WorkItemState{Name: deref(s.Name), Category: deref(s.Category)})
	}
	return out, nil
}

// WorkItemTypeCategories returns the project's work item type categories.
func (c *WorkItemClient) WorkItemTypeCategories(ctx context.Context) ([]WorkItemTypeCategory, error) {
	cats, err := c.Wit.GetWorkItemTypeCategories(ctx, workitemtracking.GetWorkItemTypeCategoriesArgs{Project: &c.Project})
	if err != nil {
		return nil, mapErr(err)
	}
	var out []WorkItemTypeCategory
	for _, cat := range deref(cats) {
		wc := WorkItemTypeCategory{ReferenceName: deref(cat.ReferenceName), DefaultType: deref(deref(cat.DefaultWorkItemType).Name)}
		for _, t := range deref(cat.WorkItemTypes) {
			wc.Types = append(wc.Types, deref(t.Name))
		}
		out = append(out, wc)
	}
	return out, nil
}
