package shared

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// testFactory returns a Factory whose base repo is org/proj/web on dev.azure.com, with a PAT
// from ADO_TOKEN, HTTP served by reg and git returning branch (or err) as the current branch.
func testFactory(t *testing.T, org string, reg *httpmock.Registry, branch string, branchErr error) *cmdutil.Factory {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	cfg, err := config.Load()
	require.NoError(t, err)
	ios, _, _, _ := iostreams.Test()
	return &cmdutil.Factory{
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: org, Project: "proj", Name: "web"}, nil
		},
		GitClient: &git.Client{Run: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			if branchErr != nil {
				return nil, branchErr
			}
			return []byte("refs/heads/" + branch + "\n"), nil
		}},
	}
}

func prJSON(id int, status, repo string) map[string]any {
	return map[string]any{"pullRequestId": id, "status": status, "repository": map[string]any{"name": repo}}
}

func list(prs ...map[string]any) httpmock.Responder {
	return httpmock.JSONResponse(map[string]any{"count": len(prs), "value": prs})
}

func TestFind(t *testing.T) {
	const listPath = "proj/_apis/git/repositories/web/pullRequests"
	tests := []struct {
		name       string
		ref        string
		branch     string
		branchErr  error
		base       *string // base path of the stubs; defaults to the factory's org
		stub       func(reg *httpmock.Registry, org string)
		wantID     int
		wantRepo   string
		wantSource string // searchCriteria.sourceRefName of the list call
		wantErr    string
	}{
		{
			name: "number",
			ref:  "123",
			stub: func(reg *httpmock.Registry, org string) {
				reg.Register(httpmock.REST(http.MethodGet, org+"proj/_apis/git/pullRequests/123"), httpmock.JSONResponse(prJSON(123, "active", "web")))
			},
			wantID: 123, wantRepo: "web",
		},
		{
			name: "bang number",
			ref:  "!45",
			stub: func(reg *httpmock.Registry, org string) {
				reg.Register(httpmock.REST(http.MethodGet, org+"proj/_apis/git/pullRequests/45"), httpmock.JSONResponse(prJSON(45, "completed", "WEB")))
			},
			wantID: 45, wantRepo: "web",
		},
		{
			name: "number not found",
			ref:  "9",
			stub: func(reg *httpmock.Registry, org string) {
				reg.Register(httpmock.REST(http.MethodGet, org+"proj/_apis/git/pullRequests/9"),
					httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401180: The requested pull request was not found."}))
			},
			wantErr: "pull request !9 not found in proj/web",
		},
		{
			name: "number of another repository",
			ref:  "10",
			stub: func(reg *httpmock.Registry, org string) {
				reg.Register(httpmock.REST(http.MethodGet, org+"proj/_apis/git/pullRequests/10"), httpmock.JSONResponse(prJSON(10, "active", "api")))
			},
			wantErr: "pull request !10 not found in proj/web",
		},
		{
			name: "server error",
			ref:  "11",
			stub: func(reg *httpmock.Registry, org string) {
				reg.Register(httpmock.REST(http.MethodGet, org+"proj/_apis/git/pullRequests/11"),
					httpmock.StatusJSONResponse(http.StatusInternalServerError, map[string]any{"message": "boom"}))
			},
			wantErr: "boom (HTTP 500)",
		},
		{
			name: "URL names its own repository",
			ref:  "https://dev.azure.com/urlorg/Other%20Proj/_git/api/pullrequest/77?_a=files",
			base: ptr("urlorg"),
			stub: func(reg *httpmock.Registry, org string) {
				reg.Register(httpmock.REST(http.MethodGet, org+"Other%20Proj/_apis/git/pullRequests/77"), httpmock.JSONResponse(prJSON(77, "active", "api")))
			},
			wantID: 77, wantRepo: "api",
		},
		{
			name: "visualstudio.com URL",
			ref:  "https://vsorg.visualstudio.com/proj/_git/web/pullrequest/5",
			base: ptr(""),
			stub: func(reg *httpmock.Registry, org string) {
				reg.Register(httpmock.REST(http.MethodGet, org+"proj/_apis/git/pullRequests/5"), httpmock.JSONResponse(prJSON(5, "active", "web")))
			},
			wantID: 5, wantRepo: "web",
		},
		{
			name: "branch name prefers an active PR",
			ref:  "feature/x",
			stub: func(reg *httpmock.Registry, org string) {
				reg.Register(httpmock.REST(http.MethodGet, org+listPath), list(prJSON(3, "completed", "web"), prJSON(2, "active", "web")))
				reg.Register(httpmock.REST(http.MethodGet, org+listPath), list())
			},
			wantID: 2, wantRepo: "web", wantSource: "refs/heads/feature/x",
		},
		{
			name: "branch name falls back to the newest PR",
			ref:  "old",
			stub: func(reg *httpmock.Registry, org string) {
				reg.Register(httpmock.REST(http.MethodGet, org+listPath), list(prJSON(8, "abandoned", "web"), prJSON(4, "completed", "web")))
				reg.Register(httpmock.REST(http.MethodGet, org+listPath), list())
			},
			wantID: 8, wantRepo: "web", wantSource: "refs/heads/old",
		},
		{
			name:   "current branch",
			branch: "topic",
			stub: func(reg *httpmock.Registry, org string) {
				reg.Register(httpmock.REST(http.MethodGet, org+listPath), list(prJSON(6, "active", "web")))
				reg.Register(httpmock.REST(http.MethodGet, org+listPath), list())
			},
			wantID: 6, wantRepo: "web", wantSource: "refs/heads/topic",
		},
		{
			name:   "no PR for branch",
			branch: "lonely",
			stub: func(reg *httpmock.Registry, org string) {
				reg.Register(httpmock.REST(http.MethodGet, org+listPath), list())
			},
			wantErr: `no pull request found for branch "lonely" in proj/web`,
		},
		{
			name:      "detached HEAD",
			branchErr: &git.Error{ExitCode: 1},
			wantErr:   "not on any branch; pass a pull request number, URL or branch name",
		},
		{
			name:      "git failure",
			branchErr: errors.New("git exploded"),
			wantErr:   "git exploded",
		},
		{name: "bang without number", ref: "!abc", wantErr: `invalid pull request number "!abc"`},
		{name: "zero", ref: "0", wantErr: `invalid pull request number "0"`},
		{name: "URL without pullrequest", ref: "https://dev.azure.com/o/p/_git/r", wantErr: "invalid pull request URL"},
		{name: "URL with bad id", ref: "https://dev.azure.com/o/p/_git/r/pullrequest/x", wantErr: "invalid pull request URL"},
		{name: "http URL", ref: "http://dev.azure.com/o/p/_git/r/pullrequest/1", wantErr: "invalid pull request URL"},
		{name: "non-ADO URL", ref: "https://github.com/o/r/pullrequest/1", wantErr: "invalid pull request URL"},
		{name: "unparsable URL", ref: "https://user:secret@[::1/x/pullrequest/1", wantErr: "invalid pull request URL"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org := fmt.Sprintf("findorg%d", i)
			base := org
			if tt.base != nil {
				base = *tt.base
			}
			reg := &httpmock.Registry{}
			if tt.stub != nil {
				reg.StubLocations(base, httpmock.GitPRLocations...)
				prefix := base + "/"
				if base == "" {
					prefix = ""
				}
				tt.stub(reg, prefix)
			}
			f := testFactory(t, org, reg, tt.branch, tt.branchErr)

			pr, c, repo, err := Find(context.Background(), f, tt.ref)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.NotContains(t, err.Error(), "secret")
				return
			}
			require.NoError(t, err)
			reg.Verify(t)
			assert.Equal(t, tt.wantID, *pr.PullRequestId)
			assert.Equal(t, tt.wantRepo, repo.Name)
			assert.Equal(t, repo.Name, c.Repo)
			assert.Equal(t, repo.Project, c.Project)
			if tt.wantSource != "" {
				// The list request, found by its query: the OPTIONS discovery request is
				// only sent on the first run in a process (-count>1).
				i := slices.IndexFunc(reg.Requests, func(r *http.Request) bool {
					return r.URL.Query().Has("searchCriteria.status")
				})
				require.GreaterOrEqual(t, i, 0, "no pull request list request")
				last := reg.Requests[i].URL.Query()
				assert.Equal(t, tt.wantSource, last.Get("searchCriteria.sourceRefName"))
				assert.Equal(t, "all", last.Get("searchCriteria.status"))
				assert.Equal(t, "30", last.Get("$top"))
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestFindNotLoggedIn(t *testing.T) {
	f := testFactory(t, "nologinorg", &httpmock.Registry{}, "", nil)
	t.Setenv("ADO_TOKEN", "")
	_, _, _, err := Find(context.Background(), f, "1")
	var authErr *cmdutil.AuthError
	assert.ErrorAs(t, err, &authErr)
}

func TestFindBaseRepoError(t *testing.T) {
	f := testFactory(t, "x", &httpmock.Registry{}, "", nil)
	f.BaseRepo = func() (adocontext.Repo, error) {
		return adocontext.Repo{}, errors.New("not an Azure DevOps repository")
	}
	_, _, _, err := Find(context.Background(), f, "1")
	assert.EqualError(t, err, "not an Azure DevOps repository")
}
