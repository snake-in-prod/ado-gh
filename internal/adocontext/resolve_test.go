package adocontext

import (
	"errors"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func remotes(rs ...git.Remote) func() ([]git.Remote, error) {
	return func() ([]git.Remote, error) { return rs, nil }
}

func noGit() ([]git.Remote, error) {
	panic("git must not be called")
}

var (
	originR   = git.Remote{Name: "origin", FetchURL: "https://dev.azure.com/o/p/_git/origin"}
	upstreamR = git.Remote{Name: "upstream", FetchURL: "git@ssh.dev.azure.com:v3/o/p/upstream"}
	otherR    = git.Remote{Name: "other", FetchURL: "https://fab.visualstudio.com/p/_git/other"}
	githubR   = git.Remote{Name: "gh", FetchURL: "https://github.com/a/b.git"}
)

func TestResolveBaseRepo(t *testing.T) {
	const noRepo = "not an Azure DevOps repository; use -R ORG/PROJECT/REPO or set ADO_REPO"
	tests := []struct {
		name     string
		override string
		env      map[string]string
		remotes  func() ([]git.Remote, error)
		want     Repo
		wantErr  string
	}{
		{name: "-R full beats env and remotes", override: "org/proj/repo",
			env: map[string]string{"ADO_REPO": "x/y/z", "ADO_ORG": "eo", "ADO_PROJECT": "ep"}, remotes: noGit,
			want: Repo{Host: "dev.azure.com", Org: "org", Project: "proj", Name: "repo"}},
		{name: "-R project/repo uses ADO_ORG", override: "proj/repo",
			env: map[string]string{"ADO_ORG": "eo", "ADO_PROJECT": "ep"}, remotes: noGit,
			want: Repo{Host: "dev.azure.com", Org: "eo", Project: "proj", Name: "repo"}},
		{name: "-R repo uses ADO_ORG and ADO_PROJECT", override: "repo",
			env: map[string]string{"ADO_ORG": "eo", "ADO_PROJECT": "ep"}, remotes: noGit,
			want: Repo{Host: "dev.azure.com", Org: "eo", Project: "ep", Name: "repo"}},
		{name: "-R project/repo without ADO_ORG", override: "proj/repo", remotes: noGit,
			wantErr: `-R "proj/repo" has no organization; use ORG/PROJECT/REPO or set ADO_ORG`},
		{name: "-R repo without ADO_PROJECT", override: "repo", env: map[string]string{"ADO_ORG": "eo"}, remotes: noGit,
			wantErr: `-R "repo" has no project; use [ORG/]PROJECT/REPO or set ADO_PROJECT`},
		{name: "-R too many segments", override: "a/b/c/d", remotes: noGit,
			wantErr: `invalid -R "a/b/c/d": expected [ORG/]PROJECT/REPO`},
		{name: "-R empty segment", override: "a//c", remotes: noGit,
			wantErr: `invalid -R "a//c": expected [ORG/]PROJECT/REPO`},
		{name: "invalid ADO_ORG", override: "p/r", env: map[string]string{"ADO_ORG": "a/b"}, remotes: noGit,
			wantErr: `invalid ADO_ORG "a/b"`},
		{name: "ADO_REPO beats remotes", env: map[string]string{"ADO_REPO": "x/y/z"}, remotes: noGit,
			want: Repo{Host: "dev.azure.com", Org: "x", Project: "y", Name: "z"}},
		{name: "partial ADO_REPO", env: map[string]string{"ADO_REPO": "z", "ADO_ORG": "x", "ADO_PROJECT": "y"}, remotes: noGit,
			want: Repo{Host: "dev.azure.com", Org: "x", Project: "y", Name: "z"}},
		{name: "partial ADO_REPO without ADO_ORG", env: map[string]string{"ADO_REPO": "y/z"}, remotes: noGit,
			wantErr: `ADO_REPO "y/z" has no organization; use ORG/PROJECT/REPO or set ADO_ORG`},
		{name: "origin first", remotes: remotes(otherR, upstreamR, originR),
			want: Repo{Host: "dev.azure.com", Org: "o", Project: "p", Name: "origin"}},
		{name: "upstream when origin is not ADO",
			remotes: remotes(otherR, git.Remote{Name: "origin", FetchURL: githubR.FetchURL}, upstreamR),
			want:    Repo{Host: "ssh.dev.azure.com", Org: "o", Project: "p", Name: "upstream"}},
		{name: "first ADO remote", remotes: remotes(githubR, otherR),
			want: Repo{Host: "fab.visualstudio.com", Org: "fab", Project: "p", Name: "other"}},
		{name: "ADO_ORG ignored for remotes", env: map[string]string{"ADO_ORG": "eo"}, remotes: remotes(originR),
			want: Repo{Host: "dev.azure.com", Org: "o", Project: "p", Name: "origin"}},
		{name: "no ADO remote", remotes: remotes(githubR), wantErr: noRepo},
		{name: "no remotes", remotes: remotes(), wantErr: noRepo},
		{name: "not a git repo",
			remotes: func() ([]git.Remote, error) {
				return nil, &git.Error{ExitCode: 128, Stderr: "fatal: not a git repository"}
			},
			wantErr: noRepo},
		{name: "git failure",
			remotes: func() ([]git.Remote, error) { return nil, errors.New("git: boom") },
			wantErr: "git: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"ADO_REPO", "ADO_ORG", "ADO_PROJECT"} {
				t.Setenv(k, tt.env[k])
			}
			got, err := ResolveBaseRepo(tt.override, tt.remotes)
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
