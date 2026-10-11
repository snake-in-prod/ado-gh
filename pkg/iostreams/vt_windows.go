//go:build windows

package iostreams

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVirtualTerminal turns on ANSI escape processing for a console handle so
// legacy conhost renders colours instead of raw escapes. Errors (e.g. a forced
// TTY that is really a pipe) are ignored. Swappable in tests.
var enableVirtualTerminal = func(f *os.File) {
	h := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return
	}
	_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}
