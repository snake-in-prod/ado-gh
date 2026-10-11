//go:build !windows

package iostreams

import "os"

// enableVirtualTerminal is a no-op off Windows: terminals already process ANSI
// escapes. Swappable in tests.
var enableVirtualTerminal = func(*os.File) {}
