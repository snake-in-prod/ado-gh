package shared

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// decodePowerShell returns the script of a -EncodedCommand argument (base64 of UTF-16LE).
func decodePowerShell(t *testing.T, b64 string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

func TestShellRunnerCommand(t *testing.T) {
	gitDir := filepath.Join("Git", "cmd")
	scoopDir := filepath.Join("scoop", "shims")
	gitSh := filepath.Join("Git", "bin", "sh.exe")
	scoopSh := filepath.Join("scoop", "apps", "git", "current", "bin", "sh.exe")
	tests := []struct {
		name     string
		goos     string
		shell    string
		onPath   map[string]string // lookPath results
		files    []string          // paths stat finds
		args     []string
		wantArgs []string // "<ps>" is replaced by the decoded -EncodedCommand script
		wantPS   string
		wantErr  string
	}{
		{name: "unix sh", goos: "linux", args: []string{"a b", "c"},
			wantArgs: []string{"sh", "-c", "echo $1", "--", "a b", "c"}},
		{name: "unix no args", goos: "darwin",
			wantArgs: []string{"sh", "-c", "echo $1", "--"}},
		{name: "unix alias_shell", goos: "linux", shell: "/bin/bash", args: []string{"x"},
			wantArgs: []string{"/bin/bash", "-c", "echo $1", "--", "x"}},
		{name: "windows sh on PATH", goos: "windows", onPath: map[string]string{"sh": `C:\sh.exe`, "pwsh": "pwsh.exe"},
			args: []string{"x"}, wantArgs: []string{`C:\sh.exe`, "-c", "echo $1", "--", "x"}},
		{name: "windows Git for Windows sh", goos: "windows",
			onPath: map[string]string{"git": filepath.Join(gitDir, "git.exe"), "pwsh": "pwsh.exe"},
			files:  []string{gitSh}, wantArgs: []string{gitSh, "-c", "echo $1", "--"}},
		{name: "windows scoop sh", goos: "windows",
			onPath: map[string]string{"git": filepath.Join(scoopDir, "git.exe"), "pwsh": "pwsh.exe"},
			files:  []string{scoopSh}, wantArgs: []string{scoopSh, "-c", "echo $1", "--"}},
		{name: "windows pwsh when git has no sh", goos: "windows",
			onPath:   map[string]string{"git": filepath.Join(gitDir, "git.exe"), "pwsh": `C:\pwsh.exe`, "powershell": "powershell.exe"},
			args:     []string{"a b", "c"},
			wantArgs: []string{`C:\pwsh.exe`, "-NoProfile", "-NonInteractive", "-EncodedCommand", "<ps>"},
			wantPS:   "$ProgressPreference='SilentlyContinue'; & { $1=$args[0]; $2=$args[1]; echo $1\n} 'a b' 'c'; exit $LASTEXITCODE"},
		{name: "windows powershell fallback", goos: "windows", onPath: map[string]string{"powershell": `C:\powershell.exe`},
			wantArgs: []string{`C:\powershell.exe`, "-NoProfile", "-NonInteractive", "-EncodedCommand", "<ps>"},
			wantPS:   "$ProgressPreference='SilentlyContinue'; & { echo $1\n}; exit $LASTEXITCODE"},
		{name: "powershell one arg, all single quotes doubled", goos: "windows", onPath: map[string]string{"pwsh": "pwsh"},
			args:     []string{"'\u2018\u2019\u201a\u201b $x"},
			wantArgs: []string{"pwsh", "-NoProfile", "-NonInteractive", "-EncodedCommand", "<ps>"},
			wantPS:   "$ProgressPreference='SilentlyContinue'; & { $1=$args[0]; echo $1\n} '''\u2018\u2018\u2019\u2019\u201a\u201a\u201b\u201b $x'; exit $LASTEXITCODE"},
		{name: "windows no shell", goos: "windows",
			wantErr: "unable to locate sh, pwsh or powershell to run the shell alias; install Git for Windows or set alias_shell"},
		{name: "alias_shell powershell on unix", goos: "linux", shell: "powershell.exe",
			wantArgs: []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", "<ps>"},
			wantPS:   "$ProgressPreference='SilentlyContinue'; & { echo $1\n}; exit $LASTEXITCODE"},
		{name: "alias_shell pwsh path on windows", goos: "windows", shell: `C:\Program Files\PowerShell\7\PWSH.EXE`, onPath: map[string]string{"sh": "sh"},
			wantArgs: []string{`C:\Program Files\PowerShell\7\PWSH.EXE`, "-NoProfile", "-NonInteractive", "-EncodedCommand", "<ps>"},
			wantPS:   "$ProgressPreference='SilentlyContinue'; & { echo $1\n}; exit $LASTEXITCODE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got *exec.Cmd
			r := shellRunner{
				goos: tt.goos,
				lookPath: func(name string) (string, error) {
					if p, ok := tt.onPath[name]; ok {
						return p, nil
					}
					return "", exec.ErrNotFound
				},
				stat: func(name string) (os.FileInfo, error) {
					for _, f := range tt.files {
						if filepath.Clean(name) == f {
							return nil, nil
						}
					}
					return nil, fs.ErrNotExist
				},
				run: func(c *exec.Cmd) error { got = c; return nil },
			}
			ios, _, _, _ := iostreams.Test()
			err := r.Run(ios, tt.shell, "!echo $1", tt.args)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			args := got.Args
			if tt.wantPS != "" {
				assert.Equal(t, tt.wantPS, decodePowerShell(t, args[len(args)-1]))
				args = append(args[:len(args)-1:len(args)-1], "<ps>")
			}
			assert.Equal(t, tt.wantArgs, args)
			assert.Same(t, ios.In, got.Stdin)
			assert.Same(t, ios.Out, got.Stdout)
			assert.Same(t, ios.ErrOut, got.Stderr)
		})
	}
}

func TestShellRunnerRunError(t *testing.T) {
	boom := errors.New("boom")
	r := shellRunner{goos: "linux", run: func(*exec.Cmd) error { return boom }}
	ios, _, _, _ := iostreams.Test()
	assert.ErrorIs(t, r.Run(ios, "", "!true", nil), boom)
}

func TestRunShellRealSh(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	ios, _, out, _ := iostreams.Test()
	require.NoError(t, RunShell(ios, "", `!printf '%s|%s|%s' "$1" "$2" "$#"`, []string{"a b", "it's $x"}))
	assert.Equal(t, "a b|it's $x|2", out.String())

	err := RunShell(ios, "", "!exit 3", nil)
	var ext *cmdutil.ExternalExitError
	require.ErrorAs(t, err, &ext)
	assert.Equal(t, 3, ext.Code)
}

// powerShell returns a PowerShell on PATH (powershell.exe is Windows PowerShell under WSL).
func powerShell(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"pwsh", "powershell", "powershell.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("no PowerShell on PATH")
	return ""
}

func TestRunShellRealPowerShell(t *testing.T) {
	ps := powerShell(t)
	lines := func(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

	t.Run("two args", func(t *testing.T) {
		ios, _, out, _ := iostreams.Test()
		require.NoError(t, RunShell(ios, ps, `!Write-Output "$1|$2|$($args.Count)"`, []string{"a b", "it's $x"}))
		assert.Equal(t, "a b|it's $x|2\n", lines(out.String()))
	})
	t.Run("one arg is a string", func(t *testing.T) {
		ios, _, out, _ := iostreams.Test()
		require.NoError(t, RunShell(ios, ps, `!Write-Output $1.GetType().Name "[$1]"`, []string{"a b"}))
		assert.Equal(t, "String\n[a b]\n", lines(out.String()))
	})
	t.Run("smart quotes arrive unchanged", func(t *testing.T) {
		ios, _, out, _ := iostreams.Test()
		// Code points keep the output ASCII whatever the console code page.
		require.NoError(t, RunShell(ios, ps, `!Write-Output ([int[]][char[]]$1 -join ',')`, []string{"\u2018\u2019\u201a\u201b'"}))
		assert.Equal(t, "8216,8217,8218,8219,39\n", lines(out.String()))
	})
	t.Run("external exit code", func(t *testing.T) {
		ios, _, _, _ := iostreams.Test()
		script := "!sh -c 'exit 7'"
		if runtime.GOOS == "windows" || strings.HasSuffix(ps, ".exe") {
			script = "!cmd.exe /c exit 7"
		}
		var ext *cmdutil.ExternalExitError
		require.ErrorAs(t, RunShell(ios, ps, script, nil), &ext)
		assert.Equal(t, 7, ext.Code)
	})
}
