package extension

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// escapeSeq matches terminal escape sequences: CSI, string sequences (OSC, DCS, SOS, PM, APC)
// up to BEL, ST or the end of the text, other ESC sequences, and their 8-bit C1 forms.
var escapeSeq = regexp.MustCompile(`(?s)\x1b\[[0-?]*[ -/]*[@-~]` +
	`|\x1b[\]PX^_].*?(?:\x07|\x1b\\|$)` +
	`|\x1b[ -/]*[0-~]` +
	`|\x{9b}[0-?]*[ -/]*[@-~]` +
	`|[\x{90}\x{98}\x{9d}\x{9e}\x{9f}].*?(?:\x07|\x{9c}|\x1b\\|$)`)

// Sanitize strips terminal escape sequences, control characters other than newline and tab,
// and bidi controls from untrusted text (manifests, tags, search results, READMEs) before it
// is printed, so it cannot recolour, move the cursor, retitle or reorder the trust UI.
func Sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' || unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}
		return r
	}, escapeSeq.ReplaceAllString(s, ""))
}

// Trust holds the install flags that answer the confirmation and token consent questions.
type Trust struct {
	// Yes skips the install confirmation; required off a terminal.
	Yes bool
	// AllowToken grants a token-requesting extension ADO_TOKEN without asking.
	AllowToken bool
}

// Check refuses a non-interactive install without --yes. Run it before downloading anything.
func (t Trust) Check(ios *iostreams.IOStreams) error {
	if !t.Yes && !ios.CanPrompt() {
		return cmdutil.FlagErrorf("--yes required when not running interactively")
	}
	return nil
}

// Summary is what the user confirms before an install. Every field but Name and Kind may come
// from the source and is sanitized before printing.
type Summary struct {
	Name, Description string
	Source            string
	// Ref is the resolved tag, commit or version.
	Ref  string
	Kind Kind
	// Checksum is the checksum status, e.g. "verified" or "not applicable (git)".
	Checksum string
	// Token is true when the manifest requests the ADO token.
	Token bool
}

// Confirm shows s on a terminal, asks to install unless t.Yes (a no is cmdutil.CancelError,
// exit 2) and, when s.Token, asks for token consent (default no) unless t.AllowToken. Off a
// terminal it needs t.Yes, and consent needs t.AllowToken. Without consent it warns that the
// extension may not work. It returns whether the token was granted.
func Confirm(f *cmdutil.Factory, s Summary, t Trust) (bool, error) {
	ios := f.IOStreams
	if err := t.Check(ios); err != nil {
		return false, err
	}
	if ios.IsStderrTTY() {
		token := "not requested"
		if s.Token {
			token = "requests your Azure DevOps token (ADO_TOKEN)"
		}
		for _, row := range [][2]string{{"Extension", s.Name}, {"Description", s.Description}, {"Source", s.Source},
			{"Ref", s.Ref}, {"Kind", string(s.Kind)}, {"Checksum", s.Checksum}, {"Token", token}} {
			if row[1] != "" {
				fmt.Fprintf(ios.ErrOut, "%-12s %s\n", row[0]+":", tableprinter.OneLine(Sanitize(row[1])))
			}
		}
	}
	if !t.Yes {
		ok, err := f.Prompter.Confirm(fmt.Sprintf("Install extension %s?", s.Name), true)
		if err != nil {
			return false, err
		}
		if !ok {
			fmt.Fprintln(ios.ErrOut, "Installation cancelled")
			return false, cmdutil.CancelError
		}
	}
	if !s.Token {
		return false, nil
	}
	consent := t.AllowToken
	if !consent && ios.CanPrompt() {
		var err error
		if consent, err = f.Prompter.Confirm(fmt.Sprintf("Give %s your Azure DevOps token?", s.Name), false); err != nil {
			return false, err
		}
	}
	if !consent {
		fmt.Fprintf(ios.ErrOut, "%s %s requests your Azure DevOps token but was not given it; it may not work. Reinstall with --allow-token to grant it.\n",
			ios.ColorScheme().Yellow("!"), s.Name)
	}
	return consent, nil
}
