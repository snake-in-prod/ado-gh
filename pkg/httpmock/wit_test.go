package httpmock

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/search"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/work"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/workitemtracking"
)

type witClients struct {
	wit  workitemtracking.Client
	work work.Client
	core core.Client
}

// TestWorkItemLocations drives the real SDK clients through WorkItemLocations and checks the
// URL each call lands on, so route templates and location IDs match the SDK.
func TestWorkItemLocations(t *testing.T) {
	p, team, typ, id, cid, iter := "proj", "team", "Bug", 7, 3, "6f0c0000-0000-0000-0000-000000000001"
	iterUUID := uuid.MustParse(iter)
	iterID := &iterUUID
	str := func(s string) *string { return &s }
	tests := []struct {
		method, path string
		call         func(ctx context.Context, c witClients) error
	}{
		{"GET", "proj/_apis/wit/workitems/7", func(ctx context.Context, c witClients) error {
			_, err := c.wit.GetWorkItem(ctx, workitemtracking.GetWorkItemArgs{Project: &p, Id: &id})
			return err
		}},
		{"PATCH", "proj/_apis/wit/workitems/7", func(ctx context.Context, c witClients) error {
			_, err := c.wit.UpdateWorkItem(ctx, workitemtracking.UpdateWorkItemArgs{Project: &p, Id: &id, Document: &[]webapi.JsonPatchOperation{}})
			return err
		}},
		{"DELETE", "proj/_apis/wit/workitems/7", func(ctx context.Context, c witClients) error {
			_, err := c.wit.DeleteWorkItem(ctx, workitemtracking.DeleteWorkItemArgs{Project: &p, Id: &id})
			return err
		}},
		{"POST", "proj/_apis/wit/workitems/$Bug", func(ctx context.Context, c witClients) error {
			_, err := c.wit.CreateWorkItem(ctx, workitemtracking.CreateWorkItemArgs{Project: &p, Type: &typ, Document: &[]webapi.JsonPatchOperation{}})
			return err
		}},
		{"POST", "proj/_apis/wit/workitemsbatch", func(ctx context.Context, c witClients) error {
			_, err := c.wit.GetWorkItemsBatch(ctx, workitemtracking.GetWorkItemsBatchArgs{Project: &p, WorkItemGetRequest: &workitemtracking.WorkItemBatchGetRequest{}})
			return err
		}},
		{"GET", "proj/_apis/wit/workitems/7/updates", func(ctx context.Context, c witClients) error {
			_, err := c.wit.GetUpdates(ctx, workitemtracking.GetUpdatesArgs{Project: &p, Id: &id})
			return err
		}},
		{"GET", "proj/_apis/wit/workitems/7/comments", func(ctx context.Context, c witClients) error {
			_, err := c.wit.GetComments(ctx, workitemtracking.GetCommentsArgs{Project: &p, WorkItemId: &id})
			return err
		}},
		{"POST", "proj/_apis/wit/workitems/7/comments", func(ctx context.Context, c witClients) error {
			_, err := c.wit.AddWorkItemComment(ctx, workitemtracking.AddWorkItemCommentArgs{Project: &p, WorkItemId: &id, Request: &workitemtracking.CommentCreate{}, Format: &workitemtracking.CommentFormatValues.Markdown})
			return err
		}},
		{"DELETE", "proj/_apis/wit/workitems/7/comments/3", func(ctx context.Context, c witClients) error {
			return c.wit.DeleteComment(ctx, workitemtracking.DeleteCommentArgs{Project: &p, WorkItemId: &id, CommentId: &cid})
		}},
		{"PATCH", "proj/_apis/wit/recyclebin/7", func(ctx context.Context, c witClients) error {
			_, err := c.wit.RestoreWorkItem(ctx, workitemtracking.RestoreWorkItemArgs{Project: &p, Id: &id, Payload: &workitemtracking.WorkItemDeleteUpdate{}})
			return err
		}},
		{"POST", "proj/team/_apis/wit/wiql", func(ctx context.Context, c witClients) error {
			_, err := c.wit.QueryByWiql(ctx, workitemtracking.QueryByWiqlArgs{Project: &p, Team: &team, Wiql: &workitemtracking.Wiql{}})
			return err
		}},
		{"POST", "proj/_apis/wit/wiql", func(ctx context.Context, c witClients) error {
			_, err := c.wit.QueryByWiql(ctx, workitemtracking.QueryByWiqlArgs{Project: &p, Wiql: &workitemtracking.Wiql{}})
			return err
		}},
		{"GET", "proj/team/_apis/wit/wiql/" + iter, func(ctx context.Context, c witClients) error {
			_, err := c.wit.QueryById(ctx, workitemtracking.QueryByIdArgs{Project: &p, Team: &team, Id: iterID})
			return err
		}},
		{"GET", "proj/_apis/wit/queries/Shared%20Queries%2FBugs", func(ctx context.Context, c witClients) error {
			_, err := c.wit.GetQuery(ctx, workitemtracking.GetQueryArgs{Project: &p, Query: str("Shared Queries/Bugs")})
			return err
		}},
		{"GET", "proj/_apis/wit/queries", func(ctx context.Context, c witClients) error {
			_, err := c.wit.GetQueries(ctx, workitemtracking.GetQueriesArgs{Project: &p})
			return err
		}},
		{"GET", "proj/_apis/wit/classificationnodes", func(ctx context.Context, c witClients) error {
			_, err := c.wit.GetRootNodes(ctx, workitemtracking.GetRootNodesArgs{Project: &p})
			return err
		}},
		{"GET", "proj/_apis/wit/classificationnodes/iterations/Sprint%201", func(ctx context.Context, c witClients) error {
			g := workitemtracking.TreeStructureGroupValues.Iterations
			_, err := c.wit.GetClassificationNode(ctx, workitemtracking.GetClassificationNodeArgs{Project: &p, StructureGroup: &g, Path: str("Sprint 1")})
			return err
		}},
		{"GET", "proj/_apis/wit/workitemtypes", func(ctx context.Context, c witClients) error {
			_, err := c.wit.GetWorkItemTypes(ctx, workitemtracking.GetWorkItemTypesArgs{Project: &p})
			return err
		}},
		{"GET", "proj/_apis/wit/workitemtypes/User%20Story/states", func(ctx context.Context, c witClients) error {
			_, err := c.wit.GetWorkItemTypeStates(ctx, workitemtracking.GetWorkItemTypeStatesArgs{Project: &p, Type: str("User Story")})
			return err
		}},
		{"GET", "proj/_apis/wit/workitemtypes/Bug/fields/System.Reason", func(ctx context.Context, c witClients) error {
			_, err := c.wit.GetWorkItemTypeFieldWithReferences(ctx, workitemtracking.GetWorkItemTypeFieldWithReferencesArgs{Project: &p, Type: &typ, Field: str("System.Reason")})
			return err
		}},
		{"GET", "proj/_apis/wit/workitemtypecategories", func(ctx context.Context, c witClients) error {
			_, err := c.wit.GetWorkItemTypeCategories(ctx, workitemtracking.GetWorkItemTypeCategoriesArgs{Project: &p})
			return err
		}},
		{"GET", "proj/_apis/wit/fields", func(ctx context.Context, c witClients) error {
			_, err := c.wit.GetWorkItemFields(ctx, workitemtracking.GetWorkItemFieldsArgs{Project: &p})
			return err
		}},
		{"GET", "_apis/wit/workitemrelationtypes", func(ctx context.Context, c witClients) error {
			_, err := c.wit.GetRelationTypes(ctx, workitemtracking.GetRelationTypesArgs{})
			return err
		}},
		{"GET", "proj/team/_apis/work/teamsettings", func(ctx context.Context, c witClients) error {
			_, err := c.work.GetTeamSettings(ctx, work.GetTeamSettingsArgs{Project: &p, Team: &team})
			return err
		}},
		{"GET", "proj/team/_apis/work/teamsettings/iterations", func(ctx context.Context, c witClients) error {
			_, err := c.work.GetTeamIterations(ctx, work.GetTeamIterationsArgs{Project: &p, Team: &team, Timeframe: str("current")})
			return err
		}},
		{"GET", "proj/team/_apis/work/teamsettings/iterations/" + iter + "/workitems", func(ctx context.Context, c witClients) error {
			_, err := c.work.GetIterationWorkItems(ctx, work.GetIterationWorkItemsArgs{Project: &p, Team: &team, IterationId: iterID})
			return err
		}},
		{"GET", "proj/team/_apis/work/teamsettings/iterations/" + iter + "/teamdaysoff", func(ctx context.Context, c witClients) error {
			_, err := c.work.GetTeamDaysOff(ctx, work.GetTeamDaysOffArgs{Project: &p, Team: &team, IterationId: iterID})
			return err
		}},
		{"GET", "proj/team/_apis/work/teamsettings/teamfieldvalues", func(ctx context.Context, c witClients) error {
			_, err := c.work.GetTeamFieldValues(ctx, work.GetTeamFieldValuesArgs{Project: &p, Team: &team})
			return err
		}},
		{"GET", "proj/team/_apis/work/taskboardcolumns", func(ctx context.Context, c witClients) error {
			_, err := c.work.GetColumns(ctx, work.GetColumnsArgs{Project: &p, Team: &team})
			return err
		}},
		{"GET", "proj/team/_apis/work/taskboardworkitems/" + iter, func(ctx context.Context, c witClients) error {
			_, err := c.work.GetWorkItemColumns(ctx, work.GetWorkItemColumnsArgs{Project: &p, Team: &team, IterationId: iterID})
			return err
		}},
		{"GET", "proj/team/_apis/work/boards/Stories", func(ctx context.Context, c witClients) error {
			_, err := c.work.GetBoard(ctx, work.GetBoardArgs{Project: &p, Team: &team, Id: str("Stories")})
			return err
		}},
		{"GET", "proj/team/_apis/work/boards/Stories/columns", func(ctx context.Context, c witClients) error {
			_, err := c.work.GetBoardColumns(ctx, work.GetBoardColumnsArgs{Project: &p, Team: &team, Board: str("Stories")})
			return err
		}},
		{"GET", "proj/team/_apis/work/backlogs", func(ctx context.Context, c witClients) error {
			_, err := c.work.GetBacklogs(ctx, work.GetBacklogsArgs{Project: &p, Team: &team})
			return err
		}},
		{"GET", "proj/team/_apis/work/backlogs/Microsoft.RequirementCategory/workitems", func(ctx context.Context, c witClients) error {
			_, err := c.work.GetBacklogLevelWorkItems(ctx, work.GetBacklogLevelWorkItemsArgs{Project: &p, Team: &team, BacklogId: str("Microsoft.RequirementCategory")})
			return err
		}},
		{"GET", "proj/team/_apis/work/backlogconfiguration", func(ctx context.Context, c witClients) error {
			_, err := c.work.GetBacklogConfigurations(ctx, work.GetBacklogConfigurationsArgs{Project: &p, Team: &team})
			return err
		}},
		{"GET", "_apis/projects/proj", func(ctx context.Context, c witClients) error {
			_, err := c.core.GetProject(ctx, core.GetProjectArgs{ProjectId: &p})
			return err
		}},
		{"GET", "_apis/projects/proj/teams", func(ctx context.Context, c witClients) error {
			_, err := c.core.GetTeams(ctx, core.GetTeamsArgs{ProjectId: &p})
			return err
		}},
	}
	for i, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			org := fmt.Sprintf("witorg%d", i) // the SDK caches discovery per base URL
			c := &http.Client{}
			reg := ReplaceTripper(c)
			reg.StubLocations(org, WorkItemLocations...)
			reg.Register(REST(tt.method, org+"/"+tt.path), JSONResponse(map[string]any{"count": 0, "value": []any{}}))
			sdk := azuredevops.NewClientWithOptions(&azuredevops.Connection{}, "https://dev.azure.com/"+org, azuredevops.WithHTTPClient(c))
			err := tt.call(context.Background(), witClients{
				wit: &workitemtracking.ClientImpl{Client: *sdk}, work: &work.ClientImpl{Client: *sdk}, core: &core.ClientImpl{Client: *sdk},
			})
			if err != nil {
				t.Errorf("call: %v", err)
			}
			reg.Verify(t)
		})
	}
}

var orgSeq atomic.Int64

// uniqueOrg returns an org name of its own per call: the SDK caches discovery per base URL for
// the whole process, so a reused org skips the discovery requests these tests verify (-count>1).
func uniqueOrg(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, orgSeq.Add(1))
}

func TestStubSearch(t *testing.T) {
	c := &http.Client{}
	reg := ReplaceTripper(c)
	org := uniqueOrg("searchorg")
	reg.StubLocations(org, WorkItemLocations...)
	reg.StubSearch(org)
	reg.Register(REST(http.MethodPost, org+"/proj/_apis/search/workitemsearchresults"), JSONResponse(map[string]any{"count": 1}))

	ctx := context.Background()
	sdk := azuredevops.NewClientWithOptions(&azuredevops.Connection{}, "https://dev.azure.com/"+org, azuredevops.WithHTTPClient(c))
	areas, err := sdk.GetResourceAreas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var base string
	for _, a := range *areas {
		if *a.Id == search.ResourceAreaId {
			base = *a.LocationUrl
		}
	}
	if base != "https://almsearch.dev.azure.com/"+org+"/" {
		t.Fatalf("search base = %q", base)
	}
	client := &search.ClientImpl{Client: *azuredevops.NewClientWithOptions(&azuredevops.Connection{}, base, azuredevops.WithHTTPClient(c))}
	p := "proj"
	resp, err := client.FetchWorkItemSearchResults(ctx, search.FetchWorkItemSearchResultsArgs{
		Project: &p, Request: &search.WorkItemSearchRequest{SearchText: &p, Top: new(int), Filters: &map[string][]string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Count == nil || *resp.Count != 1 {
		t.Errorf("resp = %+v", resp)
	}
	reg.Verify(t)
	for _, req := range reg.Requests[len(reg.Requests)-2:] {
		if req.URL.Host != "almsearch.dev.azure.com" {
			t.Errorf("%s %s went to %s, want almsearch", req.Method, req.URL.Path, req.URL.Host)
		}
	}
}
