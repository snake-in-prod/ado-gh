package iostreams

import (
	"fmt"
	"os"
	"testing"
)

// pipeFiles returns non-terminal *os.File handles for stdin, stdout and stderr.
func pipeFiles(t *testing.T) (in, out, errOut *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	return r, w, w
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"ADO_FORCE_TTY", "GH_FORCE_TTY", "NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "ADO_PROMPT_DISABLED", "CI"} {
		t.Setenv(k, "")
	}
}

func TestFromFiles(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantTTY   bool
		wantColor bool
		wantWidth int
		wantPrmpt bool
	}{
		{name: "piped", wantWidth: 80},
		{name: "GH_FORCE_TTY ignored", env: map[string]string{"GH_FORCE_TTY": "1"}, wantWidth: 80},
		{name: "ADO_FORCE_TTY true", env: map[string]string{"ADO_FORCE_TTY": "true"}, wantTTY: true, wantColor: true, wantWidth: 80, wantPrmpt: true},
		{name: "ADO_FORCE_TTY width", env: map[string]string{"ADO_FORCE_TTY": "120"}, wantTTY: true, wantColor: true, wantWidth: 120, wantPrmpt: true},
		{name: "ADO_FORCE_TTY false", env: map[string]string{"ADO_FORCE_TTY": "false"}, wantWidth: 80},
		{name: "ADO_FORCE_TTY 0", env: map[string]string{"ADO_FORCE_TTY": "0"}, wantWidth: 80},
		{name: "NO_COLOR on TTY", env: map[string]string{"ADO_FORCE_TTY": "1", "NO_COLOR": "1"}, wantTTY: true, wantWidth: 80, wantPrmpt: true},
		{name: "CLICOLOR=0 on TTY", env: map[string]string{"ADO_FORCE_TTY": "1", "CLICOLOR": "0"}, wantTTY: true, wantWidth: 80, wantPrmpt: true},
		{name: "CLICOLOR_FORCE when piped", env: map[string]string{"CLICOLOR_FORCE": "1"}, wantColor: true, wantWidth: 80},
		{name: "CLICOLOR_FORCE beats NO_COLOR", env: map[string]string{"CLICOLOR_FORCE": "1", "NO_COLOR": "1"}, wantColor: true, wantWidth: 80},
		{name: "ADO_PROMPT_DISABLED", env: map[string]string{"ADO_FORCE_TTY": "1", "ADO_PROMPT_DISABLED": "1"}, wantTTY: true, wantColor: true, wantWidth: 80},
		{name: "CI", env: map[string]string{"ADO_FORCE_TTY": "1", "CI": "true"}, wantTTY: true, wantColor: true, wantWidth: 80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			ios := fromFiles(pipeFiles(t))
			for name, got := range map[string]bool{"stdin": ios.IsStdinTTY(), "stdout": ios.IsStdoutTTY(), "stderr": ios.IsStderrTTY()} {
				if got != tt.wantTTY {
					t.Errorf("%s TTY = %v, want %v", name, got, tt.wantTTY)
				}
			}
			if got := ios.ColorEnabled(); got != tt.wantColor {
				t.Errorf("ColorEnabled() = %v, want %v", got, tt.wantColor)
			}
			if got := ios.TerminalWidth(); got != tt.wantWidth {
				t.Errorf("TerminalWidth() = %d, want %d", got, tt.wantWidth)
			}
			if got := ios.CanPrompt(); got != tt.wantPrmpt {
				t.Errorf("CanPrompt() = %v, want %v", got, tt.wantPrmpt)
			}
		})
	}
}

func TestSystem(t *testing.T) {
	ios := System()
	if ios.In != os.Stdin || ios.Out != os.Stdout || ios.ErrOut != os.Stderr {
		t.Error("System() must use the process standard streams")
	}
}

func TestTest(t *testing.T) {
	ios, in, out, errOut := Test()
	if ios.IsStdinTTY() || ios.IsStdoutTTY() || ios.IsStderrTTY() || ios.ColorEnabled() {
		t.Error("Test() must default to non-TTY without color")
	}
	if got := ios.TerminalWidth(); got != 80 {
		t.Errorf("TerminalWidth() = %d, want 80", got)
	}
	in.WriteString("input")
	buf := make([]byte, 5)
	if _, err := ios.In.Read(buf); err != nil || string(buf) != "input" {
		t.Errorf("In read %q, %v", buf, err)
	}
	fmt.Fprint(ios.Out, "out")
	fmt.Fprint(ios.ErrOut, "err")
	if out.String() != "out" || errOut.String() != "err" {
		t.Errorf("captured out=%q err=%q", out, errOut)
	}

	ios.SetStdinTTY(true)
	ios.SetStdoutTTY(true)
	ios.SetStderrTTY(true)
	ios.SetColorEnabled(true)
	ios.SetTerminalWidth(42)
	if !ios.IsStdinTTY() || !ios.IsStdoutTTY() || !ios.IsStderrTTY() || !ios.ColorEnabled() || ios.TerminalWidth() != 42 {
		t.Error("setters did not take effect")
	}
	if !ios.CanPrompt() {
		t.Error("CanPrompt() = false with stdin and stdout TTY")
	}
	ios.SetNeverPrompt(true)
	if ios.CanPrompt() {
		t.Error("CanPrompt() = true with neverPrompt")
	}
}

func TestColorScheme(t *testing.T) {
	ios, _, _, _ := Test()
	off := ios.ColorScheme()
	ios.SetColorEnabled(true)
	on := ios.ColorScheme()

	fns := map[string]struct {
		f    func(*ColorScheme) func(string) string
		code string
	}{
		"Bold":    {func(c *ColorScheme) func(string) string { return c.Bold }, "1"},
		"Red":     {func(c *ColorScheme) func(string) string { return c.Red }, "31"},
		"Green":   {func(c *ColorScheme) func(string) string { return c.Green }, "32"},
		"Yellow":  {func(c *ColorScheme) func(string) string { return c.Yellow }, "33"},
		"Magenta": {func(c *ColorScheme) func(string) string { return c.Magenta }, "35"},
		"Cyan":    {func(c *ColorScheme) func(string) string { return c.Cyan }, "36"},
		"Gray":    {func(c *ColorScheme) func(string) string { return c.Gray }, "90"},
	}
	for name, tt := range fns {
		if got := tt.f(off)("x"); got != "x" {
			t.Errorf("%s off = %q, want plain", name, got)
		}
		want := "\x1b[" + tt.code + "mx\x1b[0m"
		if got := tt.f(on)("x"); got != want {
			t.Errorf("%s on = %q, want %q", name, got, want)
		}
	}
}

func TestRefreshScreen(t *testing.T) {
	ios, _, out, _ := Test()
	ios.RefreshScreen()
	if out.Len() != 0 {
		t.Errorf("non-TTY RefreshScreen wrote %q, want nothing", out.String())
	}
	ios.SetStdoutTTY(true)
	ios.RefreshScreen()
	if got, want := out.String(), "\x1b[0;0H\x1b[J"; got != want {
		t.Errorf("TTY RefreshScreen wrote %q, want %q", got, want)
	}
}

func TestFromFilesEnablesVirtualTerminal(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "colour on TTY", env: map[string]string{"ADO_FORCE_TTY": "1"}, want: true},
		{name: "TTY with colour disabled", env: map[string]string{"ADO_FORCE_TTY": "1", "NO_COLOR": "1"}, want: true},
		{name: "not a TTY", env: map[string]string{"CLICOLOR_FORCE": "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			var got []*os.File
			orig := enableVirtualTerminal
			t.Cleanup(func() { enableVirtualTerminal = orig })
			enableVirtualTerminal = func(f *os.File) { got = append(got, f) }
			in, out, _ := pipeFiles(t)
			_, errOut, _ := pipeFiles(t)
			fromFiles(in, out, errOut)
			if !tt.want {
				if len(got) != 0 {
					t.Errorf("enableVirtualTerminal called %d times, want none", len(got))
				}
				return
			}
			if len(got) != 2 || got[0] != out || got[1] != errOut {
				t.Errorf("enableVirtualTerminal calls = %v, want stdout then stderr", got)
			}
		})
	}
}

func TestEnableVirtualTerminalDefault(t *testing.T) {
	_, out, _ := pipeFiles(t)
	enableVirtualTerminal(out) // no-op off Windows; ignores the error for a non-console handle on Windows
}
