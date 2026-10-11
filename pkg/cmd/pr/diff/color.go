package diff

import (
	"bytes"
	"fmt"
	"io"

	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// gh's diff colours: bold white headers, green additions, red removals; @@ and context plain.
const (
	colorHeader   = "\x1b[1;37m"
	colorAddition = "\x1b[32m"
	colorRemoval  = "\x1b[31m"
	colorReset    = "\x1b[m"
)

// useColor resolves --color: auto follows the streams (TTY, NO_COLOR, CLICOLOR, CLICOLOR_FORCE).
func useColor(ios *iostreams.IOStreams, mode string) (bool, error) {
	switch mode {
	case "auto":
		return ios.ColorEnabled(), nil
	case "always":
		return true, nil
	case "never":
		return false, nil
	}
	return false, fmt.Errorf("invalid value for --color: %q (want auto, always or never)", mode)
}

// writeDiff writes diff (git diff, format-patch or generated output) to w, colouring each line
// like gh when color is set.
func writeDiff(w io.Writer, diff []byte, color bool) error {
	if !color {
		_, err := w.Write(diff)
		return err
	}
	var buf bytes.Buffer
	for line := range bytes.Lines(diff) {
		body, nl := bytes.CutSuffix(line, []byte("\n"))
		if c := lineColor(body); c != "" {
			buf.WriteString(c)
			buf.Write(body)
			buf.WriteString(colorReset)
		} else {
			buf.Write(body)
		}
		if nl {
			buf.WriteByte('\n')
		}
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func lineColor(line []byte) string {
	for _, p := range []string{"+++", "---", "diff", "index"} {
		if bytes.HasPrefix(line, []byte(p)) {
			return colorHeader
		}
	}
	switch {
	case bytes.HasPrefix(line, []byte("+")):
		return colorAddition
	case bytes.HasPrefix(line, []byte("-")):
		return colorRemoval
	}
	return ""
}
