package help

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/snake-in-prod/ado-gh/internal/build"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
)

// newHelpTestRoot builds a small tree: pr list (aliased, with a flag), a hidden command, an
// alias and an extension, plus the help command.
func newHelpTestRoot(tty bool) (*cobra.Command, *bytes.Buffer) {
	ios, _, out, _ := iostreams.Test()
	ios.SetStdoutTTY(tty)
	f := cmdutil.NewFactory("1.2.3", ios)
	noop := func(*cobra.Command, []string) {}
	root := &cobra.Command{Use: "ado <command>", Short: "Azure DevOps CLI", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(out)
	root.SetErr(out)
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().Bool("debug", false, "Log HTTP requests")
	root.AddGroup(&cobra.Group{ID: "additional", Title: "Additional"}, &cobra.Group{ID: "alias", Title: "Alias"}, &cobra.Group{ID: "extension", Title: "Extension"})
	pr := &cobra.Command{Use: "pr <command>", Short: "Manage pull requests"}
	list := &cobra.Command{Use: "list", Short: "List pull requests", Aliases: []string{"ls"}, Run: noop}
	list.Flags().StringP("state", "s", "open", "Filter by state")
	pr.AddCommand(list)
	root.AddCommand(pr,
		&cobra.Command{Use: "secret", Short: "Hidden one", Hidden: true, Run: noop},
		&cobra.Command{Use: "myalias", Short: "Alias for x", GroupID: "alias", Run: noop},
		&cobra.Command{Use: "myext", Short: "Extension myext", GroupID: "extension", Run: noop})
	help := NewCmdHelp(f)
	root.SetHelpCommand(help)
	root.AddCommand(help)
	return root, out
}

func execHelp(t *testing.T, root *cobra.Command, args ...string) {
	t.Helper()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceStdout(t *testing.T) {
	for _, tty := range []bool{false, true} {
		root, out := newHelpTestRoot(tty)
		execHelp(t, root, "help", "reference")
		got := out.String()
		for _, want := range []string{"# ado reference\n\n", "## `ado pr <command> [flags]`\n\nManage pull requests\n\n", "### `ado pr list [flags]`\n\nList pull requests\n\n",
			"```\n-s, --state string   Filter by state (default \"open\")\n```\n", "Aliases: `ls`"} {
			if !strings.Contains(got, want) {
				t.Errorf("tty=%v: missing %q in:\n%s", tty, want, got)
			}
		}
		for _, bad := range []string{"secret", "myalias", "myext", "`ado help", "\x1b["} {
			if strings.Contains(got, bad) {
				t.Errorf("tty=%v: unexpected %q in:\n%s", tty, bad, got)
			}
		}
	}
}

func TestReferenceDir(t *testing.T) {
	root, out := newHelpTestRoot(false)
	dir := filepath.Join(t.TempDir(), "docs", "reference") // created on demand
	execHelp(t, root, "help", "reference", "--dir", dir)
	if out.Len() != 0 {
		t.Errorf("stdout = %q", out)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if strings.Contains(string(b), "Auto generated") {
			t.Errorf("%s has the autogen tag", e.Name())
		}
	}
	if got := strings.Join(names, " "); got != "ado.md ado_pr.md ado_pr_list.md index.md" {
		t.Errorf("files = %s", got)
	}
	index, _ := os.ReadFile(filepath.Join(dir, "index.md"))
	want := "# ado reference\n\n- [ado](ado.md): Azure DevOps CLI\n- [ado pr](ado_pr.md): Manage pull requests\n- [ado pr list](ado_pr_list.md): List pull requests\n"
	if string(index) != want {
		t.Errorf("index.md = %q, want %q", index, want)
	}
}

func TestReferenceMan(t *testing.T) {
	root, _ := newHelpTestRoot(false)
	dir := t.TempDir()
	execHelp(t, root, "help", "reference", "--man", dir)
	b, err := os.ReadFile(filepath.Join(dir, "ado-pr-list.1"))
	if err != nil {
		t.Fatal(err)
	}
	th := `.TH "ADO-PR-LIST" "1" "` + manDate(build.Date).Format("Jan 2006") + `" "ado" "ado manual"` + "\n"
	if !strings.Contains(string(b), th) {
		t.Errorf("missing %q in:\n%s", th, b)
	}
	for _, name := range []string{"ado.1", "ado-pr.1"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Error(err)
		}
	}
	for _, name := range []string{"ado-secret.1", "ado-myalias.1", "ado-myext.1", "ado-help.1"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s written", name)
		}
	}
	if strings.Contains(string(b), "Auto generated") {
		t.Errorf("autogen tag in:\n%s", b)
	}
}

func TestReferenceDirError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--dir", "--man"} {
		root, _ := newHelpTestRoot(false)
		root.SetArgs([]string{"help", "reference", flag, filepath.Join(file, "sub")})
		if err := root.Execute(); err == nil {
			t.Errorf("%s: no error for a path below a file", flag)
		}
	}
}

func TestManDate(t *testing.T) {
	if got := manDate("2026-10-01T23:30:00-02:00"); !got.Equal(time.Date(2026, 10, 2, 1, 30, 0, 0, time.UTC)) || got.Location() != time.UTC {
		t.Errorf("manDate = %v", got)
	}
	if got := manDate(""); !got.Equal(time.Unix(0, 0)) {
		t.Errorf("manDate(\"\") = %v, want a fixed fallback", got)
	}
}

func TestHelpTopics(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"help", "pr", "list"}, "List pull requests"},
		{[]string{"help"}, "Usage:"},
		{[]string{"help", "nope"}, "Unknown help topic [`nope`]"},
		{[]string{"help", "help"}, "Help provides help for any command"},
	}
	for _, tt := range tests {
		root, out := newHelpTestRoot(false)
		execHelp(t, root, tt.args...)
		if !strings.Contains(out.String(), tt.want) {
			t.Errorf("%v: out = %q, want %q", tt.args, out, tt.want)
		}
	}
}

func TestHelpCompletion(t *testing.T) {
	root, out := newHelpTestRoot(false)
	execHelp(t, root, "__complete", "help", "p")
	if !strings.Contains(out.String(), "pr\tManage pull requests") {
		t.Errorf("out = %q", out)
	}
	root, out = newHelpTestRoot(false)
	execHelp(t, root, "__complete", "help", "pr", "")
	if !strings.Contains(out.String(), "list\tList pull requests") {
		t.Errorf("out = %q", out)
	}
	root, out = newHelpTestRoot(false)
	execHelp(t, root, "__complete", "help", "nope", "")
	if strings.Contains(out.String(), "pr\t") {
		t.Errorf("out = %q", out)
	}
}
