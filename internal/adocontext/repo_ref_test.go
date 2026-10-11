package adocontext

import (
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func refBase(r Repo) func() (Repo, error) {
	return func() (Repo, error) { return r, nil }
}

func refNoBase() (Repo, error) { return Repo{}, errors.New("no base") }

func refPanicBase() (Repo, error) { panic("base must not be called") }

func refOrg(org string) func() string { return func() string { return org } }

func refPanicOrg() string { panic("config org must not be called") }

func TestParseRepoRef(t *testing.T) {
	vsBase := Repo{Host: "fab.visualstudio.com", Org: "fab", Project: "bp", Name: "cur"}
	sshBase := Repo{Host: "ssh.dev.azure.com", Org: "bo", Project: "bp", Name: "cur"}
	tests := []struct {
		name    string
		arg     string
		env     map[string]string
		base    func() (Repo, error)
		org     func() string
		want    Repo
		wantErr string
	}{
		{name: "full", arg: "o/p/r", base: refPanicBase, org: refPanicOrg,
			want: Repo{Host: "dev.azure.com", Org: "o", Project: "p", Name: "r"}},
		{name: "bare inherits host, org and project from base", arg: "r", base: refBase(vsBase), org: refPanicOrg,
			env:  map[string]string{"ADO_ORG": "eo", "ADO_PROJECT": "ep"},
			want: Repo{Host: "fab.visualstudio.com", Org: "fab", Project: "bp", Name: "r"}},
		{name: "project/repo inherits host and org from base", arg: "p/r", base: refBase(sshBase), org: refPanicOrg,
			want: Repo{Host: "ssh.dev.azure.com", Org: "bo", Project: "p", Name: "r"}},
		{name: "bare falls back to env", arg: "r", base: refNoBase, org: refPanicOrg,
			env:  map[string]string{"ADO_ORG": "eo", "ADO_PROJECT": "ep"},
			want: Repo{Host: "dev.azure.com", Org: "eo", Project: "ep", Name: "r"}},
		{name: "config org after ADO_ORG", arg: "p/r", base: refNoBase, org: refOrg("co"),
			want: Repo{Host: "dev.azure.com", Org: "co", Project: "p", Name: "r"}},
		{name: "config org with ADO_PROJECT", arg: "r", base: refNoBase, org: refOrg("co"),
			env:  map[string]string{"ADO_PROJECT": "ep"},
			want: Repo{Host: "dev.azure.com", Org: "co", Project: "ep", Name: "r"}},
		{name: "missing org", arg: "p/r", base: refNoBase, org: refOrg(""),
			wantErr: `repository "p/r" has no organization; use ORG/PROJECT/REPO, set ADO_ORG or run ` + "`ado config set org <org>`"},
		{name: "missing project", arg: "r", base: refNoBase, org: refOrg("co"),
			wantErr: `repository "r" has no project; use [ORG/]PROJECT/REPO or set ADO_PROJECT`},
		{name: "invalid config org", arg: "p/r", base: refNoBase, org: refOrg("a/b"),
			wantErr: `invalid org config value "a/b"`},
		{name: "too many segments", arg: "a/b/c/d", base: refPanicBase, org: refPanicOrg,
			wantErr: `invalid repository "a/b/c/d": expected [ORG/]PROJECT/REPO`},
		{name: "empty", arg: "", base: refPanicBase, org: refPanicOrg,
			wantErr: `invalid repository "": expected [ORG/]PROJECT/REPO`},
		{name: "https remote", arg: "https://o@dev.azure.com/o/p/_git/r", base: refPanicBase, org: refPanicOrg,
			want: Repo{Host: "dev.azure.com", Org: "o", Project: "p", Name: "r"}},
		{name: "visualstudio.com", arg: "https://fab.visualstudio.com/DefaultCollection/p/_git/r", base: refPanicBase,
			want: Repo{Host: "fab.visualstudio.com", Org: "fab", Project: "p", Name: "r"}},
		{name: "ssh scp", arg: "git@ssh.dev.azure.com:v3/o/p/r", base: refPanicBase,
			want: Repo{Host: "ssh.dev.azure.com", Org: "o", Project: "p", Name: "r"}},
		{name: "ssh url", arg: "ssh://git@ssh.dev.azure.com/v3/o/p/r", base: refPanicBase,
			want: Repo{Host: "ssh.dev.azure.com", Org: "o", Project: "p", Name: "r"}},
		{name: "web path query", arg: "https://dev.azure.com/o/p/_git/r?path=/README.md&version=GBmain", base: refPanicBase,
			want: Repo{Host: "dev.azure.com", Org: "o", Project: "p", Name: "r"}},
		{name: "web fragment", arg: "https://dev.azure.com/o/p/_git/r#top", base: refPanicBase,
			want: Repo{Host: "dev.azure.com", Org: "o", Project: "p", Name: "r"}},
		{name: "web pull request", arg: "https://dev.azure.com/o/p/_git/My%20Repo/pullrequest/5?_a=files", base: refPanicBase,
			want: Repo{Host: "dev.azure.com", Org: "o", Project: "p", Name: "My Repo"}},
		{name: "web commit", arg: "https://fab.visualstudio.com/p/_git/r/commit/c0ffee", base: refPanicBase,
			want: Repo{Host: "fab.visualstudio.com", Org: "fab", Project: "p", Name: "r"}},
		{name: "limited refs kept", arg: "https://dev.azure.com/o/p/_git/_full/r", base: refPanicBase,
			want: Repo{Host: "dev.azure.com", Org: "o", Project: "p", Name: "r"}},
		{name: "not ADO, URL not echoed", arg: "https://user:s3cret@github.com/a/b", base: refPanicBase,
			wantErr: "invalid repository URL: expected https://dev.azure.com/ORG/PROJECT/_git/REPO or git@ssh.dev.azure.com:v3/ORG/PROJECT/REPO"},
		{name: "bad URL not echoed", arg: "https://user:s3cret@dev.azure.com/o/p/_git", base: refPanicBase,
			wantErr: "invalid repository URL: expected https://dev.azure.com/ORG/PROJECT/_git/REPO or git@ssh.dev.azure.com:v3/ORG/PROJECT/REPO"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"ADO_ORG", "ADO_PROJECT"} {
				t.Setenv(k, tt.env[k])
			}
			org := tt.org
			if org == nil {
				org = refPanicOrg
			}
			got, err := ParseRepoRef(tt.arg, tt.base, org)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				assert.NotContains(t, err.Error(), "s3cret")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWebURLs(t *testing.T) {
	dev := Repo{Host: "ssh.dev.azure.com", Org: "my org", Project: "P 1", Name: "r#1"}
	vs := Repo{Host: "fab.visualstudio.com", Org: "fab", Project: "p", Name: "r"}
	assert.Equal(t, "https://dev.azure.com/my%20org/", OrgURL(dev.Host, dev.Org))
	assert.Equal(t, "https://fab.visualstudio.com/", OrgURL(vs.Host, vs.Org))
	assert.Equal(t, "https://dev.azure.com/o/", OrgURL("vs-ssh.visualstudio.com", "o"))
	assert.Equal(t, "https://dev.azure.com/my%20org/P%201/_git/r%231", RepoURL(dev))
	assert.Equal(t, "https://fab.visualstudio.com/p/_git/r", RepoURL(vs))
	assert.Equal(t, "https://fab.visualstudio.com/p/_git/r/pullrequest/12", PRURL(vs, 12))
	assert.Equal(t, "https://dev.azure.com/my%20org/P%201", ProjectURL(dev))
	assert.Equal(t, "https://dev.azure.com/my%20org/P%201/_workitems/edit/7", WorkItemURL(dev, 7))
	assert.Equal(t, "https://fab.visualstudio.com/p/_workitems/edit/7", WorkItemURL(vs, 7))
	assert.Equal(t, "a%20b%2Bc%26d%23e%5Cf", QueryEscape(`a b+c&d#e\f`))
	assert.Equal(t, "https://dev.azure.com/my%20org/P%201/_sprints/taskboard/Web%20Team/P%201/R1%5CSprint%203",
		TaskboardURL(dev, "Web Team", `\P 1\R1\Sprint 3`))
	assert.Equal(t, "https://fab.visualstudio.com/p/_sprints/taskboard/t/p/S%231", TaskboardURL(vs, "t", `p\S#1`))
	assert.Equal(t, "https://fab.visualstudio.com/p/_sprints/taskboard/t/p/Sprint%201", TaskboardURL(vs, "t", `\P\Sprint 1`),
		"the project prefix is trimmed whatever its casing")
	assert.Equal(t, "https://dev.azure.com/my%20org/P%201/_boards/board/t/Web%20Team/Microsoft.RequirementCategory",
		BoardURL(dev, "Web Team", "Microsoft.RequirementCategory"))
	assert.Equal(t, "https://dev.azure.com/my%20org/P%201/_build?definitionId=5", PipelineURL(dev, 5))
	assert.Equal(t, "https://fab.visualstudio.com/p/_build/results?buildId=812", RunURL(vs, 812))
}

func TestSplitOrgURL(t *testing.T) {
	tests := []struct {
		raw  string
		repo Repo
		rest []string
	}{
		{"https://dev.azure.com/org/My%20Proj/_build/results?buildId=1", Repo{Host: "dev.azure.com", Org: "org", Project: "My Proj"}, []string{"_build", "results"}},
		{"https://DEV.azure.com/org/_apis/wit/workItems/1/", Repo{Host: "dev.azure.com", Org: "org"}, []string{"_apis", "wit", "workItems", "1"}},
		{"https://Fab.VisualStudio.com/DefaultCollection/p/_build", Repo{Host: "fab.visualstudio.com", Org: "fab", Project: "p"}, []string{"_build"}},
		{"https://fab.visualstudio.com/_apis/build/builds/1", Repo{Host: "fab.visualstudio.com", Org: "fab"}, []string{"_apis", "build", "builds", "1"}},
	}
	for _, tt := range tests {
		u, err := url.Parse(tt.raw)
		require.NoError(t, err)
		repo, rest, ok := SplitOrgURL(u)
		require.True(t, ok, tt.raw)
		assert.Equal(t, tt.repo, repo, tt.raw)
		assert.Equal(t, tt.rest, rest, tt.raw)
	}
	for _, raw := range []string{
		"http://dev.azure.com/org/p/_build",
		"https://example.com/org/p/_build",
		"https://a.b.visualstudio.com/p/_build",
		"https://dev.azure.com/",
		"https://dev.azure.com/_build",
		"https://dev.azure.com/org/p",
		"https://dev.azure.com/org/p/q/_build",
	} {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		_, _, ok := SplitOrgURL(u)
		assert.False(t, ok, raw)
	}
}
