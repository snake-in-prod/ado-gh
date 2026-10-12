package extension

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// ErrOldGit refuses a private Azure DevOps clone with a git that ignores GIT_CONFIG_COUNT.
var ErrOldGit = errors.New("git >= 2.31 required for private Azure DevOps extensions")

// ScriptOptions configures InstallScript.
type ScriptOptions struct {
	// Root is the extensions root, Name the extension name (without ado-).
	Root, Name string
	Source     Source
	// URL is the clone URL, from CloneURL.
	URL string
	// Pin is a tag or commit to check out; empty means the default branch.
	Pin   string
	Force bool
	Trust
}

// CloneURL returns the URL to clone s from: arg itself when it is an SSH URL (the user's
// keys, never a token), else the canonical HTTPS URL, so userinfo in arg is dropped.
func CloneURL(arg string, s Source) string {
	if strings.HasPrefix(strings.ToLower(arg), "ssh://") || !strings.Contains(arg, "://") && strings.Contains(arg, ":") {
		return arg
	}
	if s.Host == hostGitHub {
		return "https://" + hostGitHub + "/" + s.Owner + "/" + s.Repo + ".git"
	}
	return "https://" + hostADO + "/" + url.PathEscape(s.Owner) + "/" + url.PathEscape(s.Project) + "/_git/" + url.PathEscape(s.Repo)
}

// InstallScript clones a script extension into o.Root/ado-<name>: into a temp dir inside the
// root first (LF line endings, no submodules, https/ssh only, no terminal prompt off a TTY),
// checks out o.Pin, requires an executable ado-<name>, reads the optional manifest, confirms
// the install and token consent (see Confirm), then renames the clone into place and writes
// the install record. An HTTPS Azure DevOps clone gets the user's token as a URL-scoped
// http.extraheader through GIT_CONFIG_* env, never argv or a config file; other hosts never
// get it.
func InstallScript(ctx context.Context, f *cmdutil.Factory, o ScriptOptions) (Extension, error) {
	if err := ValidateName(o.Name); err != nil {
		return Extension{}, err
	}
	if strings.HasPrefix(o.Pin, "-") {
		return Extension{}, fmt.Errorf("invalid --pin %q", o.Pin)
	}
	if err := o.Check(f.IOStreams); err != nil {
		return Extension{}, err
	}
	entry := filepath.Join(o.Root, Prefix+o.Name)
	_, err := os.Lstat(entry)
	exists := err == nil
	if exists && !o.Force {
		return Extension{}, fmt.Errorf("extension %q is already installed; use --force to replace it", o.Name)
	}
	env, err := scriptEnv(ctx, f, o)
	if err != nil {
		return Extension{}, err
	}
	if err := os.MkdirAll(o.Root, 0o755); err != nil {
		return Extension{}, err
	}
	// A dot name: List never shows it. Removed on failure; gone after the rename.
	tmp, err := os.MkdirTemp(o.Root, ".clone-")
	if err != nil {
		return Extension{}, err
	}
	defer os.RemoveAll(tmp)

	g := *f.GitClient
	g.Dir, g.Env = o.Root, env
	if _, err := g.Exec(ctx, "clone", "--quiet", "-c", "core.autocrlf=false", "-c", "core.eol=lf",
		"--no-recurse-submodules", "--", o.URL, tmp); err != nil {
		return Extension{}, fmt.Errorf("cloning %s: %w", o.URL, err)
	}
	g.Dir = tmp
	rec := &Record{Source: o.URL, Host: o.Source.Host, Kind: KindScript, Pinned: o.Pin != ""}
	if rec.Commit, rec.Tag, err = checkoutPin(ctx, &g, o.Pin); err != nil {
		return Extension{}, err
	}
	if err := checkScriptExecutable(ctx, &g, runtime.GOOS, o.Name); err != nil {
		return Extension{}, err
	}
	m, err := ReadManifest(tmp, o.Name)
	if err != nil {
		return Extension{}, err
	}
	ref := rec.Commit + " (default branch)"
	if rec.Tag != "" {
		ref = rec.Tag + " (" + rec.Commit + ")"
	} else if o.Pin != "" {
		ref = rec.Commit
	}
	if rec.TokenConsent, err = Confirm(f, Summary{Name: o.Name, Description: m.Description, Source: o.URL, Ref: ref,
		Kind: KindScript, Checksum: "not applicable (git)", Token: m.Token}, o.Trust); err != nil {
		return Extension{}, err
	}
	if exists {
		if err := Remove(o.Root, o.Name); err != nil {
			return Extension{}, err
		}
	}
	if err := os.Rename(tmp, entry); err != nil {
		return Extension{}, err
	}
	if err := WriteRecord(o.Root, o.Name, rec); err != nil {
		return Extension{}, err
	}
	return Lookup(o.Root, o.Name)
}

// scriptEnv returns the env of every git call of a script install.
func scriptEnv(ctx context.Context, f *cmdutil.Factory, o ScriptOptions) ([]string, error) {
	// GIT_ALLOW_PROTOCOL also limits submodules and redirects; FROM_USER covers the rest.
	env := []string{"GIT_ALLOW_PROTOCOL=https:ssh", "GIT_PROTOCOL_FROM_USER=0"}
	if !f.IOStreams.CanPrompt() {
		env = append(env, "GIT_TERMINAL_PROMPT=0")
	}
	if o.Source.Host != hostADO || !strings.HasPrefix(o.URL, "https://") {
		return env, nil
	}
	tok, err := lookupToken(f, hostADO, o.Source.Owner)
	var authErr *cmdutil.AuthError
	if errors.As(err, &authErr) || err == nil && tok.Value == "" {
		return env, nil // not logged in: a public repo, or git's credential helper
	}
	if err != nil {
		return nil, err
	}
	if err := checkGitVersion(ctx, f.GitClient); err != nil {
		return nil, err
	}
	header, err := git.ConfigEnv("http."+o.URL+".extraheader", "Authorization: "+tok.Authorization())
	if err != nil {
		return nil, err
	}
	return append(env, header...), nil
}

var gitVersion = regexp.MustCompile(`(\d+)\.(\d+)`)

// checkGitVersion requires git >= 2.31, the first to read GIT_CONFIG_COUNT; older git
// silently ignores it.
func checkGitVersion(ctx context.Context, g *git.Client) error {
	out, err := g.Exec(ctx, "version")
	if err != nil {
		return err
	}
	m := gitVersion.FindStringSubmatch(string(out))
	if m == nil {
		return fmt.Errorf("unrecognized git version %q", strings.TrimSpace(string(out)))
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < 2 || major == 2 && minor < 31 {
		return ErrOldGit
	}
	return nil
}

// checkoutPin checks out pin (detached) in g.Dir and returns the commit SHA, plus pin as tag
// when it names a tag. An empty pin keeps the default branch and returns HEAD's SHA.
func checkoutPin(ctx context.Context, g *git.Client, pin string) (commit, tag string, err error) {
	if pin == "" {
		out, err := g.Exec(ctx, "rev-parse", "HEAD")
		return strings.TrimSpace(string(out)), "", err
	}
	out, err := g.Exec(ctx, "rev-parse", "--verify", "--quiet", pin+"^{commit}")
	if err != nil {
		return "", "", fmt.Errorf("no tag or commit %q in the repository", pin)
	}
	commit = strings.TrimSpace(string(out))
	if _, err := g.Exec(ctx, "checkout", "--quiet", "--detach", "--no-recurse-submodules", commit); err != nil {
		return "", "", err
	}
	if _, err := g.Exec(ctx, "rev-parse", "--verify", "--quiet", "refs/tags/"+pin); err == nil {
		tag = pin
	}
	return commit, tag, nil
}

// checkScriptExecutable requires ado-<name> at the root of the clone in g.Dir with git mode
// 100755. Windows checkouts lose the exec bit, so there a .ps1 or .cmd sibling, or a regular
// ado-<name> starting with a shebang (run through sh), is enough too.
func checkScriptExecutable(ctx context.Context, g *git.Client, goos, name string) error {
	out, err := g.Exec(ctx, "ls-files", "-s", "--", Prefix+name)
	if err != nil {
		return err
	}
	mode, _, _ := strings.Cut(string(out), " ")
	if mode == "100755" {
		return nil
	}
	if goos == "windows" {
		base := filepath.Join(g.Dir, Prefix+name)
		for _, ext := range []string{".ps1", ".cmd"} {
			if fi, err := os.Lstat(base + ext); err == nil && fi.Mode().IsRegular() {
				return nil
			}
		}
		if mode == "100644" && hasShebang(base) {
			return nil
		}
	}
	return fmt.Errorf("the repository has no executable %[1]s at its root; commit it with `git update-index --chmod=+x %[1]s`", Prefix+name)
}

func hasShebang(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 2)
	_, err = io.ReadFull(f, b)
	return err == nil && bytes.Equal(b, []byte("#!"))
}
