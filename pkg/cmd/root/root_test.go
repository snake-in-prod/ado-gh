package root

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

func run(t *testing.T, args ...string) (f *cmdutil.Factory, stdout string, err error) {
	t.Helper()
	return runWithAliases(t, nil, args...)
}

// runWithAliases runs ado with a fresh config dir holding aliases.
func runWithAliases(t *testing.T, aliases map[string]string, args ...string) (f *cmdutil.Factory, stdout string, err error) {
	t.Helper()
	t.Setenv("ADO_DEBUG", "")
	seedAliases(t, aliases)
	ios, _, out, _ := iostreams.Test()
	f = cmdutil.NewFactory("1.2.3", ios)
	cmd := NewCmdRoot(f)
	cmd.SetArgs(args)
	_, err = cmd.ExecuteC()
	return f, out.String(), err
}

func TestVersion(t *testing.T) {
	_, out, err := run(t, "--version")
	if err != nil || out != "ado version 1.2.3\n" {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

func TestHelpListsGroups(t *testing.T) {
	_, out, err := run(t, "help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Core commands", "Additional commands", "completion", "api", "auth", "browse", "config", "pr", "repo", "--debug", "--version"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q:\n%s", want, out)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	_, _, err := run(t, "nope")
	if err == nil || !strings.Contains(err.Error(), `unknown command "nope" for "ado"`) {
		t.Errorf("err = %v", err)
	}
}

func TestUnknownFlagIsFlagError(t *testing.T) {
	_, _, err := run(t, "--nope")
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("err = %v, want FlagError", err)
	}
}

func TestDebugFlag(t *testing.T) {
	f, _, err := run(t, "completion", "bash")
	if err != nil || f.Debug {
		t.Fatalf("debug on without flag: %v", err)
	}
	f, _, err = run(t, "--debug", "completion", "bash")
	if err != nil || !f.Debug {
		t.Errorf("--debug did not enable debug: %v", err)
	}
}

func TestRepoFlag(t *testing.T) {
	for _, flag := range []string{"-R", "--repo"} {
		f, _, err := run(t, flag, "o/p/r", "completion", "bash")
		if err != nil || f.RepoOverride != "o/p/r" {
			t.Errorf("%s: RepoOverride = %q, err = %v", flag, f.RepoOverride, err)
		}
	}
}

func TestDebugFlagDefaultsToFactory(t *testing.T) {
	seedAliases(t, nil)
	ios, _, _, _ := iostreams.Test()
	f := cmdutil.NewFactory("1.2.3", ios)
	f.Debug = true // as set from ADO_DEBUG
	cmd := NewCmdRoot(f)
	cmd.SetArgs([]string{"completion", "bash"})
	if err := cmd.Execute(); err != nil || !f.Debug {
		t.Errorf("ADO_DEBUG lost after flag parsing: %v", err)
	}
}

// webCmds must expose -w/--web.
var webCmds = map[string]bool{
	"ado pr checks":       true,
	"ado pr comment":      true,
	"ado pr create":       true,
	"ado pr diff":         true,
	"ado pr list":         true,
	"ado pr view":         true,
	"ado board":           true,
	"ado sprint current":  true,
	"ado sprint view":     true,
	"ado pipeline run":    true,
	"ado pipeline view":   true,
	"ado release create":  true,
	"ado release view":    true,
	"ado repo view":       true,
	"ado workitem create": true,
	"ado workitem list":   true,
	"ado workitem query":  true,
	"ado workitem view":   true,
}

// noWebCmds is the documented exclusion list: command path -> reason it has no --web.
var noWebCmds = map[string]string{
	"ado api":                 "raw REST access, no single page",
	"ado completion":          "prints shell scripts, no page",
	"ado auth login":          "local credentials, no page",
	"ado auth logout":         "local credentials, no page",
	"ado auth status":         "local credentials, no page",
	"ado auth token":          "local credentials, no page",
	"ado config get":          "local config, no page",
	"ado config list":         "local config, no page",
	"ado config set":          "local config, no page",
	"ado pr checkout":         "local git operation",
	"ado pr close":            "state change, prints the PR URL",
	"ado pr edit":             "state change, prints the PR URL",
	"ado pr merge":            "state change, prints the PR URL",
	"ado pr ready":            "state change, prints the PR URL",
	"ado pr reopen":           "state change, prints the PR URL",
	"ado pr review":           "state change, prints the PR URL",
	"ado pr status":           "summary across several PRs, no single page",
	"ado repo list":           "listing across projects, no single page",
	"ado pipeline approve":    "acts on approvals; `ado pipeline view -w` opens the run",
	"ado pipeline cancel":     "changes run state; `ado pipeline view -w` opens the run",
	"ado pipeline reject":     "acts on approvals; `ado pipeline view -w` opens the run",
	"ado pipeline rerun":      "prints the new run URL; `ado pipeline view -w` opens it",
	"ado pipeline watch":      "terminal watch loop; `ado pipeline view -w` opens the run",
	"ado pipeline disable":    "changes queue status; `ado pipeline view -w` opens runs",
	"ado pipeline download":   "writes files locally",
	"ado pipeline enable":     "changes queue status; `ado pipeline view -w` opens runs",
	"ado pipeline list":       "lists runs/definitions; `ado pipeline view -w` opens a run",
	"ado pipeline logs":       "prints logs; `ado pipeline view -w` opens the run",
	"ado release approve":     "acts on approvals; `ado release view -w` opens the release",
	"ado release cancel":      "changes deployment state; `ado release view -w` opens the release",
	"ado release deploy":      "changes deployment state; `ado release view -w` opens the release",
	"ado release list":        "lists releases; `ado release view -w` opens one",
	"ado release logs":        "prints logs; `ado release view -w` opens the release",
	"ado release reject":      "acts on approvals; `ado release view -w` opens the release",
	"ado release watch":       "terminal watch loop; `ado release view -w` opens the release",
	"ado repo clone":          "local git operation",
	"ado repo create":         "creation prints the new repository URL",
	"ado repo delete":         "destructive, no page to open",
	"ado repo restore":        "restore prints the repository URL",
	"ado repo set-default":    "local git config",
	"ado sprint board":        "terminal taskboard; `ado sprint view -w` opens the taskboard",
	"ado sprint list":         "lists iterations; `ado sprint current -w` opens the current sprint",
	"ado workitem assign":     "changes fields; `ado wi view -w` opens the item",
	"ado workitem close":      "changes state; `ado wi view -w` opens the item",
	"ado workitem comment":    "prints the comment URL",
	"ado workitem edit":       "changes fields; `ado wi view -w` opens the item",
	"ado workitem query list": "lists saved queries; `ado wi query <path> -w` opens one",
	"ado workitem reopen":     "changes state; `ado wi view -w` opens the item",
	"ado workitem delete":     "removes the item",
	"ado workitem link":       "changes links; `ado wi view -w` opens the item",
	"ado workitem restore":    "prints the work item URL",
	"ado workitem unlink":     "changes links; `ado wi view -w` opens the item",
	"ado browse":              "opens the browser by default (-n prints the URL)",
	"ado alias set":           "local config, no page",
	"ado alias list":          "local config, no page",
	"ado alias delete":        "local config, no page",
	"ado alias import":        "local config, no page",
}

// TestWebFlagAudit fails for commands missing from both lists and for stale entries.
func TestWebFlagAudit(t *testing.T) {
	seedAliases(t, nil)
	ios, _, _, _ := iostreams.Test()
	seen := map[string]bool{}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, s := range c.Commands() {
			walk(s)
		}
		if !c.Runnable() {
			return
		}
		p := c.CommandPath()
		seen[p] = true
		fl := c.Flags().Lookup("web")
		reason, excluded := noWebCmds[p]
		switch {
		case webCmds[p] && excluded:
			t.Errorf("%s: in both webCmds and noWebCmds", p)
		case webCmds[p] && (fl == nil || fl.Shorthand != "w"):
			t.Errorf("%s: missing -w/--web", p)
		case excluded && fl != nil:
			t.Errorf("%s: has --web but is excluded (%s)", p, reason)
		case !webCmds[p] && !excluded:
			t.Errorf("%s: unclassified; add it to webCmds or noWebCmds", p)
		}
	}
	walk(NewCmdRoot(cmdutil.NewFactory("test", ios)))
	for p := range webCmds {
		if !seen[p] {
			t.Errorf("stale webCmds entry %q", p)
		}
	}
	for p := range noWebCmds {
		if !seen[p] {
			t.Errorf("stale noWebCmds entry %q", p)
		}
	}
}

func TestCompletion(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		_, out, err := run(t, "completion", shell)
		if err != nil || !strings.Contains(out, "ado") {
			t.Errorf("%s: err = %v, out = %.80q", shell, err, out)
		}
	}
}

// seedAliases points ADO_CONFIG_DIR at a fresh dir, so the user's own aliases never leak in,
// and saves aliases there.
func seedAliases(t *testing.T, aliases map[string]string) {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	if len(aliases) == 0 {
		return
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range aliases {
		cfg.SetAlias(k, v)
	}
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}
}

func TestAliasDispatch(t *testing.T) {
	aliases := map[string]string{
		"co":   "completion $1",
		"c":    "completion",
		"pr":   "completion bash", // built-in wins
		"wi":   "completion bash", // built-in cobra alias wins
		"help": "completion bash", // reserved
		"cc":   "co bash",         // an alias expands once: no chaining
	}
	tests := []struct {
		args    []string
		wantOut string
		wantErr string
	}{
		{args: []string{"co", "bash"}, wantOut: "bash completion V2 for ado"},
		{args: []string{"c", "zsh"}, wantOut: "zsh completion for ado"}, // unused args appended
		{args: []string{"co"}, wantErr: "not enough arguments"},
		{args: []string{"cc"}, wantErr: "does not start with an ado command"},
		{args: []string{"pr"}, wantOut: "Manage pull requests"},
		{args: []string{"wi"}, wantOut: "Manage work items"},
		{args: []string{"help"}, wantOut: "Alias commands"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			_, out, err := runWithAliases(t, aliases, tt.args...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !strings.Contains(out, tt.wantOut) {
				t.Errorf("err = %v, out = %.200q, want %q", err, out, tt.wantOut)
			}
		})
	}
}

func TestAliasHelpAndCompletion(t *testing.T) {
	aliases := map[string]string{"co": "completion $1", "pr": "completion bash", "sh1": "!echo hi"}
	_, out, err := runWithAliases(t, aliases, "--help")
	if err != nil {
		t.Fatal(err)
	}
	_, section, _ := strings.Cut(out, "Alias commands")
	section, _, _ = strings.Cut(section, "\n\n")
	for _, want := range []string{"co", `Alias for "completion $1"`, "sh1"} {
		if !strings.Contains(section, want) {
			t.Errorf("alias section missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(section, "pr ") {
		t.Errorf("alias shadowing a built-in listed:\n%s", section)
	}

	_, out, err = runWithAliases(t, aliases, "__complete", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"co\t", "sh1\t"} {
		if !strings.Contains(out, want) {
			t.Errorf("completion missing %q:\n%s", want, out)
		}
	}
}

func TestNoAliasGroupWithoutAliases(t *testing.T) {
	for _, aliases := range []map[string]string{nil, {"pr": "repo list", "help": "pr list"}} {
		// Only shadowed aliases: still no group, and no cobra panic.
		_, out, err := runWithAliases(t, aliases, "--help")
		if err != nil || strings.Contains(out, "Alias commands") {
			t.Errorf("%v: err = %v, empty alias section shown:\n%s", aliases, err, out)
		}
	}
}

func TestShellAliasDispatch(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil || runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	_, out, err := runWithAliases(t, map[string]string{"greet": `!echo "hi $1" "$2"`}, "greet", "a b", "--debug")
	if err != nil || out != "hi a b --debug\n" {
		t.Errorf("out = %q, err = %v", out, err)
	}
}
