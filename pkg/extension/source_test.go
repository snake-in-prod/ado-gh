package extension

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

func TestParseSource(t *testing.T) {
	tests := []struct {
		arg  string
		want string
	}{
		{"snake-in-prod/ado-hello", "github.com/snake-in-prod/ado-hello"},
		{"Snake-In-Prod/ADO-Hello.git", "github.com/snake-in-prod/ado-hello"},
		{"https://github.com/myco/ado-x", "github.com/myco/ado-x"},
		{"https://GitHub.com/MyCo/ado-x.git/", "github.com/myco/ado-x"},
		{"https://github.com:443/myco/ado-x", "github.com/myco/ado-x"},
		{"https://token@github.com/myco/ado-x", "github.com/myco/ado-x"},
		{"git@github.com:myco/ado-x.git", "github.com/myco/ado-x"},
		{"ssh://git@github.com/myco/ado-x", "github.com/myco/ado-x"},
		{"https://dev.azure.com/MyOrg/My%20Proj/_git/ado-lint", "dev.azure.com/myorg/my proj/ado-lint"},
		{"https://myorg@dev.azure.com/myorg/proj/_git/ado-lint", "dev.azure.com/myorg/proj/ado-lint"},
		{"https://dev.azure.com/myorg/proj/_git/ado-lint.git", "dev.azure.com/myorg/proj/ado-lint"},
		{"https://dev.azure.com/myorg/_git/ado-lint", "dev.azure.com/myorg/ado-lint/ado-lint"},
		{"https://myorg.visualstudio.com/proj/_git/ado-lint", "dev.azure.com/myorg/proj/ado-lint"},
		{"https://MyOrg.visualstudio.com/DefaultCollection/proj/_git/ado-lint", "dev.azure.com/myorg/proj/ado-lint"},
		{"git@ssh.dev.azure.com:v3/myorg/proj/ado-lint", "dev.azure.com/myorg/proj/ado-lint"},
		{"myorg@vs-ssh.visualstudio.com:v3/myorg/proj/ado-lint", "dev.azure.com/myorg/proj/ado-lint"},
		{".", "local"},
		{"./ado-x", "local"},
		{"../ado-x", "local"},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			s, err := ParseSource(tt.arg)
			require.NoError(t, err)
			assert.Equal(t, tt.want, s.String())
		})
	}
}

func TestParseSourceFields(t *testing.T) {
	s, err := ParseSource("https://dev.azure.com/MyOrg/My%20Proj/_git/ADO-Lint")
	require.NoError(t, err)
	assert.Equal(t, Source{Host: "dev.azure.com", Owner: "MyOrg", Project: "My Proj", Repo: "ADO-Lint"}, s, "original case kept")
	assert.False(t, s.IsLocal())

	s, err = ParseSource("Snake-In-Prod/ado-hello.git")
	require.NoError(t, err)
	assert.Equal(t, Source{Host: "github.com", Owner: "Snake-In-Prod", Repo: "ado-hello"}, s)

	wd, err := os.Getwd()
	require.NoError(t, err)
	s, err = ParseSource("./sub")
	require.NoError(t, err)
	assert.Equal(t, Source{Path: filepath.Join(wd, "sub")}, s, "made absolute")
	assert.True(t, s.IsLocal())

	abs := t.TempDir()
	s, err = ParseSource(abs)
	require.NoError(t, err)
	assert.Equal(t, abs, s.Path)
}

func TestParseSourceInvalid(t *testing.T) {
	for _, arg := range []string{
		"",
		"ado-x",
		"a/b/c",
		"my org/ado-x",
		"-x/ado-y",
		"myco/..",
		"myco/.",
		"myco/.git",
		"https://github.com/../ado-x",
		"https://github.com/myco/./",
		"https://github.com/myco/ado-x/tree/main",
		"https://github.com/myco/ado-x?x=1",
		"https://github.com/myco/ado-x#readme",
		"https://github.com/myco/ado%2Fx",
		"http://github.com/myco/ado-x",
		"https://github.com:8443/myco/ado-x",
		"https://github.com.evil.com/myco/ado-x",
		"https://dev.azure.com@evil.com/myorg/proj/_git/ado-x",
		"https://dev.azure.com/myorg/../_git/ado-x",
		"https://dev.azure.com/myorg/proj/_git/..",
		"https://dev.azure.com/myorg/proj/_git/.git",
		"https://dev.azure.com/./proj/_git/ado-x",
		"https://gitlab.com/myco/ado-x",
		"ftp://github.com/myco/ado-x",
		"myco/ado-x\x00",
	} {
		t.Run(arg, func(t *testing.T) {
			_, err := ParseSource(arg)
			assert.Error(t, err)
		})
	}
}

func TestParseSourceErrorHidesCredentials(t *testing.T) {
	_, err := ParseSource("https://user:s3cret@gitlab.com/a/b")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "s3cret")
	assert.Contains(t, err.Error(), "OWNER/REPO")
}

func TestCheckAllowed(t *testing.T) {
	gh := Source{Host: "github.com", Owner: "MyCo", Repo: "ado-x"}
	other := Source{Host: "github.com", Owner: "other", Repo: "ado-x"}
	ado := Source{Host: "dev.azure.com", Owner: "MyOrg", Project: "Tools", Repo: "ado-lint"}
	local := Source{Path: "/src/ado-x"}
	tests := []struct {
		name     string
		patterns []string
		src      Source
		allowed  bool
	}{
		{"no allowlist", nil, gh, true},
		{"owner glob", []string{"github.com/myco/*"}, gh, true},
		{"case-insensitive pattern", []string{"GitHub.com/MYCO/*"}, gh, true},
		{"other owner", []string{"github.com/myco/*"}, other, false},
		{"exact", []string{"github.com/myco/ado-x"}, gh, true},
		{"star is one segment", []string{"dev.azure.com/myorg/*"}, ado, false},
		{"double star", []string{"dev.azure.com/myorg/**"}, ado, true},
		{"two stars", []string{"dev.azure.com/myorg/*/*"}, ado, true},
		{"inner glob", []string{"dev.azure.com/*/tools/ado-*"}, ado, true},
		{"leading double star", []string{"**/ado-x"}, gh, true},
		{"double star in the middle", []string{"dev.azure.com/**/ado-lint"}, ado, true},
		{"double star everything", []string{"**"}, ado, true},
		{"double star no match", []string{"**/ado-y"}, gh, false},
		{"double star is not local", []string{"**"}, local, false},
		{"star is not local", []string{"*"}, local, false},
		{"local", []string{"local"}, local, true},
		{"LOCAL", []string{"LOCAL"}, local, true},
		{"local only", []string{"local"}, gh, false},
		{"github pattern vs ado", []string{"github.com/**"}, ado, false},
		{"second pattern", []string{"local", "dev.azure.com/myorg/tools/*"}, ado, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckAllowed(tt.patterns, tt.src)
			if tt.allowed {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not allowed by extension_sources")
			assert.Contains(t, err.Error(), strings.Join(tt.patterns, ", "), "names the allowlist")
			assert.Contains(t, err.Error(), tt.src.String())
		})
	}
}

func TestCheckAllowedBadPatternsFailClosed(t *testing.T) {
	gh := Source{Host: "github.com", Owner: "myco", Repo: "ado-x"}
	for _, bad := range []string{"github.com/[x", "", "  ", "github.com//x", "github.com/myco/", `github.com/myco/\`} {
		t.Run(bad, func(t *testing.T) {
			// The good pattern matches, but a bad one anywhere refuses everything.
			err := CheckAllowed([]string{"github.com/myco/*", bad}, gh)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid extension_sources pattern")
			assert.Error(t, ValidateSourcePatterns([]string{bad}))
		})
	}
	assert.NoError(t, ValidateSourcePatterns([]string{"local", "**", "dev.azure.com/my org/**"}))
}

// hostIs matches requests by host and path: httpmock.REST ignores the host.
func hostIs(host, path string) httpmock.Matcher {
	return func(req *http.Request) bool { return req.URL.Host == host && req.URL.Path == path }
}

func sourceFactory(t *testing.T, sources string) (*cmdutil.Factory, *httpmock.Registry) {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	cfg, err := config.Load()
	require.NoError(t, err)
	cfg.Set("", config.ExtensionSourcesKey, sources)
	reg := &httpmock.Registry{}
	ios, _, _, _ := iostreams.Test()
	return &cmdutil.Factory{
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: reg}, nil },
	}, reg
}

func clearTokenEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN", "ADO_TOKEN", "AZURE_DEVOPS_EXT_PAT"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func TestCheckSource(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("ADO_TOKEN", "adotoken")
	t.Setenv("GH_TOKEN", "ghtoken")
	t.Setenv("GITHUB_TOKEN", "githubtoken")
	gh := Source{Host: "github.com", Owner: "myco", Repo: "ado-x"}

	t.Run("no allowlist makes no request", func(t *testing.T) {
		f, reg := sourceFactory(t, "")
		require.NoError(t, CheckSource(context.Background(), f, gh))
		assert.Empty(t, reg.Requests)
	})

	t.Run("ADO and local sources need no request", func(t *testing.T) {
		f, reg := sourceFactory(t, "dev.azure.com/myorg/**,local")
		require.NoError(t, CheckSource(context.Background(), f, Source{Host: "dev.azure.com", Owner: "myorg", Project: "p", Repo: "ado-x"}))
		require.NoError(t, CheckSource(context.Background(), f, Source{Path: "/src/ado-x"}))
		err := CheckSource(context.Background(), f, Source{Host: "dev.azure.com", Owner: "evil", Project: "p", Repo: "ado-x"})
		assert.ErrorContains(t, err, `extension source "dev.azure.com/evil/p/ado-x" is not allowed by extension_sources (dev.azure.com/myorg/**, local)`)
		assert.Empty(t, reg.Requests)
	})

	t.Run("refused before resolving", func(t *testing.T) {
		f, reg := sourceFactory(t, "github.com/other/*")
		assert.ErrorContains(t, CheckSource(context.Background(), f, gh), "not allowed by extension_sources")
		assert.Empty(t, reg.Requests)
	})

	t.Run("resolved full_name matches", func(t *testing.T) {
		f, reg := sourceFactory(t, "github.com/myco/*")
		reg.Register(hostIs("api.github.com", "/repos/myco/ado-x"), httpmock.JSONResponse(map[string]string{"full_name": "MyCo/ado-x"}))
		require.NoError(t, CheckSource(context.Background(), f, gh))
		reg.Verify(t)
		req := reg.Requests[0]
		assert.Equal(t, "Bearer ghtoken", req.Header.Get("Authorization"), "GH_TOKEN wins over GITHUB_TOKEN")
		assert.Equal(t, "application/vnd.github+json", req.Header.Get("Accept"))
	})

	t.Run("transferred repo is checked by its new name", func(t *testing.T) {
		f, reg := sourceFactory(t, "github.com/myco/*")
		reg.Register(hostIs("api.github.com", "/repos/myco/ado-x"),
			httpmock.WithHeader(httpmock.StatusStringResponse(http.StatusMovedPermanently, ""), "Location", "https://api.github.com/repositories/42"))
		reg.Register(hostIs("api.github.com", "/repositories/42"), httpmock.JSONResponse(map[string]string{"full_name": "evil/ado-x"}))
		err := CheckSource(context.Background(), f, gh)
		assert.ErrorContains(t, err, `extension source "github.com/evil/ado-x" is not allowed by extension_sources (github.com/myco/*)`)
		reg.Verify(t)
	})

	t.Run("not found", func(t *testing.T) {
		f, reg := sourceFactory(t, "github.com/myco/*")
		reg.Register(hostIs("api.github.com", "/repos/myco/ado-x"), httpmock.StatusStringResponse(http.StatusNotFound, `{"message":"Not Found"}`))
		assert.ErrorContains(t, CheckSource(context.Background(), f, gh), "GitHub repository myco/ado-x not found")
	})

	t.Run("unauthorized names the GitHub token, not ado auth login", func(t *testing.T) {
		f, reg := sourceFactory(t, "github.com/myco/*")
		reg.Register(hostIs("api.github.com", "/repos/myco/ado-x"), httpmock.StatusStringResponse(http.StatusUnauthorized, ""))
		err := CheckSource(context.Background(), f, gh)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GH_TOKEN")
		var authErr *cmdutil.AuthError
		assert.False(t, errors.As(err, &authErr))
	})

	t.Run("server error", func(t *testing.T) {
		f, reg := sourceFactory(t, "github.com/myco/*")
		reg.Register(hostIs("api.github.com", "/repos/myco/ado-x"), httpmock.StatusStringResponse(http.StatusInternalServerError, ""))
		assert.ErrorContains(t, CheckSource(context.Background(), f, gh), "HTTP 500")
	})

	t.Run("bad full_name", func(t *testing.T) {
		f, reg := sourceFactory(t, "github.com/myco/*")
		reg.Register(hostIs("api.github.com", "/repos/myco/ado-x"), httpmock.JSONResponse(map[string]string{"full_name": "../x"}))
		assert.ErrorContains(t, CheckSource(context.Background(), f, gh), "unexpected full_name")
	})

	t.Run("bad pattern fails closed", func(t *testing.T) {
		f, _ := sourceFactory(t, "github.com/[x")
		assert.ErrorContains(t, CheckSource(context.Background(), f, gh), "invalid extension_sources pattern")
	})
}

type plainConfig struct{}

func (plainConfig) Get(string, string) string  { return "" }
func (plainConfig) Set(string, string, string) {}
func (plainConfig) Write() error               { return nil }

func TestCheckSourceConfigFailsClosed(t *testing.T) {
	gh := Source{Host: "github.com", Owner: "myco", Repo: "ado-x"}
	boom := errors.New("boom")
	f := &cmdutil.Factory{Config: func() (cmdutil.Config, error) { return nil, boom }}
	assert.ErrorIs(t, CheckSource(context.Background(), f, gh), boom)

	f = &cmdutil.Factory{Config: func() (cmdutil.Config, error) { return plainConfig{}, nil }}
	assert.ErrorContains(t, CheckSource(context.Background(), f, gh), "extension_sources")
}

func TestGitHubClientTokenHosts(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("ADO_TOKEN", "adotoken")
	t.Setenv("GITHUB_TOKEN", "githubtoken")
	f, reg := sourceFactory(t, "")
	// The API redirects an asset download to another host: the token must not follow.
	reg.Register(hostIs("api.github.com", "/repos/o/r/releases/assets/1"),
		httpmock.WithHeader(httpmock.StatusStringResponse(http.StatusFound, ""), "Location", "https://objects.githubusercontent.com/blob"))
	reg.Register(hostIs("objects.githubusercontent.com", "/blob"), httpmock.StringResponse("bin"))
	reg.Register(hostIs("dev.azure.com", "/o/_apis/x"), httpmock.StringResponse("{}"))
	reg.Register(hostIs("api.github.com", "/plain"), httpmock.StringResponse("{}"))

	hc, err := GitHubClient(f)
	require.NoError(t, err)
	for _, u := range []string{
		"https://api.github.com/repos/o/r/releases/assets/1",
		"https://dev.azure.com/o/_apis/x",
		"http://api.github.com/plain",
	} {
		resp, err := hc.Get(u)
		require.NoError(t, err, u)
		resp.Body.Close()
	}
	reg.Verify(t)
	auth := map[string]string{}
	for _, req := range reg.Requests {
		auth[req.URL.Scheme+"://"+req.URL.Host] = req.Header.Get("Authorization")
		assert.NotContains(t, req.Header.Get("Authorization"), "adotoken", "ADO token never sent by the GitHub client")
	}
	assert.Equal(t, map[string]string{
		"https://api.github.com":                "Bearer githubtoken",
		"https://objects.githubusercontent.com": "",
		"https://dev.azure.com":                 "",
		"http://api.github.com":                 "",
	}, auth)
}

func TestGitHubClientWithoutToken(t *testing.T) {
	clearTokenEnv(t)
	f, reg := sourceFactory(t, "")
	reg.Register(hostIs("api.github.com", "/x"), httpmock.StringResponse("{}"))
	hc, err := GitHubClient(f)
	require.NoError(t, err)
	resp, err := hc.Get("https://api.github.com/x")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Empty(t, reg.Requests[0].Header.Get("Authorization"))

	boom := errors.New("boom")
	f.HttpClient = func() (*http.Client, error) { return nil, boom }
	_, err = GitHubClient(f)
	assert.ErrorIs(t, err, boom)
}
