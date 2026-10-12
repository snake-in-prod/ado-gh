package httpmock

import (
	"net/http"
	"slices"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
)

const (
	buildRoute      = "{project}/_apis/build/builds/{buildId}"
	runRoute        = "{project}/_apis/pipelines/{pipelineId}/runs/{runId}"
	releaseRoute    = "{project}/_apis/release/releases/{releaseId}"
	releaseEnvRoute = releaseRoute + "/environments/{environmentId}"
)

// BuildLocations are the Build API locations (definitions, builds, timeline, logs, artifacts,
// stage retry) used by the pipeline commands, for StubLocations. Routes are lowercase, so
// register stubs on lowercase paths.
var BuildLocations = []Location{
	{ID: "dbeaf647-6167-421a-bda9-c9327b25e2e6", Area: "build", ResourceName: "definitions",
		RouteTemplate: "{project}/_apis/build/definitions/{definitionId}", ResourceVersion: 7},
	{ID: "0cd358e1-9217-4d94-8269-1c1ee6f93dcf", Area: "build", ResourceName: "builds",
		RouteTemplate: buildRoute, ResourceVersion: 7},
	{ID: "8baac422-4c6e-4de5-8532-db96d92acffa", Area: "build", ResourceName: "timeline",
		RouteTemplate: buildRoute + "/timeline/{timelineId}"},
	{ID: "35a80daf-7f30-45fc-86e8-6b813d9c90df", Area: "build", ResourceName: "logs",
		RouteTemplate: buildRoute + "/logs/{logId}"},
	{ID: "1db06c96-014e-44e1-ac91-90b2d4b3e984", Area: "build", ResourceName: "artifacts",
		RouteTemplate: buildRoute + "/artifacts", ResourceVersion: 5},
	{ID: "b8aac6c9-744b-46e1-88fc-3550969f9313", Area: "build", ResourceName: "stages",
		RouteTemplate: buildRoute + "/stages/{stageRefName}"},
}

// PipelinesLocations are the Pipelines API locations (pipelines, runs, run logs and artifacts),
// for StubLocations.
var PipelinesLocations = []Location{
	{ID: "28e1305e-2afe-47bf-abaf-cbb0e6a91988", Area: "pipelines", ResourceName: "pipelines",
		RouteTemplate: "{project}/_apis/pipelines/{pipelineId}"},
	{ID: "7859261e-d2e9-4a68-b820-a5d84cc5bb3d", Area: "pipelines", ResourceName: "runs",
		RouteTemplate: runRoute},
	{ID: "fb1b6d27-3957-43d5-a14b-a2d70403e545", Area: "pipelines", ResourceName: "logs",
		RouteTemplate: runRoute + "/logs/{logId}"},
	{ID: "85023071-bd5e-4438-89b0-2a5bf362a19d", Area: "pipelines", ResourceName: "artifacts",
		RouteTemplate: runRoute + "/artifacts"},
}

// ApprovalLocations are the YAML pipeline approvals locations, for StubLocations.
var ApprovalLocations = []Location{
	{ID: "37794717-f36f-4d78-b2bf-4dc30d0cfbcd", Area: "pipelinesApproval", ResourceName: "approvals",
		RouteTemplate: "{project}/_apis/pipelines/approvals/{approvalId}"},
}

// PipelineLocations is everything a pipeline client built on one org URL can call: Build,
// Pipelines, approvals and ResourceAreasLocation (for StubVSRM), all in one StubLocations call
// because the SDK caches discovery per base URL. Give every test its own org name.
var PipelineLocations = slices.Concat(BuildLocations, PipelinesLocations, ApprovalLocations, []Location{ResourceAreasLocation})

// ReleaseLocations are the Classic Release locations on the vsrm host.
var ReleaseLocations = []Location{
	{ID: "a166fde7-27ad-408e-ba75-703c2cc9d500", Area: "Release", ResourceName: "releases",
		RouteTemplate: releaseRoute, ResourceVersion: 8},
	{ID: "d8f96f24-8ea7-4cb6-baab-2df8fc515665", Area: "Release", ResourceName: "definitions",
		RouteTemplate: "{project}/_apis/release/definitions/{definitionId}", ResourceVersion: 4},
	{ID: "a7e426b1-03dc-48af-9dfe-c98bac612dcb", Area: "Release", ResourceName: "environments",
		RouteTemplate: releaseEnvRoute, ResourceVersion: 7},
	{ID: "17c91af7-09fd-4256-bff1-c24ee4f73bc0", Area: "Release", ResourceName: "logs",
		RouteTemplate: releaseEnvRoute + "/deployphases/{releaseDeployPhaseId}/tasks/{taskId}/logs"},
	{ID: "c37fbab5-214b-48e4-a55b-cb6b4f6e4038", Area: "Release", ResourceName: "logs",
		RouteTemplate: releaseRoute + "/logs"},
	{ID: "b47c6458-e73b-47cb-a770-4df1e8813a91", Area: "Release", ResourceName: "approvals",
		RouteTemplate: "{project}/_apis/release/approvals"},
	{ID: "9328e074-59fb-465a-89d9-b09c82ee5109", Area: "Release", ResourceName: "approvals",
		RouteTemplate: "{project}/_apis/release/approvals/{approvalId}"},
}

// StubVSRM registers the discovery calls a Classic Release client makes: GET
// {org}/_apis/ResourceAreas answering https://vsrm.dev.azure.com/{org}/, then OPTIONS on that
// host. The org's own StubLocations must include ResourceAreasLocation (PipelineLocations
// does). Then register the release calls on the {org}/{project}/_apis/release/... paths.
func (r *Registry) StubVSRM(org string) {
	r.StubResourceAreas(org, ResourceArea{
		ID: release.ResourceAreaId.String(), Name: "Release", LocationURL: "https://vsrm.dev.azure.com/" + org + "/",
	})
	options := REST(http.MethodOptions, org+"/_apis")
	r.RegisterOptional(func(req *http.Request) bool { return req.URL.Host == "vsrm.dev.azure.com" && options(req) },
		locationsResponse(ReleaseLocations))
}
