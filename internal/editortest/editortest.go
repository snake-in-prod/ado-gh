// Package editortest runs the test binary as a fake editor, for tests of code that opens one.
package editortest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const (
	envOn   = "ADO_TEST_EDITOR"
	envSeen = "ADO_TEST_EDITOR_SEEN"
	envText = "ADO_TEST_EDITOR_TEXT"
	// EnvFail is the variable that, when set, makes the editor exit 3 after recording the initial text.
	EnvFail = "ADO_TEST_EDITOR_FAIL"
)

// HelperProcess acts as the editor when the test binary was started by a Command editor, and
// does nothing otherwise. Call it from the test that Command names.
func HelperProcess() {
	if os.Getenv(envOn) != "1" {
		return
	}
	os.Exit(edit(os.Args[len(os.Args)-1]))
}

// edit records the initial text of path in $ADO_TEST_EDITOR_SEEN, then replaces it with
// $ADO_TEST_EDITOR_TEXT, and returns the editor's exit code.
func edit(path string) int {
	b, err := os.ReadFile(path)
	if err == nil {
		err = os.WriteFile(os.Getenv(envSeen), b, 0o600)
	}
	if err == nil && os.Getenv(EnvFail) != "" {
		return 3
	}
	if err == nil {
		err = os.WriteFile(path, []byte(os.Getenv(envText)), 0o600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return 0
}

// Command returns an editor command that runs test, a test of the current test binary that
// calls HelperProcess. The editor replaces the file's text with text and records the text it
// was opened with in the returned seen file.
func Command(t testing.TB, test, text string) (cmd, seen string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	seen = filepath.Join(t.TempDir(), "seen")
	t.Setenv(envOn, "1")
	t.Setenv(envSeen, seen)
	t.Setenv(envText, text)
	if runtime.GOOS == "windows" {
		return `"` + exe + `" -test.run=^` + test + `$ --`, seen
	}
	return "'" + exe + "' '-test.run=^" + test + "$' --", seen
}
