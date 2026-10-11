package browse

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/internal/git/gittest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const (
	browseURLRepo = "https://dev.azure.com/org/My%20Proj/_git/my%20repo"
	browseURLProj = "https://dev.azure.com/org/My%20Proj"
	browseURLSHA  = "0123456789abcdef0123456789abcdef01234567"
	browseURLLine = "&line=42&lineEnd=43&lineStartColumn=1&lineEndColumn=1&lineStyle=plain"
)

// browseURLFactory returns a Factory for org/My Proj/my repo; override acts as -R, so the
// path prefix and commit expansion skip git.
func browseURLFactory(t *testing.T, run *gittest.Runner, override bool) *cmdutil.Factory {
	t.Helper()
	t.Setenv("ADO_REPO", "")
	ios, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: "org", Project: "My Proj", Name: "my repo"}, nil
		},
		GitClient: &git.Client{Run: run.Run},
	}
	if override {
		f.RepoOverride = "org/My Proj/my repo"
	}
	return f
}

func TestBuildURL(t *testing.T) {
	inPkg := map[string]string{"rev-parse --show-prefix": "pkg/\n"}
	tests := []struct {
		name     string
		opts     Options
		replies  map[string]string
		override bool
		want     string
	}{
		{name: "repo home", want: browseURLRepo},
		{name: "PR bang", opts: Options{Target: "!123"}, want: browseURLRepo + "/pullrequest/123"},
		{name: "PR bare number", opts: Options{Target: "123"}, want: browseURLRepo + "/pullrequest/123"},
		{name: "seven digits are a PR", opts: Options{Target: "1234567"}, want: browseURLRepo + "/pullrequest/1234567"},
		{name: "work item", opts: Options{Target: "#456"}, want: browseURLProj + "/_workitems/edit/456"},
		{name: "full SHA", opts: Options{Target: browseURLSHA}, want: browseURLRepo + "/commit/" + browseURLSHA},
		{
			name: "short SHA expanded in clone", opts: Options{Target: "abc1234"},
			replies: map[string]string{"rev-parse --verify --quiet abc1234^{commit}": browseURLSHA + "\n"},
			want:    browseURLRepo + "/commit/" + browseURLSHA,
		},
		{name: "short SHA kept when unknown", opts: Options{Target: "abc1234"}, want: browseURLRepo + "/commit/abc1234"},
		{name: "short SHA kept with -R", opts: Options{Target: "abc1234"}, override: true, want: browseURLRepo + "/commit/abc1234"},
		{
			name: "file at root has no version", opts: Options{Target: "main.go"},
			replies: map[string]string{"rev-parse --show-prefix": "\n"},
			want:    browseURLRepo + "?path=%2Fmain.go&_a=contents",
		},
		{
			name: "file relative to cwd", opts: Options{Target: "api.go"}, replies: inPkg,
			want: browseURLRepo + "?path=%2Fpkg%2Fapi.go&_a=contents",
		},
		{
			name: "parent dir normalized", opts: Options{Target: "../cmd/x.go"}, replies: inPkg,
			want: browseURLRepo + "?path=%2Fcmd%2Fx.go&_a=contents",
		},
		{
			name: "dot is cwd folder", opts: Options{Target: "."}, replies: inPkg,
			want: browseURLRepo + "?path=%2Fpkg&_a=contents",
		},
		{
			name: "dotfile kept", opts: Options{Target: ".gitignore"}, override: true,
			want: browseURLRepo + "?path=%2F.gitignore&_a=contents",
		},
		{
			name: "leading slash is repo root", opts: Options{Target: "/README.md"}, replies: inPkg,
			want: browseURLRepo + "?path=%2FREADME.md&_a=contents",
		},
		{
			name: "path with -R is repo-root", opts: Options{Target: "pkg/api.go"}, override: true,
			want: browseURLRepo + "?path=%2Fpkg%2Fapi.go&_a=contents",
		},
		{
			name: "line", opts: Options{Target: "pkg/api.go:42"}, override: true,
			want: browseURLRepo + "?path=%2Fpkg%2Fapi.go" + browseURLLine + "&_a=contents",
		},
		{
			name: "line range", opts: Options{Target: "a.go:10-20"}, override: true,
			want: browseURLRepo + "?path=%2Fa.go&line=10&lineEnd=21&lineStartColumn=1&lineEndColumn=1&lineStyle=plain&_a=contents",
		},
		{
			name: "single-line range", opts: Options{Target: "a.go:5-5"}, override: true,
			want: browseURLRepo + "?path=%2Fa.go&line=5&lineEnd=6&lineStartColumn=1&lineEndColumn=1&lineStyle=plain&_a=contents",
		},
		{
			name: "path encoding", opts: Options{Target: "my dir/a&b+c.go"}, override: true,
			want: browseURLRepo + "?path=%2Fmy%20dir%2Fa%26b%2Bc.go&_a=contents",
		},
		{name: "branch home", opts: Options{Branch: "main"}, want: browseURLRepo + "?version=GBmain"},
		{
			name: "branch with slash and space", opts: Options{Branch: "users/me/a b+c"},
			want: browseURLRepo + "?version=GBusers%2Fme%2Fa%20b%2Bc",
		},
		{
			name: "file on branch with line", opts: Options{Target: "a.go:42", Branch: "dev"}, override: true,
			want: browseURLRepo + "?path=%2Fa.go&version=GBdev" + browseURLLine + "&_a=contents",
		},
		{
			name: "commit without SHA is local HEAD", opts: Options{Commit: LastCommit},
			replies: map[string]string{"rev-parse --verify --quiet HEAD^{commit}": browseURLSHA + "\n"},
			want:    browseURLRepo + "?version=GC" + browseURLSHA,
		},
		{
			name: "commit SHA with -R", opts: Options{Commit: "abc1234", Target: "a.go"}, override: true,
			want: browseURLRepo + "?path=%2Fa.go&version=GCabc1234&_a=contents",
		},
		{
			name: "commit revision resolved in clone", opts: Options{Commit: "main~1"},
			replies: map[string]string{"rev-parse --verify --quiet main~1^{commit}": browseURLSHA},
			want:    browseURLRepo + "?version=GC" + browseURLSHA,
		},
		{name: "settings fallback without auth", opts: Options{Settings: true}, want: browseURLProj + "/_settings/repositories"},
		{name: "wiki", opts: Options{Wiki: true}, want: browseURLProj + "/_wiki"},
		{name: "pipelines", opts: Options{Pipelines: true}, want: browseURLProj + "/_build"},
		{name: "boards", opts: Options{Boards: true}, want: browseURLProj + "/_boards"},
		{name: "prs", opts: Options{PRs: true}, want: browseURLRepo + "/pullrequests"},
		{name: "branches", opts: Options{Branches: true}, want: browseURLRepo + "/branches"},
		{name: "section with -R", opts: Options{Wiki: true}, override: true, want: browseURLProj + "/_wiki"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No credentials: --settings can't fetch the repository ID and falls back offline.
			t.Setenv("ADO_CONFIG_DIR", t.TempDir())
			t.Setenv("ADO_TOKEN", "")
			t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
			keyring.MockInit()
			run := &gittest.Runner{Replies: tt.replies}
			f := browseURLFactory(t, run, tt.override)
			f.Config = func() (cmdutil.Config, error) { return config.Load() }
			f.HttpClient = func() (*http.Client, error) { return &http.Client{Transport: &httpmock.Registry{}}, nil }
			got, err := BuildURL(context.Background(), f, tt.opts)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			if tt.override {
				assert.Empty(t, run.Calls, "-R must not consult the local clone")
			}
		})
	}
}

func TestBuildURLErrors(t *testing.T) {
	tests := []struct {
		name     string
		opts     Options
		override bool
		flagErr  bool
		want     string
	}{
		{name: "branch and commit", opts: Options{Branch: "a", Commit: "abc1234"}, flagErr: true, want: "specify only one of"},
		{name: "two sections", opts: Options{Wiki: true, Boards: true}, flagErr: true, want: "specify only one of"},
		{name: "section and branch", opts: Options{PRs: true, Branch: "a"}, flagErr: true, want: "specify only one of"},
		{name: "target with section", opts: Options{Target: "a.go", Settings: true}, flagErr: true, want: `"a.go" is an invalid argument when using --settings`},
		{name: "number with branch", opts: Options{Target: "12", Branch: "a"}, flagErr: true, want: `"12" is an invalid argument when using --branch or --commit`},
		{name: "PR with commit", opts: Options{Target: "!12", Commit: "abc1234"}, override: true, flagErr: true, want: "invalid argument"},
		{name: "work item with branch", opts: Options{Target: "#4", Branch: "a"}, flagErr: true, want: "invalid argument"},
		{name: "SHA with branch", opts: Options{Target: "abc1234", Branch: "a"}, flagErr: true, want: "invalid argument"},
		{name: "bad line", opts: Options{Target: "a.go:x"}, override: true, flagErr: true, want: "invalid line number after colon"},
		{name: "extra colon", opts: Options{Target: "a.go:1:2"}, override: true, flagErr: true, want: "invalid line number after colon"},
		{name: "line zero", opts: Options{Target: "a.go:0"}, override: true, flagErr: true, want: "invalid line range"},
		{name: "reversed range", opts: Options{Target: "a.go:20-10"}, override: true, flagErr: true, want: "invalid line range"},
		{name: "outside repo", opts: Options{Target: "../x"}, override: true, flagErr: true, want: "outside the repository"},
		{name: "HEAD outside clone", opts: Options{Commit: LastCommit}, override: true, want: "--commit without a SHA needs a local clone"},
		{name: "unresolvable commit", opts: Options{Commit: "main"}, override: true, want: `cannot resolve commit "main"`},
		{name: "prefix fails", opts: Options{Target: "a.go"}, want: "finding the path from the repository root"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := browseURLFactory(t, &gittest.Runner{}, tt.override)
			_, err := BuildURL(context.Background(), f, tt.opts)
			require.ErrorContains(t, err, tt.want)
			var flagErr *cmdutil.FlagError
			assert.Equal(t, tt.flagErr, errors.As(err, &flagErr))
		})
	}
}

func TestBuildURLBaseRepoError(t *testing.T) {
	f := browseURLFactory(t, &gittest.Runner{}, false)
	f.BaseRepo = func() (adocontext.Repo, error) { return adocontext.Repo{}, errors.New("no repo") }
	_, err := BuildURL(context.Background(), f, Options{})
	require.EqualError(t, err, "no repo")
}

var browseURLOrgSeq atomic.Int64

// browseURLSettingsFactory returns a Factory with credentials and its own org (the SDK caches
// locations per base URL for the whole process).
func browseURLSettingsFactory(t *testing.T) (*cmdutil.Factory, *httpmock.Registry, string) {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	keyring.MockInit()
	org := fmt.Sprintf("browseurl-%d", browseURLOrgSeq.Add(1))
	reg := &httpmock.Registry{}
	f := browseURLFactory(t, &gittest.Runner{}, true)
	f.BaseRepo = func() (adocontext.Repo, error) {
		return adocontext.Repo{Host: "dev.azure.com", Org: org, Project: "My Proj", Name: "web"}, nil
	}
	f.Config = func() (cmdutil.Config, error) { return config.Load() }
	f.HttpClient = func() (*http.Client, error) { return &http.Client{Transport: reg}, nil }
	return f, reg, org
}

func TestBuildURLSettings(t *testing.T) {
	f, reg, org := browseURLSettingsFactory(t)
	reg.StubLocations(org, httpmock.GitRepoLocations...)
	reg.Register(httpmock.REST(http.MethodGet, org+"/My%20Proj/_apis/git/repositories/web"),
		httpmock.JSONResponse(map[string]any{"id": "11111111-2222-3333-4444-555555555555", "name": "web"}))
	got, err := BuildURL(context.Background(), f, Options{Settings: true})
	require.NoError(t, err)
	assert.Equal(t, "https://dev.azure.com/"+org+"/My%20Proj/_settings/repositories?repo=11111111-2222-3333-4444-555555555555", got)
	reg.Verify(t)
}

func TestBuildURLSettingsFallback(t *testing.T) {
	f, reg, org := browseURLSettingsFactory(t)
	reg.StubLocations(org, httpmock.GitRepoLocations...)
	reg.Register(httpmock.REST(http.MethodGet, org+"/My%20Proj/_apis/git/repositories/web"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "TF401019: not found"}))
	got, err := BuildURL(context.Background(), f, Options{Settings: true})
	require.NoError(t, err)
	assert.Equal(t, "https://dev.azure.com/"+org+"/My%20Proj/_settings/repositories", got)
	reg.Verify(t)
}

func TestBuildURLLegacyHost(t *testing.T) {
	f := browseURLFactory(t, &gittest.Runner{}, true)
	f.BaseRepo = func() (adocontext.Repo, error) {
		return adocontext.Repo{Host: "fabrikam.visualstudio.com", Org: "fabrikam", Project: "P", Name: "r"}, nil
	}
	got, err := BuildURL(context.Background(), f, Options{Target: "#7"})
	require.NoError(t, err)
	assert.Equal(t, "https://fabrikam.visualstudio.com/P/_workitems/edit/7", got)
}
