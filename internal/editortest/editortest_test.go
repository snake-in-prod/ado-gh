package editortest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEditorTestHelperProcess(*testing.T) { HelperProcess() }

func TestCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs the command with sh")
	}
	cmd, seen := Command(t, "TestEditorTestHelperProcess", "edited")
	path := filepath.Join(t.TempDir(), "body.md")
	require.NoError(t, os.WriteFile(path, []byte("initial"), 0o600))

	require.NoError(t, exec.Command("sh", "-c", cmd+` "$@"`, cmd, path).Run())
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "edited", string(got))
	got, err = os.ReadFile(seen)
	require.NoError(t, err)
	assert.Equal(t, "initial", string(got))
}

func TestEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "body.md")
	require.NoError(t, os.WriteFile(path, []byte("initial"), 0o600))
	t.Setenv(envSeen, filepath.Join(dir, "seen"))
	t.Setenv(envText, "edited")

	t.Setenv(EnvFail, "1")
	assert.Equal(t, 3, edit(path))
	t.Setenv(EnvFail, "")
	assert.Equal(t, 0, edit(path))
	assert.Equal(t, 2, edit(filepath.Join(dir, "missing")), "an I/O error fails the editor")
}
