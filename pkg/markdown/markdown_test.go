package markdown

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStyle(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	called := false
	orig := isRealTerminal
	t.Cleanup(func() { isRealTerminal = orig })
	terminal := false
	isRealTerminal = func(io.Writer) bool { called = true; return terminal }

	for _, tc := range []struct {
		name   string
		color  bool
		env    string
		real   bool
		want   string
		detect bool
	}{
		{"colour off", false, "dark", true, "notty", false},
		{"GLAMOUR_STYLE", true, "light", true, "light", false},
		{"forced TTY skips detection", true, "", false, "dark", true},
		{"auto on forced TTY", true, "auto", false, "dark", true},
		{"real TTY detects", true, "", true, "auto", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GLAMOUR_STYLE", tc.env)
			ios.SetColorEnabled(tc.color)
			terminal, called = tc.real, false
			assert.Equal(t, tc.want, style(ios))
			assert.Equal(t, tc.detect, called)
		})
	}
}

func TestRenderWrapWidth(t *testing.T) {
	long := strings.Repeat("word ", 60) + "\n"
	for _, tc := range []struct{ width, max int }{{200, 120}, {60, 60}} {
		ios, _, _, _ := iostreams.Test()
		ios.SetColorEnabled(false) // notty style: no escapes to measure around
		ios.SetTerminalWidth(tc.width)
		out, err := Render(ios, "# Title\n\n"+long)
		require.NoError(t, err)
		assert.Contains(t, out, "# Title")
		longest := 0
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "word") {
				longest = max(longest, len(strings.TrimRight(l, " ")))
			}
		}
		assert.LessOrEqual(t, longest, tc.max, tc.width)
		assert.Greater(t, longest, tc.max-10, tc.width)
	}
}

func TestRenderBadStyle(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	ios.SetColorEnabled(true)
	t.Setenv("GLAMOUR_STYLE", "/nonexistent/style.json")
	_, err := Render(ios, "x")
	assert.Error(t, err)
}

func TestIsRealTerminalDefault(t *testing.T) {
	assert.False(t, isRealTerminal(&bytes.Buffer{}))
}
