package root

import (
	"errors"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
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
	for _, want := range []string{"Core commands", "Additional commands", "completion", "config", "--debug", "--version"} {
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

func TestCompletion(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		_, out, err := run(t, "completion", shell)
		if err != nil || !strings.Contains(out, "ado") {
			t.Errorf("%s: err = %v, out = %.80q", shell, err, out)
		}
	}
}
