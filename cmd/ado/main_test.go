package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/snake-in-prod/ado-gh/internal/build"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

func TestRun(t *testing.T) {
	t.Setenv("ADO_CONFIG_DIR", t.TempDir()) // never the user's aliases
	oldVersion, oldCommit, oldDate, oldDirty := build.Version, build.Commit, build.Date, build.Dirty
	// as injected by -ldflags "-X …/internal/build.Version=9.9.9 …"
	build.Version, build.Commit, build.Date, build.Dirty = "9.9.9", "abc1234def", "2026-10-10T08:00:00Z", false
	t.Cleanup(func() {
		build.Version, build.Commit, build.Date, build.Dirty = oldVersion, oldCommit, oldDate, oldDirty
	})
	versionOut := "ado version 9.9.9 (2026-10-10)\n" +
		"commit abc1234 · " + runtime.Version() + " · " + runtime.GOOS + "/" + runtime.GOARCH + "\n" +
		"https://github.com/snake-in-prod/ado-gh/releases/tag/v9.9.9\n"

	tests := []struct {
		args       []string
		wantCode   cmdutil.ExitCode
		wantOut    string
		wantErrOut string
	}{
		{[]string{"--version"}, cmdutil.ExitOK, versionOut, ""},
		{[]string{"version"}, cmdutil.ExitOK, versionOut, ""},
		{[]string{"version", "--json", "version,dirty"}, cmdutil.ExitOK, `{"dirty":false,"version":"9.9.9"}` + "\n", ""},
		{[]string{"nope"}, cmdutil.ExitError, "", "unknown command \"nope\" for \"ado\""},
		{[]string{"--nope"}, cmdutil.ExitError, "", "Usage:"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			ios, _, out, errOut := iostreams.Test()
			if got := run(tt.args, ios); got != tt.wantCode {
				t.Errorf("code = %d, want %d", got, tt.wantCode)
			}
			if out.String() != tt.wantOut {
				t.Errorf("stdout = %q, want %q", out, tt.wantOut)
			}
			if !strings.Contains(errOut.String(), tt.wantErrOut) {
				t.Errorf("stderr = %q, want %q", errOut, tt.wantErrOut)
			}
		})
	}
}

// TestMain lets the test binary act as a fake extension: ADO_FAKE_HELLO=1 exits 3, =echo prints
// its directory, args and part of its env.
func TestMain(m *testing.M) {
	switch os.Getenv("ADO_FAKE_HELLO") {
	case "1":
		os.Exit(3)
	case "echo":
		fmt.Printf("exe=%s args=%q repo=%q debug=%q name=%q\n", filepath.Base(filepath.Dir(os.Args[0])), os.Args[1:],
			os.Getenv("ADO_REPO"), os.Getenv("ADO_DEBUG"), os.Getenv("ADO_EXTENSION_NAME"))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeExtension copies the test binary to dir/ado-<name>[.exe].
func fakeExtension(t *testing.T, dir, name string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	file := "ado-" + name
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), b, 0o755); err != nil {
		t.Fatal(err)
	}
}

// isolate gives the test fresh config and data dirs and a PATH holding only a fresh dir, which
// it returns.
func isolate(t *testing.T) (pathDir string) {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("ADO_DEBUG", "")
	t.Setenv("ADO_REPO", "")
	pathDir = t.TempDir()
	t.Setenv("PATH", pathDir)
	return pathDir
}

func TestRunExternalExitCode(t *testing.T) {
	fakeExtension(t, isolate(t), "hello") // a PATH extension
	t.Setenv("ADO_FAKE_HELLO", "1")

	ios, _, _, errOut := iostreams.Test()
	if got := run([]string{"hello"}, ios); got != 3 {
		t.Errorf("code = %d, want 3", got)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errOut)
	}
}

// -R and --debug before the extension name become ADO_REPO / ADO_DEBUG; after it they are the
// extension's own arguments.
func TestRunExtensionGlobalFlags(t *testing.T) {
	fakeExtension(t, isolate(t), "hello")
	t.Setenv("ADO_FAKE_HELLO", "echo")
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"-R", "o/p/r", "--debug", "hello", "-R", "y", "z"}, `args=["-R" "y" "z"] repo="o/p/r" debug="1" name="hello"`},
		{[]string{"hello", "-R", "o/p/r", "--debug"}, `args=["-R" "o/p/r" "--debug"] repo="" debug="" name="hello"`},
		{[]string{"--repo=o/p/r", "hello"}, `args=[] repo="o/p/r" debug="" name="hello"`},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			ios, _, out, errOut := iostreams.Test()
			if got := run(tt.args, ios); got != cmdutil.ExitOK {
				t.Fatalf("code = %d, stderr = %q", got, errOut)
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Errorf("out = %q, want %q", out, tt.want)
			}
		})
	}
}

// Dispatch order: built-in > alias > installed extension > ado-<name> on PATH.
func TestRunDispatchOrder(t *testing.T) {
	pathDir := isolate(t)
	t.Setenv("ADO_FAKE_HELLO", "echo")
	installed := filepath.Join(os.Getenv("XDG_DATA_HOME"), "ado", "extensions", "ado-both")
	for _, name := range []string{"completion", "al", "both", "pathonly"} {
		fakeExtension(t, pathDir, name)
	}
	fakeExtension(t, installed, "both")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetAlias("al", "completion bash")
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		args []string
		want string
	}{
		{[]string{"completion", "bash"}, "bash completion"},
		{[]string{"al"}, "bash completion"},
		{[]string{"both"}, "exe=ado-both "}, // the installed entry's dir, not PATH's
		{[]string{"pathonly", "x"}, "exe=" + filepath.Base(pathDir) + ` args=["x"]`},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			ios, _, out, errOut := iostreams.Test()
			if got := run(tt.args, ios); got != cmdutil.ExitOK {
				t.Fatalf("code = %d, stderr = %q", got, errOut)
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Errorf("out = %.200q, want %q", out, tt.want)
			}
		})
	}
}

func TestRunUnknownWithoutPathExtension(t *testing.T) {
	isolate(t)
	ios, _, _, errOut := iostreams.Test()
	if got := run([]string{"-R", "o/p/r", "nope"}, ios); got != cmdutil.ExitError || !strings.Contains(errOut.String(), `unknown command "nope"`) {
		t.Errorf("code = %d, stderr = %q", got, errOut)
	}
}

// `help <ext>` runs `<ext> --help`; other help topics stay with cobra.
func TestRunHelpExtension(t *testing.T) {
	pathDir := isolate(t)
	t.Setenv("ADO_FAKE_HELLO", "echo")
	fakeExtension(t, filepath.Join(os.Getenv("XDG_DATA_HOME"), "ado", "extensions", "ado-hello"), "hello")
	fakeExtension(t, pathDir, "pathonly")
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"help", "hello"}, `args=["--help"] repo="" debug="" name="hello"`},
		{[]string{"-R", "o/p/r", "help", "pathonly"}, `args=["--help"] repo="o/p/r" debug="" name="pathonly"`},
		{[]string{"help", "completion"}, "Generate shell completion scripts"},
		{[]string{"help", "nope"}, "Unknown help topic"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			ios, _, out, errOut := iostreams.Test()
			if got := run(tt.args, ios); got != cmdutil.ExitOK {
				t.Fatalf("code = %d, stderr = %q", got, errOut)
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Errorf("out = %.200q, want %q", out, tt.want)
			}
		})
	}
}

// The unknown-command fallback looks up only the name given, never every ado-* on PATH.
func TestRunPathLookupOnlyGivenName(t *testing.T) {
	pathDir := isolate(t)
	t.Setenv("ADO_FAKE_HELLO", "echo")
	fakeExtension(t, pathDir, "pathonly")
	fakeExtension(t, pathDir, "other")
	var rootCmd *cobra.Command
	old := newCmdRoot
	t.Cleanup(func() { newCmdRoot = old })
	newCmdRoot = func(f *cmdutil.Factory) *cobra.Command {
		rootCmd = old(f)
		return rootCmd
	}
	ios, _, _, errOut := iostreams.Test()
	if got := run([]string{"pathonly"}, ios); got != cmdutil.ExitOK {
		t.Fatalf("code = %d, stderr = %q", got, errOut)
	}
	if c, _, err := rootCmd.Find([]string{"other"}); err == nil && c != rootCmd {
		t.Error("ado-other registered without being named")
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
	if gotUA != "ado-cli/"+build.Version {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if !strings.Contains(errOut.String(), "> GET "+srv.URL) {
		t.Errorf("debug log = %q", errOut)
	}
}
