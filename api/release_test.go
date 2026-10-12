package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
	"github.com/snake-in-prod/ado-gh/internal/pipelinefixtures"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newReleaseTestClient returns a ReleaseClient for project "proj" of org, whose Release area
// is on vsrm.dev.azure.com; every test needs its own org because the SDK caches discovery.
func newReleaseTestClient(t *testing.T, org string) (*ReleaseClient, *httpmock.Registry) {
	t.Helper()
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.ResourceAreasLocation)
	reg.StubVSRM(org)
	t.Cleanup(func() { reg.Verify(t) })
	return NewReleaseClient(NewHTTPClient(ClientOptions{Transport: reg}), "https://dev.azure.com/"+org, "proj"), reg
}

// relFixture serves the release fixture name with status 200.
func relFixture(t *testing.T, name string) httpmock.Responder {
	return httpmock.JSONResponse(json.RawMessage(pipelinefixtures.Read(t, name)))
}

// relSaw records each request responder answers into *reqs.
func relSaw(reqs *[]*http.Request, responder httpmock.Responder) httpmock.Responder {
	return func(r *http.Request) (*http.Response, error) {
		*reqs = append(*reqs, r)
		return responder(r)
	}
}

// TestReleaseClientHosts checks the Release host is found through the org's ResourceAreas on
// first use (vsrm for dev.azure.com and visualstudio.com orgs, the org URL itself when the
// org lists no Release area, as on Azure DevOps Server) and is not looked up again.
func TestReleaseClientHosts(t *testing.T) {
	tests := []struct {
		name, orgURL, host, path string
		stub                     func(reg *httpmock.Registry)
	}{
		{"dev.azure.com", "https://dev.azure.com/relhostorg", "vsrm.dev.azure.com", "/relhostorg/proj/_apis/release/releases/1",
			func(reg *httpmock.Registry) {
				reg.StubLocations("relhostorg", httpmock.ResourceAreasLocation)
				reg.StubVSRM("relhostorg")
			}},
		{"visualstudio.com", "https://relvsorg.visualstudio.com/", "relvsorg.vsrm.visualstudio.com", "/proj/_apis/release/releases/1",
			func(reg *httpmock.Registry) {
				// Stubs match once, in order: the org's discovery, then the vsrm host's.
				reg.StubLocations("", httpmock.ResourceAreasLocation)
				reg.Register(httpmock.REST(http.MethodGet, "_apis/ResourceAreas"), httpmock.JSONResponse(map[string]any{
					"count": 1, "value": []httpmock.ResourceArea{{
						ID: release.ResourceAreaId.String(), Name: "Release", LocationURL: "https://relvsorg.vsrm.visualstudio.com/",
					}},
				}))
				reg.StubLocations("", httpmock.ReleaseLocations...)
			}},
		{"no release area", "https://dev.azure.com/relonpremorg", "dev.azure.com", "/relonpremorg/proj/_apis/release/releases/1",
			func(reg *httpmock.Registry) {
				reg.StubLocations("relonpremorg", slices.Concat(httpmock.ReleaseLocations, []httpmock.Location{httpmock.ResourceAreasLocation})...)
				reg.StubResourceAreas("relonpremorg")
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &httpmock.Registry{}
			tt.stub(reg)
			var reqs []*http.Request
			reg.Register(httpmock.REST(http.MethodGet, tt.path[1:]), relSaw(&reqs, relFixture(t, "release.json")))
			reg.Register(httpmock.REST(http.MethodGet, tt.path[1:]+"/environments/2/deployphases/1/tasks/3/logs"),
				relSaw(&reqs, httpmock.StringResponse("line\n")))
			c := NewReleaseClient(NewHTTPClient(ClientOptions{Transport: reg}), tt.orgURL, "proj")

			r, err := c.ReleaseByID(context.Background(), 1)
			require.NoError(t, err)
			assert.Equal(t, "Release-1", *r.Name)
			assert.Len(t, *r.Environments, 2)

			rc, err := c.TaskLog(context.Background(), 1, 2, 1, 3)
			require.NoError(t, err)
			b, _ := io.ReadAll(rc)
			rc.Close()
			assert.Equal(t, "line\n", string(b))

			require.Len(t, reqs, 2)
			for _, req := range reqs {
				assert.Equal(t, tt.host, req.URL.Host)
			}
			assert.Equal(t, "tasks", reqs[0].URL.Query().Get("$expand"))
			reg.Verify(t)
		})
	}
}

func TestReleaseClientAreaError(t *testing.T) {
	reg := &httpmock.Registry{}
	reg.StubLocations("relareaerrorg", httpmock.ResourceAreasLocation)
	reg.Register(httpmock.REST(http.MethodGet, "relareaerrorg/_apis/ResourceAreas"),
		httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "TF400813: boom"}))
	c := NewReleaseClient(NewHTTPClient(ClientOptions{Transport: reg}), "https://dev.azure.com/relareaerrorg", "proj")

	_, err := c.ReleaseByID(context.Background(), 1)
	var he *HTTPError
	require.ErrorAs(t, err, &he)
	assert.Equal(t, http.StatusInternalServerError, he.StatusCode)
	reg.Verify(t)
}

func TestNewReleaseClient(t *testing.T) {
	c := NewReleaseClient(http.DefaultClient, "https://dev.azure.com/o", "proj")
	assert.Same(t, http.DefaultClient, c.HTTP)
	assert.Equal(t, "https://dev.azure.com/o", c.OrgURL)
	assert.Equal(t, "proj", c.Project)
	assert.Nil(t, c.Release)
}

// TestReleaseClient_Releases pins the pager: the int continuation token is read from the
// response header as a string and sent back as a number, $top is the limit, and the result is
// cut at the limit.
func TestReleaseClient_Releases(t *testing.T) {
	org := "relreleasesorg"
	c, reg := newReleaseTestClient(t, org)
	var reqs []*http.Request
	path := org + "/proj/_apis/release/releases"
	reg.Register(httpmock.REST(http.MethodGet, path), relSaw(&reqs,
		httpmock.WithHeader(httpmock.StringResponse(`{"count":2,"value":[{"id":9},{"id":8}]}`), azuredevops.HeaderKeyContinuationToken, "7")))
	reg.Register(httpmock.REST(http.MethodGet, path), relSaw(&reqs,
		httpmock.WithHeader(httpmock.StringResponse(`{"count":2,"value":[{"id":7},{"id":6}]}`), azuredevops.HeaderKeyContinuationToken, "5")))

	def := 1
	got, err := c.Releases(context.Background(), release.GetReleasesArgs{DefinitionId: &def}, 3)
	require.NoError(t, err)
	var ids []int
	for _, r := range got {
		ids = append(ids, *r.Id)
	}
	assert.Equal(t, []int{9, 8, 7}, ids)
	require.Len(t, reqs, 2)
	assert.Equal(t, "1", reqs[0].URL.Query().Get("definitionId"))
	assert.Equal(t, "3", reqs[0].URL.Query().Get("$top"))
	assert.Empty(t, reqs[0].URL.Query().Get("continuationToken"))
	assert.Equal(t, "7", reqs[1].URL.Query().Get("continuationToken"))
}

func TestReleaseClient_ReleasesAllPages(t *testing.T) {
	org := "relallorg"
	c, reg := newReleaseTestClient(t, org)
	path := org + "/proj/_apis/release/releases"
	var reqs []*http.Request
	reg.Register(httpmock.REST(http.MethodGet, path), relSaw(&reqs, relFixture(t, "releases.json")))

	got, err := c.Releases(context.Background(), release.GetReleasesArgs{}, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Release-1", *got[0].Name)
	assert.Empty(t, reqs[0].URL.Query().Get("$top"))
}

func TestReleaseClient_ReleasesBadToken(t *testing.T) {
	org := "relbadtokenorg"
	c, reg := newReleaseTestClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/release/releases"),
		httpmock.WithHeader(httpmock.StringResponse(`{"count":1,"value":[{"id":9}]}`), azuredevops.HeaderKeyContinuationToken, "abc"))

	_, err := c.Releases(context.Background(), release.GetReleasesArgs{}, 0)
	assert.ErrorContains(t, err, `continuation token "abc"`)
}

func TestReleaseClient_ReleasesError(t *testing.T) {
	org := "relerrorg"
	c, reg := newReleaseTestClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/release/releases/404"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "VS402853: release not found"}))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/release/releases"),
		httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))

	_, err := c.ReleaseByID(context.Background(), 404)
	assert.True(t, IsNotFound(err), "err = %v", err)
	_, err = c.Releases(context.Background(), release.GetReleasesArgs{}, 0)
	var he *HTTPError
	require.ErrorAs(t, err, &he)
	assert.Equal(t, http.StatusInternalServerError, he.StatusCode)
}

// TestReleaseClient_Definitions checks the definitions token, a string, is sent back verbatim.
func TestReleaseClient_Definitions(t *testing.T) {
	org := "reldefsorg"
	c, reg := newReleaseTestClient(t, org)
	var reqs []*http.Request
	path := org + "/proj/_apis/release/definitions"
	reg.Register(httpmock.REST(http.MethodGet, path), relSaw(&reqs,
		httpmock.WithHeader(relFixture(t, "release_definitions.json"), azuredevops.HeaderKeyContinuationToken, "ado-live-release")))
	reg.Register(httpmock.REST(http.MethodGet, path), relSaw(&reqs, httpmock.StringResponse(`{"count":1,"value":[{"id":2,"name":"web"}]}`)))

	text := "a"
	got, err := c.Definitions(context.Background(), release.GetReleaseDefinitionsArgs{SearchText: &text}, 0)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "ado-live-release", *got[0].Name)
	assert.Equal(t, `\ado-live`, *got[0].Path)
	assert.Equal(t, "web", *got[1].Name)
	require.Len(t, reqs, 2)
	assert.Equal(t, "a", reqs[0].URL.Query().Get("searchText"))
	assert.Equal(t, "ado-live-release", reqs[1].URL.Query().Get("continuationToken"))
}

func TestReleaseClient_Approvals(t *testing.T) {
	org := "relapprorg"
	c, reg := newReleaseTestClient(t, org)
	var reqs []*http.Request
	path := org + "/proj/_apis/release/approvals"
	reg.Register(httpmock.REST(http.MethodGet, path), relSaw(&reqs,
		httpmock.WithHeader(relFixture(t, "release_approvals.json"), azuredevops.HeaderKeyContinuationToken, "4")))
	reg.Register(httpmock.REST(http.MethodGet, path), relSaw(&reqs, httpmock.StringResponse(`{"count":0,"value":[]}`)))

	typ := release.ApprovalTypeValues.All
	got, err := c.Approvals(context.Background(), release.GetApprovalsArgs{ReleaseIdsFilter: &[]int{1, 2}, TypeFilter: &typ}, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 4, *got[0].Id)
	assert.Equal(t, release.ApprovalStatusValues.Pending, *got[0].Status)
	require.Len(t, reqs, 2)
	assert.Equal(t, "1,2", reqs[0].URL.Query().Get("releaseIdsFilter"))
	assert.Equal(t, "all", reqs[0].URL.Query().Get("typeFilter"))
	assert.Equal(t, "4", reqs[1].URL.Query().Get("continuationToken"))
}

func TestReleaseClient_ApprovalsBadToken(t *testing.T) {
	org := "relapprbadorg"
	c, reg := newReleaseTestClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/release/approvals"),
		httpmock.WithHeader(httpmock.StringResponse(`{"count":0,"value":[]}`), azuredevops.HeaderKeyContinuationToken, "x"))

	_, err := c.Approvals(context.Background(), release.GetApprovalsArgs{}, 0)
	assert.ErrorContains(t, err, `continuation token "x"`)
}

func TestReleaseClient_UpdateApproval(t *testing.T) {
	org := "relupdapprorg"
	c, reg := newReleaseTestClient(t, org)
	var body string
	reg.Register(httpmock.REST(http.MethodPatch, org+"/proj/_apis/release/approvals/4"), func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		return httpmock.JSONResponse(map[string]any{"id": 4, "status": "approved"})(r)
	})
	reg.Register(httpmock.REST(http.MethodPatch, org+"/proj/_apis/release/approvals/5"),
		httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "not yours"}))

	got, err := c.UpdateApproval(context.Background(), 4, release.ApprovalStatusValues.Approved, "ship it")
	require.NoError(t, err)
	assert.Equal(t, release.ApprovalStatusValues.Approved, *got.Status)
	assert.JSONEq(t, `{"status":"approved","comments":"ship it"}`, body)

	_, err = c.UpdateApproval(context.Background(), 5, release.ApprovalStatusValues.Rejected, "")
	var he *HTTPError
	require.ErrorAs(t, err, &he)
	assert.Equal(t, http.StatusBadRequest, he.StatusCode)

	bad := NewReleaseClient(NewHTTPClient(ClientOptions{Transport: &httpmock.Registry{}}), "https://dev.azure.com/relupdapprdisc", "proj")
	_, err = bad.UpdateApproval(context.Background(), 4, release.ApprovalStatusValues.Approved, "")
	assert.ErrorContains(t, err, "no registered HTTP stubs matched OPTIONS https://dev.azure.com/relupdapprdisc/_apis", "discovery fails")
}

func TestReleaseClient_Logs(t *testing.T) {
	org := "rellogsorg"
	c, reg := newReleaseTestClient(t, org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/release/releases/1/logs"), httpmock.StringResponse("PK zip"))
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/release/releases/1/environments/2/deployphases/1/tasks/0/logs"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "log not found"}))

	rc, err := c.Logs(context.Background(), 1)
	require.NoError(t, err)
	b, _ := io.ReadAll(rc)
	rc.Close()
	assert.Equal(t, "PK zip", string(b))

	_, err = c.TaskLog(context.Background(), 1, 2, 1, 0)
	assert.True(t, IsNotFound(err), "err = %v", err)
}

// TestReleaseClientDiscoveryErrors checks every wrapper returns the area lookup's error.
func TestReleaseClientDiscoveryErrors(t *testing.T) {
	ctx := context.Background()
	calls := map[string]func(c *ReleaseClient) error{
		"Releases": func(c *ReleaseClient) error { _, err := c.Releases(ctx, release.GetReleasesArgs{}, 0); return err },
		"Definitions": func(c *ReleaseClient) error {
			_, err := c.Definitions(ctx, release.GetReleaseDefinitionsArgs{}, 0)
			return err
		},
		"Approvals": func(c *ReleaseClient) error { _, err := c.Approvals(ctx, release.GetApprovalsArgs{}, 0); return err },
		"TaskLog":   func(c *ReleaseClient) error { _, err := c.TaskLog(ctx, 1, 1, 1, 1); return err },
		"Logs":      func(c *ReleaseClient) error { _, err := c.Logs(ctx, 1); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			reg := &httpmock.Registry{} // no stubs: discovery fails
			c := NewReleaseClient(NewHTTPClient(ClientOptions{Transport: reg}), "https://dev.azure.com/reldisc"+name, "proj")
			assert.Error(t, call(c))
		})
	}
}
