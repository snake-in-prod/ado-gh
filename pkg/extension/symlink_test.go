package extension

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMakeLinkPerOS(t *testing.T) {
	root, target := t.TempDir(), t.TempDir()
	require.NoError(t, makeLink(target, filepath.Join(root, Prefix+"hello")))

	fi, err := os.Lstat(filepath.Join(root, Prefix+"hello"))
	require.NoError(t, err)
	assert.Equal(t, runtime.GOOS != "windows", fi.Mode()&os.ModeSymlink != 0, "symlink on Unix, pointer file on Windows")

	ext, err := Lookup(root, "hello")
	require.NoError(t, err)
	assert.Equal(t, KindLocal, ext.Kind)
	if runtime.GOOS == "windows" {
		assert.Equal(t, target, ext.Dir)
	}
}
