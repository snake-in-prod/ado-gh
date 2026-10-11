package shared

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/policy"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	checksProjectID  = "6ce954b1-ce1f-45d1-b94d-e6bf2464ba2c"
	checksRepoURL    = "https://dev.azure.com/org/My%20Proj/_git/web"
	checksPRURL      = checksRepoURL + "/pullrequest/7"
	checksBuildType  = "0609b952-1397-4640-95ec-e00a01b2c241"
	checksStatusType = "cbdc66da-9728-4af8-aada-9a5a32e4a226"
	checksReviewType = "fa4e907d-c16b-4a4c-9dfa-4906e5d171dd"
)

// decodeChecksFixture unmarshals a JSON fixture into v, as the SDK does.
func decodeChecksFixture[T any](t *testing.T, fixture any) T {
	t.Helper()
	b, err := json.Marshal(fixture)
	require.NoError(t, err)
	var v T
	require.NoError(t, json.Unmarshal(b, &v))
	return v
}

func checksEval(typeID, typeName, status string, blocking bool, settings, ctx map[string]any) map[string]any {
	e := map[string]any{"status": status, "configuration": map[string]any{
		"isBlocking": blocking, "type": map[string]any{"id": typeID, "displayName": typeName}, "settings": settings,
	}}
	if ctx != nil {
		e["context"] = ctx
	}
	return e
}

func checksStatus(id int, genre, name, state, updated string) map[string]any {
	return map[string]any{
		"id": id, "state": state, "context": map[string]any{"genre": genre, "name": name},
		"description": name + " " + state, "targetUrl": "https://ci/" + name,
		"creationDate": "2026-10-01T10:00:00Z", "updatedDate": updated,
	}
}

func TestCheckBuckets(t *testing.T) {
	tests := []struct{ state, bucket string }{
		{"approved", "pass"}, {"succeeded", "pass"},
		{"rejected", "fail"}, {"broken", "fail"}, {"failed", "fail"}, {"error", "fail"},
		{"queued", "pending"}, {"running", "pending"}, {"notSet", "pending"}, {"pending", "pending"}, {"expired", "pending"},
		{"notApplicable", "skipping"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.bucket, checkBucket(tt.state), tt.state)
	}
}

func TestAggregateChecks(t *testing.T) {
	evals := decodeChecksFixture[[]policy.PolicyEvaluationRecord](t, []map[string]any{
		// Build: named by settings.displayName, linked to its build, times kept.
		func() map[string]any {
			e := checksEval(checksBuildType, "Build", "running", true, map[string]any{"displayName": "CI pipeline"}, map[string]any{"buildId": 99})
			e["startedDate"] = "2026-10-01T10:00:00Z"
			return e
		}(),
		// Build without a display name or build ID: type name, PR link.
		checksEval(checksBuildType, "Build", "approved", false, map[string]any{}, nil),
		// Expired via context.isExpired.
		checksEval(checksBuildType, "Build", "approved", true, map[string]any{"displayName": "Old"}, map[string]any{"isExpired": true, "buildId": 5}),
		// Expired via a raw "expired" status.
		checksEval(checksBuildType, "Build", "expired", true, map[string]any{"displayName": "Older"}, nil),
		// Status policy with a defaultDisplayName folds the matching status (case-insensitive).
		checksEval(checksStatusType, "Status", "approved", true, map[string]any{"statusGenre": "CI", "statusName": "Lint", "defaultDisplayName": "Linting"}, nil),
		// Status policy without a display name and no matching status.
		checksEval(checksStatusType, "Status", "queued", false, map[string]any{"statusGenre": "sec", "statusName": "scan"}, nil),
		// Reviewer policy: type display name, listed only.
		checksEval(checksReviewType, "Minimum number of reviewers", "rejected", true, nil, nil),
		// The other listed-only policy types.
		checksEval("fd2167ab-b0be-447a-8ec8-39368250530e", "Required reviewers", "queued", true, nil, nil),
		checksEval("40e92b44-2fe1-4dd6-b3d8-74a9c21d0c6e", "Work item linking", "rejected", true, nil, nil),
		checksEval("c6a1889d-b943-4856-b76f-9e46bb6b0df2", "Comment requirements", "approved", true, nil, nil),
		checksEval("fa4e907d-c16b-4a4c-9dfa-4916e5d171ab", "Require a merge strategy", "approved", true, nil, nil),
		// Unknown policy type.
		checksEval("00000000-0000-0000-0000-000000000001", "Custom", "approved", false, nil, nil),
	})
	statuses := decodeChecksFixture[[]git.GitPullRequestStatus](t, []map[string]any{
		// The highest id wins, even listed first.
		checksStatus(4, "ci", "lint", "succeeded", "2026-10-01T10:02:00Z"),
		checksStatus(1, "ci", "lint", "failed", "2026-10-01T10:01:00Z"),
		checksStatus(2, "", "e2e", "pending", "2026-10-01T10:05:00Z"),
		// Same id: the later updatedDate wins, even listed first.
		checksStatus(3, "qa", "smoke", "error", "2026-10-01T10:09:00Z"),
		checksStatus(3, "qa", "smoke", "pending", "2026-10-01T10:01:00Z"),
		checksStatus(6, "qa", "skip", "notApplicable", "2026-10-01T10:03:00Z"),
	})

	got := aggregateChecks(evals, statuses, checksRepoURL, checksPRURL)
	ts := func(s string) *time.Time {
		v, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)
		return &v
	}
	want := []Check{
		{Name: "CI pipeline", State: "running", Bucket: "pending", Type: "build", IsRequired: true,
			Link: "https://dev.azure.com/org/My%20Proj/_build/results?buildId=99", StartedAt: ts("2026-10-01T10:00:00Z")},
		{Name: "Build", State: "approved", Bucket: "pass", Type: "build", Link: checksPRURL},
		{Name: "Old", State: "expired", Bucket: "pending", Type: "build", IsRequired: true,
			Link: "https://dev.azure.com/org/My%20Proj/_build/results?buildId=5"},
		{Name: "Older", State: "expired", Bucket: "pending", Type: "build", IsRequired: true, Link: checksPRURL},
		{Name: "Linting", State: "approved", Bucket: "pass", Type: "statusPolicy", IsRequired: true,
			Description: "lint succeeded", Link: "https://ci/lint"},
		{Name: "sec/scan", State: "queued", Bucket: "pending", Type: "statusPolicy", Link: checksPRURL},
		{Name: "Minimum number of reviewers", State: "rejected", Bucket: "fail", Type: "minimumReviewers", IsRequired: true, Link: checksPRURL},
		{Name: "Required reviewers", State: "queued", Bucket: "pending", Type: "requiredReviewers", IsRequired: true, Link: checksPRURL},
		{Name: "Work item linking", State: "rejected", Bucket: "fail", Type: "workItemLinking", IsRequired: true, Link: checksPRURL},
		{Name: "Comment requirements", State: "approved", Bucket: "pass", Type: "commentResolution", IsRequired: true, Link: checksPRURL},
		{Name: "Require a merge strategy", State: "approved", Bucket: "pass", Type: "mergeStrategy", IsRequired: true, Link: checksPRURL},
		{Name: "Custom", State: "approved", Bucket: "pass", Type: "policy", Link: checksPRURL},
		{Name: "e2e", State: "pending", Bucket: "pending", Type: "status", Description: "e2e pending",
			Link: "https://ci/e2e", StartedAt: ts("2026-10-01T10:00:00Z")},
		{Name: "qa/smoke", State: "error", Bucket: "fail", Type: "status", Description: "smoke error",
			Link: "https://ci/smoke", StartedAt: ts("2026-10-01T10:00:00Z"), CompletedAt: ts("2026-10-01T10:09:00Z")},
		{Name: "qa/skip", State: "notApplicable", Bucket: "skipping", Type: "status", Description: "skip notApplicable",
			Link: "https://ci/skip", StartedAt: ts("2026-10-01T10:00:00Z"), CompletedAt: ts("2026-10-01T10:03:00Z")},
	}
	assert.Equal(t, want, got)
}

func TestAggregateChecksEmpty(t *testing.T) {
	got := aggregateChecks(nil, nil, checksRepoURL, checksPRURL)
	assert.NotNil(t, got)
	assert.Empty(t, got)
	// A status without a target URL links to the PR.
	statuses := decodeChecksFixture[[]git.GitPullRequestStatus](t, []map[string]any{{"id": 1, "state": "succeeded", "context": map[string]any{"name": "x"}}})
	assert.Equal(t, []Check{{Name: "x", State: "succeeded", Bucket: "pass", Type: "status", Link: checksPRURL}},
		aggregateChecks(nil, statuses, checksRepoURL, checksPRURL))
}

func TestCheckGatingAndTally(t *testing.T) {
	checks := []Check{
		{Type: "build", Bucket: "pass"},
		{Type: "statusPolicy", Bucket: "pending"},
		{Type: "status", Bucket: "fail"},
		{Type: "status", Bucket: "skipping"},
		{Type: "minimumReviewers", Bucket: "fail"},
		{Type: "requiredReviewers", Bucket: "pending"},
		{Type: "workItemLinking", Bucket: "fail"},
		{Type: "commentResolution", Bucket: "fail"},
		{Type: "mergeStrategy", Bucket: "fail"},
		{Type: "policy", Bucket: "fail"},
	}
	assert.Equal(t, CheckCounts{Pass: 1, Fail: 1, Pending: 1, Skipping: 1}, TallyChecks(checks))
	assert.True(t, checks[0].Gating())
	assert.False(t, checks[4].Gating())
}

func TestCheckJSON(t *testing.T) {
	b, err := json.Marshal(Check{Name: "n", State: "approved", Bucket: "pass", Type: "build"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"n","state":"approved","bucket":"pass","type":"build","isRequired":false,
		"description":"","link":"","startedAt":null,"completedAt":null}`, string(b))
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	assert.Len(t, CheckJSONFields, len(m))
	for _, f := range CheckJSONFields {
		assert.Contains(t, m, f)
	}
}

// checksEnv returns a PR client on its own org and a PR in project checksProjectID.
func checksEnv(t *testing.T, org string, withProject bool) (*httpmock.Registry, func() ([]Check, error), func() ([]Check, error)) {
	t.Helper()
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.GitPRLocations...)
	t.Cleanup(func() { reg.Verify(t) })
	f := testFactory(t, org, reg, "main", nil)
	c, err := NewClient(context.Background(), f, adocontext.Repo{Host: "dev.azure.com", Org: org, Project: "proj", Name: "web"})
	require.NoError(t, err)
	id := 7
	pr := &git.GitPullRequest{PullRequestId: &id}
	if withProject {
		pid := uuid.MustParse(checksProjectID)
		pr.Repository = &git.GitRepository{Project: &core.TeamProjectReference{Id: &pid}}
	}
	return reg,
		func() ([]Check, error) { return Checks(context.Background(), c, pr, checksRepoURL) },
		func() ([]Check, error) { return PolicyChecks(context.Background(), c, pr, checksRepoURL) }
}

func TestChecksFetch(t *testing.T) {
	org := "checksfetchorg"
	reg, checks, _ := checksEnv(t, org, true)
	reg.Register(httpmock.REST(http.MethodGet, org+"/"+checksProjectID+"/_apis/policy/evaluations"),
		httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{
			checksEval(checksStatusType, "Status", "approved", true, map[string]any{"statusGenre": "ci", "statusName": "lint"}, nil),
		}}))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/web/pullRequests/7/statuses"),
		httpmock.JSONResponse(map[string]any{"count": 2, "value": []map[string]any{
			checksStatus(1, "ci", "lint", "succeeded", "2026-10-01T10:01:00Z"),
			checksStatus(2, "ci", "test", "failed", "2026-10-01T10:01:00Z"),
		}}))

	got, err := checks()
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "ci/lint", got[0].Name)
	assert.Equal(t, "https://ci/lint", got[0].Link)
	assert.Equal(t, "ci/test", got[1].Name)
	q := reg.Requests[1].URL.Query()
	assert.Equal(t, "vstfs:///CodeReview/CodeReviewId/"+checksProjectID+"/7", q.Get("artifactId"))
	assert.False(t, q.Has("includeNotApplicable"))
}

func TestChecksWithoutProjectID(t *testing.T) {
	org := "checksnoprojorg"
	reg, checks, policies := checksEnv(t, org, false)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/web/pullRequests/7/statuses"),
		httpmock.JSONResponse(map[string]any{"count": 0, "value": []any{}}))

	got, err := checks()
	require.NoError(t, err)
	assert.Empty(t, got)
	got, err = policies()
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestPolicyChecks(t *testing.T) {
	org := "checkspolicyorg"
	reg, _, policies := checksEnv(t, org, true)
	reg.Register(httpmock.REST(http.MethodGet, org+"/"+checksProjectID+"/_apis/policy/evaluations"),
		httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{
			checksEval(checksBuildType, "Build", "approved", true, map[string]any{"displayName": "CI"}, nil),
		}}))

	got, err := policies()
	require.NoError(t, err)
	assert.Equal(t, []Check{{Name: "CI", State: "approved", Bucket: "pass", Type: "build", IsRequired: true, Link: checksPRURL}}, got)
}

func TestChecksErrors(t *testing.T) {
	t.Run("evaluations", func(t *testing.T) {
		org := "checkserrevalorg"
		reg, checks, _ := checksEnv(t, org, true)
		reg.Register(httpmock.REST(http.MethodGet, org+"/"+checksProjectID+"/_apis/policy/evaluations"),
			httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
		_, err := checks()
		assert.EqualError(t, err, "boom (HTTP 500)")
	})
	t.Run("statuses", func(t *testing.T) {
		org := "checkserrstatusorg"
		reg, checks, _ := checksEnv(t, org, false)
		reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories/web/pullRequests/7/statuses"),
			httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))
		_, err := checks()
		assert.EqualError(t, err, "denied (HTTP 403)")
	})
}

// TestAggregateLiveFixtures runs responses captured from a real PR (status policy + minimum
// reviewers policy, four posted statuses) through the aggregation. Identities are scrubbed.
func TestAggregateLiveFixtures(t *testing.T) {
	var evals struct {
		Value []policy.PolicyEvaluationRecord `json:"value"`
	}
	var statuses struct {
		Value []git.GitPullRequestStatus `json:"value"`
	}
	for file, v := range map[string]any{"testdata/live_evaluations.json": &evals, "testdata/live_statuses.json": &statuses} {
		b, err := os.ReadFile(file)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, v))
	}

	checks := aggregateChecks(evals.Value, statuses.Value, "https://dev.azure.com/o/p/_git/r", "https://dev.azure.com/o/p/_git/r/pullrequest/1")
	byName := map[string]Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	require.Len(t, checks, 2, "the four ado-smoke/ci statuses fold into the status policy row")
	ci := byName["ado-smoke/ci"]
	assert.Equal(t, "pass", ci.Bucket, "latest status succeeded")
	assert.Equal(t, "statusPolicy", ci.Type)
	assert.True(t, ci.IsRequired)
	assert.Equal(t, "https://example.com/ci", ci.Link)
	rev := byName["Minimum number of reviewers"]
	assert.Equal(t, "pending", rev.Bucket)
	assert.False(t, rev.Gating(), "reviewer policies don't affect the exit code")
	assert.Equal(t, CheckCounts{Pass: 1}, TallyChecks(checks))
}
