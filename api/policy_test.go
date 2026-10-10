package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/policy"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const policyProjectID = "6ce954b1-ce1f-45d1-b94d-e6bf2464ba2c"

func TestPolicyEvaluations(t *testing.T) {
	org := "policyevalorg"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/"+policyProjectID+"/_apis/policy/evaluations"),
		httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{{"status": "approved"}}}))

	got, err := c.PolicyEvaluations(context.Background(), policyProjectID, 42)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, policy.PolicyEvaluationStatusValues.Approved, *got[0].Status)
	assert.Equal(t, "vstfs:///CodeReview/CodeReviewId/"+policyProjectID+"/42", reg.Requests[1].URL.Query().Get("artifactId"))
}

func TestPolicyEvaluationsError(t *testing.T) {
	org := "policyevalerrorg"
	c, reg := newTestPRClient(t, org)
	reg.Register(httpmock.MatchAny, httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))

	_, err := c.PolicyEvaluations(context.Background(), policyProjectID, 1)
	assert.EqualError(t, err, "denied (HTTP 403)")
}
