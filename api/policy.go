package api

import (
	"context"
	"fmt"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/policy"
)

// PolicyEvaluations lists the branch policy evaluations of pull request id in the project
// with ID projectID (the artifact ID needs the project GUID, not its name).
func (c *PRClient) PolicyEvaluations(ctx context.Context, projectID string, id int) ([]policy.PolicyEvaluationRecord, error) {
	artifact := fmt.Sprintf("vstfs:///CodeReview/CodeReviewId/%s/%d", projectID, id)
	out, err := c.Policy.GetPolicyEvaluations(ctx, policy.GetPolicyEvaluationsArgs{Project: &projectID, ArtifactId: &artifact})
	if err != nil {
		return nil, mapErr(err)
	}
	return *out, nil
}
