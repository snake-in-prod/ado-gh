package extension

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/internal/git/gittest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

const (
	scriptGitHubURL = "https://github.com/o/ado-hello.git"
	scriptADOURL    = "https://dev.azure.com/org/proj/_git/ado-hello"
	scriptSHA       = "1111111111111111111111111111111111111111"
)

// scriptFixture is a stubbed script install: clone creates the target with .git and an
// ado-hello script; everything else is answered by the runner.
type scriptFixture struct {
	f    *cmdutil.Factory
	r    *gittest.Runner
	root string
	// manifest, when set, is written to the clone as ado-extension.yml.
	manifest string
	trust    Trust
}

func newScriptFixture(t *testing.T) *scriptFixture {
	t.Helper()
	clearTokenEnv(t)
	t.Setenv("ADO_TOKEN_TYPE", "")
	t.Setenv("GIT_CONFIG_COUNT", "")
	f, _ := sourceFactory(t, "")
	r := &gittest.Runner{Replies: map[string]string{
		"version":                  "git version 2.43.0\n",
		"rev-parse HEAD":           scriptSHA + "\n",
		"ls-files -s -- ado-hello": "100755 " + scriptSHA + " 0\tado-hello\n",
	}}
	f.Prompter = &cmdutil.Prompter{IO: f.IOStreams, Accessible: true}
	s := &scriptFixture{f: f, r: r, root: filepath.Join(t.TempDir(), "extensions"), trust: Trust{Yes: true}}
	f.GitClient = &git.Client{Run: func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "clone" {
			if _, failing := r.Fails[strings.Join(args, " ")]; !failing {
				dst := args[len(args)-1]
				require.NoError(t, os.MkdirAll(filepath.Join(dst, ".git"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dst, "ado-hello"), []byte("#!/bin/sh\n"), 0o755))
				if s.manifest != "" {
					require.NoError(t, os.WriteFile(filepath.Join(dst, ManifestFile), []byte(s.manifest), 0o644))
				}
			}
		}
		return r.Run(ctx, dir, args...)
	}}
	return s
}

func (s *scriptFixture) install(src Source, url, pin string, force bool) (Extension, error) {
	return InstallScript(context.Background(), s.f, ScriptOptions{Root: s.root, Name: "hello", Source: src, URL: url, Pin: pin, Force: force, Trust: s.trust})
}

// cloneCall returns the recorded clone call and asserts its fixed arguments.
func (s *scriptFixture) cloneCall(t *testing.T, url string) (call, dir string) {
	t.Helper()
	for i, c := range s.r.Calls {
		if strings.HasPrefix(c, "clone ") {
			prefix := "clone --quiet -c core.autocrlf=false -c core.eol=lf --no-recurse-submodules -- " + url + " "
			require.True(t, strings.HasPrefix(c, prefix), c)
			tmp := strings.TrimPrefix(c, prefix)
			assert.Equal(t, s.root, filepath.Dir(tmp), "temp dir inside the extensions root")
			assert.Equal(t, s.root, s.r.Dirs[i])
			return c, tmp
		}
	}
	t.Fatal("no clone call")
	return "", ""
}

// rootEntries lists the names under the extensions root.
func (s *scriptFixture) rootEntries(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(s.root)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

var ghSource = Source{Host: hostGitHub, Owner: "o", Repo: "ado-hello"}
var adoSource = Source{Host: hostADO, Owner: "org", Project: "proj", Repo: "ado-hello"}

func TestInstallScriptGitHub(t *testing.T) {
	s := newScriptFixture(t)
	t.Setenv("ADO_TOKEN", "ado-secret") // never sent to GitHub
	ext, err := s.install(ghSource, scriptGitHubURL, "", false)
	require.NoError(t, err)
	assert.Equal(t, Extension{Name: "hello", Path: filepath.Join(s.root, "ado-hello"), Dir: filepath.Join(s.root, "ado-hello"), Kind: KindScript}, ext)

	_, tmp := s.cloneCall(t, scriptGitHubURL)
	assert.NoDirExists(t, tmp, "temp dir renamed")
	assert.Equal(t, []string{".records", "ado-hello"}, s.rootEntries(t))
	assert.NotContains(t, s.r.Calls, "version", "no version check without a token")
	assert.Equal(t, []string{"rev-parse HEAD", "ls-files -s -- ado-hello"}, s.r.Calls[1:])
	assert.Equal(t, []string{tmp, tmp}, s.r.Dirs[1:], "checked in the clone before the rename")
	for _, env := range s.r.Envs {
		assert.Equal(t, []string{"GIT_ALLOW_PROTOCOL=https:ssh", "GIT_PROTOCOL_FROM_USER=0", "GIT_TERMINAL_PROMPT=0"}, env)
	}

	rec, err := ReadRecord(s.root, "hello")
	require.NoError(t, err)
	assert.Equal(t, &Record{Source: scriptGitHubURL, Host: hostGitHub, Kind: KindScript, Commit: scriptSHA}, rec)
}

func TestInstallScriptADOToken(t *testing.T) {
	tests := map[string]struct {
		tokenType, inheritedCount, wantIndex, wantCount, wantAuth string
	}{
		"PAT as Basic":            {"", "", "0", "1", "Basic " + base64.StdEncoding.EncodeToString([]byte(":ado-secret"))},
		"Entra as Bearer":         {"bearer", "", "0", "1", "Bearer ado-secret"},
		"after inherited entries": {"", "2", "2", "3", "Basic " + base64.StdEncoding.EncodeToString([]byte(":ado-secret"))},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := newScriptFixture(t)
			t.Setenv("ADO_TOKEN", "ado-secret")
			t.Setenv("ADO_TOKEN_TYPE", tt.tokenType)
			t.Setenv("GIT_CONFIG_COUNT", tt.inheritedCount)
			_, err := s.install(adoSource, scriptADOURL, "", false)
			require.NoError(t, err)
			s.cloneCall(t, scriptADOURL)
			assert.Equal(t, "version", s.r.Calls[0], "version checked first")
			want := []string{"GIT_ALLOW_PROTOCOL=https:ssh", "GIT_PROTOCOL_FROM_USER=0", "GIT_TERMINAL_PROMPT=0",
				"GIT_CONFIG_COUNT=" + tt.wantCount,
				"GIT_CONFIG_KEY_" + tt.wantIndex + "=http." + scriptADOURL + ".extraheader",
				"GIT_CONFIG_VALUE_" + tt.wantIndex + "=Authorization: " + tt.wantAuth}
			for i, c := range s.r.Calls[1:] {
				assert.Equal(t, want, s.r.Envs[i+1], c)
				assert.NotContains(t, c, "ado-secret", "token never in argv")
				assert.NotContains(t, c, "extraheader", "never git -c / clone -c")
			}
		})
	}
}

func TestInstallScriptNoToken(t *testing.T) {
	sshURL := "git@ssh.dev.azure.com:v3/org/proj/ado-hello"
	tests := map[string]struct {
		url   string
		token string
	}{
		"ADO not logged in": {scriptADOURL, ""},
		"ADO over SSH":      {sshURL, "ado-secret"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := newScriptFixture(t)
			t.Setenv("ADO_TOKEN", tt.token)
			_, err := s.install(adoSource, tt.url, "", false)
			require.NoError(t, err)
			s.cloneCall(t, tt.url)
			assert.NotContains(t, s.r.Calls, "version")
			for _, env := range s.r.Envs {
				assert.NotContains(t, strings.Join(env, "\n"), "GIT_CONFIG")
			}
		})
	}
}

func TestInstallScriptPromptOnTTY(t *testing.T) {
	s := newScriptFixture(t)
	s.f.IOStreams.SetStdinTTY(true)
	s.f.IOStreams.SetStdoutTTY(true)
	_, err := s.install(ghSource, scriptGitHubURL, "", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"GIT_ALLOW_PROTOCOL=https:ssh", "GIT_PROTOCOL_FROM_USER=0"}, s.r.Envs[0])
}

func TestInstallScriptConfirm(t *testing.T) {
	tests := map[string]struct {
		tty         bool
		input, pin  string
		trust       Trust
		manifest    string
		wantErr     string
		wantConsent bool
		wantOut     []string
	}{
		"summary and consent": {tty: true, input: "y\ny\n", pin: "v1",
			manifest: "description: \"Says \\e[31mhello\"\ntoken: true\n", wantConsent: true,
			wantOut: []string{"Description: Says hello\n", "Source:      " + scriptGitHubURL + "\n", "Ref:         v1 (" + scriptSHA + ")\n",
				"Kind:        script\n", "Checksum:    not applicable (git)\n", "Token:       requests your Azure DevOps token"}},
		"default branch, no manifest": {tty: true, input: "y\n",
			wantOut: []string{"Ref:         " + scriptSHA + " (default branch)\n", "Token:       not requested\n"}},
		"declined": {tty: true, input: "n\n", wantErr: "CancelError", wantOut: []string{"Installation cancelled"}},
		"no consent": {trust: Trust{Yes: true}, manifest: "token: true\n",
			wantOut: []string{"hello requests your Azure DevOps token but was not given it"}},
		"--allow-token": {trust: Trust{Yes: true, AllowToken: true}, manifest: "token: true\n", wantConsent: true},
		"bad manifest":  {trust: Trust{Yes: true}, manifest: "name: other\n", wantErr: `does not match the repository name "ado-hello"`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := newScriptFixture(t)
			s.manifest, s.trust = tt.manifest, tt.trust
			s.r.Replies["rev-parse --verify --quiet v1^{commit}"] = scriptSHA + "\n"
			s.r.Replies["rev-parse --verify --quiet refs/tags/v1"] = scriptSHA + "\n"
			ios, in, _, errOut := iostreams.Test()
			in.WriteString(tt.input)
			ios.In = iotest.OneByteReader(in)
			ios.SetStdinTTY(tt.tty)
			ios.SetStdoutTTY(tt.tty)
			ios.SetStderrTTY(tt.tty)
			s.f.IOStreams, s.f.Prompter = ios, &cmdutil.Prompter{IO: ios, Accessible: true}
			_, err := s.install(ghSource, scriptGitHubURL, tt.pin, false)
			for _, w := range tt.wantOut {
				assert.Contains(t, errOut.String(), w)
			}
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				assert.NoDirExists(t, filepath.Join(s.root, "ado-hello"), "nothing installed")
				assert.NoFileExists(t, RecordPath(s.root, "hello"))
				assert.Empty(t, s.rootEntries(t), "temp clone removed")
				return
			}
			require.NoError(t, err)
			rec, err := ReadRecord(s.root, "hello")
			require.NoError(t, err)
			assert.Equal(t, tt.wantConsent, rec.TokenConsent)
		})
	}
}

func TestInstallScriptNeedsYes(t *testing.T) {
	s := newScriptFixture(t)
	s.trust = Trust{}
	_, err := s.install(ghSource, scriptGitHubURL, "", false)
	assert.EqualError(t, err, "--yes required when not running interactively")
	assert.Empty(t, s.r.Calls, "refused before cloning")
}

func TestReadManifest(t *testing.T) {
	dir := t.TempDir()
	m, err := ReadManifest(dir, "hello")
	require.NoError(t, err)
	assert.Equal(t, &Manifest{Name: "hello"}, m, "missing manifest is empty")

	outside := filepath.Join(t.TempDir(), "secret.yml")
	require.NoError(t, os.WriteFile(outside, []byte("token: true\n"), 0o644))
	if err := os.Symlink(outside, filepath.Join(dir, ManifestFile)); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	_, err = ReadManifest(dir, "hello")
	assert.ErrorContains(t, err, "read "+ManifestFile, "symlink out of the checkout refused")
}

func TestInstallScriptOldGit(t *testing.T) {
	s := newScriptFixture(t)
	t.Setenv("ADO_TOKEN", "ado-secret")
	s.r.Replies["version"] = "git version 2.30.2\n"
	_, err := s.install(adoSource, scriptADOURL, "", false)
	assert.EqualError(t, err, "git >= 2.31 required for private Azure DevOps extensions")
	assert.Equal(t, []string{"version"}, s.r.Calls, "nothing cloned")
}

func TestCheckGitVersion(t *testing.T) {
	tests := map[string]string{
		"git version 2.31.0\n":                 "",
		"git version 2.39.3 (Apple Git-145)\n": "",
		"git version 2.41.0.windows.1\n":       "",
		"git version 3.0.0\n":                  "",
		"git version 2.30.9\n":                 ErrOldGit.Error(),
		"git version 1.9.5\n":                  ErrOldGit.Error(),
		"git version unknown\n":                `unrecognized git version "git version unknown"`,
	}
	for out, wantErr := range tests {
		r := &gittest.Runner{Replies: map[string]string{"version": out}}
		err := checkGitVersion(context.Background(), &git.Client{Run: r.Run})
		if wantErr == "" {
			assert.NoError(t, err, out)
		} else {
			assert.EqualError(t, err, wantErr, out)
		}
	}
}

func TestInstallScriptPin(t *testing.T) {
	tests := map[string]struct {
		pin     string
		isTag   bool
		wantTag string
	}{
		"tag":    {"v1.0.0", true, "v1.0.0"},
		"commit": {"2222222", false, ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := newScriptFixture(t)
			s.r.Replies["rev-parse --verify --quiet "+tt.pin+"^{commit}"] = scriptSHA + "\n"
			if tt.isTag {
				s.r.Replies["rev-parse --verify --quiet refs/tags/"+tt.pin] = scriptSHA + "\n"
			}
			_, err := s.install(ghSource, scriptGitHubURL, tt.pin, false)
			require.NoError(t, err)
			assert.Contains(t, s.r.Calls, "checkout --quiet --detach --no-recurse-submodules "+scriptSHA)
			assert.NotContains(t, s.r.Calls, "rev-parse HEAD")
			rec, err := ReadRecord(s.root, "hello")
			require.NoError(t, err)
			assert.Equal(t, &Record{Source: scriptGitHubURL, Host: hostGitHub, Kind: KindScript, Commit: scriptSHA, Tag: tt.wantTag, Pinned: true}, rec)
		})
	}
}

func TestInstallScriptFailures(t *testing.T) {
	tests := map[string]struct {
		pin     string
		setup   func(s *scriptFixture)
		wantErr string
		noGit   bool
	}{
		"option-like pin": {pin: "--upload-pack=x", wantErr: `invalid --pin "--upload-pack=x"`, noGit: true},
		"unknown pin":     {pin: "v9", wantErr: `no tag or commit "v9" in the repository`},
		"not executable": {
			setup: func(s *scriptFixture) {
				s.r.Replies["ls-files -s -- ado-hello"] = "100644 " + scriptSHA + " 0\tado-hello\n"
			},
			wantErr: "the repository has no executable ado-hello at its root",
		},
		"clone fails": {
			setup: func(s *scriptFixture) {
				s.r.Fails = map[string]error{}
				orig := s.f.GitClient.Run
				s.f.GitClient.Run = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
					if args[0] == "clone" {
						s.r.Fails[strings.Join(args, " ")] = &git.Error{Args: args, ExitCode: 128, Stderr: "fatal: repository not found"}
					}
					return orig(ctx, dir, args...)
				}
			},
			wantErr: "cloning " + scriptGitHubURL + ": fatal: repository not found",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if name == "not executable" && runtime.GOOS == "windows" {
				t.Skip("on Windows a file with a shebang counts as executable (o13); see TestCheckScriptExecutableWindows")
			}
			s := newScriptFixture(t)
			if tt.setup != nil {
				tt.setup(s)
			}
			_, err := s.install(ghSource, scriptGitHubURL, tt.pin, false)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			if tt.noGit {
				assert.Empty(t, s.r.Calls)
				return
			}
			assert.Empty(t, s.rootEntries(t), "temp clone removed, nothing installed")
		})
	}
}

func TestInstallScriptAlreadyInstalled(t *testing.T) {
	s := newScriptFixture(t)
	old := filepath.Join(s.root, "ado-hello")
	require.NoError(t, os.MkdirAll(old, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(old, "stale"), nil, 0o644))

	_, err := s.install(ghSource, scriptGitHubURL, "", false)
	assert.EqualError(t, err, `extension "hello" is already installed; use --force to replace it`)
	assert.Empty(t, s.r.Calls)

	ext, err := s.install(ghSource, scriptGitHubURL, "", true)
	require.NoError(t, err)
	assert.Equal(t, KindScript, ext.Kind)
	assert.NoFileExists(t, filepath.Join(old, "stale"), "replaced")
	assert.FileExists(t, filepath.Join(old, "ado-hello"))
}

func TestInstallScriptInvalidName(t *testing.T) {
	s := newScriptFixture(t)
	_, err := InstallScript(context.Background(), s.f, ScriptOptions{Root: s.root, Name: "../x", Source: ghSource, URL: scriptGitHubURL})
	assert.ErrorContains(t, err, "invalid extension name")
}

func TestCloneURL(t *testing.T) {
	tests := map[string]struct {
		arg  string
		src  Source
		want string
	}{
		"owner/repo":       {"o/ado-hello", ghSource, scriptGitHubURL},
		"GitHub https":     {"https://user:pw@github.com/o/ado-hello", ghSource, scriptGitHubURL},
		"GitHub scp":       {"git@github.com:o/ado-hello.git", ghSource, "git@github.com:o/ado-hello.git"},
		"ADO https":        {"https://org.visualstudio.com/proj/_git/ado-hello", adoSource, scriptADOURL},
		"ADO ssh":          {"ssh://git@ssh.dev.azure.com/v3/org/proj/ado-hello", adoSource, "ssh://git@ssh.dev.azure.com/v3/org/proj/ado-hello"},
		"project w/ space": {"x", Source{Host: hostADO, Owner: "org", Project: "My Proj", Repo: "ado-hello"}, "https://dev.azure.com/org/My%20Proj/_git/ado-hello"},
	}
	for name, tt := range tests {
		assert.Equal(t, tt.want, CloneURL(tt.arg, tt.src), name)
	}
}

func TestCheckScriptExecutableWindows(t *testing.T) {
	tests := map[string]struct {
		mode    string
		files   map[string]string
		wantErr bool
	}{
		"exec bit":             {mode: "100755"},
		"shebang":              {mode: "100644", files: map[string]string{"ado-hello": "#!/bin/sh\n"}},
		"ps1 sibling":          {mode: "100644", files: map[string]string{"ado-hello": "x", "ado-hello.ps1": ""}},
		"cmd only":             {files: map[string]string{"ado-hello.cmd": ""}},
		"no shebang":           {mode: "100644", files: map[string]string{"ado-hello": "echo hi\n"}, wantErr: true},
		"symlink with shebang": {mode: "120000", files: map[string]string{"ado-hello": "#!/bin/sh\n"}, wantErr: true},
		"missing":              {wantErr: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for n, content := range tt.files {
				require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte(content), 0o644))
			}
			r := &gittest.Runner{Replies: map[string]string{}}
			if tt.mode != "" {
				r.Replies["ls-files -s -- ado-hello"] = tt.mode + " " + scriptSHA + " 0\tado-hello\n"
			}
			err := checkScriptExecutable(context.Background(), &git.Client{Dir: dir, Run: r.Run}, "windows", "hello")
			if tt.wantErr {
				assert.ErrorContains(t, err, "no executable ado-hello")
			} else {
				assert.NoError(t, err)
			}
			// Unix accepts only the exec bit.
			err = checkScriptExecutable(context.Background(), &git.Client{Dir: dir, Run: r.Run}, "linux", "hello")
			assert.Equal(t, tt.mode != "100755", err != nil, "linux")
		})
	}
}

// realGitRepo creates a repo with an executable ado-hello committed and tagged v1, then a
// second commit, using real git with an isolated config.
func realGitRepo(t *testing.T) (dir string, g *git.Client) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(t.TempDir(), "gitconfig"),
		"GIT_CONFIG_COUNT": "",
	} {
		t.Setenv(k, v)
	}
	dir = t.TempDir()
	g = &git.Client{Dir: dir}
	ctx := context.Background()
	run := func(args ...string) {
		t.Helper()
		_, err := g.Exec(ctx, args...)
		require.NoError(t, err, args)
	}
	run("init", "--quiet")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ado-hello"), []byte("#!/bin/sh\necho v1\n"), 0o644))
	run("add", "--chmod=+x", "ado-hello")
	run("commit", "--quiet", "-m", "v1")
	run("tag", "v1")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ado-hello"), []byte("#!/bin/sh\necho v2\n"), 0o644))
	run("commit", "--quiet", "-am", "v2")
	return dir, g
}

func TestScriptCheckoutRealGit(t *testing.T) {
	dir, g := realGitRepo(t)
	ctx := context.Background()
	out, err := g.Exec(ctx, "rev-parse", "v1^{commit}")
	require.NoError(t, err)
	v1 := strings.TrimSpace(string(out))

	commit, tag, err := checkoutPin(ctx, g, "v1")
	require.NoError(t, err)
	assert.Equal(t, v1, commit)
	assert.Equal(t, "v1", tag)
	b, err := os.ReadFile(filepath.Join(dir, "ado-hello"))
	require.NoError(t, err)
	assert.Contains(t, string(b), "echo v1")

	commit, tag, err = checkoutPin(ctx, g, v1[:10])
	require.NoError(t, err)
	assert.Equal(t, v1, commit)
	assert.Empty(t, tag)

	_, _, err = checkoutPin(ctx, g, "nope")
	assert.EqualError(t, err, `no tag or commit "nope" in the repository`)

	require.NoError(t, checkScriptExecutable(ctx, g, "linux", "hello"), "mode 100755 in the index")
	_, err = g.Exec(ctx, "update-index", "--chmod=-x", "ado-hello")
	require.NoError(t, err)
	assert.Error(t, checkScriptExecutable(ctx, g, "linux", "hello"))
}

// TestInstallScriptRealGitRefusesFile runs the real clone: its arguments parse, and
// GIT_ALLOW_PROTOCOL refuses anything but https and ssh.
func TestInstallScriptRealGitRefusesFile(t *testing.T) {
	src, _ := realGitRepo(t)
	s := newScriptFixture(t)
	s.f.GitClient = &git.Client{}
	_, err := s.install(ghSource, src, "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transport 'file' not allowed")
	assert.Empty(t, s.rootEntries(t), "temp clone removed")
}
