package cmdutil

import (
	"os"
	"runtime"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/editortest"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEditorHelperProcess is not a test: it is the fake editor of editortest.Command.
func TestEditorHelperProcess(*testing.T) { editortest.HelperProcess() }

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

	cmd, seen := editortest.Command(t, "TestEditorHelperProcess", "edited")
	got, err := Edit(ios, cmd, "initial text")
	require.NoError(t, err)
	assert.Equal(t, "edited", got)
	b, err := os.ReadFile(seen)
	require.NoError(t, err)
	assert.Equal(t, "initial text", string(b), "editor opens prefilled")

	cmd, _ = editortest.Command(t, "TestEditorHelperProcess", "\uFEFFwith BOM")
	got, err = Edit(ios, cmd, "")
	require.NoError(t, err)
	assert.Equal(t, "with BOM", got, "a UTF-8 BOM is dropped")

	t.Setenv(editortest.EnvFail, "1")
	_, err = Edit(ios, cmd, "")
	assert.ErrorContains(t, err, "running the editor")

	_, err = Edit(ios, " ", "")
	assert.ErrorContains(t, err, "no editor")
}

func TestWindowsEditorArgs(t *testing.T) {
	tests := []struct {
		editor string
		want   []string
	}{
		{"notepad", []string{"notepad"}},
		{"code --wait", []string{"code", "--wait"}},
		{`"C:\Program Files\Microsoft VS Code\Code.exe" --wait`, []string{`C:\Program Files\Microsoft VS Code\Code.exe`, "--wait"}},
		{` "C:\Program Files\Notepad++\notepad++.exe"`, []string{`C:\Program Files\Notepad++\notepad++.exe`}},
		{`"unterminated quote`, []string{`"unterminated`, "quote"}},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, windowsEditorArgs(tt.editor), tt.editor)
	}
}
