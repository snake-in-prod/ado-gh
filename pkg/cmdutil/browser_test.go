package cmdutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ghbrowser "github.com/cli/go-gh/v2/pkg/browser"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

type mapConfig map[string]string

func (m mapConfig) Get(_, key string) string { return m[key] }

func (m mapConfig) Set(_, key, value string) { m[key] = value }

func (mapConfig) Write() error { return nil }

// ghConfigWithBrowser points go-gh at a gh config that sets a browser, so
// tests prove ado never reads it.
func ghConfigWithBrowser(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("browser: gh-config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_CONFIG_DIR", dir)
	t.Setenv("GH_BROWSER", "gh-env")
}

func TestResolveBrowserLauncher(t *testing.T) {
	tests := []struct {
		name                        string
		adoBrowser, cfg, envBrowser string
		want                        string
	}{
		{"ADO_BROWSER wins", "ado-env", "cfg", "env", "ado-env"},
		{"config over BROWSER", "", "cfg", "env", "cfg"},
		{"BROWSER last", "", "", "env", "env"},
		{"GH_BROWSER and gh config ignored", "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ghConfigWithBrowser(t)
			t.Setenv("ADO_BROWSER", tt.adoBrowser)
			t.Setenv("BROWSER", tt.envBrowser)
			if got := ResolveBrowserLauncher(mapConfig{"browser": tt.cfg}); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewBrowser(t *testing.T) {
	ghConfigWithBrowser(t)

	if _, ok := NewBrowser("", nil, nil).(systemBrowser); !ok {
		t.Error("empty launcher must use the system opener, not go-gh's GH_* resolution")
	}
	b, ok := NewBrowser("ado-no-such-launcher", nil, nil).(*ghbrowser.Browser)
	if !ok {
		t.Fatal("explicit launcher must go through go-gh browser.New")
	}
	err := b.Browse("https://dev.azure.com/org")
	if err == nil || !strings.Contains(err.Error(), "ado-no-such-launcher") {
		t.Errorf("err = %v, want launcher lookup failure", err)
	}
}

func TestSystemBrowserRejectsNonHTTP(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "javascript:alert(1)", "/usr/bin/sh", "::"} {
		if err := (systemBrowser{}).Browse(u); err == nil {
			t.Errorf("Browse(%q) = nil, want error", u)
		}
	}
}

func TestFactoryBrowserUsesResolvedLauncher(t *testing.T) {
	ghConfigWithBrowser(t)
	t.Setenv("ADO_BROWSER", "")
	t.Setenv("BROWSER", "env-launcher-missing")
	ios, _, _, _ := iostreams.Test()
	f := NewFactory("1.0.0", ios)
	f.Config = func() (Config, error) { return mapConfig{"browser": "cfg-launcher-missing"}, nil }

	err := f.Browser.Browse("https://dev.azure.com/org")
	if err == nil || !strings.Contains(err.Error(), "cfg-launcher-missing") {
		t.Errorf("err = %v, want config launcher used", err)
	}
}
