package tableprinter

import (
	"testing"
	"time"

	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	now     = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	created = now.Add(-3 * time.Hour)
)

func render(t *testing.T, tty, color bool) string {
	t.Helper()
	ios, _, out, _ := iostreams.Test()
	ios.SetStdoutTTY(tty)
	ios.SetColorEnabled(color)
	ios.SetTerminalWidth(80)

	tp := New(ios, "id", "title", "created")
	tp.AddField("1")
	tp.AddField("Fix bug", WithColor(ios.ColorScheme().Green))
	tp.AddTimeField(now, created, nil)
	tp.EndRow()
	tp.AddField("22")
	tp.AddField("Add feature")
	tp.AddTimeField(now, time.Time{}, nil)
	tp.EndRow()
	require.NoError(t, tp.Render())
	return out.String()
}

func TestTTYTable(t *testing.T) {
	assert.Equal(t, ""+
		"ID  TITLE        CREATED\n"+
		"1   Fix bug      about 3 hours ago\n"+
		"22  Add feature  \n", render(t, true, false))
}

func TestTTYTableColored(t *testing.T) {
	assert.Equal(t, ""+
		"\x1b[1mID\x1b[0m  \x1b[1mTITLE      \x1b[0m  \x1b[1mCREATED\x1b[0m\n"+
		"1   \x1b[32mFix bug    \x1b[0m  about 3 hours ago\n"+
		"22  Add feature  \n", render(t, true, true))
}

func TestNonTTYTSV(t *testing.T) {
	// No header, tabs, RFC3339 times, no color even when color is enabled.
	assert.Equal(t, ""+
		"1\tFix bug\t2026-10-10T09:00:00Z\n"+
		"22\tAdd feature\t\n", render(t, false, true))
}

func TestOneLine(t *testing.T) {
	if got := OneLine("a\tb\r\nc\nd\re"); got != "a b c d e" {
		t.Errorf("OneLine = %q", got)
	}
}
