package api

import (
	"context"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
)

// ReleaseDefinition fetches release definition id with its artifacts and release-scope
// variables (a single-definition GET returns them without $expand).
func (c *ReleaseClient) ReleaseDefinition(ctx context.Context, id int) (*release.ReleaseDefinition, error) {
	rc, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	d, err := rc.GetReleaseDefinition(ctx, release.GetReleaseDefinitionArgs{Project: &c.Project, DefinitionId: &id})
	return d, mapErr(err)
}

// CreateRelease creates a release from meta and returns it.
func (c *ReleaseClient) CreateRelease(ctx context.Context, meta release.ReleaseStartMetadata) (*release.Release, error) {
	rc, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	r, err := rc.CreateRelease(ctx, release.CreateReleaseArgs{Project: &c.Project, ReleaseStartMetadata: &meta})
	return r, mapErr(err)
}
