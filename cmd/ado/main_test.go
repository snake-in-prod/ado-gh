package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

func TestRun(t *testing.T) {
	t.Setenv("ADO_CONFIG_DIR", t.TempDir()) // never the user's aliases
	old := version
	version = "9.9.9" // as injected by -ldflags "-X main.version=9.9.9"
	t.Cleanup(func() { version = old })

	tests := []struct {
		args       []string
		wantCode   cmdutil.ExitCode
		wantOut    string
		wantErrOut string
	}{
		{[]string{"--version"}, cmdutil.ExitOK, "ado version 9.9.9\n", ""},
		{[]string{"nope"}, cmdutil.ExitError, "", "unknown command \"nope\" for \"ado\""},
		{[]string{"--nope"}, cmdutil.ExitError, "", "Usage:"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			ios, _, out, errOut := iostreams.Test()
			if got := run(tt.args, ios); got != tt.wantCode {
				t.Errorf("code = %d, want %d", got, tt.wantCode)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("stdout = %q, want %q", out, tt.wantOut)
			}
			if !strings.Contains(errOut.String(), tt.wantErrOut) {
				t.Errorf("stderr = %q, want %q", errOut, tt.wantErrOut)
			}
		})
	}
}

// TestMain lets the test binary act as a fake extension `ado-hello` that exits 3.
func TestMain(m *testing.M) {
	if os.Getenv("ADO_FAKE_HELLO") == "1" {
		os.Exit(3)
	}
	os.Exit(m.Run())
}

func TestRunExternalExitCode(t *testing.T) {
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	old := newCmdRoot
	t.Cleanup(func() { newCmdRoot = old })
	newCmdRoot = func(f *cmdutil.Factory) *cobra.Command {
		c := old(f)
		// Stands in for extension dispatch until Phase 2 registers ado-<name> commands.
		c.AddCommand(&cobra.Command{Use: "hello", RunE: func(*cobra.Command, []string) error {
			hello := exec.Command(exe)
			hello.Env = append(os.Environ(), "ADO_FAKE_HELLO=1")
			return cmdutil.ExternalExit(hello.Run())
		}})
		return c
	}

	ios, _, _, errOut := iostreams.Test()
	if got := run([]string{"hello"}, ios); got != 3 {
		t.Errorf("code = %d, want 3", got)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errOut)
	}
}

func TestGlobalFlagsLen(t *testing.T) {
	tests := []struct {
		args []string
		want int
	}{
		{nil, 0},
		{[]string{"prs", "-R", "x"}, 0},
		{[]string{"--debug", "prs"}, 1},
		{[]string{"--debug=false", "prs"}, 1},
		{[]string{"-R", "o/p/r", "prs"}, 2},
		{[]string{"--repo", "o/p/r", "prs"}, 2},
		{[]string{"--repo=o/p/r", "prs"}, 1},
		{[]string{"-Ro/p/r", "--debug", "prs", "x"}, 2},
		{[]string{"-R"}, 1},
		{[]string{"--version"}, 0},
	}
	for _, tt := range tests {
		if got := globalFlagsLen(tt.args); got != tt.want {
			t.Errorf("globalFlagsLen(%q) = %d, want %d", tt.args, got, tt.want)
		}
	}
}

func TestRunAliasPeelsGlobalFlags(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil || runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_DEBUG", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetAlias("args", `!echo "$#:$1"`)
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}
	var f *cmdutil.Factory
	old := newCmdRoot
	t.Cleanup(func() { newCmdRoot = old })
	newCmdRoot = func(ff *cmdutil.Factory) *cobra.Command {
		f = ff
		return old(ff)
	}

	tests := []struct {
		args      []string
		wantOut   string
		wantRepo  string
		wantDebug bool
	}{
		{[]string{"-R", "o/p/r", "args", "x"}, "1:x\n", "o/p/r", false},
		{[]string{"--debug", "--repo=o/p/r", "args"}, "0:\n", "o/p/r", true},
		{[]string{"-Ro/p/r", "args", "-R", "y"}, "2:-R\n", "o/p/r", false}, // after the name: the alias's own args
		{[]string{"args", "--debug"}, "1:--debug\n", "", false},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			ios, _, out, errOut := iostreams.Test()
			if got := run(tt.args, ios); got != cmdutil.ExitOK {
				t.Fatalf("code = %d, stderr = %q", got, errOut)
			}
			if out.String() != tt.wantOut || f.RepoOverride != tt.wantRepo || f.Debug != tt.wantDebug {
				t.Errorf("out = %q, repo = %q, debug = %v", out, f.RepoOverride, f.Debug)
			}
		})
	}
}

func TestRunBuiltinKeepsGlobalFlags(t *testing.T) {
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	ios, _, out, _ := iostreams.Test()
	if got := run([]string{"-R", "o/p/r", "completion", "bash"}, ios); got != cmdutil.ExitOK || !strings.Contains(out.String(), "bash completion") {
		t.Errorf("code = %d, out = %.80q", got, out)
	}
}

func TestNewFactoryHTTPClient(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	ios, _, _, errOut := iostreams.Test()
	f := newFactory(ios)
	f.Debug = true // as set by --debug before the client is first built
	c, err := f.HttpClient()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotUA != "ado-cli/"+version {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if !strings.Contains(errOut.String(), "> GET "+srv.URL) {
		t.Errorf("debug log = %q", errOut)
	}
}
