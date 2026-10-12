// Package create implements `ado extension create`.
package create

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/spf13/cobra"

	"github.com/snake-in-prod/ado-gh/internal/build"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	extpkg "github.com/snake-in-prod/ado-gh/pkg/extension"
)

const (
	// goVersion is the go line of the generated go.mod: the minimum Go to build the extension.
	goVersion = "1.26"
	// pipelineGoVersion is the Go that azure-pipelines.yml installs: GoTool@0 needs a full
	// version and cannot read go.mod.
	pipelineGoVersion = "1.27.2"
	docsURL           = build.RepoURL + "/blob/main/docs/extensions.md"
)

//go:embed templates
var templates embed.FS

// scaffold is one generated file: dest (slash path, "%s" = name) from a template.
type scaffold struct {
	dest, tmpl string
	exec       bool
}

var kinds = map[string][]scaffold{
	"script": {
		{"ado-%s", "script.sh", true},
		{"ado-extension.yml", "ado-extension.yml", false},
		{"README.md", "README.md", false},
	},
	"go": {
		{"main.go", "main.go.tmpl", false},
		{"go.mod", "go.mod.tmpl", false},
		{".gitignore", "gitignore", false},
		{"ado-extension.yml", "ado-extension.yml", false},
		{".github/workflows/release.yml", "release.yml", false},
		{"azure-pipelines.yml", "azure-pipelines.yml", false},
	},
	"other": {
		{"script/build.sh", "build.sh", true},
		{".gitignore", "gitignore", false},
		{"ado-extension.yml", "ado-extension.yml", false},
		{".github/workflows/release.yml", "release.yml", false},
		{"azure-pipelines.yml", "azure-pipelines.yml", false},
		{"README.md", "README.md", false},
	},
}

// modulePart is a path element valid in a Go module path.
var modulePart = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._~-]*[A-Za-z0-9_~-])?$`)

// NewCmdCreate returns `ado extension create [<name>]`.
func NewCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var precompiled string
	cmd := &cobra.Command{
		Use:   "create [<name>]",
		Short: "Create a new extension",
		Long: `Create a new extension in a directory ado-<name> and make an initial git commit.

By default it is a script extension: an executable ado-<name> shell script. With
--precompiled=go it is a Go program, with --precompiled=other a program in any language you
build with script/build.sh. A precompiled extension comes with two release pipelines that
build binaries named ado-<name>-<os>-<arch>[.exe] plus checksums.txt when you push a v* tag:
.github/workflows/release.yml publishes them as a GitHub release (with build-provenance
attestations), azure-pipelines.yml as an Azure Artifacts universal package.

The Go module path is dev.azure.com/<org>/<project>/_git/ado-<name> when run inside an
Azure DevOps repository (or with -R ORG/PROJECT), else ado-<name>.

Without a name, ado asks for the name and the kind of extension.`,
		Example: `  ado extension create
  ado extension create hello
  ado extension create hello --precompiled=go
  ado extension create hello --precompiled=other`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := "script"
			if cmd.Flags().Changed("precompiled") {
				if precompiled != "go" && precompiled != "other" {
					return cmdutil.FlagErrorf("invalid value for --precompiled: %q (use go or other)", precompiled)
				}
				kind = precompiled
			}
			var name string
			if len(args) > 0 {
				name = args[0]
			} else {
				if !f.IOStreams.CanPrompt() {
					return cmdutil.FlagErrorf("<name> is required when not running interactively")
				}
				var err error
				if name, err = f.Prompter.Input("Extension name:", ""); err != nil {
					return err
				}
				if !cmd.Flags().Changed("precompiled") {
					i, err := f.Prompter.Select("What kind of extension?",
						[]string{"Script (Bash, Python, PowerShell, ...)", "Go", "Other precompiled (C++, Rust, ...)"})
					if err != nil {
						return err
					}
					kind = []string{"script", "go", "other"}[i]
				}
			}
			name = strings.TrimPrefix(strings.TrimSpace(name), extpkg.Prefix)
			if err := extpkg.ValidateName(name); err != nil {
				return err
			}
			if err := extpkg.CheckCollision(cmd.Root(), name); err != nil {
				return err
			}
			return run(cmd, f, name, kind)
		},
	}
	cmd.Flags().StringVar(&precompiled, "precompiled", "", "Create a precompiled extension: `go` or other")
	return cmd
}

func run(cmd *cobra.Command, f *cmdutil.Factory, name, kind string) error {
	dir := extpkg.Prefix + name
	if err := os.Mkdir(dir, 0o755); errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("directory %q already exists", dir)
	} else if err != nil {
		return err
	}
	data := map[string]string{"Name": name, "Kind": kind, "Module": modulePath(f, dir),
		"GoVersion": goVersion, "PipelineGoVersion": pipelineGoVersion, "DocsURL": docsURL}
	var chmod []string
	for _, s := range kinds[kind] {
		dest := s.dest
		if strings.Contains(dest, "%s") {
			dest = fmt.Sprintf(dest, name)
		}
		if err := writeTemplate(filepath.Join(dir, filepath.FromSlash(dest)), s.tmpl, data, s.exec); err != nil {
			return err
		}
		if s.exec {
			chmod = append(chmod, dest)
		}
	}

	g := *f.GitClient
	g.Dir = dir
	steps := [][]string{{"init", "--quiet"}, {"add", "."}}
	if len(chmod) > 0 {
		// Sets mode 100755 in the index even where the filesystem has no exec bit (Windows).
		steps = append(steps, append([]string{"add", "--chmod=+x", "--"}, chmod...))
	}
	steps = append(steps, []string{"commit", "--quiet", "-m", "initial commit"})
	for _, args := range steps {
		if _, err := g.Exec(cmd.Context(), args...); err != nil {
			return fmt.Errorf("git %s: %w", args[0], err)
		}
	}

	ios := f.IOStreams
	if ios.IsStderrTTY() {
		fmt.Fprintf(ios.ErrOut, "%s Created %s with an initial commit\n", ios.ColorScheme().Green("✓"), dir)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s is ready for development!\n\nNext steps\n", dir)
	switch kind {
	case "other":
		fmt.Fprintf(&b, "- build an %[1]s executable in %[1]s, then run 'cd %[1]s; ado extension install .; ado %[2]s'\n", dir, name)
		fmt.Fprintf(&b, "- fill in script/build.sh to build %s-<os>-<arch>[.exe] release binaries into dist/\n", dir)
	default:
		fmt.Fprintf(&b, "- run 'cd %s; ado extension install .; ado %s' to see your new extension in action\n", dir, name)
	}
	if kind == "go" {
		fmt.Fprintf(&b, "- run 'go build && ado %s' to see changes in your code as you develop\n", name)
	}
	fmt.Fprintf(&b, "- push it to a repository named %s on GitHub or Azure DevOps to share it; on GitHub, add the topic ado-extension so 'ado extension search' finds it\n", dir)
	if kind != "script" {
		b.WriteString("- push a tag such as v0.1.0 to publish binaries with .github/workflows/release.yml or azure-pipelines.yml\n")
	}
	fmt.Fprintf(&b, "\nFor more information on writing extensions:\n%s\n", docsURL)
	_, err := fmt.Fprint(ios.Out, b.String())
	return err
}

// modulePath is dev.azure.com/<org>/<project>/_git/<dir> from the ADO context, else dir.
func modulePath(f *cmdutil.Factory, dir string) string {
	r, err := f.BaseProject()
	if err != nil || !modulePart.MatchString(r.Org) || !modulePart.MatchString(r.Project) {
		return dir
	}
	return path.Join("dev.azure.com", r.Org, r.Project, "_git", dir)
}

func writeTemplate(dest, name string, data any, exec bool) error {
	t, err := template.New(name).Delims("<%", "%>").ParseFS(templates, "templates/"+name)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if exec {
		perm = 0o755
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, buf.Bytes(), perm)
}
