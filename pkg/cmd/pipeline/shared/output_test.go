package shared

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"go build":          "go build",
		"":                  "_",
		".":                 "_",
		"..":                "_",
		"a/b":               "a_b",
		`a\b`:               "a_b",
		`x:*?"<>|y`:         "x_______y",
		"tab\there\x00\x7f": "tab_here__",
		"trailing. . ":      "trailing",
		"CON":               "_CON",
		"con.txt":           "_con.txt",
		"Nul ":              "_Nul",
		"COM1":              "_COM1",
		"lpt9.log":          "_lpt9.log",
		"COM¹":              "_COM¹",
		"COM0":              "COM0",
		"CONSOLE":           "CONSOLE",
		"conin$":            "_conin$",
		"Deploy ✓":          "Deploy ✓",
	} {
		assert.Equal(t, want, SafeName(in), "SafeName(%q)", in)
	}
}

func TestCreateFileInRoot(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer root.Close()

	f, err := CreateFile(root, "a/b/c.log", os.O_TRUNC)
	require.NoError(t, err)
	_, err = f.WriteString("hi")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	b, err := os.ReadFile(filepath.Join(dir, "a", "b", "c.log"))
	require.NoError(t, err)
	assert.Equal(t, "hi", string(b))

	_, err = CreateFile(root, "a/b/c.log", os.O_EXCL)
	assert.ErrorIs(t, err, os.ErrExist, "O_EXCL refuses an existing file")
}

func TestCreateFileTraversal(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "out")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	// Symlinks need privileges on Windows; without them only the name checks run.
	names := []string{"../x", "a/../../x", "/abs", "", ".", "a/", `a\..\b`, `..\x`, "a/./b"}
	if os.Symlink(outside, filepath.Join(dir, "link")) == nil &&
		os.Symlink(filepath.Join(outside, "f"), filepath.Join(dir, "filelink")) == nil {
		names = append(names, "link/x", "filelink")
	}
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer root.Close()

	for _, name := range names {
		f, err := CreateFile(root, name, os.O_TRUNC)
		if err == nil {
			f.Close()
		}
		assert.Error(t, err, "CreateFile(%q)", name)
	}
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing written outside the root")
	_, err = os.Stat(filepath.Join(parent, "x"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}
