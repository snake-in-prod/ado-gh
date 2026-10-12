package extension

// makeLink writes a pointer file instead of a symlink (gh parity): creating symlinks on Windows
// needs administrator rights or Developer Mode, and Go makes a file symlink for a missing target.
func makeLink(target, link string) error { return WritePointer(link, target) }
