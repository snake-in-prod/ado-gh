// Package fsutil holds path checks and writers that keep untrusted names (archive entries,
// log names, downloaded files) inside a target directory on every OS.
package fsutil

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SafeName turns s into a file name valid on Windows, macOS and Linux: separators, `:*?"<>|`
// and control characters become "_", trailing dots and spaces are dropped, Windows reserved
// device names (CON, NUL, COM1, ...) get a "_" prefix, and an empty result (from "", "." or
// "..") becomes "_".
func SafeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, s)
	s = strings.TrimRight(s, ". ")
	if s == "" {
		return "_"
	}
	if isReservedName(s) {
		return "_" + s
	}
	return s
}

// isReservedName reports whether Windows treats name as a device, whatever its extension.
func isReservedName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	r := []rune(base)
	return len(r) == 4 && (string(r[:3]) == "COM" || string(r[:3]) == "LPT") && strings.ContainsRune("123456789¹²³", r[3])
}

// CreateFile opens the file at the slash-separated path name under root for writing with the
// extra flag (os.O_TRUNC or os.O_EXCL), creating it and its parent directories. name must pass
// LocalPath, and os.Root refuses symlinks leading out of root, so nothing is ever written
// outside it.
func CreateFile(root *os.Root, name string, flag int) (*os.File, error) {
	rel, err := LocalPath(name)
	if err != nil {
		return nil, err
	}
	if err := root.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		return nil, err
	}
	return root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|flag, 0o644)
}

// LocalPath returns the slash-separated relative path name in OS form, or an error unless it is
// local and portable: fs.ValidPath (no "..", ".", empty or leading "/"), no "\", and no element
// with ":" or a Windows device name, so an archive behaves the same on every OS.
func LocalPath(name string) (string, error) {
	if strings.Contains(name, `\`) || !fs.ValidPath(name) {
		return "", fmt.Errorf("invalid path %q", name)
	}
	for _, el := range strings.Split(name, "/") {
		if strings.Contains(el, ":") || isReservedName(el) {
			return "", fmt.Errorf("invalid path %q", name)
		}
	}
	return filepath.Localize(name)
}
