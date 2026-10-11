package shared

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.md")
	require.NoError(t, os.WriteFile(path, []byte("file"), 0o600))

	got, err := ReadBody(nil, path)
	require.NoError(t, err)
	assert.Equal(t, "file", got)

	got, err = ReadBody(strings.NewReader("stdin"), "-")
	require.NoError(t, err)
	assert.Equal(t, "stdin", got)

	_, err = ReadBody(nil, filepath.Join(t.TempDir(), "missing"))
	assert.ErrorContains(t, err, "reading the description")
}

// TestBodyEditorHelperProcess is not a test: run as an editor by bodyEditorCmd, it records the
// file's initial text in $ADO_TEST_BODY_SEEN and replaces it with $ADO_TEST_BODY_TEXT.
func TestBodyEditorHelperProcess(*testing.T) {
	if os.Getenv("ADO_TEST_BODY_EDITOR") != "1" {
		return
	}
	path := os.Args[len(os.Args)-1]
	b, _ := os.ReadFile(path)
	_ = os.WriteFile(os.Getenv("ADO_TEST_BODY_SEEN"), b, 0o600)
	_ = os.WriteFile(path, []byte(os.Getenv("ADO_TEST_BODY_TEXT")), 0o600)
	os.Exit(0)
}

// bodyEditorCmd sets ADO_EDITOR to TestBodyEditorHelperProcess writing text and returns the file
// where it records the text it was opened with.
func bodyEditorCmd(t *testing.T, text string) (seen string) {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	seen = filepath.Join(t.TempDir(), "seen")
	t.Setenv("ADO_TEST_BODY_EDITOR", "1")
	t.Setenv("ADO_TEST_BODY_SEEN", seen)
	t.Setenv("ADO_TEST_BODY_TEXT", text)
	cmd := "'" + exe + "' '-test.run=^TestBodyEditorHelperProcess$' --"
	if runtime.GOOS == "windows" {
		if strings.Contains(exe, " ") {
			t.Skip("test binary path has spaces")
		}
		cmd = exe + " -test.run=^TestBodyEditorHelperProcess$ --"
	}
	t.Setenv("ADO_EDITOR", cmd)
	return seen
}

func TestBodyFlagsRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.md")
	require.NoError(t, os.WriteFile(path, []byte("from file"), 0o600))

	tests := []struct {
		name    string
		args    []string
		tty     bool
		stdin   string
		edited  string
		initial string
		given   bool
		want    string
		wantErr string
		flagErr bool
		seen    string
	}{
		{name: "body", args: []string{"-b", "hello"}, given: true, want: "hello"},
		{name: "body file", args: []string{"-F", path}, given: true, want: "from file"},
		{name: "stdin", args: []string{"--body-file", "-"}, stdin: "from stdin", given: true, want: "from stdin"},
		{name: "editor flag", args: []string{"-e"}, tty: true, edited: "typed", want: "typed", seen: ""},
		{name: "editor prefilled", args: []string{"--editor"}, tty: true, initial: "old body", edited: "new body", want: "new body", seen: "old body"},
		{name: "tty without body opens editor", tty: true, edited: "typed", want: "typed", seen: ""},
		{name: "non-tty without body", wantErr: "--body", flagErr: true},
		{name: "editor flag without tty", args: []string{"-e"}, wantErr: "--editor", flagErr: true},
		{name: "empty body", args: []string{"-b", ""}, given: true, wantErr: "body cannot be blank", flagErr: true},
		{name: "blank editor result", tty: true, edited: " \n", wantErr: "body cannot be blank", flagErr: true},
		{name: "missing file", args: []string{"-F", filepath.Join(t.TempDir(), "missing")}, given: true, wantErr: "reading the description"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen := bodyEditorCmd(t, tt.edited)
			ios, in, _, _ := iostreams.Test()
			in.WriteString(tt.stdin)
			ios.SetStdinTTY(tt.tty)
			ios.SetStdoutTTY(tt.tty)
			f := &cmdutil.Factory{IOStreams: ios, Config: func() (cmdutil.Config, error) { return bodyTestConfig{}, nil }}

			var b BodyFlags
			var got string
			var runErr error
			cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error {
				assert.Equal(t, tt.given, b.Given())
				got, runErr = b.Read(f, tt.initial)
				return nil
			}}
			AddBodyFlags(cmd, &b)
			cmd.SetArgs(tt.args)
			require.NoError(t, cmd.Execute())

			if tt.wantErr != "" {
				require.ErrorContains(t, runErr, tt.wantErr)
				var fe *cmdutil.FlagError
				assert.Equal(t, tt.flagErr, errors.As(runErr, &fe))
				return
			}
			require.NoError(t, runErr)
			assert.Equal(t, tt.want, got)
			if tt.edited != "" {
				s, err := os.ReadFile(seen)
				require.NoError(t, err)
				assert.Equal(t, tt.seen, string(s))
			}
		})
	}
}

func TestAddBodyFlagsExclusive(t *testing.T) {
	var b BodyFlags
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	AddBodyFlags(cmd, &b)
	cmd.SetArgs([]string{"-b", "x", "-e"})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	assert.ErrorContains(t, cmd.Execute(), "none of the others can be")
}

type bodyTestConfig struct{}

func (bodyTestConfig) Get(_, _ string) string { return "" }
func (bodyTestConfig) Set(_, _, _ string)     {}
func (bodyTestConfig) Write() error           { return nil }
