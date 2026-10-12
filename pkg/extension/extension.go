// Package extension holds the on-disk layout of ado extensions: the extensions directory,
// entry kinds, the author manifest, install records and the shared name validator.
//
// Layout under Dir():
//
//	ado-<name>/            script (git clone, has .git) or binary install directory
//	ado-<name>             local install: a symlink, or a pointer file holding the target path
//	.records/<name>.yml    install record written by ado, never inside the entry
package extension

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/snake-in-prod/ado-gh/internal/config"
	aliasShared "github.com/snake-in-prod/ado-gh/pkg/cmd/alias/shared"
)

const (
	// Prefix starts every extension repo and entry name.
	Prefix = "ado-"
	// ManifestFile is the author manifest at the extension repo root.
	ManifestFile = "ado-extension.yml"
	recordsDir   = ".records"
)

// Kind is how an extension was installed.
type Kind string

const (
	// KindScript is a git clone with an executable ado-<name> at its root.
	KindScript Kind = "script"
	// KindBinary is a directory holding a downloaded binary.
	KindBinary Kind = "binary"
	// KindLocal is a symlink or pointer file to a local directory.
	KindLocal Kind = "local"
)

// Dir returns the extensions root: <DataDir>/extensions.
func Dir() string { return filepath.Join(config.DataDir(), "extensions") }

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidateName checks an extension name (without the ado- prefix). It is the one check used by
// install, remove, exec and upgrade before a name becomes a path: no separators, no ".", "..",
// leading dot or dash, no trailing dot (Windows drops it), lowercase only.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) || strings.HasSuffix(name, ".") {
		return fmt.Errorf("invalid extension name %q: use lowercase letters, digits, '.', '_' or '-', starting with a letter or digit", name)
	}
	return nil
}

// NameFromRepo derives the extension name from a repo name such as "ado-hello" (→ "hello").
// Repo names are case-insensitive, so the name is lowercased.
func NameFromRepo(repo string) (string, error) {
	repo = strings.ToLower(repo)
	name, ok := strings.CutPrefix(repo, Prefix)
	if !ok {
		return "", fmt.Errorf("extension repository %q must be named %s<name>", repo, Prefix)
	}
	if err := ValidateName(name); err != nil {
		return "", err
	}
	return name, nil
}

// CheckCollision refuses a name taken by a core command or a built-in cobra alias (e.g. wi).
// User aliases and other extensions don't count: aliases shadow extensions by design.
func CheckCollision(root *cobra.Command, name string) error {
	taken := fmt.Errorf("%q collides with a core command", name)
	if name == "help" { // cobra adds its help command only at Execute time
		return taken
	}
	for _, c := range root.Commands() {
		if c.GroupID == aliasShared.AliasGroupID || c.GroupID == aliasShared.ExtensionGroupID {
			continue
		}
		if c.Name() == name || c.HasAlias(name) {
			return taken
		}
	}
	return nil
}

// Manifest is the author manifest ado-extension.yml.
type Manifest struct {
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	Token         bool     `yaml:"token"`
	Binaries      Binaries `yaml:"binaries"`
	MinAdoVersion string   `yaml:"minAdoVersion"`
}

// Binaries says where precompiled binaries come from; empty means a script extension.
type Binaries struct {
	GitHubReleases bool            `yaml:"github-releases"`
	AzureArtifacts *AzureArtifacts `yaml:"azure-artifacts"`
}

// AzureArtifacts names a universal package. An empty Project means an org-scoped feed.
type AzureArtifacts struct {
	Organization string `yaml:"organization"`
	Project      string `yaml:"project,omitempty"`
	Feed         string `yaml:"feed"`
	Package      string `yaml:"package"`
}

// ParseManifest parses and validates ado-extension.yml for the extension repoName (the
// repo-derived name). The manifest name, when set, must equal it; an empty name defaults to it.
func ParseManifest(data []byte, repoName string) (*Manifest, error) {
	if err := ValidateName(repoName); err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ManifestFile, err)
	}
	if m.Name = strings.TrimPrefix(m.Name, Prefix); m.Name == "" {
		m.Name = repoName
	}
	if m.Name != repoName {
		return nil, fmt.Errorf("%s: name %q does not match the repository name %q", ManifestFile, m.Name, Prefix+repoName)
	}
	if a := m.Binaries.AzureArtifacts; a != nil {
		if m.Binaries.GitHubReleases {
			return nil, fmt.Errorf("%s: binaries: set either github-releases or azure-artifacts, not both", ManifestFile)
		}
		// These become ArtifactTool arguments: refuse option-like and odd values.
		for _, f := range []struct {
			key, value string
			optional   bool
		}{
			{"organization", a.Organization, false},
			{"project", a.Project, true}, // project names may contain spaces
			{"feed", a.Feed, false},
			{"package", a.Package, false},
		} {
			if f.optional && f.value == "" {
				continue
			}
			bad := f.value == "" || strings.HasPrefix(f.value, "-") || strings.ContainsAny(f.value, `/\`) ||
				strings.ContainsFunc(f.value, unicode.IsControl) ||
				(f.key != "project" && strings.ContainsFunc(f.value, unicode.IsSpace))
			if bad {
				return nil, fmt.Errorf("%s: invalid azure-artifacts %s %q", ManifestFile, f.key, f.value)
			}
		}
	}
	return &m, nil
}

// Record is the install record ado writes to .records/<name>.yml.
type Record struct {
	Source       string `yaml:"source"`
	Host         string `yaml:"host"`
	Kind         Kind   `yaml:"kind"`
	Tag          string `yaml:"tag,omitempty"`
	Commit       string `yaml:"commit,omitempty"`
	Version      string `yaml:"version,omitempty"`
	Pinned       bool   `yaml:"pinned"`
	TokenConsent bool   `yaml:"tokenConsent"`
	Checksum     string `yaml:"checksum,omitempty"`
}

// RecordPath returns where the install record of name lives under root. Validate name first.
func RecordPath(root, name string) string {
	return filepath.Join(root, recordsDir, name+".yml")
}

// ReadRecord reads the install record of name. A missing record yields an empty record, which
// means no token consent.
func ReadRecord(root, name string) (*Record, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	var r Record
	b, err := os.ReadFile(RecordPath(root, name))
	if errors.Is(err, fs.ErrNotExist) {
		return &r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("parse install record of %q: %w", name, err)
	}
	return &r, nil
}

// WriteRecord writes the install record of name under root.
func WriteRecord(root, name string, r *Record) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	b, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, recordsDir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(RecordPath(root, name), b, 0o600)
}

// Extension is an installed entry. Path is the entry itself (what remove deletes); Dir holds the
// ado-<name> executable: the entry, or the pointer file's target.
type Extension struct {
	Name string
	Path string
	Dir  string
	Kind Kind
}

// List returns the installed extensions under root sorted by name, skipping .records,
// non-ado- entries and entries with invalid names or unreadable pointer files.
// A missing root yields none.
func List(root string) ([]Extension, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var exts []Extension
	for _, e := range entries {
		name, ok := strings.CutPrefix(e.Name(), Prefix)
		if !ok || ValidateName(name) != nil {
			continue
		}
		if ext, err := entry(root, name); err == nil {
			exts = append(exts, ext)
		}
	}
	slices.SortFunc(exts, func(a, b Extension) int { return strings.Compare(a.Name, b.Name) })
	return exts, nil
}

// Lookup returns the installed extension name under root.
func Lookup(root, name string) (Extension, error) {
	if err := ValidateName(name); err != nil {
		return Extension{}, err
	}
	ext, err := entry(root, name)
	if errors.Is(err, fs.ErrNotExist) {
		return Extension{}, fmt.Errorf("extension %q is not installed", name)
	}
	return ext, err
}

// entry derives the kind from the entry itself (Lstat, never following a link): symlink or
// regular (pointer) file → local; directory with .git → script; other directory → binary.
func entry(root, name string) (Extension, error) {
	p := filepath.Join(root, Prefix+name)
	fi, err := os.Lstat(p)
	if err != nil {
		return Extension{}, err
	}
	ext := Extension{Name: name, Path: p, Dir: p}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		ext.Kind = KindLocal
	case fi.IsDir():
		ext.Kind = KindBinary
		if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
			ext.Kind = KindScript
		}
	case fi.Mode().IsRegular():
		target, err := readPointer(p)
		if err != nil {
			return Extension{}, err
		}
		ext.Kind, ext.Dir = KindLocal, target
	default:
		return Extension{}, fmt.Errorf("extension %q: unsupported entry type %s", name, fi.Mode().Type())
	}
	return ext, nil
}

// WritePointer writes a local-install pointer file: the absolute target directory, one line.
// Windows local installs always use it; it is read on every OS.
func WritePointer(path, target string) error {
	if !filepath.IsAbs(target) {
		return fmt.Errorf("pointer target %q is not absolute", target)
	}
	return os.WriteFile(path, []byte(target+"\n"), 0o644)
}

// readPointer reads at most 1 KiB of a pointer file (gh parity) and returns its trimmed target.
func readPointer(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1024))
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(string(b))
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("pointer file %s: target %q is not an absolute path", path, target)
	}
	return target, nil
}
