package httpmock

import (
	"net/http"
	"slices"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/search"
)

const (
	witItemRoute = "{project}/_apis/wit/workitems"
	witTypeRoute = "{project}/_apis/wit/workitemtypes"
	teamRoute    = "{project}/{team}/_apis/work"
)

// WITLocations are the work item tracking (wit) API locations used by the work item commands,
// for StubLocations. Routes are lowercase, so register stubs on lowercase paths.
var WITLocations = []Location{
	{ID: "72c7ddf8-2cdc-4f60-90cd-ab71c14a399b", Area: "wit", ResourceName: "workItems",
		RouteTemplate: witItemRoute + "/{id}"},
	{ID: "62d3d110-0047-428c-ad3c-4fe872c91c74", Area: "wit", ResourceName: "workItems",
		RouteTemplate: witItemRoute + "/${type}"},
	{ID: "908509b6-4248-4475-a1cd-829139ba419f", Area: "wit", ResourceName: "workItemsBatch",
		RouteTemplate: "{project}/_apis/wit/workitemsbatch"},
	{ID: "6570bf97-d02c-4a91-8d93-3abe9895b1a9", Area: "wit", ResourceName: "updates",
		RouteTemplate: witItemRoute + "/{id}/updates/{updateNumber}"},
	{ID: "608aac0a-32e1-4493-a863-b9cf4566d257", Area: "wit", ResourceName: "comments",
		RouteTemplate: witItemRoute + "/{workItemId}/comments/{commentId}"},
	{ID: "b70d8d39-926c-465e-b927-b1bf0e5ca0e0", Area: "wit", ResourceName: "recyclebin",
		RouteTemplate: "{project}/_apis/wit/recyclebin/{id}"},
	{ID: "1a9c53f7-f243-4447-b110-35ef023636e4", Area: "wit", ResourceName: "wiql",
		RouteTemplate: "{project}/{team}/_apis/wit/wiql"},
	{ID: "a02355f5-5f8a-4671-8e32-369d23aac83d", Area: "wit", ResourceName: "wiql",
		RouteTemplate: "{project}/{team}/_apis/wit/wiql/{id}"},
	{ID: "a67d190c-c41f-424b-814d-0e906f659301", Area: "wit", ResourceName: "queries",
		RouteTemplate: "{project}/_apis/wit/queries/{*query}"},
	{ID: "a70579d1-f53a-48ee-a5be-7be8659023b9", Area: "wit", ResourceName: "classificationNodes",
		RouteTemplate: "{project}/_apis/wit/classificationnodes"},
	{ID: "5a172953-1b41-49d3-840a-33f79c3ce89f", Area: "wit", ResourceName: "classificationNodes",
		RouteTemplate: "{project}/_apis/wit/classificationnodes/{structureGroup}/{*path}"},
	{ID: "7c8d7a76-4a09-43e8-b5df-bd792f4ac6aa", Area: "wit", ResourceName: "workItemTypes",
		RouteTemplate: witTypeRoute + "/{type}"},
	{ID: "7c9d7a76-4a09-43e8-b5df-bd792f4ac6aa", Area: "wit", ResourceName: "states",
		RouteTemplate: witTypeRoute + "/{type}/states"},
	{ID: "bd293ce5-3d25-4192-8e67-e8092e879efb", Area: "wit", ResourceName: "fields",
		RouteTemplate: witTypeRoute + "/{type}/fields/{field}"},
	{ID: "9b9f5734-36c8-415e-ba67-f83b45c31408", Area: "wit", ResourceName: "workItemTypeCategories",
		RouteTemplate: "{project}/_apis/wit/workitemtypecategories/{category}"},
	{ID: "b51fd764-e5c2-4b9b-aaf7-3395cf4bdd94", Area: "wit", ResourceName: "fields",
		RouteTemplate: "{project}/_apis/wit/fields/{fieldNameOrRefName}"},
	{ID: "f5d33bc9-5b49-4a3c-a9bd-f3cd46dd2165", Area: "wit", ResourceName: "workItemRelationTypes",
		RouteTemplate: "_apis/wit/workitemrelationtypes/{relation}"},
}

// WorkLocations are the team-scoped work (iterations, boards, backlogs) API locations, for
// StubLocations.
var WorkLocations = []Location{
	{ID: "c3c1012b-bea7-49d7-b45e-1664e566f84c", Area: "work", ResourceName: "teamsettings",
		RouteTemplate: teamRoute + "/teamsettings"},
	{ID: "c9175577-28a1-4b06-9197-8636af9f64ad", Area: "work", ResourceName: "iterations",
		RouteTemplate: teamRoute + "/teamsettings/iterations/{id}"},
	{ID: "5b3ef1a6-d3ab-44cd-bafd-c7f45db850fa", Area: "work", ResourceName: "workItems",
		RouteTemplate: teamRoute + "/teamsettings/iterations/{iterationId}/workitems"},
	{ID: "2d4faa2e-9150-4cbf-a47a-932b1b4a0773", Area: "work", ResourceName: "teamdaysoff",
		RouteTemplate: teamRoute + "/teamsettings/iterations/{iterationId}/teamdaysoff"},
	{ID: "07ced576-58ed-49e6-9c1e-5cb53ab8bf2a", Area: "work", ResourceName: "teamfieldvalues",
		RouteTemplate: teamRoute + "/teamsettings/teamfieldvalues"},
	{ID: "c6815dbe-8e7e-4ffe-9a79-e83ee712aa92", Area: "work", ResourceName: "taskboardColumns",
		RouteTemplate: teamRoute + "/taskboardcolumns"},
	{ID: "1be23c36-8872-4abc-b57d-402cd6c669d9", Area: "work", ResourceName: "taskboardWorkItems",
		RouteTemplate: teamRoute + "/taskboardworkitems/{iterationId}/{workItemId}"},
	{ID: "23ad19fc-3b8e-4877-8462-b3f92bc06b40", Area: "work", ResourceName: "boards",
		RouteTemplate: teamRoute + "/boards/{id}"},
	{ID: "c555d7ff-84e1-47df-9923-a3fe0cd8751b", Area: "work", ResourceName: "columns",
		RouteTemplate: teamRoute + "/boards/{board}/columns"},
	{ID: "a93726f9-7867-4e38-b4f2-0bfafc2f6a94", Area: "work", ResourceName: "backlogs",
		RouteTemplate: teamRoute + "/backlogs/{id}"},
	{ID: "7c468d96-ab1d-4294-a360-92f07e9ccd98", Area: "work", ResourceName: "workItems",
		RouteTemplate: teamRoute + "/backlogs/{backlogId}/workitems"},
	{ID: "7799f497-3cb5-4f16-ad4f-5cd06012db64", Area: "work", ResourceName: "backlogconfiguration",
		RouteTemplate: teamRoute + "/backlogconfiguration"},
}

// CoreLocations are the project and team API locations, for StubLocations.
var CoreLocations = []Location{
	{ID: "603fe2ac-9723-48b9-88ad-09305aa6c6e1", Area: "core", ResourceName: "projects",
		RouteTemplate: "_apis/projects/{projectId}"},
	{ID: "d30a3dd1-f8ba-442a-b86a-bd0c0c383e59", Area: "core", ResourceName: "teams",
		RouteTemplate: "_apis/projects/{projectId}/teams/{teamId}"},
}

// WorkItemLocations is everything a work item client built on one org URL can call: WIT, work,
// core and ResourceAreasLocation (for StubSearch). The SDK caches discovery per base URL, so
// give every test its own org name.
var WorkItemLocations = slices.Concat(WITLocations, WorkLocations, CoreLocations, []Location{ResourceAreasLocation})

// SearchLocations are the Work Item Search locations on the almsearch host.
var SearchLocations = []Location{
	{ID: "73b2c9e2-ff9e-4447-8cda-5f5b21ff7cae", Area: "search", ResourceName: "workItemSearchResults",
		RouteTemplate: "{project}/_apis/search/workitemsearchresults"},
}

// StubSearch registers the discovery calls a Work Item Search client makes: GET
// {org}/_apis/ResourceAreas answering https://almsearch.dev.azure.com/{org}/, then OPTIONS on
// that host. The org's own StubLocations must include ResourceAreasLocation (WorkItemLocations
// does). Then register POST {org}/{project}/_apis/search/workitemsearchresults.
func (r *Registry) StubSearch(org string) {
	r.StubResourceAreas(org, ResourceArea{
		ID: search.ResourceAreaId.String(), Name: "Search", LocationURL: "https://almsearch.dev.azure.com/" + org + "/",
	})
	options := REST(http.MethodOptions, org+"/_apis")
	r.Register(func(req *http.Request) bool { return req.URL.Host == "almsearch.dev.azure.com" && options(req) },
		locationsResponse(SearchLocations))
}
