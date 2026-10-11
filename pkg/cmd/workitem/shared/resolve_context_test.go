package shared

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newContextTestClient returns a WorkItemClient for project "Proj" of an org named after the
// test (the SDK caches location discovery per base URL).
func newContextTestClient(t *testing.T) (*api.WorkItemClient, *httpmock.Registry, string) {
	t.Helper()
	org := "ctx-" + strings.NewReplacer("/", "-", " ", "-", "@", "-").Replace(strings.ToLower(t.Name()))
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.WorkItemLocations...)
	t.Cleanup(func() { reg.Verify(t) })
	hc := api.NewHTTPClient(api.ClientOptions{Transport: reg})
	return api.NewWorkItemClient(hc, "https://dev.azure.com/"+org, "Proj"), reg, org
}

type teamTestConfig map[string]string

func (m teamTestConfig) Get(host, key string) string {
	if v, ok := m[host+"|"+key]; ok {
		return v
	}
	return m["|"+key]
}
func (m teamTestConfig) Set(host, key, value string) { m[host+"|"+key] = value }
func (teamTestConfig) Write() error                  { return nil }

func TestResolveTeam(t *testing.T) {
	project := adocontext.Repo{Host: "fab.visualstudio.com", Org: "Fab", Project: "Proj"}
	tests := []struct {
		name string
		flag string
		cfg  teamTestConfig
		want string
	}{
		{"flag wins", "Flag Team", teamTestConfig{"dev.azure.com/fab/proj|team": "Config Team"}, "Flag Team"},
		{"project config", "", teamTestConfig{"dev.azure.com/fab/proj|team": "Config Team", "|team": "Global"}, "Config Team"},
		{"global config", "", teamTestConfig{"dev.azure.com/fab/other|team": "Other", "|team": "Global"}, "Global"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No stubs: these cases must not call the API.
			c := api.NewWorkItemClient(api.NewHTTPClient(api.ClientOptions{Transport: &httpmock.Registry{}}), "https://dev.azure.com/ctx-team", "Proj")
			got, err := ResolveTeam(context.Background(), c, tt.cfg, project, tt.flag)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveTeamProjectDefault(t *testing.T) {
	c, reg, org := newContextTestClient(t)
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/projects/Proj"), httpmock.StringResponse(
		`{"id":"a0000000-0000-0000-0000-000000000001","name":"Proj","defaultTeam":{"id":"a0000000-0000-0000-0000-000000000002","name":"Proj Team"}}`))
	got, err := ResolveTeam(context.Background(), c, teamTestConfig{}, adocontext.Repo{Org: "o", Project: "Proj"}, "")
	require.NoError(t, err)
	assert.Equal(t, "Proj Team", got)
}

// stubNodeTree answers GET classificationnodes/{group} at $depth depth with the tree of Proj.
func stubNodeTree(reg *httpmock.Registry, org, group, depth string) {
	root, child := `\Proj\Area`, "Team A"
	if group == "iterations" {
		root, child = `\Proj\Iteration`, "Release 1"
	}
	node := func(path string, children ...map[string]any) map[string]any {
		parts := strings.Split(path, `\`)
		return map[string]any{"id": len(path), "name": parts[len(parts)-1], "path": path, "children": children}
	}
	tree := node(root, node(root+`\`+child, node(root+`\`+child+`\Web`), node(root+`\`+child+`\Sprint 3`)), node(root+`\Other`))
	tree["name"] = "Proj"
	reg.Register(func(r *http.Request) bool {
		return httpmock.REST(http.MethodGet, org+"/Proj/_apis/wit/classificationnodes/"+group)(r) && r.URL.Query().Get("$depth") == depth
	}, httpmock.JSONResponse(tree))
}

func TestResolvePath(t *testing.T) {
	tests := []struct {
		name, group, path, depth, want string
	}{
		{"relative area", "areas", "Team A", "1", `Proj\Team A`},
		{"project prefix, any case", "areas", `proj\team a\WEB`, "2", `Proj\Team A\Web`},
		{"slashes", "iterations", "Proj/Release 1/Sprint 3", "2", `Proj\Release 1\Sprint 3`},
		{"project root", "areas", `\Proj`, "", "Proj"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, reg, org := newContextTestClient(t)
			stubNodeTree(reg, org, tt.group, tt.depth)
			got, err := ResolvePath(context.Background(), c, tt.group, tt.path)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolvePathNotFound(t *testing.T) {
	c, reg, org := newContextTestClient(t)
	stubNodeTree(reg, org, "iterations", "2")
	reg.Register(httpmock.REST(http.MethodGet, org+"/Proj/_apis/wit/classificationnodes/areas"),
		httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
	_, err := ResolvePath(context.Background(), c, "iterations", `Release 1\Nope`)
	require.EqualError(t, err, `iteration path "Release 1\Nope" not found in project Proj`)
	_, err = ResolvePath(context.Background(), c, "areas", "Boom")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestResolveIterationPath(t *testing.T) {
	c, reg, org := newContextTestClient(t)
	reg.Register(httpmock.REST(http.MethodGet, org+"/Proj/Web%20Team/_apis/work/teamsettings/iterations"),
		httpmock.StringResponse(`{"count":1,"value":[{"id":"a0000000-0000-0000-0000-000000000003","name":"Sprint 3","path":"Proj\\Sprint 3"}]}`))
	stubNodeTree(reg, org, "iterations", "1")
	ctx := context.Background()
	teamCalls := 0
	team := func() (string, error) { teamCalls++; return "Web Team", nil }

	got, err := ResolveIterationPath(ctx, c, "@Current", team)
	require.NoError(t, err)
	assert.Equal(t, `Proj\Sprint 3`, got)

	got, err = ResolveIterationPath(ctx, c, "Release 1", team)
	require.NoError(t, err)
	assert.Equal(t, `Proj\Release 1`, got)
	assert.Equal(t, 1, teamCalls, "the team is only resolved for @current")

	_, err = ResolveIterationPath(ctx, c, "@current", func() (string, error) { return "", assert.AnError })
	require.ErrorIs(t, err, assert.AnError)
}

func TestResolveIdentityFilter(t *testing.T) {
	ctx := context.Background()
	got, err := ResolveIdentityFilter(ctx, nil, "@ME")
	require.NoError(t, err)
	assert.Equal(t, IdentityFilter{Me: true}, got, "@me uses the WIQL @Me macro, no lookup")

	got, err = ResolveIdentityFilter(ctx, nil, "None")
	require.NoError(t, err)
	assert.Equal(t, IdentityFilter{None: true}, got)

	reg := &httpmock.Registry{}
	t.Cleanup(func() { reg.Verify(t) })
	org := "ctx-identity-filter"
	reg.StubLocations(org, httpmock.ResourceAreasLocation)
	reg.StubIMS(org)
	reg.Register(func(r *http.Request) bool {
		return r.URL.Host == "vssps.dev.azure.com" && r.URL.Query().Get("filterValue") == "Ann Lee"
	}, httpmock.JSONResponse(map[string]any{"count": 1, "value": []map[string]any{{
		"id": "a0000000-0000-0000-0000-000000000004", "providerDisplayName": "Ann Lee",
		"properties": map[string]any{"Account": map[string]any{"$type": "System.String", "$value": "ann@contoso.com"}},
	}}}))
	r := &api.IdentityResolver{HTTP: api.NewHTTPClient(api.ClientOptions{Transport: reg}), OrgURL: "https://dev.azure.com/" + org + "/"}
	got, err = ResolveIdentityFilter(ctx, r, "Ann Lee")
	require.NoError(t, err)
	assert.Equal(t, IdentityFilter{Value: "ann@contoso.com"}, got)
}
