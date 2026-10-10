package cmdutil

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFactoryBaseRepo(t *testing.T) {
	t.Setenv("ADO_REPO", "")
	ios, _, _, _ := iostreams.Test()
	f := NewFactory("1.0.0", ios)
	var gitArgs []string
	f.GitClient.Run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		gitArgs = args
		return []byte("origin\thttps://dev.azure.com/o/p/_git/r (fetch)\n"), nil
	}
	repo, err := f.BaseRepo()
	require.NoError(t, err)
	assert.Equal(t, adocontext.Repo{Host: "dev.azure.com", Org: "o", Project: "p", Name: "r"}, repo)
	assert.Equal(t, []string{"remote", "-v"}, gitArgs)

	f.RepoOverride = "a/b/c" // set by -R after construction
	repo, err = f.BaseRepo()
	require.NoError(t, err)
	assert.Equal(t, adocontext.Repo{Host: "dev.azure.com", Org: "a", Project: "b", Name: "c"}, repo)
}

func TestNewFactoryDebugFromEnv(t *testing.T) {
	for v, want := range map[string]bool{"": false, "0": false, "false": false, "1": true, "true": true, "api": true} {
		t.Setenv("ADO_DEBUG", v)
		ios, _, _, _ := iostreams.Test()
		if got := NewFactory("1.0.0", ios).Debug; got != want {
			t.Errorf("ADO_DEBUG=%q: Debug = %v, want %v", v, got, want)
		}
	}
}

func TestNewFactoryLazy(t *testing.T) {
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	ios, _, _, _ := iostreams.Test()
	f := NewFactory("1.2.3", ios)
	if f.AppVersion != "1.2.3" || f.IOStreams != ios || f.Prompter.IO != ios {
		t.Fatal("factory not wired to version and streams")
	}
	c1, err := f.HttpClient()
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := f.HttpClient()
	if c1 != c2 {
		t.Error("HttpClient must be memoized")
	}
	if c1.Transport == http.DefaultTransport {
		t.Error("HttpClient must not share the global transport")
	}
	cfg, err := f.Config()
	if err != nil || cfg.Get("", "browser") != "" {
		t.Errorf("default config = %v, %v; want empty", cfg, err)
	}
}

func TestNewFactoryConfigFromFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADO_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("browser: firefox\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ios, _, _, _ := iostreams.Test()
	f := NewFactory("1.0.0", ios)
	cfg, err := f.Config()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Get("", "browser"); got != "firefox" {
		t.Errorf("browser = %q, want firefox", got)
	}
	cfg.Set("", "editor", "vim")
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.yml"))
	if !strings.Contains(string(b), "editor: vim") {
		t.Errorf("config not written: %s", b)
	}
	if again, _ := f.Config(); again != cfg {
		t.Error("Config must be memoized")
	}
}
