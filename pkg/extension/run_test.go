package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// envKeys are the variables the fake extension reports, present or not.
var envKeys = []string{"ADO_HOST", "ADO_ORG", "ADO_PROJECT", "ADO_REPO", "ADO_REPO_NAME", "ADO_EXTENSION_NAME",
	"ADO_EXECUTABLE", "ADO_TOKEN", "ADO_TOKEN_TYPE", "AZURE_DEVOPS_EXT_PAT", "ADO_DEBUG", "NO_COLOR"}

// TestMain lets the test binary act as an extension: with ADO_TEST_EXTENSION=1 it prints its
// args, the envKeys it got and its stdin as JSON, then exits with $ADO_TEST_EXTENSION_EXIT.
func TestMain(m *testing.M) {
	if os.Getenv("ADO_TEST_EXTENSION") == "1" {
		os.Exit(fakeExtension())
	}
	os.Exit(m.Run())
}

type fakeReport struct {
	Args  []string          `json:"args"`
	Env   map[string]string `json:"env"`
	Stdin string            `json:"stdin"`
}

func fakeExtension() int {
	in, _ := io.ReadAll(os.Stdin)
	r := fakeReport{Args: os.Args[1:], Env: map[string]string{}, Stdin: string(in)}
	for _, k := range envKeys {
		if v, ok := os.LookupEnv(k); ok {
			r.Env[k] = v
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(r)
	code, _ := strconv.Atoi(os.Getenv("ADO_TEST_EXTENSION_EXIT"))
	return code
}

// clearExtEnv empties the inherited variables env() reads or filters.
func clearExtEnv(t *testing.T) {
	t.Helper()
	for _, k := range envKeys {
		t.Setenv(k, "")
		os.Unsetenv(k) // t.Setenv restores it
	}
}

func extFactory(repo adocontext.Repo, repoErr error) (*cmdutil.Factory, *iostreams.IOStreams) {
	ios, _, _, _ := iostreams.Test()
	return &cmdutil.Factory{
		IOStreams:  ios,
		Executable: "/opt/ado/ado",
		BaseRepo:   func() (adocontext.Repo, error) { return repo, repoErr },
	}, ios
}

// envOf returns env as a map (the last value wins, as for exec) and fails on duplicate keys.
func envOf(t *testing.T, env []string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if _, dup := m[k]; dup && k != "" {
			t.Errorf("duplicate env key %s", k)
		}
		m[k] = v
	}
	return m
}

// captured runs l with a stub run and returns the command it would start.
func captured(t *testing.T, l launcher, f *cmdutil.Factory, consent bool, args ...string) *exec.Cmd {
	t.Helper()
	var got *exec.Cmd
	l.run = func(c *exec.Cmd) error { got = c; return nil }
	if l.goos == "" {
		l.goos = "linux"
	}
	require.NoError(t, l.launch(f, "hello", "/x/ado-hello", consent, args))
	return got
}

func TestEnvContract(t *testing.T) {
	gitRepo := adocontext.Repo{Host: "ssh.dev.azure.com", Org: "o", Project: "p", Name: "r"}
	unresolved := errors.New("not an Azure DevOps repository")
	tests := []struct {
		name     string
		repo     adocontext.Repo
		repoErr  error
		override string
		debug    bool
		inherit  map[string]string
		want     map[string]string // "" values must be present and empty
		absent   []string
	}{
		{name: "git remote", repo: gitRepo,
			inherit: map[string]string{"NO_COLOR": "1", "ADO_REPO_NAME": "stale"},
			want: map[string]string{"ADO_HOST": "dev.azure.com", "ADO_ORG": "o", "ADO_PROJECT": "p", "ADO_REPO_NAME": "r",
				"ADO_EXTENSION_NAME": "hello", "ADO_EXECUTABLE": "/opt/ado/ado", "NO_COLOR": "1"},
			absent: []string{"ADO_REPO", "ADO_DEBUG", "ADO_FORCE_TTY", "CLICOLOR_FORCE"}},
		{name: "visualstudio.com host", repo: adocontext.Repo{Host: "fabrikam.visualstudio.com", Org: "fabrikam", Project: "p", Name: "r"},
			want: map[string]string{"ADO_HOST": "fabrikam.visualstudio.com", "ADO_ORG": "fabrikam"}},
		{name: "vs-ssh host", repo: adocontext.Repo{Host: "vs-ssh.visualstudio.com", Org: "fabrikam", Project: "p", Name: "r"},
			want: map[string]string{"ADO_HOST": "dev.azure.com"}},
		{name: "-R", repo: adocontext.Repo{Host: "dev.azure.com", Org: "o", Project: "p", Name: "r"}, override: "p/r",
			inherit: map[string]string{"ADO_REPO": "x/y/z", "ADO_REPO_NAME": "stale"},
			want:    map[string]string{"ADO_REPO": "o/p/r", "ADO_ORG": "o"}, absent: []string{"ADO_REPO_NAME"}},
		{name: "-R unresolvable", repoErr: unresolved, override: "bad",
			want: map[string]string{"ADO_REPO": "bad", "ADO_HOST": "", "ADO_ORG": ""}},
		{name: "inherited ADO_REPO", repo: adocontext.Repo{Host: "dev.azure.com", Org: "x", Project: "y", Name: "z"},
			inherit: map[string]string{"ADO_REPO": "x/y/z"},
			want:    map[string]string{"ADO_REPO": "x/y/z", "ADO_ORG": "x"}, absent: []string{"ADO_REPO_NAME"}},
		{name: "unresolvable", repoErr: unresolved,
			want: map[string]string{"ADO_HOST": "", "ADO_ORG": "", "ADO_PROJECT": "", "ADO_REPO_NAME": "", "ADO_EXTENSION_NAME": "hello"}},
		{name: "unresolvable keeps ADO_ORG", repoErr: unresolved,
			inherit: map[string]string{"ADO_ORG": "eo", "ADO_PROJECT": "ep"},
			want:    map[string]string{"ADO_HOST": "dev.azure.com", "ADO_ORG": "eo", "ADO_PROJECT": "ep", "ADO_REPO_NAME": ""}},
		{name: "debug", repo: gitRepo, debug: true, want: map[string]string{"ADO_DEBUG": "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearExtEnv(t)
			t.Setenv("ADO_TEST_INHERITED", "kept")
			for k, v := range tt.inherit {
				t.Setenv(k, v)
			}
			f, ios := extFactory(tt.repo, tt.repoErr)
			ios.SetStdoutTTY(true) // resolved TTY/colour state is not exported
			ios.SetColorEnabled(true)
			f.RepoOverride, f.Debug = tt.override, tt.debug
			env := envOf(t, captured(t, launcher{}, f, false).Env)
			for k, v := range tt.want {
				got, ok := env[k]
				assert.True(t, ok, "%s missing", k)
				assert.Equal(t, v, got, k)
			}
			for _, k := range tt.absent {
				assert.NotContains(t, env, k)
			}
			assert.Equal(t, "kept", env["ADO_TEST_INHERITED"], "os.Environ() inherited")
		})
	}
}

func TestEnvToken(t *testing.T) {
	repo := adocontext.Repo{Host: "dev.azure.com", Org: "o", Project: "p", Name: "r"}
	tests := []struct {
		name     string
		repoErr  error
		consent  bool
		inherit  map[string]string
		tok      api.Token
		tokErr   error
		want     map[string]string
		absent   []string
		wantWarn string
	}{
		{name: "no consent strips inherited tokens", inherit: map[string]string{"ADO_TOKEN": "a", "AZURE_DEVOPS_EXT_PAT": "b", "ADO_TOKEN_TYPE": "bearer"},
			absent:   []string{"ADO_TOKEN", "AZURE_DEVOPS_EXT_PAT", "ADO_TOKEN_TYPE"},
			wantWarn: "! ADO_TOKEN and AZURE_DEVOPS_EXT_PAT not passed to extension \"hello\": it has no token consent\n"},
		{name: "no consent, one token", inherit: map[string]string{"AZURE_DEVOPS_EXT_PAT": "b"},
			absent:   []string{"ADO_TOKEN", "AZURE_DEVOPS_EXT_PAT"},
			wantWarn: "! AZURE_DEVOPS_EXT_PAT not passed to extension \"hello\": it has no token consent\n"},
		{name: "no consent, nothing to withhold", absent: []string{"ADO_TOKEN", "ADO_TOKEN_TYPE"}},
		{name: "consent PAT", consent: true, tok: api.Token{Value: "pat"}, inherit: map[string]string{"ADO_TOKEN_TYPE": "bearer"},
			want: map[string]string{"ADO_TOKEN": "pat"}, absent: []string{"ADO_TOKEN_TYPE"}},
		{name: "consent Entra", consent: true, tok: api.Token{Value: "jwt", Bearer: true},
			want: map[string]string{"ADO_TOKEN": "jwt", "ADO_TOKEN_TYPE": "bearer"}},
		{name: "consent, not logged in", consent: true, tokErr: &cmdutil.AuthError{Err: errors.New("not logged in to dev.azure.com/o")},
			absent: []string{"ADO_TOKEN"}, wantWarn: "! no token for extension \"hello\": not logged in to dev.azure.com/o\n"},
		{name: "consent, no org keeps inherited", consent: true, repoErr: errors.New("no repo"),
			inherit: map[string]string{"ADO_TOKEN": "inherited"}, want: map[string]string{"ADO_TOKEN": "inherited"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearExtEnv(t)
			for k, v := range tt.inherit {
				t.Setenv(k, v)
			}
			f, ios := extFactory(repo, tt.repoErr)
			var asked []string
			l := launcher{token: func(_ *cmdutil.Factory, host, org string) (api.Token, error) {
				asked = append(asked, host+"/"+org)
				return tt.tok, tt.tokErr
			}}
			env := envOf(t, captured(t, l, f, tt.consent).Env)
			for k, v := range tt.want {
				assert.Equal(t, v, env[k], k)
			}
			for _, k := range tt.absent {
				assert.NotContains(t, env, k, "filtered, not set empty")
			}
			assert.Equal(t, tt.wantWarn, ios.ErrOut.(fmt.Stringer).String())
			if tt.consent && tt.repoErr == nil {
				assert.Equal(t, []string{"dev.azure.com/o"}, asked)
			} else {
				assert.Empty(t, asked)
			}
		})
	}
}

// The default token lookup is shared.Lookup, and an Entra ADO_TOKEN keeps its type.
func TestEnvTokenLookup(t *testing.T) {
	clearExtEnv(t)
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "env-tok")
	f, _ := extFactory(adocontext.Repo{Host: "dev.azure.com", Org: "o", Project: "p", Name: "r"}, nil)
	f.Config = func() (cmdutil.Config, error) { return config.Load() }

	env := envOf(t, captured(t, launcher{}, f, true).Env)
	assert.Equal(t, "env-tok", env["ADO_TOKEN"])
	assert.NotContains(t, env, "ADO_TOKEN_TYPE")

	t.Setenv("ADO_TOKEN_TYPE", "bearer")
	env = envOf(t, captured(t, launcher{}, f, true).Env)
	assert.Equal(t, "env-tok", env["ADO_TOKEN"])
	assert.Equal(t, "bearer", env["ADO_TOKEN_TYPE"])
}

func TestCommand(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		exe     string
		args    []string
		onPath  map[string]string
		files   []string
		want    []string
		wantErr string
	}{
		{name: "unix execs the file", goos: "linux", exe: "/e/ado-x", args: []string{"a"}, want: []string{"/e/ado-x", "a"}},
		{name: "unix .ps1 name is just a file", goos: "darwin", exe: "/e/ado-x.ps1", want: []string{"/e/ado-x.ps1"}},
		{name: "exe", goos: "windows", exe: "C:/e/ado-x.EXE", args: []string{"a&b"}, want: []string{"C:/e/ado-x.EXE", "a&b"}},
		{name: "ps1 via pwsh", goos: "windows", exe: "C:/e/ado-x.ps1", args: []string{"a b"},
			onPath: map[string]string{"pwsh": "C:/pwsh.exe", "powershell": "C:/ps.exe"},
			want:   []string{"C:/pwsh.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "C:/e/ado-x.ps1", "a b"}},
		{name: "ps1 via powershell", goos: "windows", exe: "C:/e/ado-x.ps1",
			onPath: map[string]string{"powershell": "C:/ps.exe"},
			want:   []string{"C:/ps.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "C:/e/ado-x.ps1"}},
		{name: "ps1 without PowerShell", goos: "windows", exe: "C:/e/ado-x.ps1", wantErr: "PowerShell is required"},
		{name: "cmd", goos: "windows", exe: "C:/e/ado-x.cmd", args: []string{"a b", "c-d"}, want: []string{"C:/e/ado-x.cmd", "a b", "c-d"}},
		{name: "cmd refuses &", goos: "windows", exe: "C:/e/ado-x.cmd", args: []string{"ok", "a&calc"}, wantErr: `refusing to pass "a&calc" to ado-x.cmd`},
		{name: "cmd refuses |", goos: "windows", exe: "C:/e/ado-x.cmd", args: []string{"a|b"}, wantErr: "refusing"},
		{name: "cmd refuses quote", goos: "windows", exe: "C:/e/ado-x.cmd", args: []string{`a"b`}, wantErr: "refusing"},
		{name: "bat refuses %", goos: "windows", exe: "C:/e/ado-x.bat", args: []string{"%PATH%"}, wantErr: "refusing"},
		{name: "script via sh on PATH", goos: "windows", exe: "C:/e/ado-x", args: []string{"a"},
			onPath: map[string]string{"sh": "C:/sh.exe", "pwsh": "C:/pwsh.exe"},
			want:   []string{"C:/sh.exe", "-c", `command "$@"`, "--", "C:/e/ado-x", "a"}},
		{name: "script via Git for Windows sh", goos: "windows", exe: "C:/e/ado-x",
			onPath: map[string]string{"git": "C:/Git/cmd/git.exe"}, files: []string{filepath.Clean("C:/Git/bin/sh.exe")},
			want: []string{filepath.Clean("C:/Git/bin/sh.exe"), "-c", `command "$@"`, "--", "C:/e/ado-x"}},
		{name: "script without sh", goos: "windows", exe: "C:/e/ado-x", onPath: map[string]string{"pwsh": "C:/pwsh.exe"},
			wantErr: "the sh.exe interpreter is required to run ado-x; install Git for Windows"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := launcher{
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
			}
			cmd, err := l.command(tt.exe, tt.args)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, cmd.Args)
		})
	}
}

func TestExecutable(t *testing.T) {
	dir := t.TempDir()
	touch := func(name string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o755))
	}
	base := filepath.Join(dir, "ado-x")
	assert.Equal(t, base, executable("windows", dir, "x"))
	touch("ado-x")
	touch("ado-x.cmd")
	assert.Equal(t, base+".cmd", executable("windows", dir, "x"))
	touch("ado-x.ps1")
	assert.Equal(t, base+".ps1", executable("windows", dir, "x"))
	require.NoError(t, os.Mkdir(base+".exe", 0o755)) // not a file
	assert.Equal(t, base+".ps1", executable("windows", dir, "x"))
	assert.Equal(t, base, executable("linux", dir, "x"))
	assert.Equal(t, executable(runtime.GOOS, dir, "x"), Extension{Name: "x", Dir: dir}.Executable())
}

// installFake copies the test binary to root/ado-hello/ado-hello[.exe] and returns the entry.
func installFake(t *testing.T, root string) Extension {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	b, err := os.ReadFile(exe)
	require.NoError(t, err)
	dir := filepath.Join(root, "ado-hello")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	name := "ado-hello"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), b, 0o755))
	ext, err := Lookup(root, "hello")
	require.NoError(t, err)
	return ext
}

// runFake runs the installed fake with the given consent and exit code and returns its report.
func runFake(t *testing.T, root string, ext Extension, consent bool, code string) (fakeReport, string, error) {
	t.Helper()
	require.NoError(t, WriteRecord(root, "hello", &Record{Kind: KindBinary, TokenConsent: consent}))
	t.Setenv("ADO_TEST_EXTENSION_EXIT", code)
	f, ios := extFactory(adocontext.Repo{Host: "dev.azure.com", Org: "o", Project: "p", Name: "r"}, nil)
	f.Config = func() (cmdutil.Config, error) { return config.Load() }
	ios.In = io.NopCloser(strings.NewReader("input"))
	err := Run(f, ext, []string{"a b", "--flag", "-R", "x"})
	var r fakeReport
	require.NoError(t, json.Unmarshal([]byte(ios.Out.(fmt.Stringer).String()), &r), "stdout: %s", ios.Out)
	return r, ios.ErrOut.(fmt.Stringer).String(), err
}

// TestRunHelperProcess runs a real extension process on every OS: args, stdin, stdout, env and
// the exit code pass through, and the token follows the install record's consent.
func TestRunHelperProcess(t *testing.T) {
	clearExtEnv(t)
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TEST_EXTENSION", "1")
	t.Setenv("ADO_TOKEN", "secret")
	root := t.TempDir()
	ext := installFake(t, root)

	r, stderr, err := runFake(t, root, ext, false, "3")
	var exitErr *cmdutil.ExternalExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 3, exitErr.Code)
	assert.Equal(t, []string{"a b", "--flag", "-R", "x"}, r.Args)
	assert.Equal(t, "input", r.Stdin)
	assert.Equal(t, "hello", r.Env["ADO_EXTENSION_NAME"])
	assert.Equal(t, "o", r.Env["ADO_ORG"])
	assert.Equal(t, "r", r.Env["ADO_REPO_NAME"])
	assert.NotContains(t, r.Env, "ADO_TOKEN")
	assert.Contains(t, stderr, "ADO_TOKEN not passed")

	r, stderr, err = runFake(t, root, ext, true, "0")
	require.NoError(t, err)
	assert.Equal(t, "secret", r.Env["ADO_TOKEN"])
	assert.Empty(t, stderr)
}

func TestRunPathHelperProcess(t *testing.T) {
	clearExtEnv(t)
	t.Setenv("ADO_TEST_EXTENSION", "1")
	t.Setenv("ADO_TEST_EXTENSION_EXIT", "0")
	t.Setenv("ADO_TOKEN", "secret")
	ext := installFake(t, t.TempDir())
	f, ios := extFactory(adocontext.Repo{}, errors.New("no repo"))
	require.NoError(t, RunPath(f, "hello", ext.Executable(), []string{"x"}))
	var r fakeReport
	require.NoError(t, json.Unmarshal([]byte(ios.Out.(fmt.Stringer).String()), &r))
	assert.Equal(t, []string{"x"}, r.Args)
	assert.NotContains(t, r.Env, "ADO_TOKEN", "PATH extensions never get a token")
}

func TestRunMissingExecutable(t *testing.T) {
	clearExtEnv(t)
	f, _ := extFactory(adocontext.Repo{}, errors.New("no repo"))
	err := Run(f, Extension{Name: "gone", Path: filepath.Join(t.TempDir(), "ado-gone"), Dir: t.TempDir()}, nil)
	var exitErr *cmdutil.ExternalExitError
	require.EqualError(t, err, `extension "gone" has no executable ado-gone; reinstall it with ado extension install --force`)
	assert.False(t, errors.As(err, &exitErr), "a start failure is reported, not passed through")

	t.Run("local install not built yet", func(t *testing.T) {
		dir := t.TempDir()
		err := Run(f, Extension{Name: "gone", Path: filepath.Join(t.TempDir(), "ado-gone"), Dir: dir, Kind: KindLocal}, nil)
		require.EqualError(t, err, `extension "gone" has no executable ado-gone in `+dir+` yet; build it there`)
	})
	t.Run("other stat errors are kept", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("a path through a file is ERROR_PATH_NOT_FOUND on Windows")
		}
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, nil, 0o600))
		err := Run(f, Extension{Name: "gone", Path: filepath.Join(t.TempDir(), "ado-gone"), Dir: file}, nil)
		require.ErrorContains(t, err, `extension "gone": stat `+filepath.Join(file, "ado-gone"))
		assert.NotContains(t, err.Error(), "reinstall")
	})
}

func TestRunCommandError(t *testing.T) {
	clearExtEnv(t)
	f, _ := extFactory(adocontext.Repo{}, errors.New("no repo"))
	err := launcher{goos: "windows", lookPath: func(string) (string, error) { return "", exec.ErrNotFound }}.
		launch(f, "x", "C:/e/ado-x.ps1", false, nil)
	require.ErrorContains(t, err, "PowerShell is required")
}

// TestLaunchCatchesInterrupt: ado survives ^C while the extension runs (signal.Notify), and
// SIGINT is not left ignored afterwards (signal.Ignore would be inherited across exec).
func TestLaunchCatchesInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal(os.Interrupt) is not implemented on Windows")
	}
	clearExtEnv(t)
	f, _ := extFactory(adocontext.Repo{}, errors.New("no repo"))
	l := launcher{goos: runtime.GOOS, run: func(*exec.Cmd) error {
		p, err := os.FindProcess(os.Getpid())
		if err != nil {
			return err
		}
		if err := p.Signal(os.Interrupt); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond) // without Notify the test binary dies here
		return nil
	}}
	require.NoError(t, l.launch(f, "x", "/x/ado-x", false, nil))
	assert.False(t, signal.Ignored(os.Interrupt))
}
