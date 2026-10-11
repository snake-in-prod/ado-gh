package shared

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/internal/git/gittest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const (
	linkTestProjectID = "11111111-1111-1111-1111-111111111111"
	linkTestRepoID    = "22222222-2222-2222-2222-222222222222"
	linkTestSHA       = "0123456789abcdef0123456789abcdef01234567"
)

// linkTestEnv is a LinkParser on project "My Proj" and repository "web" of an org named after
// the test (the SDK caches location discovery per base URL).
type linkTestEnv struct {
	org string
	reg *httpmock.Registry
	git *gittest.Runner
	f   *cmdutil.Factory
	p   *LinkParser
}

func newLinkTestEnv(t *testing.T) *linkTestEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_REPO", "")
	keyring.MockInit()
	ios, _, _, _ := iostreams.Test()
	org := "links-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &linkTestEnv{org: org, reg: &httpmock.Registry{}, git: &gittest.Runner{}}
	e.reg.StubLocations(org, slices.Concat(httpmock.WorkItemLocations, httpmock.GitPRLocations)...)
	t.Cleanup(func() { e.reg.Verify(t) })
	cfg, err := config.Load()
	require.NoError(t, err)
	e.f = &cmdutil.Factory{
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: org, Project: "My Proj", Name: "web"}, nil
		},
		GitClient: &git.Client{Run: e.git.Run},
	}
	hc := api.NewHTTPClient(api.ClientOptions{Transport: e.reg})
	orgURL := "https://dev.azure.com/" + org + "/"
	e.p = &LinkParser{F: e.f, Client: api.NewWorkItemClient(hc, orgURL, "My Proj"), OrgURL: orgURL}
	return e
}

func (e *linkTestEnv) stubTypes() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/wit/workitemrelationtypes"), httpmock.StringResponse(`{"count":7,"value":[
		{"name":"Parent","referenceName":"System.LinkTypes.Hierarchy-Reverse","attributes":{"usage":"workItemLink","enabled":true}},
		{"name":"Child","referenceName":"System.LinkTypes.Hierarchy-Forward","attributes":{"usage":"workItemLink","enabled":true}},
		{"name":"Related","referenceName":"System.LinkTypes.Related","attributes":{"usage":"workItemLink","enabled":true}},
		{"name":"Tested By","referenceName":"Microsoft.VSTS.Common.TestedBy-Forward","attributes":{"usage":"workItemLink","enabled":true}},
		{"name":"Blocks","referenceName":"Contoso.Blocks-Forward","attributes":{"usage":"workItemLink","enabled":true}},
		{"name":"Hyperlink","referenceName":"Hyperlink","attributes":{"usage":"resourceLink"}},
		{"name":"Artifact Link","referenceName":"ArtifactLink","attributes":{"usage":"resourceLink"}}]}`))
}

func (e *linkTestEnv) stubRepo() {
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web"), httpmock.JSONResponse(map[string]any{
		"id": linkTestRepoID, "name": "web", "project": map[string]any{"id": linkTestProjectID, "name": "My Proj"},
	}))
}

func (e *linkTestEnv) wiURL(id string) string {
	return "https://dev.azure.com/" + e.org + "/_apis/wit/workItems/" + id
}

func linkTestArtifact(url, name string) Link {
	return Link{Rel: "ArtifactLink", URL: url, Attributes: map[string]any{"name": name}}
}

func TestLinkParserWorkItemTypes(t *testing.T) {
	e := newLinkTestEnv(t)
	e.stubTypes() // fetched once for every case
	ctx := context.Background()
	tests := []struct {
		arg     string
		want    Link
		wantErr string
	}{
		{"parent:7", Link{Rel: "System.LinkTypes.Hierarchy-Reverse", URL: e.wiURL("7"), ID: 7}, ""},
		{"tested by:#8", Link{Rel: "Microsoft.VSTS.Common.TestedBy-Forward", URL: e.wiURL("8"), ID: 8}, ""},
		{"System.LinkTypes.Hierarchy-Reverse: 9", Link{Rel: "System.LinkTypes.Hierarchy-Reverse", URL: e.wiURL("9"), ID: 9}, ""},
		{"BLOCKS:10", Link{Rel: "Contoso.Blocks-Forward", URL: e.wiURL("10"), ID: 10}, ""},
		{"contoso.blocks-forward:11", Link{Rel: "Contoso.Blocks-Forward", URL: e.wiURL("11"), ID: 11}, ""},
		{"url:https://x.test/a:b", Link{Rel: "Hyperlink", URL: "https://x.test/a:b"}, ""},
		{"Hyperlink:https://x.test", Link{Rel: "Hyperlink", URL: "https://x.test"}, ""},
		{"url:nope", Link{}, "a hyperlink needs a URL"},
		{"parent", Link{}, "expected <type>:<target>"},
		{"parent:", Link{}, "expected <type>:<target>"},
		{"parent:x", Link{}, "invalid work item reference"},
		{"ArtifactLink:1", Link{}, `unknown link type "ArtifactLink"; valid types: Parent, Child, Related, Tested By, Blocks, pr, commit, branch, url`},
		{"commit:xyz", Link{}, `invalid commit "xyz"`},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			got, err := e.p.ParseArg(ctx, tt.arg)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLinkParserRelationTypesError(t *testing.T) {
	e := newLinkTestEnv(t)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/_apis/wit/workitemrelationtypes"),
		httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
	_, err := e.p.Parse(context.Background(), "related", "1")
	assert.ErrorContains(t, err, "boom")
}

func TestLinkParserPullRequest(t *testing.T) {
	e := newLinkTestEnv(t)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/45"), httpmock.JSONResponse(map[string]any{
		"pullRequestId": 45, "repository": map[string]any{"id": linkTestRepoID, "name": "web", "project": map[string]any{"id": linkTestProjectID}},
	}))
	got, err := e.p.ParseArg(context.Background(), "pr:!45")
	require.NoError(t, err)
	assert.Equal(t, linkTestArtifact("vstfs:///Git/PullRequestId/"+linkTestProjectID+"%2F"+linkTestRepoID+"%2F45", "Pull Request"), got)
}

func TestLinkParserPullRequestWithoutIDs(t *testing.T) {
	e := newLinkTestEnv(t)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/46"),
		httpmock.JSONResponse(map[string]any{"pullRequestId": 46, "repository": map[string]any{"name": "web"}}))
	_, err := e.p.Parse(context.Background(), "PR", "46")
	assert.ErrorContains(t, err, "cannot link pull request !46")
}

func TestLinkParserBranch(t *testing.T) {
	e := newLinkTestEnv(t)
	e.stubRepo() // looked up once for both links
	ctx := context.Background()
	got, err := e.p.Parse(ctx, "branch", "feature/x")
	require.NoError(t, err)
	assert.Equal(t, linkTestArtifact("vstfs:///Git/Ref/"+linkTestProjectID+"%2F"+linkTestRepoID+"%2FGBfeature%2Fx", "Branch"), got)
	got, err = e.p.Parse(ctx, "Branch", "refs/heads/main")
	require.NoError(t, err)
	assert.Equal(t, "vstfs:///Git/Ref/"+linkTestProjectID+"%2F"+linkTestRepoID+"%2FGBmain", got.URL)
}

func TestLinkParserRepoErrors(t *testing.T) {
	e := newLinkTestEnv(t)
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/repositories/web"),
		httpmock.JSONResponse(map[string]any{"name": "web"}))
	_, err := e.p.Parse(context.Background(), "branch", "main")
	assert.ErrorContains(t, err, "the IDs of repository My Proj/web are unknown")
}

func TestLinkParserArtifactsNeedRepoWithProjectOverride(t *testing.T) {
	for _, typ := range []string{"pr", "commit", "branch"} {
		t.Run(typ, func(t *testing.T) {
			// No HTTP client or git: nothing may be called.
			p := &LinkParser{F: &cmdutil.Factory{RepoOverride: "org/My Proj"}}
			_, err := p.Parse(context.Background(), typ, "0123abc")
			var fe *cmdutil.FlagError
			require.ErrorAs(t, err, &fe)
			assert.ErrorContains(t, err, "-R org/My Proj names only ORG/PROJECT; use -R ORG/PROJECT/REPO")
		})
	}
}

func TestLinkParserCommit(t *testing.T) {
	commitURL := "vstfs:///Git/Commit/" + linkTestProjectID + "%2F" + linkTestRepoID + "%2F" + linkTestSHA

	t.Run("full sha needs no lookup", func(t *testing.T) {
		e := newLinkTestEnv(t)
		e.stubRepo()
		got, err := e.p.Parse(context.Background(), "commit", strings.ToUpper(linkTestSHA))
		require.NoError(t, err)
		assert.Equal(t, linkTestArtifact(commitURL, "Fixed in Commit"), got)
		assert.Empty(t, e.git.Calls)
	})
	t.Run("short sha resolved by local git", func(t *testing.T) {
		e := newLinkTestEnv(t)
		e.stubRepo()
		e.git.Replies = map[string]string{"rev-parse --verify --quiet 0123abc^{commit}": linkTestSHA + "\n"}
		got, err := e.p.Parse(context.Background(), "commit", "0123abc")
		require.NoError(t, err)
		assert.Equal(t, commitURL, got.URL)
	})
	t.Run("short sha unknown to local git", func(t *testing.T) {
		e := newLinkTestEnv(t)
		e.stubRepo()
		_, err := e.p.Parse(context.Background(), "commit", "0123abc")
		assert.EqualError(t, err, "commit 0123abc not found in the local clone; fetch it or pass the full 40-character SHA")
		assert.Equal(t, []string{"rev-parse --verify --quiet 0123abc^{commit}"}, e.git.Calls)
	})
	t.Run("short sha with -R needs the full sha", func(t *testing.T) {
		e := newLinkTestEnv(t)
		e.f.RepoOverride = e.org + "/My Proj/web"
		e.stubRepo()
		_, err := e.p.Parse(context.Background(), "commit", "0123abc")
		assert.EqualError(t, err, "cannot resolve short commit 0123abc without a local clone; pass the full 40-character SHA")
		assert.Empty(t, e.git.Calls)
	})
}

func TestLinkFlags(t *testing.T) {
	e := newLinkTestEnv(t)
	e.stubTypes()
	var lf LinkFlags
	fl := pflag.NewFlagSet("link", pflag.ContinueOnError)
	lf.Register(fl)
	require.NoError(t, fl.Parse([]string{
		"--url", "https://x.test", "--child", "2", "--related", "#4", "--parent", "1", "--child", "3",
		"--link", "Tested By = 5", "--link", "blocks=6",
	}))
	got, err := lf.Parse(context.Background(), e.p)
	require.NoError(t, err)
	assert.Equal(t, []Link{
		{Rel: "System.LinkTypes.Hierarchy-Reverse", URL: e.wiURL("1"), ID: 1},
		{Rel: "System.LinkTypes.Hierarchy-Forward", URL: e.wiURL("2"), ID: 2},
		{Rel: "System.LinkTypes.Hierarchy-Forward", URL: e.wiURL("3"), ID: 3},
		{Rel: "System.LinkTypes.Related", URL: e.wiURL("4"), ID: 4},
		{Rel: "Microsoft.VSTS.Common.TestedBy-Forward", URL: e.wiURL("5"), ID: 5},
		{Rel: "Contoso.Blocks-Forward", URL: e.wiURL("6"), ID: 6},
		{Rel: "Hyperlink", URL: "https://x.test"},
	}, got)
}

func TestLinkFlagsArtifacts(t *testing.T) {
	e := newLinkTestEnv(t)
	e.stubRepo()
	e.reg.Register(httpmock.REST(http.MethodGet, e.org+"/My%20Proj/_apis/git/pullRequests/7"), httpmock.JSONResponse(map[string]any{
		"pullRequestId": 7, "repository": map[string]any{"id": linkTestRepoID, "name": "web", "project": map[string]any{"id": linkTestProjectID}},
	}))
	lf := LinkFlags{PRs: []string{"7"}, Commits: []string{linkTestSHA}, Branches: []string{"dev"}}
	got, err := lf.Parse(context.Background(), e.p)
	require.NoError(t, err)
	ids := linkTestProjectID + "%2F" + linkTestRepoID + "%2F"
	assert.Equal(t, []Link{
		linkTestArtifact("vstfs:///Git/PullRequestId/"+ids+"7", "Pull Request"),
		linkTestArtifact("vstfs:///Git/Commit/"+ids+linkTestSHA, "Fixed in Commit"),
		linkTestArtifact("vstfs:///Git/Ref/"+ids+"GBdev", "Branch"),
	}, got)
}

func TestLinkFlagsErrors(t *testing.T) {
	for _, tt := range []struct {
		name    string
		flags   LinkFlags
		wantErr string
	}{
		{"none", LinkFlags{}, "specify a link: --parent, --child"},
		{"link without id", LinkFlags{Links: []string{"Predecessor"}}, `invalid --link "Predecessor": expected <type>=<id>`},
		{"link empty type", LinkFlags{Links: []string{"=3"}}, `invalid --link "=3"`},
		{"bad url", LinkFlags{URLs: []string{"x"}}, "a hyperlink needs a URL"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.flags.Parse(context.Background(), &LinkParser{})
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestLinkMatches(t *testing.T) {
	wi := Link{Rel: "System.LinkTypes.Related", URL: "https://dev.azure.com/o/_apis/wit/workItems/5", ID: 5}
	assert.True(t, wi.Matches(api.WorkItemRelation{Rel: "system.linktypes.related", URL: "https://o.visualstudio.com/_apis/wit/workitems/5"}))
	assert.False(t, wi.Matches(api.WorkItemRelation{Rel: "System.LinkTypes.Related", URL: "https://dev.azure.com/o/_apis/wit/workItems/15"}))
	assert.False(t, wi.Matches(api.WorkItemRelation{Rel: "System.LinkTypes.Hierarchy-Forward", URL: wi.URL}))
	assert.Equal(t, "System.LinkTypes.Related:5", wi.String())

	h := Link{Rel: "Hyperlink", URL: "https://x.test/A"}
	assert.True(t, h.Matches(api.WorkItemRelation{Rel: "Hyperlink", URL: "https://x.test/a"}))
	assert.False(t, h.Matches(api.WorkItemRelation{Rel: "Hyperlink", URL: "https://x.test/b"}))
	assert.Equal(t, "Hyperlink:https://x.test/A", h.String())

	b := linkTestArtifact("vstfs:///Git/Ref/P%2FR%2FGBmain", "Branch")
	assert.True(t, b.Matches(api.WorkItemRelation{Rel: "ArtifactLink", URL: "vstfs:///Git/Ref/p%2fr%2fGBmain"}))
}
