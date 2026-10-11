// Package markdown renders Markdown for the terminal with glamour.
package markdown

import (
	"io"
	"os"

	"github.com/charmbracelet/glamour"
	ghterm "github.com/cli/go-gh/v2/pkg/term"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// isRealTerminal reports whether w is an actual terminal (not just ADO_FORCE_TTY): glamour's
// background detection queries the terminal and must not run otherwise.
var isRealTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && ghterm.IsTerminal(f)
}

// style picks the glamour style: notty without colour, else GLAMOUR_STYLE, else the
// detected dark/light background on a real terminal, else dark.
func style(ios *iostreams.IOStreams) string {
	if !ios.ColorEnabled() {
		return "notty"
	}
	if s := os.Getenv("GLAMOUR_STYLE"); s != "" && s != "auto" {
		return s
	}
	if isRealTerminal(ios.Out) {
		return "auto"
	}
	return "dark"
}

// Render renders md for the terminal of ios, word-wrapped at its width (at most 120 columns).
func Render(ios *iostreams.IOStreams, md string) (string, error) {
	tr, err := glamour.NewTermRenderer(
		glamour.WithStylePath(style(ios)),
		glamour.WithWordWrap(min(ios.TerminalWidth(), 120)),
		glamour.WithEmoji(),
		glamour.WithPreservedNewLines(),
		glamour.WithTableWrap(false),
	)
	if err != nil {
		return "", err
	}
	return tr.Render(md)
}
