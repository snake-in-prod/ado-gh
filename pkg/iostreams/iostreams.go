// Package iostreams wraps the standard streams with TTY, color and terminal
// width detection, and provides a buffer-backed constructor for tests.
package iostreams

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"

	ghterm "github.com/cli/go-gh/v2/pkg/term"
	"golang.org/x/term"
)

const defaultWidth = 80

// IOStreams holds the input/output streams and terminal capabilities of a command.
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	stdinTTY     bool
	stdoutTTY    bool
	stderrTTY    bool
	colorEnabled bool
	neverPrompt  bool
	termWidth    int
}

// System returns IOStreams for the process standard streams, honouring
// ADO_FORCE_TTY, NO_COLOR, CLICOLOR, CLICOLOR_FORCE, ADO_PROMPT_DISABLED and CI.
func System() *IOStreams {
	return fromFiles(os.Stdin, os.Stdout, os.Stderr)
}

func fromFiles(in, out, errOut *os.File) *IOStreams {
	force, width := parseForceTTY(os.Getenv("ADO_FORCE_TTY"))
	ios := &IOStreams{
		In:          in,
		Out:         out,
		ErrOut:      errOut,
		stdinTTY:    force || ghterm.IsTerminal(in),
		stdoutTTY:   force || ghterm.IsTerminal(out),
		stderrTTY:   force || ghterm.IsTerminal(errOut),
		termWidth:   width,
		neverPrompt: os.Getenv("ADO_PROMPT_DISABLED") != "" || os.Getenv("CI") != "",
	}
	ios.colorEnabled = ghterm.IsColorForced() || (!ghterm.IsColorDisabled() && ios.stdoutTTY)
	return ios
}

// parseForceTTY reads ADO_FORCE_TTY: a boolean, or a positive number that
// forces a TTY with that terminal width. Any other non-empty value forces a TTY.
func parseForceTTY(v string) (force bool, width int) {
	if b, err := strconv.ParseBool(v); err == nil {
		return b, 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return true, n
	}
	return v != "", 0
}

// Test returns IOStreams backed by buffers, non-TTY and without color, along
// with the stdin, stdout and stderr buffers.
func Test() (ios *IOStreams, in, out, errOut *bytes.Buffer) {
	in, out, errOut = &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{}
	return &IOStreams{In: in, Out: out, ErrOut: errOut}, in, out, errOut
}

// IsStdinTTY reports whether stdin is a terminal.
func (s *IOStreams) IsStdinTTY() bool { return s.stdinTTY }

// IsStdoutTTY reports whether stdout is a terminal.
func (s *IOStreams) IsStdoutTTY() bool { return s.stdoutTTY }

// IsStderrTTY reports whether stderr is a terminal.
func (s *IOStreams) IsStderrTTY() bool { return s.stderrTTY }

// SetStdinTTY overrides stdin TTY detection.
func (s *IOStreams) SetStdinTTY(v bool) { s.stdinTTY = v }

// SetStdoutTTY overrides stdout TTY detection.
func (s *IOStreams) SetStdoutTTY(v bool) { s.stdoutTTY = v }

// SetStderrTTY overrides stderr TTY detection.
func (s *IOStreams) SetStderrTTY(v bool) { s.stderrTTY = v }

// ColorEnabled reports whether output may contain ANSI colors.
func (s *IOStreams) ColorEnabled() bool { return s.colorEnabled }

// SetColorEnabled overrides color detection.
func (s *IOStreams) SetColorEnabled(v bool) { s.colorEnabled = v }

// SetNeverPrompt disables interactive prompts regardless of TTY state.
func (s *IOStreams) SetNeverPrompt(v bool) { s.neverPrompt = v }

// CanPrompt reports whether interactive prompts are allowed: stdin and stdout
// are terminals and prompting is not disabled.
func (s *IOStreams) CanPrompt() bool {
	return !s.neverPrompt && s.stdinTTY && s.stdoutTTY
}

// RefreshScreen moves the cursor home and clears the screen when stdout is a terminal, so
// the next output redraws in place (from gh's IOStreams.RefreshScreen).
func (s *IOStreams) RefreshScreen() {
	if s.stdoutTTY {
		fmt.Fprint(s.Out, "\x1b[0;0H\x1b[J")
	}
}

// SetTerminalWidth overrides the detected terminal width.
func (s *IOStreams) SetTerminalWidth(w int) { s.termWidth = w }

// TerminalWidth returns the stdout terminal width, or 80 when unknown.
func (s *IOStreams) TerminalWidth() int {
	if s.termWidth > 0 {
		return s.termWidth
	}
	if f, ok := s.Out.(*os.File); ok {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			return w
		}
	}
	return defaultWidth
}

// ColorScheme returns a ColorScheme that colors only when color is enabled.
func (s *IOStreams) ColorScheme() *ColorScheme {
	return &ColorScheme{enabled: s.colorEnabled}
}

// ColorScheme wraps text in ANSI escape codes when enabled.
type ColorScheme struct {
	enabled bool
}

func (c *ColorScheme) wrap(code, t string) string {
	if !c.enabled {
		return t
	}
	return "\x1b[" + code + "m" + t + "\x1b[0m"
}

// Bold renders t in bold.
func (c *ColorScheme) Bold(t string) string { return c.wrap("1", t) }

// Red renders t in red.
func (c *ColorScheme) Red(t string) string { return c.wrap("31", t) }

// Green renders t in green.
func (c *ColorScheme) Green(t string) string { return c.wrap("32", t) }

// Yellow renders t in yellow.
func (c *ColorScheme) Yellow(t string) string { return c.wrap("33", t) }

// Magenta renders t in magenta.
func (c *ColorScheme) Magenta(t string) string { return c.wrap("35", t) }

// Cyan renders t in cyan.
func (c *ColorScheme) Cyan(t string) string { return c.wrap("36", t) }

// Gray renders t in gray.
func (c *ColorScheme) Gray(t string) string { return c.wrap("90", t) }
