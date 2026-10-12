package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/pipelines"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/pipelinesapproval"
)

// buildDefinitionsLocation is the Build definitions resource. DefinitionsFull and SetQueueStatus
// use it through SDK.Send because the SDK's models drop fields: the list decodes into
// BuildDefinitionReference (no repository or process), and a typed PUT loses every field
// BuildDefinition doesn't declare.
var buildDefinitionsLocation = uuid.MustParse("dbeaf647-6167-421a-bda9-c9327b25e2e6")

// buildDefinitionsVersion is the api-version the SDK sends for definitions.
const buildDefinitionsVersion = "7.1-preview.7"

// PipelineClient calls the Azure DevOps Build, Pipelines and pipeline approvals APIs for one
// project. A pipeline ID is a build definition ID and a run ID is a build ID.
type PipelineClient struct {
	Build     build.Client
	Pipelines pipelines.Client
	Approvals pipelinesapproval.Client
	// SDK sends the requests the typed clients have no model for.
	SDK *azuredevops.Client
	// HTTP downloads artifacts from their downloadUrl.
	HTTP *http.Client
	// Project is the project name or ID.
	Project string
}

// NewPipelineClient returns a PipelineClient for project in the organization at orgURL,
// sending requests through hc.
func NewPipelineClient(hc *http.Client, orgURL, project string) *PipelineClient {
	sdk := NewSDKClient(hc, orgURL)
	return &PipelineClient{
		Build: &build.ClientImpl{Client: *sdk}, Pipelines: &pipelines.ClientImpl{Client: *sdk},
		Approvals: &pipelinesapproval.ClientImpl{Client: *sdk}, SDK: sdk, HTTP: hc, Project: project,
	}
}

// Definitions returns every definition matching args (e.g. RepositoryId and RepositoryType, or
// Name), following continuation tokens. The references carry no repository or process; use
// DefinitionsFull or Definition for those.
func (c *PipelineClient) Definitions(ctx context.Context, args build.GetDefinitionsArgs) ([]build.BuildDefinitionReference, error) {
	args.Project = &c.Project
	var out []build.BuildDefinitionReference
	for {
		page, err := c.Build.GetDefinitions(ctx, args)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, page.Value...)
		if page.ContinuationToken == "" {
			return out, nil
		}
		args.ContinuationToken = &page.ContinuationToken
	}
}

// DefinitionsFull returns every definition of the project in full (repository, process,
// queueStatus) with its latestBuild and latestCompletedBuild, following continuation tokens.
func (c *PipelineClient) DefinitionsFull(ctx context.Context) ([]build.BuildDefinition, error) {
	q := url.Values{"includeAllProperties": {"true"}, "includeLatestBuilds": {"true"}}
	var out []build.BuildDefinition
	for {
		resp, err := c.SDK.Send(ctx, http.MethodGet, buildDefinitionsLocation, buildDefinitionsVersion,
			map[string]string{"project": c.Project}, q, nil, "", "application/json", nil)
		if err != nil {
			return nil, mapErr(err)
		}
		token := resp.Header.Get(azuredevops.HeaderKeyContinuationToken)
		var page []build.BuildDefinition
		err = c.SDK.UnmarshalCollectionBody(resp, &page)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if token == "" {
			return out, nil
		}
		q.Set("continuationToken", token)
	}
}

// Definition fetches definition id in full, e.g. for its repository's default branch.
func (c *PipelineClient) Definition(ctx context.Context, id int) (*build.BuildDefinition, error) {
	def, err := c.Build.GetDefinition(ctx, build.GetDefinitionArgs{Project: &c.Project, DefinitionId: &id})
	return def, mapErr(err)
}

// SetQueueStatus sets definition id's queueStatus and returns the status it had (enabled when
// the definition names none). Nothing is sent when it already has status. The definition is
// read and written back as raw JSON with only queueStatus changed, so fields the SDK model
// lacks survive. A revision conflict (a concurrent update) is retried once with a fresh read.
func (c *PipelineClient) SetQueueStatus(ctx context.Context, id int, status build.DefinitionQueueStatus) (build.DefinitionQueueStatus, error) {
	prev, err := c.setQueueStatus(ctx, id, status)
	if IsDefinitionRevisionConflict(err) {
		prev, err = c.setQueueStatus(ctx, id, status)
	}
	return prev, err
}

func (c *PipelineClient) setQueueStatus(ctx context.Context, id int, status build.DefinitionQueueStatus) (build.DefinitionQueueStatus, error) {
	route := map[string]string{"project": c.Project, "definitionId": strconv.Itoa(id)}
	resp, err := c.SDK.Send(ctx, http.MethodGet, buildDefinitionsLocation, buildDefinitionsVersion, route, nil, nil, "", "application/json", nil)
	if err != nil {
		return "", mapErr(err)
	}
	defer resp.Body.Close()
	var def map[string]json.RawMessage
	if err := c.SDK.UnmarshalBody(resp, &def); err != nil {
		return "", err
	}
	prev := build.DefinitionQueueStatusValues.Enabled
	if raw, ok := def["queueStatus"]; ok {
		if err := json.Unmarshal(raw, &prev); err != nil {
			return "", err
		}
	}
	if prev == status {
		return prev, nil
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return prev, err
	}
	def["queueStatus"] = raw
	body, err := json.Marshal(def)
	if err != nil {
		return prev, err
	}
	put, err := c.SDK.Send(ctx, http.MethodPut, buildDefinitionsLocation, buildDefinitionsVersion, route, nil,
		bytes.NewReader(body), "application/json", "application/json", nil)
	if err != nil {
		return prev, mapErr(err)
	}
	put.Body.Close()
	return prev, nil
}

// IsDefinitionRevisionConflict reports whether err is a build definition update rejected
// because the definition changed since it was read: a 409, or a 400 whose message or type key
// mentions the revision. (IsRevConflict is the work item equivalent.)
func IsDefinitionRevisionConflict(err error) bool {
	var he *HTTPError
	if !errors.As(err, &he) {
		return false
	}
	return he.StatusCode == http.StatusConflict ||
		he.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(he.Message+" "+he.TypeKey), "revision")
}

// Builds returns one page of the builds matching args, newest queued first unless
// args.QueryOrder says otherwise, and the continuation token of the next page ("" after the
// last). Pass the token back in args.ContinuationToken.
func (c *PipelineClient) Builds(ctx context.Context, args build.GetBuildsArgs) ([]build.Build, string, error) {
	args.Project = &c.Project
	if args.QueryOrder == nil {
		order := build.BuildQueryOrderValues.QueueTimeDescending
		args.QueryOrder = &order
	}
	page, err := c.Build.GetBuilds(ctx, args)
	if err != nil {
		return nil, "", mapErr(err)
	}
	return page.Value, page.ContinuationToken, nil
}

// BuildByID fetches run id, including its templateParameters and queue-time parameters.
func (c *PipelineClient) BuildByID(ctx context.Context, id int) (*build.Build, error) {
	b, err := c.Build.GetBuild(ctx, build.GetBuildArgs{Project: &c.Project, BuildId: &id})
	return b, mapErr(err)
}

// Timeline fetches run buildID's timeline: a flat list of records linked by parentId.
func (c *PipelineClient) Timeline(ctx context.Context, buildID int) (*build.Timeline, error) {
	tl, err := c.Build.GetBuildTimeline(ctx, build.GetBuildTimelineArgs{Project: &c.Project, BuildId: &buildID})
	return tl, mapErr(err)
}

// BuildLogs lists the logs of run buildID.
func (c *PipelineClient) BuildLogs(ctx context.Context, buildID int) ([]build.BuildLog, error) {
	logs, err := c.Build.GetBuildLogs(ctx, build.GetBuildLogsArgs{Project: &c.Project, BuildId: &buildID})
	if err != nil {
		return nil, mapErr(err)
	}
	return deref(logs), nil
}

// BuildLog opens log logID of run buildID (a timeline record's log.id) as plain text, to be
// streamed: logs have no size limit. The caller closes it.
func (c *PipelineClient) BuildLog(ctx context.Context, buildID, logID int) (io.ReadCloser, error) {
	rc, err := c.Build.GetBuildLog(ctx, build.GetBuildLogArgs{Project: &c.Project, BuildId: &buildID, LogId: &logID})
	return rc, mapErr(err)
}

// Artifacts lists the artifacts of run buildID.
func (c *PipelineClient) Artifacts(ctx context.Context, buildID int) ([]build.BuildArtifact, error) {
	arts, err := c.Build.GetArtifacts(ctx, build.GetArtifactsArgs{Project: &c.Project, BuildId: &buildID})
	if err != nil {
		return nil, mapErr(err)
	}
	return deref(arts), nil
}

// CancelBuild asks the server to cancel run id and returns the updated run.
func (c *PipelineClient) CancelBuild(ctx context.Context, id int) (*build.Build, error) {
	status := build.BuildStatusValues.Cancelling
	b, err := c.Build.UpdateBuild(ctx, build.UpdateBuildArgs{Project: &c.Project, BuildId: &id, Build: &build.Build{Status: &status}})
	return b, mapErr(err)
}

// RetryBuild retries run id in place (PATCH ?retry=true), as `rerun --failed` does for a
// classic build, which has no stages to retry.
func (c *PipelineClient) RetryBuild(ctx context.Context, id int) (*build.Build, error) {
	retry := true
	b, err := c.Build.UpdateBuild(ctx, build.UpdateBuildArgs{Project: &c.Project, BuildId: &id, Retry: &retry, Build: &build.Build{}})
	return b, mapErr(err)
}

// RetryStage retries the failed jobs of stage identifier (the Stage timeline record's
// identifier, not its id or name) in run buildID. retryDependencies is not part of REST 7.1,
// so it is not sent.
func (c *PipelineClient) RetryStage(ctx context.Context, buildID int, identifier string) error {
	state, all := build.StageUpdateTypeValues.Retry, false
	return mapErr(c.Build.UpdateStage(ctx, build.UpdateStageArgs{
		Project: &c.Project, BuildId: &buildID, StageRefName: &identifier,
		UpdateParameters: &build.UpdateStageParameters{State: &state, ForceRetryAllJobs: &all},
	}))
}

// QueueBuild queues b (definition, sourceBranch, sourceVersion, parameters) through the Build
// API, as classic pipelines need.
func (c *PipelineClient) QueueBuild(ctx context.Context, b *build.Build) (*build.Build, error) {
	queued, err := c.Build.QueueBuild(ctx, build.QueueBuildArgs{Project: &c.Project, Build: b})
	return queued, mapErr(err)
}

// RunPipeline runs YAML pipeline pipelineID with params through the Pipelines API.
func (c *PipelineClient) RunPipeline(ctx context.Context, pipelineID int, params pipelines.RunPipelineParameters) (*pipelines.Run, error) {
	run, err := c.Pipelines.RunPipeline(ctx, pipelines.RunPipelineArgs{Project: &c.Project, PipelineId: &pipelineID, RunParameters: &params})
	return run, mapErr(err)
}

// approvalExpand asks for the approval steps (approvers, comments).
var approvalExpand = pipelinesapproval.ApprovalDetailsExpandParameter("steps")

// QueryApprovals fetches the approvals ids (from a run's Checkpoint.Approval timeline records) with
// their steps.
func (c *PipelineClient) QueryApprovals(ctx context.Context, ids []uuid.UUID) ([]pipelinesapproval.Approval, error) {
	got, err := c.Approvals.QueryApprovals(ctx, pipelinesapproval.QueryApprovalsArgs{Project: &c.Project, ApprovalIds: &ids, Expand: &approvalExpand})
	if err != nil {
		return nil, mapErr(err)
	}
	return deref(got), nil
}

// UpdateApprovals sets approvals ids to status (approved or rejected) with comment, in one
// request, and returns them as updated. With no ids it sends nothing.
func (c *PipelineClient) UpdateApprovals(ctx context.Context, ids []uuid.UUID, status pipelinesapproval.ApprovalStatus, comment string) ([]pipelinesapproval.Approval, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	params := make([]pipelinesapproval.ApprovalUpdateParameters, len(ids))
	for i := range ids {
		params[i] = pipelinesapproval.ApprovalUpdateParameters{ApprovalId: &ids[i], Status: &status}
		if comment != "" {
			params[i].Comment = &comment
		}
	}
	got, err := c.Approvals.UpdateApprovals(ctx, pipelinesapproval.UpdateApprovalsArgs{Project: &c.Project, UpdateParameters: &params})
	if err != nil {
		return nil, mapErr(err)
	}
	return deref(got), nil
}

// Download GETs an artifact's downloadUrl with the authenticated client, following redirects
// (the token goes only to Azure DevOps hosts, never to blob storage). The caller closes the
// body. The URL may be signed, so errors never contain it: a *url.Error is unwrapped and a
// non-2xx status is reported by code only.
func (c *PipelineClient) Download(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, withoutURL(err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, withoutURL(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		resp.Body.Close()
		return nil, &HTTPError{StatusCode: resp.StatusCode}
	}
	return resp.Body, nil
}

// withoutURL returns the error inside a *url.Error, whose message would print the URL.
func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
