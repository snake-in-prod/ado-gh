package shared

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func sharedTestFactory(t *testing.T, reg *httpmock.Registry, base func() (adocontext.Repo, error)) *cmdutil.Factory {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_ORG", "")
	t.Setenv("ADO_PROJECT", "")
	keyring.MockInit()
	cfg, err := config.Load()
	require.NoError(t, err)
	ios, _, _, _ := iostreams.Test()
	return &cmdutil.Factory{
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: reg}, nil },
		BaseRepo:   base,
	}
}

func TestParseRepo(t *testing.T) {
	cur := adocontext.Repo{Host: "fab.visualstudio.com", Org: "fab", Project: "p", Name: "cur"}
	f := sharedTestFactory(t, nil, func() (adocontext.Repo, error) { return cur, nil })

	got, err := ParseRepo(f, "")
	require.NoError(t, err)
	assert.Equal(t, cur, got)

	got, err = ParseRepo(f, "other")
	require.NoError(t, err)
	assert.Equal(t, adocontext.Repo{Host: "fab.visualstudio.com", Org: "fab", Project: "p", Name: "other"}, got)

	f.BaseRepo = func() (adocontext.Repo, error) { return adocontext.Repo{}, errors.New("no repo") }
	_, err = ParseRepo(f, "")
	assert.EqualError(t, err, "no repo")

	cfg, err := f.Config()
	require.NoError(t, err)
	cfg.Set("", "org", "co")
	got, err = ParseRepo(f, "p/r")
	require.NoError(t, err)
	assert.Equal(t, adocontext.Repo{Host: "dev.azure.com", Org: "co", Project: "p", Name: "r"}, got)

	f.Config = func() (cmdutil.Config, error) { return nil, errors.New("bad config") }
	_, err = ParseRepo(f, "p/r")
	assert.ErrorContains(t, err, "has no organization")
}

var sharedOrgSeq atomic.Int64

func TestNewClient(t *testing.T) {
	// An org of its own per run: the SDK caches locations per base URL for the whole process.
	org := fmt.Sprintf("shared-repo-org-%d", sharedOrgSeq.Add(1))
	reg := &httpmock.Registry{}
	reg.StubLocations(org, httpmock.GitRepoLocations...)
	reg.Register(httpmock.REST(http.MethodGet, org+"/proj/_apis/git/repositories"),
		httpmock.JSONResponse(map[string]any{"count": 1, "value": []any{map[string]any{"name": "a"}}}))
	t.Cleanup(func() { reg.Verify(t) })
	f := sharedTestFactory(t, reg, nil)

	c, err := NewClient(context.Background(), f, adocontext.Repo{Host: "dev.azure.com", Org: org, Project: "proj"})
	require.NoError(t, err)
	repos, err := c.ListRepositories(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "a", repos[0].Name)
	i := slices.IndexFunc(reg.Requests, func(r *http.Request) bool { return r.Method == http.MethodGet })
	require.GreaterOrEqual(t, i, 0)
	assert.Equal(t, "Basic OnBhdA==", reg.Requests[i].Header.Get("Authorization"))
}

func TestHumanSize(t *testing.T) {
	for in, want := range map[uint64]string{
		0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB",
		5 << 20: "5.0 MiB", 3 << 30: "3.0 GiB", 2 << 40: "2.0 TiB", 2048 << 40: "2048.0 TiB",
		// rounding to 1024.0 moves up a unit
		1048524: "1023.9 KiB", 1048525: "1.0 MiB", 1<<20 - 1: "1.0 MiB",
		1<<30 - 1: "1.0 GiB", 1<<40 - 1: "1.0 TiB", 1023<<30 + 900<<20: "1023.9 GiB",
	} {
		assert.Equal(t, want, HumanSize(in), in)
	}
}
