package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
)

// localSrcDir creates <tmp>/ado-hello, with the test binary as its executable when withExe.
func localSrcDir(t *testing.T, withExe bool) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ado-hello")
	require.NoError(t, os.Mkdir(dir, 0o755))
	if withExe {
		exe, err := os.Executable()
		require.NoError(t, err)
		b, err := os.ReadFile(exe)
		require.NoError(t, err)
		name := "ado-hello"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), b, 0o755))
	}
	return dir
}

func TestInstallLocalLink(t *testing.T) {
	src := localSrcDir(t, true)
	root := filepath.Join(t.TempDir(), "extensions") // created on demand
	ext, err := InstallLocal(root, "hello", src, false)
	require.NoError(t, err)
	entry := filepath.Join(root, "ado-hello")
	assert.Equal(t, Extension{Name: "hello", Path: entry, Dir: ext.Dir, Kind: KindLocal}, ext)

	if runtime.GOOS == "windows" {
		b, err := os.ReadFile(entry)
		require.NoError(t, err)
		assert.Equal(t, src+"\n", string(b), "pointer file")
		assert.Equal(t, src, ext.Dir)
	} else {
		target, err := os.Readlink(entry)
		require.NoError(t, err)
		assert.Equal(t, src, target, "symlink")
	}
	rec, err := ReadRecord(root, "hello")
	require.NoError(t, err)
	assert.Equal(t, &Record{Source: src, Kind: KindLocal}, rec)
}

func TestInstallLocalRelativePath(t *testing.T) {
	src := localSrcDir(t, true)
	t.Chdir(filepath.Dir(src))
	root := t.TempDir()
	_, err := InstallLocal(root, "hello", "ado-hello", false)
	require.NoError(t, err)
	ext, err := Lookup(root, "hello")
	require.NoError(t, err)
	_, err = os.Stat(ext.Executable())
	assert.NoError(t, err, "link made from the absolute path")
}

func TestInstallLocalErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ado-hello")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	tests := map[string]struct {
		name, dir, wantErr string
	}{
		"missing dir":  {"hello", filepath.Join(t.TempDir(), "ado-hello"), "no such directory"},
		"not a dir":    {"hello", file, "is not a directory"},
		"invalid name": {"../x", localSrcDir(t, false), "invalid extension name"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			_, err := InstallLocal(root, tt.name, tt.dir, false)
			assert.ErrorContains(t, err, tt.wantErr)
			entries, _ := os.ReadDir(root)
			assert.Empty(t, entries, "nothing linked")
		})
	}
}

func TestInstallLocalMissingExecutable(t *testing.T) {
	root := t.TempDir()
	ext, err := InstallLocal(root, "hello", localSrcDir(t, false), false)
	require.ErrorIs(t, err, ErrExecutableNotFound)
	assert.Equal(t, KindLocal, ext.Kind)
	got, lookErr := Lookup(root, "hello")
	require.NoError(t, lookErr, "link kept")
	assert.Equal(t, ext, got)
}

func TestInstallLocalForce(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ado-hello", "bin"), 0o755)) // binary install
	require.NoError(t, WriteRecord(root, "hello", &Record{Kind: KindBinary, TokenConsent: true}))
	src := localSrcDir(t, true)

	_, err := InstallLocal(root, "hello", src, false)
	require.ErrorContains(t, err, `extension "hello" is already installed; use --force to replace it`)
	assert.DirExists(t, filepath.Join(root, "ado-hello", "bin"))

	ext, err := InstallLocal(root, "hello", src, true)
	require.NoError(t, err)
	assert.Equal(t, KindLocal, ext.Kind)
	rec, err := ReadRecord(root, "hello")
	require.NoError(t, err)
	assert.False(t, rec.TokenConsent, "old record replaced")

	// A local install replaces another local install without touching the old target.
	src2 := localSrcDir(t, true)
	_, err = InstallLocal(root, "hello", src2, true)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(src, filepath.Base(ext.Executable())), "old target untouched")
	rec, err = ReadRecord(root, "hello")
	require.NoError(t, err)
	assert.Equal(t, src2, rec.Source)
}

func TestRemoveEntry(t *testing.T) {
	root := t.TempDir()
	src := localSrcDir(t, true)
	_, err := InstallLocal(root, "hello", src, false)
	require.NoError(t, err)
	require.NoError(t, Remove(root, "hello"))
	_, err = os.Lstat(filepath.Join(root, "ado-hello"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.NoFileExists(t, RecordPath(root, "hello"))
	assert.DirExists(t, src, "target untouched")

	assert.ErrorIs(t, Remove(root, "hello"), os.ErrNotExist)
	assert.ErrorContains(t, Remove(root, ".."), "invalid extension name")
}

// TestDispatchLocal runs a real process through a local install, both through the link
// InstallLocal makes on this OS and through a pointer file, which is read on every OS.
func TestDispatchLocal(t *testing.T) {
	clearExtEnv(t)
	t.Setenv("ADO_TEST_EXTENSION", "1")
	t.Setenv("ADO_TEST_EXTENSION_EXIT", "0")
	src := localSrcDir(t, true)
	links := map[string]func(root string) error{
		"install": func(root string) error { _, err := InstallLocal(root, "hello", src, false); return err },
		"pointer": func(root string) error { return WritePointer(filepath.Join(root, "ado-hello"), src) },
	}
	for name, link := range links {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, link(root))
			ext, err := Lookup(root, "hello")
			require.NoError(t, err)
			assert.Equal(t, KindLocal, ext.Kind)
			f, ios := extFactory(adocontext.Repo{}, errors.New("no repo"))
			require.NoError(t, Run(f, ext, []string{"x"}))
			var r fakeReport
			require.NoError(t, json.Unmarshal([]byte(ios.Out.(fmt.Stringer).String()), &r))
			assert.Equal(t, []string{"x"}, r.Args)
			assert.Equal(t, "hello", r.Env["ADO_EXTENSION_NAME"])
		})
	}
}
