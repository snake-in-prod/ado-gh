package adocontext

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseProject(t *testing.T) {
	got, err := ParseProject("--project", "org/My Proj")
	require.NoError(t, err)
	assert.Equal(t, Repo{Host: "dev.azure.com", Org: "org", Project: "My Proj"}, got)

	for _, s := range []string{"", "proj", "org/", "/proj", "a/b/c", `org/p\q`} {
		_, err := ParseProject("--project", s)
		assert.EqualError(t, err, fmt.Sprintf("invalid --project %q: expected ORG/PROJECT", s))
	}
}

func TestResolveBaseProject(t *testing.T) {
	fromGit := Repo{Host: "fab.visualstudio.com", Org: "fab", Project: "p", Name: "r"}
	gitRepo := func() (Repo, error) { return fromGit, nil }
	notCalled := func() (Repo, error) { panic("base repo must not be resolved") }
	tests := []struct {
		name     string
		override string
		env      map[string]string
		base     func() (Repo, error)
		want     Repo
		wantErr  string
	}{
		{name: "-R ORG/PROJECT outside a git repo", override: "org/proj", base: notCalled,
			want: Repo{Host: "dev.azure.com", Org: "org", Project: "proj"}},
		{name: "-R ORG/PROJECT ignores ADO_ORG", override: "org/proj", env: map[string]string{"ADO_ORG": "eo"}, base: notCalled,
			want: Repo{Host: "dev.azure.com", Org: "org", Project: "proj"}},
		{name: "-R ORG/PROJECT/REPO as before", override: "org/proj/repo",
			base: func() (Repo, error) { return ResolveBaseRepo("org/proj/repo", noGit) },
			want: Repo{Host: "dev.azure.com", Org: "org", Project: "proj", Name: "repo"}},
		{name: "-R invalid ORG/PROJECT", override: "org/", base: notCalled,
			wantErr: `invalid -R "org/": expected ORG/PROJECT`},
		{name: "git remote", base: gitRepo, want: fromGit},
		{name: "ADO_REPO", env: map[string]string{"ADO_REPO": "x/y/z"},
			base: func() (Repo, error) { return ResolveBaseRepo("", noGit) },
			want: Repo{Host: "dev.azure.com", Org: "x", Project: "y", Name: "z"}},
		{name: "outside a git repo", base: func() (Repo, error) { return Repo{}, errNoRepo },
			wantErr: "not an Azure DevOps repository; use -R ORG/PROJECT or set ADO_REPO"},
		{name: "other errors pass through", base: func() (Repo, error) { return Repo{}, errors.New("boom") },
			wantErr: "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"ADO_REPO", "ADO_ORG", "ADO_PROJECT"} {
				t.Setenv(k, tt.env[k])
			}
			got, err := ResolveBaseProject(tt.override, tt.base)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveBaseRepoStillNeedsRepo(t *testing.T) {
	t.Setenv("ADO_ORG", "")
	_, err := ResolveBaseRepo("org/proj", noGit)
	require.EqualError(t, err, `-R "org/proj" has no organization; use ORG/PROJECT/REPO or set ADO_ORG`,
		"repo-scoped commands read ORG/PROJECT as PROJECT/REPO")
}
