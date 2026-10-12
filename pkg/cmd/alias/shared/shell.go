package shared

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// RunShell runs the shell alias expansion (its leading "!" optional) with args on ios's
// streams. shell is the alias_shell config value; "" picks sh, or on Windows without sh,
// pwsh then powershell. A non-zero exit is returned as *cmdutil.ExternalExitError.
func RunShell(ios *iostreams.IOStreams, shell, expansion string, args []string) error {
	return shellRunner{}.Run(ios, shell, expansion, args)
}

// shellRunner runs shell aliases. Zero fields mean the real OS; tests set them.
type shellRunner struct {
	goos     string
	lookPath func(string) (string, error)
	stat     func(string) (os.FileInfo, error)
	run      func(*exec.Cmd) error
}

func (r shellRunner) Run(ios *iostreams.IOStreams, shell, expansion string, args []string) error {
	if r.goos == "" {
		r.goos = runtime.GOOS
	}
	if r.lookPath == nil {
		r.lookPath = exec.LookPath
	}
	if r.stat == nil {
		r.stat = os.Stat
	}
	if r.run == nil {
		r.run = (*exec.Cmd).Run
	}
	script := strings.TrimPrefix(expansion, "!")

	if shell == "" {
		var err error
		if shell, err = r.defaultShell(); err != nil {
			return err
		}
	}
	var argv []string
	if isPowerShell(shell) {
		argv = []string{"-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(powerShellScript(script, args))}
	} else {
		// $1..$n are the shell's positional parameters; "--" fills $0.
		argv = append([]string{"-c", script, "--"}, args...)
	}
	cmd := exec.Command(shell, argv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = ios.In, ios.Out, ios.ErrOut
	return cmdutil.ExternalExit(r.run(cmd))
}

// defaultShell returns sh on Unix. On Windows it looks for sh like gh's findsh (PATH, then
// next to Git for Windows or its scoop shim), then pwsh, then powershell.
func (r shellRunner) defaultShell() (string, error) {
	if r.goos != "windows" {
		return "sh", nil
	}
	if sh, ok := FindSh(r.lookPath, r.stat); ok {
		return sh, nil
	}
	for _, ps := range []string{"pwsh", "powershell"} {
		if p, err := r.lookPath(ps); err == nil {
			return p, nil
		}
	}
	return "", errors.New("unable to locate sh, pwsh or powershell to run the shell alias; install Git for Windows or set alias_shell")
}

// FindSh finds sh on Windows like gh's findsh: on PATH, else next to Git for Windows or its
// scoop shim. lookPath and stat are exec.LookPath and os.Stat outside tests.
func FindSh(lookPath func(string) (string, error), stat func(string) (os.FileInfo, error)) (string, bool) {
	if p, err := lookPath("sh"); err == nil {
		return p, true
	}
	if git, err := lookPath("git"); err == nil {
		gitDir := filepath.Dir(git)
		for _, sh := range []string{
			filepath.Join(gitDir, "..", "bin", "sh.exe"),                           // Git for Windows
			filepath.Join(gitDir, "..", "apps", "git", "current", "bin", "sh.exe"), // scoop shim
		} {
			if _, err := stat(sh); err == nil {
				return sh, true
			}
		}
	}
	return "", false
}

// isPowerShell reports whether the program shell names is pwsh or Windows PowerShell.
func isPowerShell(shell string) bool {
	base := strings.ToLower(shell[strings.LastIndexAny(shell, `/\`)+1:])
	base = strings.TrimSuffix(base, ".exe")
	return base == "pwsh" || base == "powershell"
}

// psQuotes doubles every character PowerShell treats as a single quote.
var psQuotes = strings.NewReplacer(
	"'", "''", "‘", "‘‘", "’", "’’", "‚", "‚‚", "‛", "‛‛")

// powerShellScript runs script in a script block with $1..$n assigned by index from $args
// (destructuring would leave an array in the last variable), the args passed as single-quoted
// literals, and the last native exit code kept (PowerShell otherwise flattens it to 0 or 1).
// Progress output is turned off: with -EncodedCommand and a redirected stderr, Windows
// PowerShell 5.1 writes progress records ("Preparing modules for first use") as CLIXML noise.
func powerShellScript(script string, args []string) string {
	var b strings.Builder
	b.WriteString("$ProgressPreference='SilentlyContinue'; & { ")
	for i := range args {
		fmt.Fprintf(&b, "$%d=$args[%d]; ", i+1, i)
	}
	// The newline ends a trailing # comment in script before the closing brace.
	b.WriteString(script + "\n}")
	for _, a := range args {
		b.WriteString(" '" + psQuotes.Replace(a) + "'")
	}
	b.WriteString("; exit $LASTEXITCODE")
	return b.String()
}

// encodePowerShell returns s as a -EncodedCommand value: base64 of UTF-16LE.
func encodePowerShell(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return base64.StdEncoding.EncodeToString(b)
}
