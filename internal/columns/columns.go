// Package columns renders board columns of work item cards for the terminal:
// side by side when the width allows, stacked otherwise. It is pure layout,
// shared by `ado sprint board` and `ado board`.
package columns

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/cli/go-gh/v2/pkg/text"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

const (
	gap          = 2
	minLaneWidth = 18
)

// Card is one work item on a board.
type Card struct {
	ID       int
	Title    string
	Assignee string
}

// Column is a board column. A split column keeps in-progress cards in Cards
// and finished ones in Done; WIPLimit 0 means no limit.
type Column struct {
	Name     string
	WIPLimit int
	Split    bool
	Cards    []Card
	Done     []Card
}

type lane struct {
	sub   string
	cards []Card
}

func (c Column) lanes() []lane {
	if c.Split {
		return []lane{{"Doing", c.Cards}, {"Done", c.Done}}
	}
	return []lane{{"", c.Cards}}
}

// header is "Name count" or "Name count/limit", red when over the limit.
func (c Column) header(cs *iostreams.ColorScheme, width int) string {
	n := len(c.Cards) + len(c.Done)
	h := fmt.Sprintf("%s %d", c.Name, n)
	if c.WIPLimit > 0 {
		h += "/" + strconv.Itoa(c.WIPLimit)
	}
	h = text.Truncate(width, h)
	if c.WIPLimit > 0 && n > c.WIPLimit {
		return cs.Red(h)
	}
	return cs.Bold(h)
}

// card renders "#id title (assignee)" truncated to width, with the ID colored.
func card(cs *iostreams.ColorScheme, width int, c Card) string {
	id := "#" + strconv.Itoa(c.ID)
	s := id + " " + c.Title
	if c.Assignee != "" {
		s += " (" + c.Assignee + ")"
	}
	s = text.Truncate(width, s)
	if rest, ok := strings.CutPrefix(s, id); ok {
		return cs.Cyan(id) + rest
	}
	return s
}

// Render writes cols to w within width display columns.
func Render(w io.Writer, cs *iostreams.ColorScheme, width int, cols []Column) {
	n := 0
	for _, c := range cols {
		n += len(c.lanes())
	}
	if n == 0 {
		return
	}
	lw := (width - gap*(n-1)) / n
	if lw < minLaneWidth {
		stacked(w, cs, width, cols)
		return
	}
	sep := strings.Repeat(" ", gap)
	line := func(cells []string) {
		fmt.Fprintln(w, strings.TrimRight(strings.Join(cells, sep), " "))
	}

	var heads, subs []string
	var lanes []lane
	split := false
	for _, c := range cols {
		ls := c.lanes()
		cw := len(ls)*lw + (len(ls)-1)*gap
		heads = append(heads, text.PadRight(cw, c.header(cs, cw)))
		for _, l := range ls {
			subs = append(subs, text.PadRight(lw, l.sub))
			split = split || l.sub != ""
		}
		lanes = append(lanes, ls...)
	}
	line(heads)
	if split {
		line(subs)
	}
	for row := 0; ; row++ {
		cells := make([]string, len(lanes))
		more := false
		for i, l := range lanes {
			s := ""
			if row < len(l.cards) {
				s, more = card(cs, lw, l.cards[row]), true
			}
			cells[i] = text.PadRight(lw, s)
		}
		if !more {
			return
		}
		line(cells)
	}
}

// stacked writes one column after another, cards indented under the header.
func stacked(w io.Writer, cs *iostreams.ColorScheme, width int, cols []Column) {
	for i, c := range cols {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, c.header(cs, width))
		indent := "  "
		for _, l := range c.lanes() {
			if l.sub != "" {
				fmt.Fprintln(w, "  "+l.sub)
				indent = "    "
			}
			for _, cd := range l.cards {
				fmt.Fprintln(w, indent+card(cs, width-len(indent), cd))
			}
		}
	}
}
