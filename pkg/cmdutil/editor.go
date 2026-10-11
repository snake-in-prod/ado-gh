package cmdutil

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// ResolveEditor returns the editor command from ADO_EDITOR, then the ado config "editor" key,
// then VISUAL, then EDITOR, else nano (notepad on Windows). GH_EDITOR and gh's config are ignored.
func ResolveEditor(cfg Config) string {
	if v := os.Getenv("ADO_EDITOR"); v != "" {
		return v
	}
	if v := cfg.Get("", "editor"); v != "" {
		return v
	}
	for _, k := range []string{"VISUAL", "EDITOR"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	if runtime.GOOS == "windows" {
		return "notepad"
	}
	return "nano"
}

// Edit opens editor on a temporary file holding initial, waits for it to exit and returns the
// saved text. The editor runs on ios's streams.
func Edit(ios *iostreams.IOStreams, editor, initial string) (string, error) {
	if strings.TrimSpace(editor) == "" {
		return "", errors.New("no editor configured; set ADO_EDITOR or EDITOR")
	}
	f, err := os.CreateTemp("", "ado-*.md")
	if err != nil {
		return "", err
	}
	path := f.Name()
	defer os.Remove(path)
	_, err = f.WriteString(initial)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		args := windowsEditorArgs(editor)
		cmd = exec.Command(args[0], append(args[1:], path)...)
	} else {
		// Like git: the shell handles quoting and arguments in the editor command.
		cmd = exec.Command("sh", "-c", editor+` "$@"`, editor, path)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = ios.In, ios.Out, ios.ErrOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("running the editor %q: %w", editor, err)
	}
	b, err := os.ReadFile(path)
	// Some editors (Notepad among them) save UTF-8 with a byte order mark.
	return strings.TrimPrefix(string(b), "\uFEFF"), err
}

// windowsEditorArgs splits a Windows editor command on spaces, keeping a leading quoted
// program path whole, as in "C:\Program Files\Editor\editor.exe" --wait.
// ponytail: only the program may be quoted; use a shell-words parser if quoted arguments are needed.
func windowsEditorArgs(editor string) []string {
	editor = strings.TrimSpace(editor)
	if rest, ok := strings.CutPrefix(editor, `"`); ok {
		if prog, args, ok := strings.Cut(rest, `"`); ok {
			return append([]string{prog}, strings.Fields(args)...)
		}
	}
	return strings.Fields(editor)
}
