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
		// ponytail: splits on spaces, so a quoted editor path with spaces breaks; use a
		// shell-words parser if Windows users hit it.
		args := strings.Fields(editor)
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
	return string(b), err
}
