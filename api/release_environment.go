package api

import (
	"context"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
)

// UpdateEnvironment sets the status of environment envID of release releaseID: inProgress
// starts a deployment, canceled cancels the one in progress. comment ("" for none) is kept
// with the deployment.
func (c *ReleaseClient) UpdateEnvironment(ctx context.Context, releaseID, envID int, status release.EnvironmentStatus, comment string) (*release.ReleaseEnvironment, error) {
	rc, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	data := release.ReleaseEnvironmentUpdateMetadata{Status: &status}
	if comment != "" {
		data.Comment = &comment
	}
	e, err := rc.UpdateReleaseEnvironment(ctx, release.UpdateReleaseEnvironmentArgs{
		EnvironmentUpdateData: &data, Project: &c.Project, ReleaseId: &releaseID, EnvironmentId: &envID,
	})
	return e, mapErr(err)
}
