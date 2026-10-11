// Package tableprinter wraps go-gh's tableprinter: on a TTY it renders an
// aligned table with bold uppercase headers and relative times; otherwise it
// writes headerless TSV with RFC3339 times and no color.
package tableprinter

import (
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/tableprinter"
	"github.com/cli/go-gh/v2/pkg/text"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// Re-exported field options so callers need a single import.
var (
	WithColor    = tableprinter.WithColor
	WithTruncate = tableprinter.WithTruncate
	WithPadding  = tableprinter.WithPadding
)

// TablePrinter is a go-gh TablePrinter that knows whether it targets a TTY.
type TablePrinter struct {
	tableprinter.TablePrinter
	isTTY bool
}

// New returns a TablePrinter writing to ios.Out. Headers are only printed on a TTY.
func New(ios *iostreams.IOStreams, headers ...string) *TablePrinter {
	isTTY := ios.IsStdoutTTY()
	tp := &TablePrinter{
		TablePrinter: tableprinter.New(ios.Out, isTTY, ios.TerminalWidth()),
		isTTY:        isTTY,
	}
	if isTTY && len(headers) > 0 {
		upper := make([]string, len(headers))
		for i, h := range headers {
			upper[i] = strings.ToUpper(h)
		}
		tp.AddHeader(upper, WithColor(ios.ColorScheme().Bold))
	}
	return tp
}

// oneLine replaces the tabs and line breaks that would split a TSV field or a table row.
var oneLine = strings.NewReplacer("\r\n", " ", "\t", " ", "\n", " ", "\r", " ")

// OneLine returns s with tabs and line breaks as spaces, for free text such as titles that
// must stay in one table cell or TSV field.
func OneLine(s string) string { return oneLine.Replace(s) }

// AddTimeField adds ts relative to now on a TTY, RFC3339 otherwise. A zero
// time (ADO's unset 0001-01-01 dates) renders as an empty field.
func (t *TablePrinter) AddTimeField(now, ts time.Time, color func(string) string) {
	s := ""
	switch {
	case ts.IsZero():
	case t.isTTY:
		s = text.RelativeTimeAgo(now, ts)
	default:
		s = ts.Format(time.RFC3339)
	}
	t.AddField(s, WithColor(color))
}
