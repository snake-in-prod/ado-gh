package cmdutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ghbrowser "github.com/cli/go-gh/v2/pkg/browser"
	"github.com/snake-in-prod/ado-gh/internal/browsertest"
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

func TestOpenInBrowser(t *testing.T) {
	const u = "https://dev.azure.com/org/My%20Proj/_git/web?a=1"
	const msg = "Opening " + u + " in your browser.\n"
	tests := []struct {
		name    string
		tty     bool
		err     error
		wantMsg string
	}{
		{name: "tty prints full URL on stderr", tty: true, wantMsg: msg},
		{name: "non-tty silent", tty: false},
		{name: "launcher error surfaced", tty: true, err: errors.New("boom"), wantMsg: msg},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, out, errOut := iostreams.Test()
			ios.SetStdoutTTY(tt.tty)
			b := &browsertest.Browser{Err: tt.err}
			if err := OpenInBrowser(&Factory{IOStreams: ios, Browser: b}, u); !errors.Is(err, tt.err) {
				t.Errorf("err = %v, want %v", err, tt.err)
			}
			if len(b.URLs) != 1 || b.URLs[0] != u {
				t.Errorf("opened %q, want [%q]", b.URLs, u)
			}
			if errOut.String() != tt.wantMsg || out.Len() != 0 {
				t.Errorf("stderr %q stdout %q, want stderr %q", errOut.String(), out.String(), tt.wantMsg)
			}
		})
	}
}

// TestOpenInBrowserLauncherPrecedence runs the production Factory browser with missing
// launchers, so the surfaced error names the launcher that was chosen.
func TestOpenInBrowserLauncherPrecedence(t *testing.T) {
	tests := []struct {
		name                        string
		adoBrowser, cfg, envBrowser string
		want                        string
	}{
		{"ADO_BROWSER first", "ado-env-missing", "cfg-missing", "env-missing", "ado-env-missing"},
		{"config second", "", "cfg-missing", "env-missing", "cfg-missing"},
		{"BROWSER third", "", "", "env-missing", "env-missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ghConfigWithBrowser(t)
			t.Setenv("ADO_BROWSER", tt.adoBrowser)
			t.Setenv("BROWSER", tt.envBrowser)
			ios, _, _, _ := iostreams.Test()
			f := NewFactory("1.0.0", ios)
			f.Config = func() (Config, error) { return mapConfig{"browser": tt.cfg}, nil }
			err := OpenInBrowser(f, "https://dev.azure.com/org")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want launcher %q used", err, tt.want)
			}
		})
	}
}

func TestSystemBrowserWSL(t *testing.T) {
	const u = "https://login.microsoftonline.com/x?a=1&b=2"
	tests := []struct {
		name    string
		env     map[string]string
		goos    string
		paths   map[string]bool
		wantCmd []string // nil: not a WSL launch
	}{
		{name: "wslview when installed", env: map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, goos: "linux",
			paths: map[string]bool{"wslview": true}, wantCmd: []string{"wslview", u}},
		{name: "rundll32 without wslview", env: map[string]string{"WSL_INTEROP": "/run/WSL/1_interop"}, goos: "linux",
			wantCmd: []string{"rundll32.exe", "url.dll,FileProtocolHandler", u}},
		{name: "plain linux uses the system opener", goos: "linux"},
		{name: "WSL vars ignored off linux", env: map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, goos: "windows"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			opened := false
			b := systemBrowser{
				goos:   tt.goos,
				getenv: func(k string) string { return tt.env[k] },
				lookPath: func(f string) (string, error) {
					if tt.paths[f] {
						return f, nil
					}
					return "", os.ErrNotExist
				},
				run:     func(name string, args ...string) error { got = append([]string{name}, args...); return nil },
				openURL: func(string) error { opened = true; return nil },
			}
			if err := b.Browse(u); err != nil {
				t.Fatal(err)
			}
			if tt.wantCmd == nil {
				if !opened || got != nil {
					t.Errorf("want system opener, ran %v", got)
				}
				return
			}
			if opened || strings.Join(got, " ") != strings.Join(tt.wantCmd, " ") {
				t.Errorf("ran %q (system opener %v), want %q", got, opened, tt.wantCmd)
			}
		})
	}
}
