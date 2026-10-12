//go:build !windows

package extension

import "os"

// makeLink links a local install entry to its target directory with a symlink.
func makeLink(target, link string) error { return os.Symlink(target, link) }
