package cmdutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEditorHelperProcess is not a test: run as an editor by editorHelperCmd, it records the
// file's initial text in $ADO_TEST_EDITOR_SEEN and replaces it with $ADO_TEST_EDITOR_TEXT.
func TestEditorHelperProcess(*testing.T) {
	if os.Getenv("ADO_TEST_EDITOR") != "1" {
		return
	}
	path := os.Args[len(os.Args)-1]
	b, _ := os.ReadFile(path)
	_ = os.WriteFile(os.Getenv("ADO_TEST_EDITOR_SEEN"), b, 0o600)
	if os.Getenv("ADO_TEST_EDITOR_FAIL") != "" {
		os.Exit(3)
	}
	_ = os.WriteFile(path, []byte(os.Getenv("ADO_TEST_EDITOR_TEXT")), 0o600)
	os.Exit(0)
}

// editorHelperCmd returns an editor command running TestEditorHelperProcess that writes text,
// and the file where it records the text it was opened with.
func editorHelperCmd(t *testing.T, text string) (cmd, seen string) {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	seen = filepath.Join(t.TempDir(), "seen")
	t.Setenv("ADO_TEST_EDITOR", "1")
	t.Setenv("ADO_TEST_EDITOR_SEEN", seen)
	t.Setenv("ADO_TEST_EDITOR_TEXT", text)
	if runtime.GOOS == "windows" {
		if strings.Contains(exe, " ") {
			t.Skip("test binary path has spaces")
		}
		return exe + " -test.run=^TestEditorHelperProcess$ --", seen
	}
	return "'" + exe + "' '-test.run=^TestEditorHelperProcess$' --", seen
}

func TestResolveEditor(t *testing.T) {
	def := "nano"
	if runtime.GOOS == "windows" {
		def = "notepad"
	}
	tests := []struct {
		name                        string
		adoEditor, cfg, visual, env string
		want                        string
	}{
		{"ADO_EDITOR wins", "ado", "cfg", "visual", "env", "ado"},
		{"config over VISUAL", "", "cfg", "visual", "env", "cfg"},
		{"VISUAL over EDITOR", "", "", "visual", "env", "visual"},
		{"EDITOR", "", "", "", "env", "env"},
		{"default", "", "", "", "", def},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ADO_EDITOR", tt.adoEditor)
			t.Setenv("VISUAL", tt.visual)
			t.Setenv("EDITOR", tt.env)
			assert.Equal(t, tt.want, ResolveEditor(mapConfig{"editor": tt.cfg}))
		})
	}
}

func TestEdit(t *testing.T) {
	ios, _, _, _ := iostreams.Test()

	cmd, seen := editorHelperCmd(t, "edited")
	got, err := Edit(ios, cmd, "initial text")
	require.NoError(t, err)
	assert.Equal(t, "edited", got)
	b, err := os.ReadFile(seen)
	require.NoError(t, err)
	assert.Equal(t, "initial text", string(b), "editor opens prefilled")

	t.Setenv("ADO_TEST_EDITOR_FAIL", "1")
	_, err = Edit(ios, cmd, "")
	assert.ErrorContains(t, err, "running the editor")

	_, err = Edit(ios, " ", "")
	assert.ErrorContains(t, err, "no editor")
}
