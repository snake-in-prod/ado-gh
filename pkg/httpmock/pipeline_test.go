package httpmock

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/build"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/pipelines"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/pipelinesapproval"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
)

type pipelineClients struct {
	build     build.Client
	pipelines pipelines.Client
	approval  pipelinesapproval.Client
}

// TestPipelineLocations drives the real Build, Pipelines and approvals SDK clients through
// PipelineLocations and checks the URL and api-version each call lands on, so route templates
// and location IDs match the SDK.
func TestPipelineLocations(t *testing.T) {
	p, def, run, logID, stage := "proj", 5, 42, 7, "Deploy"
	approvalID := uuid.MustParse("00000000-0000-0000-0000-000000000101")
	type pc = pipelineClients
	tests := []struct {
		method, path, version string
		call                  func(ctx context.Context, c pc) error
	}{
		{"GET", "proj/_apis/build/definitions", "7.1-preview.7", func(ctx context.Context, c pc) error {
			_, err := c.build.GetDefinitions(ctx, build.GetDefinitionsArgs{Project: &p})
			return err
		}},
		{"GET", "proj/_apis/build/definitions/5", "7.1-preview.7", func(ctx context.Context, c pc) error {
			_, err := c.build.GetDefinition(ctx, build.GetDefinitionArgs{Project: &p, DefinitionId: &def})
			return err
		}},
		{"PUT", "proj/_apis/build/definitions/5", "7.1-preview.7", func(ctx context.Context, c pc) error {
			_, err := c.build.UpdateDefinition(ctx, build.UpdateDefinitionArgs{Project: &p, DefinitionId: &def, Definition: &build.BuildDefinition{}})
			return err
		}},
		{"GET", "proj/_apis/build/builds", "7.1-preview.7", func(ctx context.Context, c pc) error {
			_, err := c.build.GetBuilds(ctx, build.GetBuildsArgs{Project: &p})
			return err
		}},
		{"GET", "proj/_apis/build/builds/42", "7.1-preview.7", func(ctx context.Context, c pc) error {
			_, err := c.build.GetBuild(ctx, build.GetBuildArgs{Project: &p, BuildId: &run})
			return err
		}},
		{"POST", "proj/_apis/build/builds", "7.1-preview.7", func(ctx context.Context, c pc) error {
			_, err := c.build.QueueBuild(ctx, build.QueueBuildArgs{Project: &p, Build: &build.Build{}})
			return err
		}},
		{"PATCH", "proj/_apis/build/builds/42", "7.1-preview.7", func(ctx context.Context, c pc) error {
			_, err := c.build.UpdateBuild(ctx, build.UpdateBuildArgs{Project: &p, BuildId: &run, Build: &build.Build{}})
			return err
		}},
		{"GET", "proj/_apis/build/builds/42/timeline", "7.1-preview.2", func(ctx context.Context, c pc) error {
			_, err := c.build.GetBuildTimeline(ctx, build.GetBuildTimelineArgs{Project: &p, BuildId: &run})
			return err
		}},
		{"GET", "proj/_apis/build/builds/42/logs", "7.1-preview.2", func(ctx context.Context, c pc) error {
			_, err := c.build.GetBuildLogs(ctx, build.GetBuildLogsArgs{Project: &p, BuildId: &run})
			return err
		}},
		{"GET", "proj/_apis/build/builds/42/logs/7", "7.1-preview.2", func(ctx context.Context, c pc) error {
			r, err := c.build.GetBuildLog(ctx, build.GetBuildLogArgs{Project: &p, BuildId: &run, LogId: &logID})
			if err == nil {
				r.Close()
			}
			return err
		}},
		{"GET", "proj/_apis/build/builds/42/artifacts", "7.1-preview.5", func(ctx context.Context, c pc) error {
			_, err := c.build.GetArtifacts(ctx, build.GetArtifactsArgs{Project: &p, BuildId: &run})
			return err
		}},
		{"PATCH", "proj/_apis/build/builds/42/stages/Deploy", "7.1-preview.1", func(ctx context.Context, c pc) error {
			return c.build.UpdateStage(ctx, build.UpdateStageArgs{Project: &p, BuildId: &run, StageRefName: &stage, UpdateParameters: &build.UpdateStageParameters{}})
		}},
		{"GET", "proj/_apis/pipelines", "7.1-preview.1", func(ctx context.Context, c pc) error {
			_, err := c.pipelines.ListPipelines(ctx, pipelines.ListPipelinesArgs{Project: &p})
			return err
		}},
		{"GET", "proj/_apis/pipelines/5", "7.1-preview.1", func(ctx context.Context, c pc) error {
			_, err := c.pipelines.GetPipeline(ctx, pipelines.GetPipelineArgs{Project: &p, PipelineId: &def})
			return err
		}},
		{"POST", "proj/_apis/pipelines/5/runs", "7.1-preview.1", func(ctx context.Context, c pc) error {
			_, err := c.pipelines.RunPipeline(ctx, pipelines.RunPipelineArgs{Project: &p, PipelineId: &def, RunParameters: &pipelines.RunPipelineParameters{}})
			return err
		}},
		{"GET", "proj/_apis/pipelines/5/runs", "7.1-preview.1", func(ctx context.Context, c pc) error {
			_, err := c.pipelines.ListRuns(ctx, pipelines.ListRunsArgs{Project: &p, PipelineId: &def})
			return err
		}},
		{"GET", "proj/_apis/pipelines/5/runs/42", "7.1-preview.1", func(ctx context.Context, c pc) error {
			_, err := c.pipelines.GetRun(ctx, pipelines.GetRunArgs{Project: &p, PipelineId: &def, RunId: &run})
			return err
		}},
		{"GET", "proj/_apis/pipelines/5/runs/42/logs", "7.1-preview.1", func(ctx context.Context, c pc) error {
			_, err := c.pipelines.ListLogs(ctx, pipelines.ListLogsArgs{Project: &p, PipelineId: &def, RunId: &run})
			return err
		}},
		{"GET", "proj/_apis/pipelines/5/runs/42/logs/7", "7.1-preview.1", func(ctx context.Context, c pc) error {
			_, err := c.pipelines.GetLog(ctx, pipelines.GetLogArgs{Project: &p, PipelineId: &def, RunId: &run, LogId: &logID})
			return err
		}},
		{"GET", "proj/_apis/pipelines/5/runs/42/artifacts", "7.1-preview.1", func(ctx context.Context, c pc) error {
			_, err := c.pipelines.GetArtifact(ctx, pipelines.GetArtifactArgs{Project: &p, PipelineId: &def, RunId: &run, ArtifactName: &stage})
			return err
		}},
		{"GET", "proj/_apis/pipelines/approvals", "7.1-preview.1", func(ctx context.Context, c pc) error {
			_, err := c.approval.QueryApprovals(ctx, pipelinesapproval.QueryApprovalsArgs{Project: &p, ApprovalIds: &[]uuid.UUID{approvalID}})
			return err
		}},
		{"GET", "proj/_apis/pipelines/approvals/00000000-0000-0000-0000-000000000101", "7.1-preview.1", func(ctx context.Context, c pc) error {
			_, err := c.approval.GetApproval(ctx, pipelinesapproval.GetApprovalArgs{Project: &p, ApprovalId: &approvalID})
			return err
		}},
		{"PATCH", "proj/_apis/pipelines/approvals", "7.1-preview.1", func(ctx context.Context, c pc) error {
			_, err := c.approval.UpdateApprovals(ctx, pipelinesapproval.UpdateApprovalsArgs{Project: &p, UpdateParameters: &[]pipelinesapproval.ApprovalUpdateParameters{}})
			return err
		}},
	}
	for i, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			org := fmt.Sprintf("pipeorg%d", i) // the SDK caches discovery per base URL
			c := &http.Client{}
			reg := ReplaceTripper(c)
			reg.StubLocations(org, PipelineLocations...)
			reg.Register(REST(tt.method, org+"/"+tt.path), JSONResponse(map[string]any{"count": 0, "value": []any{}}))
			sdk := azuredevops.NewClientWithOptions(&azuredevops.Connection{}, "https://dev.azure.com/"+org, azuredevops.WithHTTPClient(c))
			err := tt.call(context.Background(), pipelineClients{
				build: &build.ClientImpl{Client: *sdk}, pipelines: &pipelines.ClientImpl{Client: *sdk}, approval: &pipelinesapproval.ClientImpl{Client: *sdk},
			})
			if err != nil {
				t.Errorf("call: %v", err)
			}
			reg.Verify(t)
			assertAPIVersion(t, reg, tt.version)
		})
	}
}

// assertAPIVersion checks the last request asked for version, so the route and the version the
// SDK negotiates stay pinned together.
func assertAPIVersion(t *testing.T, reg *Registry, version string) {
	t.Helper()
	last := reg.Requests[len(reg.Requests)-1]
	if got := last.Header.Get("Accept"); !strings.HasSuffix(got, ";api-version="+version) {
		t.Errorf("Accept = %q, want api-version %s", got, version)
	}
}

// TestStubVSRM drives the real Classic Release SDK client through StubVSRM and ReleaseLocations
// and checks every call lands on the vsrm host at the expected path.
func TestStubVSRM(t *testing.T) {
	p, rel, env, phase, task, appr, def := "proj", 9, 11, 13, 4, 21, 3
	tests := []struct {
		method, path, version string
		call                  func(ctx context.Context, c release.Client) error
	}{
		{"GET", "proj/_apis/release/releases", "7.1-preview.8", func(ctx context.Context, c release.Client) error {
			_, err := c.GetReleases(ctx, release.GetReleasesArgs{Project: &p})
			return err
		}},
		{"GET", "proj/_apis/release/releases/9", "7.1-preview.8", func(ctx context.Context, c release.Client) error {
			_, err := c.GetRelease(ctx, release.GetReleaseArgs{Project: &p, ReleaseId: &rel})
			return err
		}},
		{"POST", "proj/_apis/release/releases", "7.1-preview.8", func(ctx context.Context, c release.Client) error {
			_, err := c.CreateRelease(ctx, release.CreateReleaseArgs{Project: &p, ReleaseStartMetadata: &release.ReleaseStartMetadata{}})
			return err
		}},
		{"GET", "proj/_apis/release/definitions", "7.1-preview.4", func(ctx context.Context, c release.Client) error {
			_, err := c.GetReleaseDefinitions(ctx, release.GetReleaseDefinitionsArgs{Project: &p})
			return err
		}},
		{"GET", "proj/_apis/release/definitions/3", "7.1-preview.4", func(ctx context.Context, c release.Client) error {
			_, err := c.GetReleaseDefinition(ctx, release.GetReleaseDefinitionArgs{Project: &p, DefinitionId: &def})
			return err
		}},
		{"PATCH", "proj/_apis/release/releases/9/environments/11", "7.1-preview.7", func(ctx context.Context, c release.Client) error {
			_, err := c.UpdateReleaseEnvironment(ctx, release.UpdateReleaseEnvironmentArgs{
				Project: &p, ReleaseId: &rel, EnvironmentId: &env, EnvironmentUpdateData: &release.ReleaseEnvironmentUpdateMetadata{},
			})
			return err
		}},
		{"GET", "proj/_apis/release/releases/9/environments/11/deployphases/13/tasks/4/logs", "7.1-preview.2", func(ctx context.Context, c release.Client) error {
			r, err := c.GetTaskLog(ctx, release.GetTaskLogArgs{Project: &p, ReleaseId: &rel, EnvironmentId: &env, ReleaseDeployPhaseId: &phase, TaskId: &task})
			if err == nil {
				r.Close()
			}
			return err
		}},
		{"GET", "proj/_apis/release/releases/9/logs", "7.1-preview.2", func(ctx context.Context, c release.Client) error {
			r, err := c.GetLogs(ctx, release.GetLogsArgs{Project: &p, ReleaseId: &rel})
			if err == nil {
				r.Close()
			}
			return err
		}},
		{"GET", "proj/_apis/release/approvals", "7.1-preview.3", func(ctx context.Context, c release.Client) error {
			_, err := c.GetApprovals(ctx, release.GetApprovalsArgs{Project: &p})
			return err
		}},
		{"PATCH", "proj/_apis/release/approvals/21", "7.1-preview.3", func(ctx context.Context, c release.Client) error {
			_, err := c.UpdateReleaseApproval(ctx, release.UpdateReleaseApprovalArgs{Project: &p, ApprovalId: &appr, Approval: &release.ReleaseApproval{}})
			return err
		}},
	}
	for i, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			org := fmt.Sprintf("vsrmorg%d", i) // the SDK caches discovery per base URL
			c := &http.Client{}
			reg := ReplaceTripper(c)
			reg.StubLocations(org, PipelineLocations...)
			reg.StubVSRM(org)
			reg.Register(REST(tt.method, org+"/"+tt.path), JSONResponse(map[string]any{"count": 0, "value": []any{}}))

			ctx := context.Background()
			sdk := azuredevops.NewClientWithOptions(&azuredevops.Connection{}, "https://dev.azure.com/"+org, azuredevops.WithHTTPClient(c))
			areas, err := sdk.GetResourceAreas(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var base string
			for _, a := range *areas {
				if *a.Id == release.ResourceAreaId {
					base = *a.LocationUrl
				}
			}
			if want := "https://vsrm.dev.azure.com/" + org + "/"; base != want {
				t.Fatalf("release base = %q, want %q", base, want)
			}
			client := &release.ClientImpl{Client: *azuredevops.NewClientWithOptions(&azuredevops.Connection{}, base, azuredevops.WithHTTPClient(c))}
			if err := tt.call(ctx, client); err != nil {
				t.Errorf("call: %v", err)
			}
			reg.Verify(t)
			assertAPIVersion(t, reg, tt.version)
			for _, req := range reg.Requests[len(reg.Requests)-2:] {
				if req.URL.Host != "vsrm.dev.azure.com" {
					t.Errorf("%s %s went to %s, want vsrm", req.Method, req.URL.Path, req.URL.Host)
				}
			}
		})
	}
}
