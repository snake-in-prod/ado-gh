package root

import (
	"errors"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

func run(t *testing.T, args ...string) (f *cmdutil.Factory, stdout string, err error) {
	t.Helper()
	t.Setenv("ADO_DEBUG", "")
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
	"ado pr checks":  true,
	"ado pr comment": true,
	"ado pr create":  true,
	"ado pr diff":    true,
	"ado pr list":    true,
	"ado pr view":    true,
	"ado repo view":  true,
}

// noWebCmds is the documented exclusion list: command path -> reason it has no --web.
var noWebCmds = map[string]string{
	"ado api":              "raw REST access, no single page",
	"ado completion":       "prints shell scripts, no page",
	"ado auth login":       "local credentials, no page",
	"ado auth logout":      "local credentials, no page",
	"ado auth status":      "local credentials, no page",
	"ado auth token":       "local credentials, no page",
	"ado config get":       "local config, no page",
	"ado config list":      "local config, no page",
	"ado config set":       "local config, no page",
	"ado pr checkout":      "local git operation",
	"ado pr close":         "state change, prints the PR URL",
	"ado pr edit":          "state change, prints the PR URL",
	"ado pr merge":         "state change, prints the PR URL",
	"ado pr ready":         "state change, prints the PR URL",
	"ado pr reopen":        "state change, prints the PR URL",
	"ado pr review":        "state change, prints the PR URL",
	"ado pr status":        "summary across several PRs, no single page",
	"ado repo list":        "listing across projects, no single page",
	"ado repo clone":       "local git operation",
	"ado repo create":      "creation prints the new repository URL",
	"ado repo delete":      "destructive, no page to open",
	"ado repo restore":     "restore prints the repository URL",
	"ado repo set-default": "local git config",
	"ado browse":           "opens the browser by default (-n prints the URL)",
}

// TestWebFlagAudit fails for commands missing from both lists and for stale entries.
func TestWebFlagAudit(t *testing.T) {
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
