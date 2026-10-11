package api

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/workitemtracking"
)

// Field is a work item field definition.
type Field struct {
	Name          string
	ReferenceName string
	// Type is the storage type: string, integer, double, boolean, dateTime, plainText, html,
	// treePath, history, ... Picklist and identity fields keep a string/integer/double Type, so
	// check IsIdentity and IsPicklist first.
	Type       string
	IsIdentity bool
	IsPicklist bool
	// IsPicklistSuggested marks a picklist that also accepts values outside its list.
	IsPicklistSuggested bool
	ReadOnly            bool
}

// Fields returns the definitions of every field in the project.
func (c *WorkItemClient) Fields(ctx context.Context) ([]Field, error) {
	fs, err := c.Wit.GetWorkItemFields(ctx, workitemtracking.GetWorkItemFieldsArgs{Project: &c.Project})
	if err != nil {
		return nil, mapErr(err)
	}
	var out []Field
	for _, f := range deref(fs) {
		if deref(f.IsDeleted) {
			continue
		}
		out = append(out, Field{
			Name: deref(f.Name), ReferenceName: deref(f.ReferenceName), Type: string(deref(f.Type)),
			IsIdentity: deref(f.IsIdentity), IsPicklist: deref(f.IsPicklist), IsPicklistSuggested: deref(f.IsPicklistSuggested),
			ReadOnly: deref(f.ReadOnly),
		})
	}
	return out, nil
}

// FieldAllowedValues returns the allowed values of field (a reference name) on work item type
// typ, as strings. It asks for $expand=allowedValues only: `all` would also list every
// identity for identity fields.
func (c *WorkItemClient) FieldAllowedValues(ctx context.Context, typ, field string) ([]string, error) {
	f, err := c.Wit.GetWorkItemTypeFieldWithReferences(ctx, workitemtracking.GetWorkItemTypeFieldWithReferencesArgs{
		Project: &c.Project, Type: &typ, Field: &field, Expand: &workitemtracking.WorkItemTypeFieldsExpandLevelValues.AllowedValues,
	})
	if err != nil {
		return nil, mapErr(err)
	}
	var out []string
	for _, v := range deref(f.AllowedValues) {
		out = append(out, fmt.Sprint(v))
	}
	return out, nil
}

// TypeFieldRefs returns the reference names of the fields of work item type typ.
func (c *WorkItemClient) TypeFieldRefs(ctx context.Context, typ string) ([]string, error) {
	fs, err := c.Wit.GetWorkItemTypeFieldsWithReferences(ctx, workitemtracking.GetWorkItemTypeFieldsWithReferencesArgs{
		Project: &c.Project, Type: &typ,
	})
	if err != nil {
		return nil, mapErr(err)
	}
	var out []string
	for _, f := range deref(fs) {
		out = append(out, deref(f.ReferenceName))
	}
	return out, nil
}
