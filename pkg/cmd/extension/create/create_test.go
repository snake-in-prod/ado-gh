package create

import (
	"bytes"
	"errors"
	"go/format"
	"go/version"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/internal/git/gittest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	extpkg "github.com/snake-in-prod/ado-gh/pkg/extension"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

type createEnv struct {
	f           *cmdutil.Factory
	git         *gittest.Runner
	out, errOut *bytes.Buffer
}

// newCreateEnv chdirs into a temp dir and returns a Factory with a stubbed git runner, no ADO
// context and, when tty, a terminal that answers prompts with input.
func newCreateEnv(t *testing.T, tty bool, input string) *createEnv {
	t.Chdir(t.TempDir())
	ios, in, out, errOut := iostreams.Test()
	in.WriteString(input)
	ios.In = iotest.OneByteReader(in)
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	ios.SetStderrTTY(tty)
	r := &gittest.Runner{}
	return &createEnv{
		f: &cmdutil.Factory{
			IOStreams: ios,
			Prompter:  &cmdutil.Prompter{IO: ios, Accessible: true},
			GitClient: &git.Client{Run: r.Run, Stream: r.Stream},
			BaseRepo:  func() (adocontext.Repo, error) { return adocontext.Repo{}, errors.New("not a repo") },
		},
		git: r, out: out, errOut: errOut,
	}
}

func (e *createEnv) run(args ...string) error {
	cmd := NewCmdCreate(e.f)
	cmd.SetArgs(args)
	cmd.SetOut(e.out)
	cmd.SetErr(e.errOut)
	return cmd.Execute()
}

// createdFiles lists the files under dir (slash paths) with whether each is executable.
func createdFiles(t *testing.T, dir string) map[string]bool {
	t.Helper()
	files := map[string]bool{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		files[filepath.ToSlash(rel)] = info.Mode()&0o111 != 0
		return err
	}))
	return files
}

func readCreated(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

func TestCreateFileSets(t *testing.T) {
	tests := map[string]struct {
		args      []string
		files     map[string]bool
		chmod     string
		manifest  extpkg.Binaries
		nextSteps []string
	}{
		"script": {
			args:      []string{"hello"},
			files:     map[string]bool{"ado-hello": true, "ado-extension.yml": false, "README.md": false},
			chmod:     "add --chmod=+x -- ado-hello",
			nextSteps: []string{"cd ado-hello; ado extension install .; ado hello"},
		},
		"go": {
			args: []string{"hello", "--precompiled=go"},
			files: map[string]bool{"main.go": false, "go.mod": false, ".gitignore": false, "ado-extension.yml": false,
				".github/workflows/release.yml": false, "azure-pipelines.yml": false},
			manifest:  extpkg.Binaries{GitHubReleases: true},
			nextSteps: []string{"cd ado-hello; ado extension install .; ado hello", "go build && ado hello", "v0.1.0"},
		},
		"other": {
			args: []string{"ado-hello", "--precompiled", "other"},
			files: map[string]bool{"script/build.sh": true, ".gitignore": false, "ado-extension.yml": false,
				".github/workflows/release.yml": false, "azure-pipelines.yml": false, "README.md": false},
			chmod:     "add --chmod=+x -- script/build.sh",
			manifest:  extpkg.Binaries{GitHubReleases: true},
			nextSteps: []string{"script/build.sh", "v0.1.0"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			e := newCreateEnv(t, true, "")
			require.NoError(t, e.run(tt.args...))

			files := createdFiles(t, "ado-hello")
			if runtime.GOOS == "windows" { // no exec bits on disk; git add --chmod=+x sets the mode
				for k := range tt.files {
					tt.files[k] = false
				}
			}
			assert.Equal(t, tt.files, files)

			calls := []string{"init --quiet", "add ."}
			if tt.chmod != "" {
				calls = append(calls, tt.chmod)
			}
			calls = append(calls, "commit --quiet -m initial commit")
			assert.Equal(t, calls, e.git.Calls)
			for _, d := range e.git.Dirs {
				assert.Equal(t, "ado-hello", d)
			}

			m, err := extpkg.ParseManifest([]byte(readCreated(t, "ado-hello/ado-extension.yml")), "hello")
			require.NoError(t, err)
			assert.Equal(t, "hello", m.Name)
			assert.False(t, m.Token)
			assert.Equal(t, tt.manifest, m.Binaries)

			for _, s := range tt.nextSteps {
				assert.Contains(t, e.out.String(), s)
			}
			assert.Contains(t, e.out.String(), "ado-hello is ready for development!")
			assert.Contains(t, e.out.String(), "docs/extensions.md")
			assert.Contains(t, e.errOut.String(), "✓ Created ado-hello with an initial commit")
		})
	}
}

func TestCreateTemplateContents(t *testing.T) {
	e := newCreateEnv(t, false, "")
	require.NoError(t, e.run("hello", "--precompiled=go"))

	assert.Equal(t, "/ado-hello\n/ado-hello.exe\n/dist/\n", readCreated(t, "ado-hello/.gitignore"))
	assert.Equal(t, "module ado-hello\n\ngo "+goVersion+"\n", readCreated(t, "ado-hello/go.mod"))

	src := readCreated(t, "ado-hello/main.go")
	formatted, err := format.Source([]byte(src))
	require.NoError(t, err)
	assert.Equal(t, string(formatted), src, "main.go is gofmt-ed")
	for _, s := range []string{"ADO_ORG", "ADO_PROJECT", "ADO_EXECUTABLE", "Hello from ado-hello!"} {
		assert.Contains(t, src, s)
	}
	assert.Empty(t, e.errOut.String(), "no status line off a terminal")
	assert.Contains(t, e.out.String(), "Next steps")

	e = newCreateEnv(t, false, "")
	require.NoError(t, e.run("hello"))
	script := readCreated(t, "ado-hello/ado-hello")
	assert.True(t, strings.HasPrefix(script, "#!/bin/sh\n"), script)
	assert.Contains(t, script, `"${ADO_EXECUTABLE:-ado}"`)
	assert.Contains(t, readCreated(t, "ado-hello/README.md"), "ado hello")
	assert.NotContains(t, readCreated(t, "ado-hello/ado-extension.yml"), "binaries")

	e = newCreateEnv(t, false, "")
	require.NoError(t, e.run("hello", "--precompiled=other"))
	readme := readCreated(t, "ado-hello/README.md")
	assert.Contains(t, readme, "ado-hello-<os>-<arch>")
	assert.Contains(t, readme, "checksums.txt")
	assert.Contains(t, readCreated(t, "ado-hello/script/build.sh"), "dist/ado-hello-windows-arm64.exe")
}

func TestCreateGoModulePath(t *testing.T) {
	tests := map[string]struct {
		repo     adocontext.Repo
		repoErr  error
		override string
		want     string
	}{
		"from the base repo":   {repo: adocontext.Repo{Org: "myorg", Project: "tools", Name: "r"}, want: "dev.azure.com/myorg/tools/_git/ado-hello"},
		"from -R ORG/PROJECT":  {repoErr: errors.New("unused"), override: "o/p", want: "dev.azure.com/o/p/_git/ado-hello"},
		"no context":           {repoErr: errors.New("no repo"), want: "ado-hello"},
		"project with a space": {repo: adocontext.Repo{Org: "myorg", Project: "My Tools"}, want: "ado-hello"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			e := newCreateEnv(t, false, "")
			e.f.RepoOverride = tt.override
			e.f.BaseRepo = func() (adocontext.Repo, error) { return tt.repo, tt.repoErr }
			require.NoError(t, e.run("hello", "--precompiled=go"))
			assert.Equal(t, "module "+tt.want+"\n\ngo "+goVersion+"\n", readCreated(t, "ado-hello/go.mod"))
		})
	}
}

// yamlStep is one step of a GitHub workflow or Azure Pipelines file.
type yamlStep struct {
	Uses   string            `yaml:"uses"`
	Name   string            `yaml:"name"`
	Run    string            `yaml:"run"`
	With   map[string]string `yaml:"with"`
	Env    map[string]string `yaml:"env"`
	Task   string            `yaml:"task"`
	Bash   string            `yaml:"bash"`
	Inputs map[string]string `yaml:"inputs"`
}

var sixTargets = "linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"

func TestCreateReleaseWorkflow(t *testing.T) {
	for _, kind := range []string{"go", "other"} {
		t.Run(kind, func(t *testing.T) {
			e := newCreateEnv(t, false, "")
			require.NoError(t, e.run("hello", "--precompiled="+kind))
			var wf struct {
				On struct {
					Push struct {
						Tags []string `yaml:"tags"`
					} `yaml:"push"`
				} `yaml:"on"`
				Permissions map[string]string `yaml:"permissions"`
				Jobs        map[string]struct {
					RunsOn string     `yaml:"runs-on"`
					Steps  []yamlStep `yaml:"steps"`
				} `yaml:"jobs"`
			}
			require.NoError(t, yaml.Unmarshal([]byte(readCreated(t, "ado-hello/.github/workflows/release.yml")), &wf))
			assert.Equal(t, []string{"v*"}, wf.On.Push.Tags)
			assert.Equal(t, map[string]string{"contents": "write", "id-token": "write", "attestations": "write"}, wf.Permissions)
			steps := wf.Jobs["release"].Steps
			byUses := map[string]yamlStep{}
			var runs []string
			for _, s := range steps {
				if s.Uses != "" {
					byUses[strings.Split(s.Uses, "@")[0]] = s
				}
				runs = append(runs, s.Run)
			}
			assert.Equal(t, "ubuntu-latest", wf.Jobs["release"].RunsOn)
			assert.Contains(t, byUses, "actions/checkout")
			assert.Equal(t, map[string]string{"subject-path": "dist/ado-hello-*"}, byUses["actions/attest-build-provenance"].With)
			if kind == "go" {
				assert.Equal(t, map[string]string{"go-version-file": "go.mod"}, byUses["actions/setup-go"].With)
				assert.Contains(t, runs[2], "for t in "+sixTargets+"; do")
				assert.Contains(t, runs[2], `-o "dist/ado-hello-$os-$arch$ext"`)
				assert.Contains(t, runs[2], "CGO_ENABLED=0")
			} else {
				assert.NotContains(t, byUses, "actions/setup-go")
				assert.Equal(t, "script/build.sh", runs[1])
			}
			assert.Contains(t, runs, "cd dist && sha256sum * > checksums.txt")
			last := steps[len(steps)-1]
			assert.Contains(t, last.Run, `gh release create "$GITHUB_REF_NAME" dist/*`)
			assert.Contains(t, last.Run, `[[ "$GITHUB_REF_NAME" == *-* ]] && echo --prerelease`)
			assert.Equal(t, map[string]string{"GH_TOKEN": "${{ github.token }}"}, last.Env)
			// checksums before attestation before release
			idx := func(pred func(yamlStep) bool) int { return slices.IndexFunc(steps, pred) }
			sum := idx(func(s yamlStep) bool { return strings.Contains(s.Run, "sha256sum") })
			att := idx(func(s yamlStep) bool { return strings.HasPrefix(s.Uses, "actions/attest-build-provenance@") })
			assert.Less(t, sum, att)
			assert.Less(t, att, len(steps)-1)
		})
	}
}

func TestCreateAzurePipelines(t *testing.T) {
	for _, kind := range []string{"go", "other"} {
		t.Run(kind, func(t *testing.T) {
			e := newCreateEnv(t, false, "")
			require.NoError(t, e.run("hello", "--precompiled="+kind))
			var p struct {
				Trigger struct {
					Branches struct {
						Exclude []string `yaml:"exclude"`
					} `yaml:"branches"`
					Tags struct {
						Include []string `yaml:"include"`
					} `yaml:"tags"`
				} `yaml:"trigger"`
				PR   string `yaml:"pr"`
				Pool struct {
					VMImage string `yaml:"vmImage"`
				} `yaml:"pool"`
				Steps []yamlStep `yaml:"steps"`
			}
			require.NoError(t, yaml.Unmarshal([]byte(readCreated(t, "ado-hello/azure-pipelines.yml")), &p))
			assert.Equal(t, []string{"*"}, p.Trigger.Branches.Exclude)
			assert.Equal(t, []string{"v*"}, p.Trigger.Tags.Include)
			assert.Equal(t, "none", p.PR)
			assert.Equal(t, "ubuntu-latest", p.Pool.VMImage)

			tasks := map[string]yamlStep{}
			var bash []string
			for _, s := range p.Steps {
				if s.Task != "" {
					tasks[s.Task] = s
				}
				if s.Bash != "" {
					bash = append(bash, s.Bash)
				}
			}
			if kind == "go" {
				require.Contains(t, tasks, "GoTool@0")
				v := tasks["GoTool@0"].Inputs["version"]
				assert.Regexp(t, `^\d+\.\d+\.\d+$`, v, "GoTool@0 needs a full Go version")
				assert.Equal(t, pipelineGoVersion, v)
				assert.Contains(t, bash[0], "for t in "+sixTargets+"; do")
				assert.Contains(t, bash[0], `-o "dist/ado-hello-$os-$arch$ext"`)
			} else {
				assert.NotContains(t, tasks, "GoTool@0")
				assert.Equal(t, "script/build.sh", bash[0])
			}
			assert.Contains(t, bash[1], "sha256sum * > checksums.txt")
			assert.Contains(t, bash[1], `v="$(Build.SourceBranchName)"`)
			assert.Contains(t, bash[1], "##vso[task.setvariable variable=pkgVersion]${v#v}")
			assert.Equal(t, map[string]string{
				"command":                   "publish",
				"publishDirectory":          "$(Build.SourcesDirectory)/dist",
				"feedsToUsePublish":         "internal",
				"vstsFeedPublish":           "PROJECT/FEED",
				"vstsFeedPackagePublish":    "ado-hello",
				"versionOption":             "custom",
				"versionPublish":            "$(pkgVersion)",
				"packagePublishDescription": "ado-hello extension binaries",
			}, tasks["UniversalPackages@0"].Inputs)
		})
	}
}

func TestCreateGoVersions(t *testing.T) {
	// The pipeline toolchain must be able to build the go.mod language version.
	assert.LessOrEqual(t, version.Compare("go"+goVersion, "go"+pipelineGoVersion), 0)
}

func TestCreatePrompts(t *testing.T) {
	tests := map[string]struct {
		args  []string
		input string
		want  string // a file only that kind creates
	}{
		"name and go":         {input: "hello\n2\n", want: "go.mod"},
		"name and script":     {input: "ado-hello\n1\n", want: "ado-hello"},
		"name and other":      {input: "hello\n3\n", want: "script/build.sh"},
		"flag skips the type": {args: []string{"--precompiled=go"}, input: "hello\n", want: "go.mod"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			e := newCreateEnv(t, true, tt.input)
			require.NoError(t, e.run(tt.args...))
			assert.FileExists(t, filepath.Join("ado-hello", tt.want))
			assert.Contains(t, e.out.String(), "Extension name")
			if len(tt.args) == 0 {
				assert.Contains(t, e.out.String(), "What kind of extension?")
			} else {
				assert.NotContains(t, e.out.String(), "What kind of extension?")
			}
		})
	}
}

func TestCreateErrors(t *testing.T) {
	tests := map[string]struct {
		args    []string
		tty     bool
		setup   func(e *createEnv)
		wantErr string
	}{
		"non-TTY without a name": {wantErr: "<name> is required when not running interactively"},
		"bad precompiled":        {args: []string{"hello", "--precompiled=rust"}, wantErr: `invalid value for --precompiled: "rust" (use go or other)`},
		"invalid name":           {args: []string{"Hello"}, wantErr: `invalid extension name "Hello"`},
		"empty name":             {args: []string{"ado-"}, wantErr: `invalid extension name ""`},
		"directory exists": {args: []string{"hello"},
			setup:   func(*createEnv) { _ = os.Mkdir("ado-hello", 0o755) },
			wantErr: `directory "ado-hello" already exists`},
		"git fails": {args: []string{"hello"},
			setup: func(e *createEnv) {
				e.git.Fails = map[string]error{"commit --quiet -m initial commit": &git.Error{Stderr: "fatal: no identity"}}
			},
			wantErr: "fatal: no identity"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			e := newCreateEnv(t, tt.tty, "")
			if tt.setup != nil {
				tt.setup(e)
			}
			err := e.run(tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestCreateCollision(t *testing.T) {
	e := newCreateEnv(t, false, "")
	root := &cobra.Command{Use: "ado"}
	root.AddCommand(&cobra.Command{Use: "pr", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(NewCmdCreate(e.f))
	root.SetArgs([]string{"create", "pr"})
	root.SetOut(e.out)
	root.SetErr(e.errOut)
	err := root.Execute()
	require.EqualError(t, err, `"pr" collides with a core command`)
	assert.NoDirExists(t, "ado-pr")
}

// setGitIdentity makes real git commits work without a user's or CI's git config.
func setGitIdentity(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "Test", "GIT_AUTHOR_EMAIL": "test@example.com",
		"GIT_COMMITTER_NAME": "Test", "GIT_COMMITTER_EMAIL": "test@example.com",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
	} {
		t.Setenv(k, v)
	}
}

func TestCreateRealGit(t *testing.T) {
	setGitIdentity(t)
	for kind, exe := range map[string]string{"script": "ado-hello", "other": "script/build.sh"} {
		t.Run(kind, func(t *testing.T) {
			e := newCreateEnv(t, false, "")
			e.f.GitClient = &git.Client{}
			args := []string{"hello"}
			if kind != "script" {
				args = append(args, "--precompiled="+kind)
			}
			require.NoError(t, e.run(args...))

			g := &git.Client{Dir: "ado-hello"}
			out, err := g.Exec(t.Context(), "ls-files", "-s", "--", exe)
			require.NoError(t, err)
			assert.True(t, strings.HasPrefix(string(out), "100755 "), string(out))
			out, err = g.Exec(t.Context(), "log", "--format=%s")
			require.NoError(t, err)
			assert.Equal(t, "initial commit\n", string(out))
			out, err = g.Exec(t.Context(), "status", "--porcelain")
			require.NoError(t, err)
			assert.Empty(t, string(out), "everything is committed")
		})
	}
}

func TestCreateGoTemplateBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the generated Go extension")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	e := newCreateEnv(t, false, "")
	e.f.BaseRepo = func() (adocontext.Repo, error) { return adocontext.Repo{Org: "myorg", Project: "tools"}, nil }
	require.NoError(t, e.run("hello", "--precompiled=go"))

	build := exec.Command(goBin, "build")
	build.Dir = "ado-hello"
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOFLAGS=")
	out, err := build.CombinedOutput()
	require.NoError(t, err, string(out))
	exe := "ado-hello"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	assert.FileExists(t, filepath.Join("ado-hello", exe))
}
