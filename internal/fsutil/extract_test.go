package fsutil

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// zipEntry is one entry of a test archive; a name ending in "/" is a directory.
type zipEntry struct {
	name, body string
	mode       fs.FileMode
}

// newTestZip builds an in-memory archive of entries.
func newTestZip(t *testing.T, entries ...zipEntry) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		fw, err := w.CreateHeader(h)
		require.NoError(t, err)
		_, err = fw.Write([]byte(e.body))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)
	return zr
}

// readTree returns the regular files under dir as slash path → content.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	got := map[string]string{}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return got
	}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		got[filepath.ToSlash(rel)] = string(b)
		return err
	}))
	return got
}

func TestExtractZipStripsArtifactFolder(t *testing.T) {
	for _, top := range []string{"drop", "DROP"} {
		dir := filepath.Join(t.TempDir(), "new", "out")
		zr := newTestZip(t,
			zipEntry{name: top + "/"},
			zipEntry{name: top + "/hello.txt", body: "hi"},
			zipEntry{name: top + "/sub/b.txt", body: "b"},
			zipEntry{name: top + "/empty/"},
		)
		skipped, err := ExtractZip(zr, dir, "drop", false)
		require.NoError(t, err)
		assert.Empty(t, skipped)
		assert.Equal(t, map[string]string{"hello.txt": "hi", "sub/b.txt": "b"}, readTree(t, dir), top)
		assert.DirExists(t, filepath.Join(dir, "empty"))
	}
}

func TestExtractZipKeepsLayoutWithoutArtifactFolder(t *testing.T) {
	dir := t.TempDir()
	zr := newTestZip(t,
		zipEntry{name: "drop/a.txt", body: "a"},
		zipEntry{name: "other.txt", body: "o"},
	)
	_, err := ExtractZip(zr, dir, "drop", false)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"drop/a.txt": "a", "other.txt": "o"}, readTree(t, dir))

	// A file named like the artifact is not its folder.
	dir = t.TempDir()
	_, err = ExtractZip(newTestZip(t, zipEntry{name: "drop", body: "d"}), dir, "drop", false)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"drop": "d"}, readTree(t, dir))
}

func TestExtractZipBackslashSeparators(t *testing.T) {
	dir := t.TempDir()
	zr := newTestZip(t,
		zipEntry{name: `drop\cs\`},
		zipEntry{name: `drop\cs\a.dll`, body: "a"},
		zipEntry{name: `drop\empty\`},
		zipEntry{name: `drop\tool`, body: "t"},
	)
	_, err := ExtractZip(zr, dir, "drop", false)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"cs/a.dll": "a", "tool": "t"}, readTree(t, dir))
	assert.DirExists(t, filepath.Join(dir, "empty"))
}

func TestExtractZipRejectsUnsafeEntries(t *testing.T) {
	for _, name := range []string{"../x", "drop/../../x", "/abs", `a\..\b`, `..\x`, `\abs`, "C:x", "drop/C:x", "NUL", "drop/nul.txt", "a//b", "./a"} {
		dir := t.TempDir()
		zr := newTestZip(t,
			zipEntry{name: "drop/ok.txt", body: "ok"},
			zipEntry{name: name, body: "evil"},
		)
		_, err := ExtractZip(zr, dir, "drop", false)
		require.Error(t, err, "%q", name)
		assert.Contains(t, err.Error(), "unsafe path")
		assert.Empty(t, readTree(t, dir), "%q: nothing written", name)
		assert.NoFileExists(t, filepath.Join(filepath.Dir(dir), "x"))
	}
}

func TestExtractZipSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	zr := newTestZip(t,
		zipEntry{name: "drop/link", body: "../../etc/passwd", mode: fs.ModeSymlink | 0o777},
		zipEntry{name: "drop/a.txt", body: "a"},
	)
	skipped, err := ExtractZip(zr, dir, "drop", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"drop/link"}, skipped)
	assert.Equal(t, map[string]string{"a.txt": "a"}, readTree(t, dir))
	_, err = os.Lstat(filepath.Join(dir, "link"))
	assert.True(t, os.IsNotExist(err))
}

func TestExtractZipCapsEntrySize(t *testing.T) {
	// An entry declaring 2 bytes but holding 10, stored uncompressed.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fw, err := w.CreateRaw(&zip.FileHeader{Name: "bomb.bin", Method: zip.Store, CompressedSize64: 10, UncompressedSize64: 2})
	require.NoError(t, err)
	_, err = fw.Write([]byte("0123456789"))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)

	dir := t.TempDir()
	_, err = ExtractZip(zr, dir, "drop", false)
	require.ErrorIs(t, err, zip.ErrFormat, "archive/zip refuses to read past the declared size")
	assert.ErrorContains(t, err, "bomb.bin: ")
	b, err := os.ReadFile(filepath.Join(dir, "bomb.bin"))
	require.NoError(t, err)
	assert.LessOrEqual(t, len(b), 2, "at most the declared size is written")
}

func TestExtractZipExistingFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("old"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o644))
	entries := []zipEntry{{name: "drop/new.txt", body: "n"}, {name: "drop/a.txt", body: "new"}}

	_, err := ExtractZip(newTestZip(t, entries...), dir, "drop", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a.txt already exists")
	assert.Contains(t, err.Error(), "--clobber")
	assert.Equal(t, map[string]string{"a.txt": "old", "keep.txt": "keep"}, readTree(t, dir), "nothing written")

	_, err = ExtractZip(newTestZip(t, entries...), dir, "drop", true)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a.txt": "new", "keep.txt": "keep", "new.txt": "n"}, readTree(t, dir))
}

func TestExtractZipBadDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	_, err := ExtractZip(newTestZip(t, zipEntry{name: "a", body: "a"}), file, "drop", false)
	assert.ErrorIs(t, err, syscall.ENOTDIR, "the target is a file")
}
