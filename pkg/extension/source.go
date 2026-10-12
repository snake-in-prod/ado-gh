package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

const (
	hostGitHub    = "github.com"
	hostGitHubAPI = "api.github.com"
	hostADO       = "dev.azure.com"
	// LocalSource is the canonical form of every local source and the allowlist pattern for them.
	LocalSource = "local"
)

// Source is a parsed `ado extension install` source.
type Source struct {
	// Host is github.com or dev.azure.com ({org}.visualstudio.com and the SSH hosts map to it);
	// empty for a local source.
	Host string
	// Owner is the GitHub owner or the Azure DevOps organization.
	Owner string
	// Project is the Azure DevOps project; empty on GitHub.
	Project string
	// Repo is the repository name, without a .git suffix.
	Repo string
	// Path is the absolute directory of a local source.
	Path string
}

// IsLocal reports whether s is a local directory.
func (s Source) IsLocal() bool { return s.Path != "" }

// String returns the canonical lowercase form matched against extension_sources:
// github.com/<owner>/<repo>, dev.azure.com/<org>/<project>/<repo> or "local".
func (s Source) String() string {
	if s.IsLocal() {
		return LocalSource
	}
	parts := []string{s.Host, s.Owner, s.Project, s.Repo}
	if s.Project == "" {
		parts = []string{s.Host, s.Owner, s.Repo}
	}
	return strings.ToLower(strings.Join(parts, "/"))
}

var (
	githubOwner = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
	githubRepo  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// errSource never includes the argument: a URL may carry a token in its userinfo.
var errSource = errors.New("unsupported extension source: use OWNER/REPO, a GitHub or Azure DevOps repository URL, or a local path such as ./ado-<name>")

// ParseSource parses an install source: `owner/repo` (github.com), a GitHub HTTPS or SSH URL,
// an Azure DevOps HTTPS or SSH URL (dev.azure.com, ssh.dev.azure.com, *.visualstudio.com), or
// a local path (".", "./dir", "../dir" or absolute), made absolute. Hosts are compared after
// parsing, `.`/`..` segments are refused and a trailing .git is stripped.
func ParseSource(arg string) (Source, error) {
	if s := filepath.ToSlash(arg); s == "." || s == ".." || strings.HasPrefix(s, "./") ||
		strings.HasPrefix(s, "../") || filepath.IsAbs(arg) {
		p, err := filepath.Abs(arg)
		return Source{Path: p}, err
	}
	if strings.ContainsFunc(arg, func(r rune) bool {
		return r == '\\' || r == '?' || r == '#' || unicode.IsControl(r)
	}) {
		return Source{}, errSource
	}
	if !strings.Contains(arg, ":") {
		owner, repo, ok := strings.Cut(arg, "/")
		if !ok || strings.Contains(repo, "/") {
			return Source{}, errSource
		}
		return githubSource(owner, repo)
	}
	if s, ok, err := parseGitHubURL(arg); ok {
		return s, err
	}
	r, err := adocontext.ParseRemoteURL(arg)
	if err != nil {
		return Source{}, errSource
	}
	s := Source{Host: hostADO, Owner: r.Org, Project: r.Project, Repo: strings.TrimSuffix(r.Name, ".git")}
	for _, seg := range []string{s.Owner, s.Project, s.Repo} {
		if seg == "" || seg == "." || seg == ".." {
			return Source{}, errSource
		}
	}
	return s, nil
}

// parseGitHubURL parses https://github.com/o/r, ssh://git@github.com/o/r and git@github.com:o/r.
// ok is false when the host isn't github.com.
func parseGitHubURL(raw string) (s Source, ok bool, err error) {
	if !strings.Contains(raw, "://") { // scp-style user@host:path
		raw = "ssh://" + strings.Replace(raw, ":", "/", 1)
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Hostname(), hostGitHub) {
		return Source{}, false, nil
	}
	defaultPort := map[string]string{"https": "443", "ssh": "22"}[u.Scheme]
	if port := u.Port(); defaultPort == "" || (port != "" && port != defaultPort) {
		return Source{}, true, errSource
	}
	owner, repo, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/"), "/")
	if strings.Contains(repo, "/") {
		return Source{}, true, errSource
	}
	s, err = githubSource(owner, repo)
	return s, true, err
}

func githubSource(owner, repo string) (Source, error) {
	repo = strings.TrimSuffix(repo, ".git")
	if !githubOwner.MatchString(owner) || !githubRepo.MatchString(repo) || repo == "." || repo == ".." {
		return Source{}, errSource
	}
	return Source{Host: hostGitHub, Owner: owner, Repo: repo}, nil
}

// ValidateSourcePatterns checks extension_sources patterns: non-empty `/`-separated segments,
// each a valid path.Match pattern or `**`.
func ValidateSourcePatterns(patterns []string) error {
	for _, p := range patterns {
		for seg := range strings.SplitSeq(p, "/") {
			if _, err := path.Match(seg, ""); err != nil || strings.TrimSpace(seg) == "" {
				return fmt.Errorf("invalid extension_sources pattern %q", p)
			}
		}
	}
	return nil
}

// CheckAllowed refuses s unless patterns is empty or a pattern matches its canonical form.
// Matching is case-insensitive and per path segment: `*` matches within one segment, `**` any
// number of segments. Local sources match only the pattern "local". Any malformed pattern
// refuses every source (fail closed).
func CheckAllowed(patterns []string, s Source) error {
	if len(patterns) == 0 {
		return nil
	}
	if err := ValidateSourcePatterns(patterns); err != nil {
		return err
	}
	canon := s.String()
	for _, p := range patterns {
		p = strings.ToLower(p)
		if s.IsLocal() {
			if p == LocalSource {
				return nil
			}
		} else if matchSegments(strings.Split(p, "/"), strings.Split(canon, "/")) {
			return nil
		}
	}
	return fmt.Errorf("extension source %q is not allowed by extension_sources (%s)", canon, strings.Join(patterns, ", "))
}

func matchSegments(pattern, segs []string) bool {
	if len(pattern) == 0 {
		return len(segs) == 0
	}
	if pattern[0] == "**" {
		for i := range len(segs) + 1 {
			if matchSegments(pattern[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, _ := path.Match(pattern[0], segs[0]) // validated by the caller
	return ok && matchSegments(pattern[1:], segs[1:])
}

// CheckSource enforces the extension_sources allowlist for s. An unreadable config refuses.
// A GitHub source is checked again under the name the GitHub API resolves it to, since
// transferred and renamed repositories redirect silently.
func CheckSource(ctx context.Context, f *cmdutil.Factory, s Source) error {
	cfg, err := f.Config()
	if err != nil {
		return fmt.Errorf("read extension_sources: %w", err)
	}
	sc, ok := cfg.(interface{ ExtensionSources() []string })
	if !ok {
		return errors.New("cannot read extension_sources from this config")
	}
	patterns := sc.ExtensionSources()
	if err := CheckAllowed(patterns, s); err != nil || len(patterns) == 0 || s.Host != hostGitHub {
		return err
	}
	hc, err := GitHubClient(f)
	if err != nil {
		return err
	}
	resolved, err := ResolveGitHub(ctx, hc, s)
	if err != nil {
		return err
	}
	return CheckAllowed(patterns, resolved)
}

// ResolveGitHub returns the GitHub source under its current name: the API's full_name,
// following redirects for transferred or renamed repositories.
func ResolveGitHub(ctx context.Context, hc *http.Client, s Source) (Source, error) {
	u := "https://" + hostGitHubAPI + "/repos/" + url.PathEscape(s.Owner) + "/" + url.PathEscape(s.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Source{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := hc.Do(req)
	var authErr *cmdutil.AuthError
	if errors.As(err, &authErr) {
		return Source{}, fmt.Errorf("GitHub rejected the credentials for %s/%s: check GH_TOKEN or GITHUB_TOKEN", s.Owner, s.Repo)
	}
	if err != nil {
		return Source{}, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Source{}, fmt.Errorf("GitHub repository %s/%s not found", s.Owner, s.Repo)
	case resp.StatusCode != http.StatusOK:
		return Source{}, fmt.Errorf("GitHub repository %s/%s: HTTP %d", s.Owner, s.Repo, resp.StatusCode)
	}
	var repo struct {
		FullName string `json:"full_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&repo); err != nil {
		return Source{}, fmt.Errorf("GitHub repository %s/%s: %w", s.Owner, s.Repo, err)
	}
	owner, name, _ := strings.Cut(repo.FullName, "/")
	resolved, err := githubSource(owner, name)
	if err != nil {
		return Source{}, fmt.Errorf("GitHub repository %s/%s: unexpected full_name %q", s.Owner, s.Repo, repo.FullName)
	}
	return resolved, nil
}

// GitHubClient returns a client for the GitHub API over f.HttpClient's transport. It sends
// GH_TOKEN (else GITHUB_TOKEN) as a Bearer token only to https://api.github.com, per request,
// so redirects to other hosts (release asset storage) go out without it; it never sends the
// Azure DevOps token.
func GitHubClient(f *cmdutil.Factory) (*http.Client, error) {
	plain, err := f.HttpClient()
	if err != nil {
		return nil, err
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	var log io.Writer
	if f.Debug {
		log = f.IOStreams.ErrOut
	}
	return api.NewHTTPClient(api.ClientOptions{
		AppVersion: f.AppVersion,
		Token: func(req *http.Request) (api.Token, error) {
			if req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Hostname(), hostGitHubAPI) {
				return api.Token{}, nil
			}
			return api.Token{Value: token, Bearer: true}, nil
		},
		Log:       log,
		Transport: plain.Transport,
	}), nil
}
