package cmdutil

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// ReadBody reads a body text from the file at path, or from in when path is "-".
func ReadBody(in io.Reader, path string) (string, error) {
	if path == "" {
		return "", FlagErrorf(`--body-file needs a file path, or "-" for stdin`)
	}
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(in)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("reading the description: %w", err)
	}
	return string(b), nil
}

// BodyFlags holds the -b/--body, -F/--body-file and -e/--editor flags added by AddBodyFlags.
type BodyFlags struct {
	Body, File string
	Editor     bool
	cmd        *cobra.Command
}

// AddBodyFlags registers the mutually exclusive body flags on cmd, bound to b.
func AddBodyFlags(cmd *cobra.Command, b *BodyFlags) {
	b.cmd = cmd
	fl := cmd.Flags()
	fl.StringVarP(&b.Body, "body", "b", "", "The body text")
	fl.StringVarP(&b.File, "body-file", "F", "", `Read body text from file (use "-" for stdin)`)
	fl.BoolVarP(&b.Editor, "editor", "e", false, "Skip prompts and open the text editor to write the body in")
	cmd.MarkFlagsMutuallyExclusive("body", "body-file", "editor")
}

// Given reports whether the body came from flags (-b or -F), so no editor is needed.
func (b *BodyFlags) Given() bool {
	return b.cmd.Flags().Changed("body") || b.cmd.Flags().Changed("body-file")
}

// Read returns the body from -b or -F, else from the editor prefilled with initial when -e is set
// or the terminal can prompt. A blank body is rejected.
func (b *BodyFlags) Read(f *Factory, initial string) (string, error) {
	var body string
	var err error
	switch {
	case b.cmd.Flags().Changed("body-file"):
		body, err = ReadBody(f.IOStreams.In, b.File)
	case b.Given():
		body = b.Body
	case !f.IOStreams.CanPrompt() && b.Editor:
		return "", FlagErrorf("--editor requires an interactive terminal; pass --body or --body-file")
	case !f.IOStreams.CanPrompt():
		return "", FlagErrorf("--body or --body-file required when not running interactively")
	default:
		cfg, cerr := f.Config()
		if cerr != nil {
			return "", cerr
		}
		body, err = Edit(f.IOStreams, ResolveEditor(cfg), initial)
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(body) == "" {
		return "", FlagErrorf("body cannot be blank")
	}
	return body, nil
}
