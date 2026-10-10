package adocontext

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cli/go-gh/v2/pkg/ssh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/internal/config"
)

// TestMain lets the test binary act as a fake `ssh -G` when copied onto PATH.
func TestMain(m *testing.M) {
	if os.Getenv("ADO_FAKE_SSH") == "1" {
		if h := os.Getenv("ADO_FAKE_SSH_HOSTNAME"); h != "" && len(os.Args) == 3 && os.Args[1] == "-G" {
			fmt.Println("user git")
			fmt.Println("hostname " + h)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestParseRemoteURL(t *testing.T) {
	r := func(host, org, project, name string) Repo {
		return Repo{Host: host, Org: org, Project: project, Name: name}
	}
	tests := []struct {
		name string
		in   string
		want Repo
	}{
		// dev.azure.com HTTPS
		{"https", "https://dev.azure.com/fabrikam/Proj/_git/repo", r("dev.azure.com", "fabrikam", "Proj", "repo")},
		{"https user", "https://fabrikam@dev.azure.com/fabrikam/Proj/_git/repo", r("dev.azure.com", "fabrikam", "Proj", "repo")},
		{"https user secret", "https://me:s3cret@dev.azure.com/fabrikam/Proj/_git/repo", r("dev.azure.com", "fabrikam", "Proj", "repo")},
		{"https port 443", "https://dev.azure.com:443/fabrikam/Proj/_git/repo", r("dev.azure.com", "fabrikam", "Proj", "repo")},
		{"https project omitted", "https://dev.azure.com/fabrikam/_git/repo", r("dev.azure.com", "fabrikam", "repo", "repo")},
		{"https _full", "https://dev.azure.com/fabrikam/Proj/_git/_full/repo", r("dev.azure.com", "fabrikam", "Proj", "repo")},
		{"https _optimized", "https://dev.azure.com/fabrikam/Proj/_git/_optimized/repo", r("dev.azure.com", "fabrikam", "Proj", "repo")},
		{"https case-insensitive", "HTTPS://Dev.Azure.COM/fabrikam/Proj/_GIT/_FULL/repo", r("dev.azure.com", "fabrikam", "Proj", "repo")},
		{"https .git kept", "https://dev.azure.com/fabrikam/Proj/_git/repo.git", r("dev.azure.com", "fabrikam", "Proj", "repo.git")},
		{"https trailing slash", "https://dev.azure.com/fabrikam/Proj/_git/repo/", r("dev.azure.com", "fabrikam", "Proj", "repo")},
		{"https escaped spaces", "https://dev.azure.com/fabrikam/My%20Project/_git/My%20Repo", r("dev.azure.com", "fabrikam", "My Project", "My Repo")},
		{"https no double decode", "https://dev.azure.com/fabrikam/A%2520B/_git/r%25", r("dev.azure.com", "fabrikam", "A%20B", "r%")},

		// *.visualstudio.com HTTPS
		{"vs", "https://fabrikam.visualstudio.com/Proj/_git/repo", r("fabrikam.visualstudio.com", "fabrikam", "Proj", "repo")},
		{"vs DefaultCollection", "https://fabrikam.visualstudio.com/DefaultCollection/Proj/_git/repo", r("fabrikam.visualstudio.com", "fabrikam", "Proj", "repo")},
		{"vs DefaultCollection case", "https://Fabrikam.VisualStudio.com/defaultcollection/Proj/_Git/repo", r("fabrikam.visualstudio.com", "fabrikam", "Proj", "repo")},
		{"vs project omitted", "https://fabrikam.visualstudio.com/_git/repo", r("fabrikam.visualstudio.com", "fabrikam", "repo", "repo")},
		{"vs DefaultCollection project omitted", "https://fabrikam.visualstudio.com/DefaultCollection/_git/repo", r("fabrikam.visualstudio.com", "fabrikam", "repo", "repo")},
		{"vs team dropped", "https://fabrikam.visualstudio.com/Proj/Team/_git/repo", r("fabrikam.visualstudio.com", "fabrikam", "Proj", "repo")},
		{"vs user", "https://fabrikam@fabrikam.visualstudio.com/Proj/_git/_optimized/repo", r("fabrikam.visualstudio.com", "fabrikam", "Proj", "repo")},

		// SSH v3
		{"scp v3", "git@ssh.dev.azure.com:v3/fabrikam/Proj/repo", r("ssh.dev.azure.com", "fabrikam", "Proj", "repo")},
		{"scp v3 case", "git@SSH.dev.azure.com:V3/fabrikam/Proj/repo", r("ssh.dev.azure.com", "fabrikam", "Proj", "repo")},
		{"scp v3 escaped", "git@ssh.dev.azure.com:v3/fabrikam/My%20Project/My%20Repo", r("ssh.dev.azure.com", "fabrikam", "My Project", "My Repo")},
		{"scp v3 _full", "git@ssh.dev.azure.com:v3/fabrikam/Proj/_full/repo", r("ssh.dev.azure.com", "fabrikam", "Proj", "repo")},
		{"scp v3 team", "git@ssh.dev.azure.com:v3/fabrikam/Proj/Team/repo", r("ssh.dev.azure.com", "fabrikam", "Proj", "repo")},
		{"scp v3 team _optimized", "git@ssh.dev.azure.com:v3/fabrikam/Proj/Team/_optimized/repo", r("ssh.dev.azure.com", "fabrikam", "Proj", "repo")},
		{"scp v3 .git", "git@ssh.dev.azure.com:v3/fabrikam/Proj/repo.git", r("ssh.dev.azure.com", "fabrikam", "Proj", "repo.git")},
		{"ssh v3", "ssh://git@ssh.dev.azure.com/v3/fabrikam/Proj/repo", r("ssh.dev.azure.com", "fabrikam", "Proj", "repo")},
		{"ssh v3 port 22", "ssh://git@ssh.dev.azure.com:22/v3/fabrikam/Proj/repo/", r("ssh.dev.azure.com", "fabrikam", "Proj", "repo")},
		{"vs-ssh v3 org from path", "other@vs-ssh.visualstudio.com:v3/fabrikam/Proj/repo", r("vs-ssh.visualstudio.com", "fabrikam", "Proj", "repo")},
		{"ssh vs-ssh v3", "ssh://fabrikam@vs-ssh.visualstudio.com:22/v3/fabrikam/Proj/repo", r("vs-ssh.visualstudio.com", "fabrikam", "Proj", "repo")},

		// legacy v1/v2 SSH
		{"legacy org from user", "ssh://fabrikam@vs-ssh.visualstudio.com/Proj/_ssh/repo", r("vs-ssh.visualstudio.com", "fabrikam", "Proj", "repo")},
		{"legacy port 22", "ssh://fabrikam@vs-ssh.visualstudio.com:22/Proj/_SSH/repo", r("vs-ssh.visualstudio.com", "fabrikam", "Proj", "repo")},
		{"legacy scp", "fabrikam@vs-ssh.visualstudio.com:Proj/_ssh/repo", r("vs-ssh.visualstudio.com", "fabrikam", "Proj", "repo")},
		{"legacy org in path", "ssh://fabrikam@vs-ssh.visualstudio.com/fabrikam/Proj/_ssh/repo", r("vs-ssh.visualstudio.com", "fabrikam", "Proj", "repo")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRemoteURL(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, fmt.Sprintf("%+v", got), "s3cret")
		})
	}
}

func TestParseRemoteURL_Rejects(t *testing.T) {
	tests := []struct{ name, in string }{
		{"empty", ""},
		{"github https", "https://github.com/cli/cli.git"},
		{"github scp", "git@github.com:cli/cli.git"},
		{"local path", "/srv/git/repo.git"},
		{"windows path", `C:\src\repo`},
		{"http", "http://dev.azure.com/fabrikam/Proj/_git/repo"},
		{"lookalike suffix", "https://dev.azure.com.evil.com/fabrikam/Proj/_git/repo"},
		{"lookalike vs", "https://notvisualstudio.com/Proj/_git/repo"},
		{"bare visualstudio.com", "https://visualstudio.com/Proj/_git/repo"},
		{"nested vs host", "https://a.fabrikam.visualstudio.com/Proj/_git/repo"},
		{"vs-ssh as https", "https://vs-ssh.visualstudio.com/fabrikam/Proj/_git/repo"},
		{"ssh host as https", "https://ssh.dev.azure.com/v3/fabrikam/Proj/repo"},
		{"https port", "https://dev.azure.com:8443/fabrikam/Proj/_git/repo"},
		{"https port 22", "https://dev.azure.com:22/fabrikam/Proj/_git/repo"},
		{"ssh port", "ssh://git@ssh.dev.azure.com:2222/v3/fabrikam/Proj/repo"},
		{"ssh port 443", "ssh://git@ssh.dev.azure.com:443/v3/fabrikam/Proj/repo"},
		{"empty segment", "https://dev.azure.com/fabrikam//Proj/_git/repo"},
		{"double trailing slash", "https://dev.azure.com/fabrikam/Proj/_git/repo//"},
		{"no path", "https://dev.azure.com"},
		{"org only", "https://dev.azure.com/fabrikam"},
		{"no _git", "https://dev.azure.com/fabrikam/Proj/repo"},
		{"no repo", "https://dev.azure.com/fabrikam/Proj/_git"},
		{"extra after repo", "https://dev.azure.com/fabrikam/Proj/_git/repo/extra"},
		{"team on dev.azure.com", "https://dev.azure.com/fabrikam/Proj/Team/_git/repo"},
		{"too many before _git on vs", "https://fabrikam.visualstudio.com/A/B/C/_git/repo"},
		{"escaped slash", "https://dev.azure.com/fabrikam/Proj/_git/a%2Fb"},
		{"escaped slash fakes segment", "https://dev.azure.com/fabrikam/Proj%2F_git/repo"},
		{"escaped backslash", "https://dev.azure.com/fabrikam/Proj/_git/a%5Cb"},
		{"escaped control", "https://dev.azure.com/fabrikam/Proj/_git/a%0Ab"},
		{"raw control", "https://dev.azure.com/fabrikam/Proj/_git/a\x7fb"},
		{"raw newline", "https://dev.azure.com/fabrikam/Proj/_git/repo\n"},
		{"backslash", `https://dev.azure.com/fabrikam\Proj/_git/repo`},
		{"bad escape", "https://dev.azure.com/fabrikam/Proj/_git/a%zz"},
		{"query", "https://dev.azure.com/fabrikam/Proj/_git/repo?x=1"},
		{"fragment", "https://dev.azure.com/fabrikam/Proj/_git/repo#x"},
		{"ssh v3 without project", "git@ssh.dev.azure.com:v3/fabrikam/repo"},
		{"ssh v3 _full without project", "git@ssh.dev.azure.com:v3/fabrikam/_full/repo"},
		{"ssh v3 too long", "git@ssh.dev.azure.com:v3/fabrikam/Proj/Team/Extra/repo"},
		{"ssh v3 org only", "git@ssh.dev.azure.com:v3/fabrikam"},
		{"ssh without v3", "git@ssh.dev.azure.com:fabrikam/Proj/repo"},
		{"ssh on dev.azure.com", "git@dev.azure.com:v3/fabrikam/Proj/repo"},
		{"ssh on vs org host", "git@fabrikam.visualstudio.com:v3/fabrikam/Proj/repo"},
		{"legacy on ssh.dev.azure.com", "ssh://fabrikam@ssh.dev.azure.com/Proj/_ssh/repo"},
		{"legacy without user", "ssh://vs-ssh.visualstudio.com/Proj/_ssh/repo"},
		{"legacy DefaultCollection", "ssh://fabrikam@vs-ssh.visualstudio.com/DefaultCollection/Proj/_ssh/repo"},
		{"legacy no marker", "ssh://fabrikam@vs-ssh.visualstudio.com/Proj/_git/repo"},
		{"legacy too short", "ssh://fabrikam@vs-ssh.visualstudio.com/_ssh/repo"},
		{"unknown scheme", "git://dev.azure.com/fabrikam/Proj/_git/repo"},
		{"secret in rejected url", "https://me:s3cret@dev.azure.com/fabrikam/Proj/_git/a%zz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRemoteURL(tt.in)
			require.Error(t, err)
			assert.Equal(t, Repo{}, got)
			assert.Equal(t, "not an Azure DevOps remote", err.Error(), "error must never echo the remote")
		})
	}
}

func TestParseRemoteURL_HostConfigKey(t *testing.T) {
	cfg := new(config.Config)
	cfg.Set("fabrikam.visualstudio.com", "editor", "vim")
	cfg.Set("dev.azure.com", "editor", "nano")

	vs, err := ParseRemoteURL("https://Fabrikam.VisualStudio.com/DefaultCollection/Proj/_git/repo")
	require.NoError(t, err)
	assert.Equal(t, "fabrikam.visualstudio.com", vs.Host, "host is not normalized to dev.azure.com")
	assert.Equal(t, "vim", cfg.Get(vs.Host, "editor"))

	dev, err := ParseRemoteURL("https://dev.azure.com/fabrikam/Proj/_git/repo")
	require.NoError(t, err)
	assert.Equal(t, "nano", cfg.Get(dev.Host, "editor"))
}

// fakeSSH puts a copy of the test binary on PATH as `ssh`, answering
// `ssh -G <alias>` with hostname (or nothing when empty), and installs a
// fresh go-gh translator.
func fakeSSH(t *testing.T, hostname string) {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	b, err := os.ReadFile(exe)
	require.NoError(t, err)
	dir := t.TempDir()
	name := "ssh"
	if runtime.GOOS == "windows" {
		name = "ssh.exe"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), b, 0o755))
	t.Setenv("PATH", dir)
	t.Setenv("ADO_FAKE_SSH", "1")
	t.Setenv("ADO_FAKE_SSH_HOSTNAME", hostname)
	old := translateSSH
	translateSSH = ssh.NewTranslator().Translate
	t.Cleanup(func() { translateSSH = old })
}

func TestParseRemoteURL_SSHAlias(t *testing.T) {
	tests := []struct {
		name     string
		resolved string
		in       string
		want     Repo
		wantErr  bool
	}{
		{"alias to ssh.dev.azure.com", "ssh.dev.azure.com", "git@devops_fabrikam:v3/Fabrikam/Project1/fab_repo",
			Repo{Host: "ssh.dev.azure.com", Org: "Fabrikam", Project: "Project1", Name: "fab_repo"}, false},
		{"alias to vs-ssh", "VS-SSH.visualstudio.com", "ssh://git@devops_fabrikam:22/v3/Fabrikam/Project1/fab_repo",
			Repo{Host: "vs-ssh.visualstudio.com", Org: "Fabrikam", Project: "Project1", Name: "fab_repo"}, false},
		{"alias to non-ADO host", "github.com", "git@devops_fabrikam:v3/Fabrikam/Project1/fab_repo", Repo{}, true},
		{"unresolved alias", "", "git@devops_fabrikam:v3/Fabrikam/Project1/fab_repo", Repo{}, true},
		{"lookalike host", "", "git@ssh.dev.azure.com.evil.com:v3/Fabrikam/Project1/fab_repo", Repo{}, true},
		{"alias without project", "ssh.dev.azure.com", "git@devops_fabrikam:v3/Fabrikam/fab_repo", Repo{}, true},
		{"alias with non-v3 path", "ssh.dev.azure.com", "git@devops_fabrikam:Fabrikam/Project1/fab_repo", Repo{}, true},
		{"known host not translated", "github.com", "git@ssh.dev.azure.com:v3/Fabrikam/Project1/fab_repo",
			Repo{Host: "ssh.dev.azure.com", Org: "Fabrikam", Project: "Project1", Name: "fab_repo"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeSSH(t, tt.resolved)
			got, err := ParseRemoteURL(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
