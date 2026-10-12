package extension

import (
	"bytes"
	"errors"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

func TestSanitize(t *testing.T) {
	tests := map[string]struct{ in, want string }{
		"plain":            {"Say hello, wörld", "Say hello, wörld"},
		"newline and tab":  {"a\n\tb", "a\n\tb"},
		"CSI colour":       {"\x1b[31mred\x1b[0m", "red"},
		"CSI cursor up":    {"ok\x1b[2A\x1b[Kspoof", "okspoof"},
		"OSC title BEL":    {"\x1b]0;evil title\x07x", "x"},
		"OSC 8 link ST":    {"\x1b]8;;https://evil\x1b\\click\x1b]8;;\x1b\\", "click"},
		"DCS":              {"\x1bPq#0;2;0;0;0\x1b\\x", "x"},
		"two-byte ESC":     {"a\x1bcb", "ab"},
		"C1 CSI":           {"a\u009b31mb", "ab"},
		"C1 OSC":           {"a\u009d0;t\u009cb", "ab"},
		"lone ESC":         {"a\x1b", "a"},
		"carriage return":  {"verified\rspoof", "verifiedspoof"},
		"bell backspace":   {"a\x07\x08b\x7f", "ab"},
		"bidi override":    {"abc\u202edcba", "abcdcba"},
		"unterminated OSC": {"a\x1b]0;title", "a"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, Sanitize(tt.in))
		})
	}
}

// confirmEnv is a Factory whose prompter reads input; tty sets stdin, stdout and stderr.
func confirmEnv(tty bool, input string) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	ios, in, out, errOut := iostreams.Test()
	in.WriteString(input)
	ios.In = iotest.OneByteReader(in)
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	ios.SetStderrTTY(tty)
	return &cmdutil.Factory{IOStreams: ios, Prompter: &cmdutil.Prompter{IO: ios, Accessible: true}}, out, errOut
}

func TestConfirmSummary(t *testing.T) {
	f, out, errOut := confirmEnv(true, "y\nn\n")
	consent, err := Confirm(f, Summary{Name: "hello", Description: "Says\x1b[2A hello\nChecksum: verified",
		Source: "https://github.com/o/ado-hello.git", Ref: "v1 (abc)", Kind: KindScript,
		Checksum: "not applicable (git)", Token: true}, Trust{})
	require.NoError(t, err)
	assert.False(t, consent, "token default no")
	assert.Equal(t, `Extension:   hello
Description: Says hello Checksum: verified
Source:      https://github.com/o/ado-hello.git
Ref:         v1 (abc)
Kind:        script
Checksum:    not applicable (git)
Token:       requests your Azure DevOps token (ADO_TOKEN)
! hello requests your Azure DevOps token but was not given it; it may not work. Reinstall with --allow-token to grant it.
`, errOut.String())
	assert.Contains(t, out.String(), "Install extension hello?")
	assert.Contains(t, out.String(), "Give hello your Azure DevOps token?")
}

func TestConfirm(t *testing.T) {
	tests := map[string]struct {
		tty         bool
		input       string
		trust       Trust
		token       bool
		wantConsent bool
		wantErr     error
		wantErrMsg  string
		wantWarn    bool
		wantPrompts int
	}{
		"tty accept, no token":       {tty: true, input: "y\n", wantPrompts: 1},
		"tty decline":                {tty: true, input: "n\n", wantErr: cmdutil.CancelError, wantPrompts: 1},
		"tty token yes":              {tty: true, input: "y\ny\n", token: true, wantConsent: true, wantPrompts: 2},
		"tty token default no":       {tty: true, input: "y\n\n", token: true, wantWarn: true, wantPrompts: 2},
		"tty --yes still asks token": {tty: true, input: "y\n", trust: Trust{Yes: true}, token: true, wantConsent: true, wantPrompts: 1},
		"tty --allow-token":          {tty: true, input: "y\n", trust: Trust{AllowToken: true}, token: true, wantConsent: true, wantPrompts: 1},
		"non-tty without --yes":      {wantErrMsg: "--yes required when not running interactively"},
		"non-tty --yes":              {trust: Trust{Yes: true}},
		"non-tty --yes no consent":   {trust: Trust{Yes: true}, token: true, wantWarn: true},
		"non-tty --allow-token":      {trust: Trust{Yes: true, AllowToken: true}, token: true, wantConsent: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			f, out, errOut := confirmEnv(tt.tty, tt.input)
			consent, err := Confirm(f, Summary{Name: "hello", Kind: KindScript, Token: tt.token}, tt.trust)
			switch {
			case tt.wantErr != nil:
				assert.True(t, errors.Is(err, tt.wantErr), "err = %v", err)
				assert.Contains(t, errOut.String(), "Installation cancelled")
			case tt.wantErrMsg != "":
				var flagErr *cmdutil.FlagError
				assert.ErrorAs(t, err, &flagErr)
				assert.EqualError(t, err, tt.wantErrMsg)
			default:
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantConsent, consent)
			assert.Equal(t, tt.wantWarn, bytes.Contains(errOut.Bytes(), []byte("was not given it")), errOut.String())
			assert.Equal(t, tt.wantPrompts, bytes.Count(out.Bytes(), []byte("?")), out.String())
		})
	}
}

func TestTrustCheck(t *testing.T) {
	f, _, _ := confirmEnv(false, "")
	assert.EqualError(t, Trust{}.Check(f.IOStreams), "--yes required when not running interactively")
	assert.NoError(t, Trust{Yes: true}.Check(f.IOStreams))
	f, _, _ = confirmEnv(true, "")
	assert.NoError(t, Trust{}.Check(f.IOStreams))
}
