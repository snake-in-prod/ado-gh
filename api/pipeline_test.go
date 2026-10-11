package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/pipelines"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/pipelinesapproval"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPipelineTestClient returns a PipelineClient for project "proj" of org; every test needs
// its own org because the SDK caches location discovery per base URL.
func newPipelineTestClient(t *testing.T, org string) (*PipelineClient, *httpmock.Registry) {
	t.Helper()
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.PipelineLocations...)
	t.Cleanup(func() { reg.Verify(t) })
	return NewPipelineClient(NewHTTPClient(ClientOptions{Transport: reg}), "https://dev.azure.com/"+org, "proj"), reg
}

// pipeFixture serves the pipeline fixture name with status.
func pipeFixture(t *testing.T, status int, name string) httpmock.Responder {
	return httpmock.StatusJSONResponse(status, json.RawMessage(pipelinefixtures.Read(t, name)))
}

// pipeSaw records each request responder answers, with its body, into *reqs and *bodies.
func pipeSaw(reqs *[]*http.Request, bodies *[]string, responder httpmock.Responder) httpmock.Responder {
	return func(r *http.Request) (*http.Response, error) {
		var b []byte
		if r.Body != nil {
			b, _ = io.ReadAll(r.Body)
		}
		*reqs = append(*reqs, r)
		*bodies = append(*bodies, string(b))
		return responder(r)
	}
}

func TestNewPipelineClientClients(t *testing.T) {
	c := NewPipelineClient(http.DefaultClient, "https://dev.azure.com/o", "proj")
	assert.NotNil(t, c.Build)
	assert.NotNil(t, c.Pipelines)
	assert.NotNil(t, c.Approvals)
	assert.NotNil(t, c.SDK)
	assert.Same(t, http.DefaultClient, c.HTTP)
	assert.Equal(t, "proj", c.Project)
}

func TestPipelineClient_Definitions(t *testing.T) {
	org := "pipedefsorg"
	c, reg := newPipelineTestClient(t, org)
	var reqs []*http.Request
	var bodies []string
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/definitions"), pipeSaw(&reqs, &bodies,
		httpmock.WithHeader(httpmock.StringResponse(`{"count":1,"value":[{"id":1,"name":"ado-gh"}]}`), azuredevops.HeaderKeyContinuationToken, "next")))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/definitions"), pipeSaw(&reqs, &bodies,
		httpmock.StringResponse(`{"count":1,"value":[{"id":2,"name":"nightly"}]}`)))

	repoID, repoType := "00000000-0000-0000-0000-000000000011", "TfsGit"
	defs, err := c.Definitions(context.Background(), build.GetDefinitionsArgs{RepositoryId: &repoID, RepositoryType: &repoType})
	require.NoError(t, err)
	require.Len(t, defs, 2)
	assert.Equal(t, 1, *defs[0].Id)
	assert.Equal(t, 2, *defs[1].Id)
	require.Len(t, reqs, 2)
	assert.Equal(t, repoID, reqs[0].URL.Query().Get("repositoryId"))
	assert.Equal(t, repoType, reqs[0].URL.Query().Get("repositoryType"))
	assert.Empty(t, reqs[0].URL.Query().Get("continuationToken"))
	assert.Equal(t, "next", reqs[1].URL.Query().Get("continuationToken"))
}

func TestPipelineClient_DefinitionsFull(t *testing.T) {
	org := "pipefullorg"
	c, reg := newPipelineTestClient(t, org)
	var reqs []*http.Request
	var bodies []string
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/definitions"), pipeSaw(&reqs, &bodies,
		httpmock.WithHeader(pipeFixture(t, http.StatusOK, "definitions.json"), azuredevops.HeaderKeyContinuationToken, "page2")))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/definitions"), pipeSaw(&reqs, &bodies,
		httpmock.StringResponse(`{"count":1,"value":[{"id":9,"name":"last","path":"\\"}]}`)))

	defs, err := c.DefinitionsFull(context.Background())
	require.NoError(t, err)
	require.Len(t, defs, 5)
	first := defs[0]
	assert.Equal(t, "ado-gh", *first.Name)
	require.NotNil(t, first.Repository)
	assert.Equal(t, "TfsGit", *first.Repository.Type)
	assert.Equal(t, "refs/heads/main", *first.Repository.DefaultBranch)
	process, ok := first.Process.(map[string]any)
	require.True(t, ok, "process = %T", first.Process)
	assert.Equal(t, 2.0, process["type"])
	assert.Equal(t, "azure-pipelines.yml", process["yamlFilename"])
	require.NotNil(t, first.LatestCompletedBuild)
	assert.Equal(t, build.BuildResultValues.Failed, *first.LatestCompletedBuild.Result)
	assert.Equal(t, 9, *defs[4].Id)

	require.Len(t, reqs, 2)
	for _, r := range reqs {
		q := r.URL.Query()
		assert.Equal(t, "true", q.Get("includeAllProperties"))
		assert.Equal(t, "true", q.Get("includeLatestBuilds"))
		assert.Contains(t, r.Header.Get("Accept"), "api-version=7.1-preview.7")
	}
	assert.Equal(t, "page2", reqs[1].URL.Query().Get("continuationToken"))
}

func TestPipelineClient_DefinitionsErrors(t *testing.T) {
	org := "pipedeferrorg"
	c, reg := newPipelineTestClient(t, org)
	denied := httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"})
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/definitions"), denied)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/definitions"), denied)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/definitions"), httpmock.StringResponse(`{"value": 5}`))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/definitions/1"), denied)
	ctx := context.Background()

	var he *HTTPError
	_, err := c.Definitions(ctx, build.GetDefinitionsArgs{})
	require.ErrorAs(t, err, &he)
	assert.Equal(t, http.StatusForbidden, he.StatusCode)
	_, err = c.DefinitionsFull(ctx)
	require.ErrorAs(t, err, &he)
	_, err = c.DefinitionsFull(ctx)
	require.Error(t, err, "undecodable page")
	_, err = c.Definition(ctx, 1)
	require.ErrorAs(t, err, &he)
}

func TestPipelineClient_Definition(t *testing.T) {
	org := "pipedeforg"
	c, reg := newPipelineTestClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/definitions/1"), pipeFixture(t, http.StatusOK, "definition_yaml.json"))
	def, err := c.Definition(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, 3, *def.Revision)
	assert.Equal(t, "refs/heads/main", *def.Repository.DefaultBranch)
}

// definitionWithout decodes a definition body and drops queueStatus.
func definitionWithout(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &m))
	delete(m, "queueStatus")
	return m
}

func TestPipelineClient_SetQueueStatus(t *testing.T) {
	org := "pipequeueorg"
	c, reg := newPipelineTestClient(t, org)
	path := org + "/proj/_apis/build/definitions/1"
	var reqs []*http.Request
	var bodies []string
	reg.Register(httpmock.REST(http.MethodGet, path), pipeFixture(t, http.StatusOK, "definition_yaml.json"))
	reg.Register(httpmock.REST(http.MethodPut, path), pipeSaw(&reqs, &bodies, httpmock.StringResponse(`{"id":1,"revision":4}`)))

	prev, err := c.SetQueueStatus(context.Background(), 1, build.DefinitionQueueStatusValues.Disabled)
	require.NoError(t, err)
	assert.Equal(t, build.DefinitionQueueStatusValues.Enabled, prev)
	require.Len(t, bodies, 1)
	assert.Contains(t, reqs[0].Header.Get("Accept"), "api-version=7.1-preview.7")
	var sent map[string]any
	require.NoError(t, json.Unmarshal([]byte(bodies[0]), &sent))
	assert.Equal(t, "disabled", sent["queueStatus"])
	assert.Equal(t, 3.0, sent["revision"], "revision sent back as read")
	assert.Nil(t, sent["variables"].(map[string]any)["token"].(map[string]any)["value"], "secret stays null")
	assert.Equal(t, definitionWithout(t, string(pipelinefixtures.Read(t, "definition_yaml.json"))), definitionWithout(t, bodies[0]),
		"every other field is sent back unchanged")
}

func TestPipelineClient_SetQueueStatusUnchanged(t *testing.T) {
	org := "pipequeuesameorg"
	c, reg := newPipelineTestClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/definitions/1"), pipeFixture(t, http.StatusOK, "definition_yaml.json"))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/definitions/2"), httpmock.StringResponse(`{"id":2,"revision":1}`))
	ctx := context.Background()

	prev, err := c.SetQueueStatus(ctx, 1, build.DefinitionQueueStatusValues.Enabled)
	require.NoError(t, err)
	assert.Equal(t, build.DefinitionQueueStatusValues.Enabled, prev)
	prev, err = c.SetQueueStatus(ctx, 2, build.DefinitionQueueStatusValues.Enabled)
	require.NoError(t, err)
	assert.Equal(t, build.DefinitionQueueStatusValues.Enabled, prev, "a missing queueStatus is enabled")
	for _, r := range reg.Requests {
		assert.NotEqual(t, http.MethodPut, r.Method, "no PUT when already in that state")
	}
}

func TestPipelineClient_SetQueueStatusConflict(t *testing.T) {
	org := "pipequeueconflictorg"
	c, reg := newPipelineTestClient(t, org)
	path := org + "/proj/_apis/build/definitions/"
	conflict := pipeFixture(t, http.StatusConflict, "error_definition_conflict.json")
	// Definition 1: conflict once, then the fresh GET and PUT succeed.
	reg.Register(httpmock.REST(http.MethodGet, path+"1"), pipeFixture(t, http.StatusOK, "definition_yaml.json"))
	reg.Register(httpmock.REST(http.MethodPut, path+"1"), conflict)
	reg.Register(httpmock.REST(http.MethodGet, path+"1"), httpmock.StringResponse(`{"id":1,"revision":4,"queueStatus":"paused"}`))
	reg.Register(httpmock.REST(http.MethodPut, path+"1"), httpmock.StringResponse(`{"id":1,"revision":5}`))
	// Definition 2: two conflicts give up.
	reg.Register(httpmock.REST(http.MethodGet, path+"2"), httpmock.StringResponse(`{"id":2,"revision":1}`))
	reg.Register(httpmock.REST(http.MethodPut, path+"2"), conflict)
	reg.Register(httpmock.REST(http.MethodGet, path+"2"), httpmock.StringResponse(`{"id":2,"revision":2}`))
	reg.Register(httpmock.REST(http.MethodPut, path+"2"), conflict)
	// Definition 3: other errors are not retried.
	reg.Register(httpmock.REST(http.MethodGet, path+"3"), httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "not found"}))
	reg.Register(httpmock.REST(http.MethodGet, path+"4"), httpmock.StringResponse(`{"id":4,"queueStatus":5}`))
	ctx := context.Background()

	prev, err := c.SetQueueStatus(ctx, 1, build.DefinitionQueueStatusValues.Disabled)
	require.NoError(t, err)
	assert.Equal(t, build.DefinitionQueueStatusValues.Paused, prev, "status from the fresh GET")

	_, err = c.SetQueueStatus(ctx, 2, build.DefinitionQueueStatusValues.Disabled)
	assert.True(t, IsDefinitionRevisionConflict(err), "err = %v", err)

	_, err = c.SetQueueStatus(ctx, 3, build.DefinitionQueueStatusValues.Disabled)
	assert.True(t, IsNotFound(err), "err = %v", err)

	_, err = c.SetQueueStatus(ctx, 4, build.DefinitionQueueStatusValues.Disabled)
	assert.Error(t, err, "undecodable queueStatus")
}

func TestPipelineClient_Builds(t *testing.T) {
	org := "pipebuildsorg"
	c, reg := newPipelineTestClient(t, org)
	var reqs []*http.Request
	var bodies []string
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/builds"), pipeSaw(&reqs, &bodies,
		httpmock.WithHeader(httpmock.StringResponse(`{"count":2,"value":[{"id":106},{"id":105}]}`), azuredevops.HeaderKeyContinuationToken, "tok")))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/builds"), pipeSaw(&reqs, &bodies,
		httpmock.StringResponse(`{"count":0,"value":[]}`)))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/build/builds"), httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "bad"}))
	ctx := context.Background()

	top := 2
	builds, token, err := c.Builds(ctx, build.GetBuildsArgs{Definitions: &[]int{1, 4}, Top: &top})
	require.NoError(t, err)
	assert.Len(t, builds, 2)
	assert.Equal(t, "tok", token)
	q := reqs[0].URL.Query()
	assert.Equal(t, "1,4", q.Get("definitions"))
	assert.Equal(t, "2", q.Get("$top"))
	assert.Equal(t, "queueTimeDescending", q.Get("queryOrder"), "newest queued first by default")

	asc := build.BuildQueryOrderValues.QueueTimeAscending
	builds, token, err = c.Builds(ctx, build.GetBuildsArgs{ContinuationToken: &token, QueryOrder: &asc})
	require.NoError(t, err)
	assert.Empty(t, builds)
	assert.Empty(t, token)
	assert.Equal(t, "tok", reqs[1].URL.Query().Get("continuationToken"))
	assert.Equal(t, "queueTimeAscending", reqs[1].URL.Query().Get("queryOrder"))

	_, _, err = c.Builds(ctx, build.GetBuildsArgs{})
	var he *HTTPError
	assert.ErrorAs(t, err, &he)
}

func TestPipelineClient_BuildReads(t *testing.T) {
	org := "pipereadsorg"
	c, reg := newPipelineTestClient(t, org)
	base := org + "/proj/_apis/build/builds/101"
	var reqs []*http.Request
	var bodies []string
	reg.Register(httpmock.REST(http.MethodGet, base), httpmock.StringResponse(`{"id":101,"status":"completed","result":"failed"}`))
	reg.Register(httpmock.REST(http.MethodGet, base+"/timeline"), pipeFixture(t, http.StatusOK, "timeline_yaml_stages.json"))
	reg.Register(httpmock.REST(http.MethodGet, base+"/logs"), httpmock.StringResponse(`{"count":1,"value":[{"id":7,"lineCount":3}]}`))
	reg.Register(httpmock.REST(http.MethodGet, base+"/logs/7"), pipeSaw(&reqs, &bodies, httpmock.StringResponse("line 1\nline 2\n")))
	reg.Register(httpmock.REST(http.MethodGet, base+"/artifacts"), pipeFixture(t, http.StatusOK, "artifacts.json"))
	ctx := context.Background()

	b, err := c.BuildByID(ctx, 101)
	require.NoError(t, err)
	assert.Equal(t, build.BuildResultValues.Failed, *b.Result)

	tl, err := c.Timeline(ctx, 101)
	require.NoError(t, err)
	require.NotNil(t, tl.Records)
	assert.NotEmpty(t, *tl.Records)

	logs, err := c.BuildLogs(ctx, 101)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, uint64(3), *logs[0].LineCount)

	rc, err := c.BuildLog(ctx, 101, 7)
	require.NoError(t, err)
	text, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, "line 1\nline 2\n", string(text))
	assert.Contains(t, reqs[0].Header.Get("Accept"), "text/plain;", "streamed as text, never GetBuildLogLines")

	arts, err := c.Artifacts(ctx, 101)
	require.NoError(t, err)
	require.Len(t, arts, 3)
	assert.Equal(t, "PipelineArtifact", *arts[1].Resource.Type)
}

func TestPipelineClient_BuildReadErrors(t *testing.T) {
	org := "pipereaderrorg"
	c, reg := newPipelineTestClient(t, org)
	base := org + "/proj/_apis/build/builds/9"
	missing := httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "Build 9 not found."})
	for _, p := range []string{"", "/timeline", "/logs", "/logs/1", "/artifacts"} {
		reg.Register(httpmock.REST(http.MethodGet, base+p), missing)
	}
	ctx := context.Background()
	_, err := c.BuildByID(ctx, 9)
	assert.True(t, IsNotFound(err), "Build: %v", err)
	_, err = c.Timeline(ctx, 9)
	assert.True(t, IsNotFound(err), "Timeline: %v", err)
	_, err = c.BuildLogs(ctx, 9)
	assert.True(t, IsNotFound(err), "BuildLogs: %v", err)
	_, err = c.BuildLog(ctx, 9, 1)
	assert.True(t, IsNotFound(err), "BuildLog: %v", err)
	_, err = c.Artifacts(ctx, 9)
	assert.True(t, IsNotFound(err), "Artifacts: %v", err)
}

func TestPipelineClient_CancelRetry(t *testing.T) {
	org := "pipecancelorg"
	c, reg := newPipelineTestClient(t, org)
	path := org + "/proj/_apis/build/builds/101"
	var reqs []*http.Request
	var bodies []string
	reg.Register(httpmock.REST(http.MethodPatch, path), pipeSaw(&reqs, &bodies, httpmock.StringResponse(`{"id":101,"status":"cancelling"}`)))
	reg.Register(httpmock.REST(http.MethodPatch, path), pipeSaw(&reqs, &bodies, httpmock.StringResponse(`{"id":101,"status":"inProgress"}`)))
	reg.Register(httpmock.REST(http.MethodPatch, path), httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "no"}))
	reg.Register(httpmock.REST(http.MethodPatch, path), httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "no"}))
	ctx := context.Background()

	b, err := c.CancelBuild(ctx, 101)
	require.NoError(t, err)
	assert.Equal(t, build.BuildStatusValues.Cancelling, *b.Status)
	assert.JSONEq(t, `{"status":"cancelling"}`, bodies[0])
	assert.Empty(t, reqs[0].URL.Query().Get("retry"))

	b, err = c.RetryBuild(ctx, 101)
	require.NoError(t, err)
	assert.Equal(t, build.BuildStatusValues.InProgress, *b.Status)
	assert.Equal(t, "true", reqs[1].URL.Query().Get("retry"))
	assert.JSONEq(t, `{}`, bodies[1])

	var he *HTTPError
	_, err = c.CancelBuild(ctx, 101)
	assert.ErrorAs(t, err, &he)
	_, err = c.RetryBuild(ctx, 101)
	assert.ErrorAs(t, err, &he)
}

func TestPipelineClient_RetryStage(t *testing.T) {
	org := "pipestageorg"
	c, reg := newPipelineTestClient(t, org)
	var reqs []*http.Request
	var bodies []string
	reg.Register(httpmock.REST(http.MethodPatch, org+"/proj/_apis/build/builds/101/stages/Test"), pipeSaw(&reqs, &bodies, httpmock.StringResponse("")))
	reg.Register(httpmock.REST(http.MethodPatch, org+"/proj/_apis/build/builds/101/stages/Deploy"),
		httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "  Stage Deploy cannot be retried.\n"}))
	ctx := context.Background()

	require.NoError(t, c.RetryStage(ctx, 101, "Test"))
	assert.JSONEq(t, `{"state":"retry","forceRetryAllJobs":false}`, bodies[0], "failed jobs only, no retryDependencies")
	assert.Contains(t, reqs[0].Header.Get("Accept"), "api-version=7.1-preview.1")

	err := c.RetryStage(ctx, 101, "Deploy")
	assert.EqualError(t, err, "Stage Deploy cannot be retried. (HTTP 400)", "message trimmed")
}

func TestPipelineClient_QueueAndRun(t *testing.T) {
	org := "pipequeueruorg"
	c, reg := newPipelineTestClient(t, org)
	var reqs []*http.Request
	var bodies []string
	reg.Register(httpmock.REST(http.MethodPost, org+"/proj/_apis/build/builds"), pipeSaw(&reqs, &bodies, httpmock.StringResponse(`{"id":110}`)))
	reg.Register(httpmock.REST(http.MethodPost, org+"/proj/_apis/build/builds"), pipeFixture(t, http.StatusBadRequest, "error_queue_validation.json"))
	reg.Register(httpmock.REST(http.MethodPost, org+"/proj/_apis/pipelines/1/runs"), pipeSaw(&reqs, &bodies, httpmock.StringResponse(`{"id":111,"state":"inProgress"}`)))
	reg.Register(httpmock.REST(http.MethodPost, org+"/proj/_apis/pipelines/1/runs"), pipeFixture(t, http.StatusBadRequest, "error_pipeline_validation.json"))
	ctx := context.Background()

	defID, branch := 2, "refs/heads/main"
	queued, err := c.QueueBuild(ctx, &build.Build{Definition: &build.DefinitionReference{Id: &defID}, SourceBranch: &branch})
	require.NoError(t, err)
	assert.Equal(t, 110, *queued.Id)
	assert.JSONEq(t, `{"definition":{"id":2},"sourceBranch":"refs/heads/main"}`, bodies[0])

	_, err = c.QueueBuild(ctx, &build.Build{Definition: &build.DefinitionReference{Id: &defID}})
	assert.EqualError(t, err, "Could not queue the build because there were validation errors or warnings: "+
		"A value for the 'environment' parameter must be provided.; "+
		"The pipeline is not valid. Job Deploy: Step input azureSubscription references service connection prod which could not be found. (HTTP 400)")

	params := map[string]string{"env": "prod"}
	run, err := c.RunPipeline(ctx, 1, pipelines.RunPipelineParameters{TemplateParameters: &params})
	require.NoError(t, err)
	assert.Equal(t, 111, *run.Id)
	assert.JSONEq(t, `{"templateParameters":{"env":"prod"}}`, bodies[1])

	_, err = c.RunPipeline(ctx, 1, pipelines.RunPipelineParameters{})
	var he *HTTPError
	require.ErrorAs(t, err, &he)
	assert.Equal(t, "PipelineValidationException", he.TypeKey)
	assert.Contains(t, he.Message, "You can't set the following variables (BuildConfiguration).")
}

func TestPipelineClient_Approvals(t *testing.T) {
	org := "pipeapprovalorg"
	c, reg := newPipelineTestClient(t, org)
	id1 := uuid.MustParse("00000000-0000-0000-0000-000000010599")
	id2 := uuid.MustParse("00000000-0000-0000-0000-000000010600")
	var reqs []*http.Request
	var bodies []string
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/pipelines/approvals"), pipeSaw(&reqs, &bodies,
		httpmock.StringResponse(`{"count":1,"value":[{"id":"00000000-0000-0000-0000-000000010599","status":"pending","minRequiredApprovers":1}]}`)))
	reg.Register(httpmock.REST(http.MethodPatch, org+"/proj/_apis/pipelines/approvals"), pipeSaw(&reqs, &bodies,
		httpmock.StringResponse(`{"count":1,"value":[{"id":"00000000-0000-0000-0000-000000010599","status":"approved"}]}`)))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/pipelines/approvals"), httpmock.StatusJSONResponse(http.StatusForbidden, map[string]any{"message": "denied"}))
	reg.Register(httpmock.REST(http.MethodPatch, org+"/proj/_apis/pipelines/approvals"), httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "not an approver"}))
	ctx := context.Background()

	got, err := c.QueryApprovals(ctx, []uuid.UUID{id1, id2})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, pipelinesapproval.ApprovalStatusValues.Pending, *got[0].Status)
	q := reqs[0].URL.Query()
	assert.Equal(t, id1.String()+","+id2.String(), q.Get("approvalIds"))
	assert.Equal(t, "steps,permissions", q.Get("$expand"))

	updated, err := c.UpdateApprovals(ctx, []uuid.UUID{id1}, pipelinesapproval.ApprovalStatusValues.Approved, "ship it")
	require.NoError(t, err)
	assert.Equal(t, pipelinesapproval.ApprovalStatusValues.Approved, *updated[0].Status)
	assert.JSONEq(t, `[{"approvalId":"`+id1.String()+`","status":"approved","comment":"ship it"}]`, bodies[1])

	updated, err = c.UpdateApprovals(ctx, nil, pipelinesapproval.ApprovalStatusValues.Approved, "")
	require.NoError(t, err, "nothing to send")
	assert.Empty(t, updated)

	var he *HTTPError
	_, err = c.QueryApprovals(ctx, []uuid.UUID{id1})
	assert.ErrorAs(t, err, &he)
	_, err = c.UpdateApprovals(ctx, []uuid.UUID{id1}, pipelinesapproval.ApprovalStatusValues.Rejected, "")
	assert.ErrorAs(t, err, &he)
}

func TestPipelineClient_Download(t *testing.T) {
	signed := "https://x.blob.core.windows.net/c/drop.zip?sv=2020&sig=SECRETSIG"
	reg := &httpmock.Registry{}
	reg.Register(httpmock.REST(http.MethodGet, "c/drop.zip"), httpmock.StringResponse("PK"))
	reg.Register(httpmock.REST(http.MethodGet, "c/drop.zip"), httpmock.StatusStringResponse(http.StatusForbidden, "AuthenticationFailed "+signed))
	reg.Register(httpmock.MatchAny, func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") })
	reg.Register(httpmock.MatchAny, func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })
	c := NewPipelineClient(NewHTTPClient(ClientOptions{Transport: reg}), "https://dev.azure.com/pipedlorg", "proj")
	ctx := context.Background()

	rc, err := c.Download(ctx, signed)
	require.NoError(t, err)
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, "PK", string(b))

	_, err = c.Download(ctx, signed)
	assert.EqualError(t, err, "HTTP 403")

	_, err = c.Download(ctx, signed)
	assert.EqualError(t, err, "connection reset", "*url.Error unwrapped: no URL")

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = c.Download(canceled, signed)
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotContains(t, err.Error(), "SECRETSIG")

	_, err = c.Download(ctx, "https://x.blob.core.windows.net/%zz?sig=SECRETSIG")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRETSIG")
}

func TestMapErrValidationResults(t *testing.T) {
	status := 400
	msg := " Could not queue the build. \n"
	props := map[string]any{"ValidationResults": []any{
		map[string]any{"result": "error", "message": " first problem "},
		map[string]any{"result": "warning"},
		"junk",
		map[string]any{"message": "second problem"},
	}}
	err := mapErr(azuredevops.WrappedError{StatusCode: &status, Message: &msg, CustomProperties: &props})
	assert.EqualError(t, err, "Could not queue the build: first problem; second problem (HTTP 400)")

	other := map[string]any{"ValidationResults": "nope"}
	err = mapErr(&azuredevops.WrappedError{StatusCode: &status, Message: &msg, CustomProperties: &other})
	assert.EqualError(t, err, "Could not queue the build. (HTTP 400)")
}

func TestIsDefinitionRevisionConflict(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"409", &HTTPError{StatusCode: http.StatusConflict}, true},
		{"400 message mentions revision", &HTTPError{StatusCode: http.StatusBadRequest, Message: "The definition Revision is out of date"}, true},
		{"400 typeKey mentions revision", &HTTPError{StatusCode: http.StatusBadRequest, TypeKey: "DefinitionRevisionConflictException"}, true},
		{"400 other", &HTTPError{StatusCode: http.StatusBadRequest, Message: "bad name"}, false},
		{"404", &HTTPError{StatusCode: http.StatusNotFound, Message: "revision"}, false},
		{"work item conflict", &HTTPError{StatusCode: http.StatusPreconditionFailed}, false},
		{"plain", errors.New("revision"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsDefinitionRevisionConflict(tt.err))
		})
	}
}
