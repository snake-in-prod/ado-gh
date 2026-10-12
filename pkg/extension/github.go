package extension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/snake-in-prod/ado-gh/internal/fsutil"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// GitHubClient returns a client for the GitHub API over f.HttpClient's plain transport. It
// sends GH_TOKEN (else GITHUB_TOKEN) as a Bearer token only to https://api.github.com, per
// request, so redirects to other hosts (release asset storage) go out without it; it never
// sends the Azure DevOps token. With --debug it logs each request without its query, which
// on a redirected asset download holds a signature.
func GitHubClient(f *cmdutil.Factory) (*http.Client, error) {
	plain, err := f.HttpClient()
	if err != nil {
		return nil, err
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	t := &githubTransport{base: plain.Transport, token: token, userAgent: "ado-cli/" + f.AppVersion}
	if t.base == nil {
		t.base = http.DefaultTransport
	}
	if f.Debug {
		t.log = f.IOStreams.ErrOut
	}
	return &http.Client{Transport: t}, nil
}

type githubTransport struct {
	base             http.RoundTripper
	token, userAgent string
	log              io.Writer
}

func (t *githubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", t.userAgent)
	req.Header.Del("Authorization")
	if t.token != "" && req.URL.Scheme == "https" && strings.EqualFold(req.URL.Hostname(), hostGitHubAPI) {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	if t.log == nil {
		return t.base.RoundTrip(req)
	}
	u := *req.URL
	u.RawQuery, u.ForceQuery = "", false
	fmt.Fprintf(t.log, "> %s %s\n", req.Method, u.Redacted())
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		fmt.Fprintf(t.log, "< error: %v\n\n", withoutURL(err))
		return nil, err
	}
	fmt.Fprintf(t.log, "< HTTP %d\n\n", resp.StatusCode)
	return resp, nil
}

// errGitHubNotFound is a 404 from the GitHub API; callers say what was missing.
var errGitHubNotFound = errors.New("not found")

// githubGet GETs u and returns a 200 response, following redirects. Other statuses are
// errors: 404 is errGitHubNotFound, 401 and an exhausted rate limit name GH_TOKEN. Errors
// never hold the URL, which may be signed after a redirect.
func githubGet(ctx context.Context, hc *http.Client, u, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, withoutURL(err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, withoutURL(err)
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	resp.Body.Close()
	switch code := resp.StatusCode; {
	case code == http.StatusNotFound:
		return nil, errGitHubNotFound
	case code == http.StatusUnauthorized:
		return nil, errors.New("GitHub rejected the credentials: check GH_TOKEN or GITHUB_TOKEN")
	case (code == http.StatusForbidden || code == http.StatusTooManyRequests) && resp.Header.Get("X-RateLimit-Remaining") == "0":
		return nil, rateLimitError(resp.Header.Get("X-RateLimit-Reset"))
	default:
		return nil, fmt.Errorf("HTTP %d", code)
	}
}

// rateLimitError names when the limit resets (reset is Unix seconds, from X-RateLimit-Reset)
// and how to raise it.
func rateLimitError(reset string) error {
	when := "retrying now won't help"
	if sec, err := strconv.ParseInt(reset, 10, 64); err == nil {
		when = "it resets at " + time.Unix(sec, 0).Format("15:04:05 MST")
	}
	return fmt.Errorf("GitHub API rate limit exceeded (%s); set GH_TOKEN to a GitHub token to raise it", when)
}

// withoutURL returns the error inside a *url.Error, whose message would print the URL.
func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// githubAPI returns the api.github.com URL of repo s plus the path elements, each escaped.
func githubAPI(s Source, elems ...string) string {
	u := "https://" + hostGitHubAPI + "/repos/" + url.PathEscape(s.Owner) + "/" + url.PathEscape(s.Repo)
	for _, e := range elems {
		u += "/" + url.PathEscape(e)
	}
	return u
}

// ResolveGitHub returns the GitHub source under its current name: the API's full_name,
// following redirects for transferred or renamed repositories.
func ResolveGitHub(ctx context.Context, hc *http.Client, s Source) (Source, error) {
	resp, err := githubGet(ctx, hc, githubAPI(s), "application/vnd.github+json")
	if errors.Is(err, errGitHubNotFound) {
		return Source{}, fmt.Errorf("GitHub repository %s/%s not found", s.Owner, s.Repo)
	}
	if err != nil {
		return Source{}, fmt.Errorf("GitHub repository %s/%s: %w", s.Owner, s.Repo, err)
	}
	defer resp.Body.Close()
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

// GitHubRelease is the part of a GitHub release ado reads.
type GitHubRelease struct {
	Tag        string        `json:"tag_name"`
	Prerelease bool          `json:"prerelease"`
	Assets     []GitHubAsset `json:"assets"`
}

// GitHubAsset is a release asset. Its download URL is built from ID, never taken from the
// response.
type GitHubAsset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// ErrNoRelease means the repository has no published release (for the tag).
var ErrNoRelease = errors.New("no release")

// FetchRelease returns the release of s tagged tag (prereleases included), or the latest
// non-prerelease, non-draft release when tag is empty. A missing release wraps ErrNoRelease.
func FetchRelease(ctx context.Context, hc *http.Client, s Source, tag string) (*GitHubRelease, error) {
	u := githubAPI(s, "releases", "latest")
	if tag != "" {
		u = githubAPI(s, "releases", "tags", tag)
	}
	resp, err := githubGet(ctx, hc, u, "application/vnd.github+json")
	switch {
	case errors.Is(err, errGitHubNotFound) && tag == "":
		return nil, fmt.Errorf("%w: %s/%s has no published release", ErrNoRelease, s.Owner, s.Repo)
	case errors.Is(err, errGitHubNotFound):
		return nil, fmt.Errorf("%w: %s/%s has no published release %q", ErrNoRelease, s.Owner, s.Repo, tag)
	case err != nil:
		return nil, fmt.Errorf("GitHub release of %s/%s: %w", s.Owner, s.Repo, err)
	}
	defer resp.Body.Close()
	var rel GitHubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("GitHub release of %s/%s: %w", s.Owner, s.Repo, err)
	}
	return &rel, nil
}

// platformAsset matches asset names ending in <os>-<arch>[.exe] for Go's release targets.
var platformAsset = regexp.MustCompile(`(?:^|[-_.])((?:aix|android|darwin|dragonfly|freebsd|illumos|linux|netbsd|openbsd|solaris|windows)-` +
	`(?:386|amd64|arm|arm64|loong64|mips|mips64|mips64le|mipsle|ppc64|ppc64le|riscv64|s390x))(?:\.exe)?$`)

// Platforms returns the sorted <os>-<arch> platforms rel has binaries for.
func (rel *GitHubRelease) Platforms() []string {
	var ps []string
	for _, a := range rel.Assets {
		if m := platformAsset.FindStringSubmatch(a.Name); m != nil && !slices.Contains(ps, m[1]) {
			ps = append(ps, m[1])
		}
	}
	slices.Sort(ps)
	return ps
}

// findAsset returns the binary asset of extension name for goos/goarch: exactly
// ado-<name>-<os>-<arch>[.exe] first, else the first asset ending in <os>-<arch>[.exe] (the
// gh-extension-precompile naming).
func (rel *GitHubRelease) findAsset(name, goos, goarch string) *GitHubAsset {
	platform := goos + "-" + goarch
	if goos == "windows" {
		platform += ".exe"
	}
	for i, a := range rel.Assets {
		if a.Name == Prefix+name+"-"+platform {
			return &rel.Assets[i]
		}
	}
	for i, a := range rel.Assets {
		if strings.HasSuffix(a.Name, platform) {
			return &rel.Assets[i]
		}
	}
	return nil
}

// SelectAsset returns the binary of extension name for goos/goarch (see findAsset). On
// darwin-arm64 without one it falls back to darwin-amd64, which runs under Rosetta 2, and
// returns a note saying so. Without a match the error lists the platforms available.
func (rel *GitHubRelease) SelectAsset(name, goos, goarch string) (a *GitHubAsset, note string, err error) {
	if a = rel.findAsset(name, goos, goarch); a != nil {
		return a, "", nil
	}
	if goos == "darwin" && goarch == "arm64" {
		if a = rel.findAsset(name, goos, "amd64"); a != nil {
			return a, fmt.Sprintf("no darwin-arm64 binary in %s; installing the darwin-amd64 one (runs under Rosetta 2)", Sanitize(rel.Tag)), nil
		}
	}
	available := "none"
	if ps := rel.Platforms(); len(ps) > 0 {
		available = strings.Join(ps, ", ")
	}
	return nil, "", fmt.Errorf("release %s has no %s binary for %s-%s; available: %s", Sanitize(rel.Tag), Prefix+name, goos, goarch, available)
}

// ChecksumAsset returns the release's checksums file: checksums.txt, else the only asset
// whose name ends in _checksums.txt (GoReleaser's default name). nil means none.
func (rel *GitHubRelease) ChecksumAsset() *GitHubAsset {
	var found *GitHubAsset
	n := 0
	for i, a := range rel.Assets {
		if a.Name == "checksums.txt" {
			return &rel.Assets[i]
		}
		if strings.HasSuffix(a.Name, "_checksums.txt") {
			found, n = &rel.Assets[i], n+1
		}
	}
	if n != 1 {
		return nil
	}
	return found
}

var sha256Hex = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// ParseChecksums parses a sha256sum / GoReleaser checksums file: "<sha256>  <name>" or
// "<sha256> *<name>" (binary mode) per line, blank lines skipped. Names are keyed by their
// base name (gh-extension-precompile lists dist/<file>); sums are lowercase hex. Lines without
// a SHA-256 are ignored.
func ParseChecksums(data []byte) map[string]string {
	sums := map[string]string{}
	for line := range strings.Lines(string(data)) {
		sum, name, ok := strings.Cut(strings.TrimSpace(line), " ")
		name = strings.TrimPrefix(strings.TrimSpace(name), "*")
		if !ok || name == "" || !sha256Hex.MatchString(sum) {
			continue
		}
		sums[path.Base(name)] = strings.ToLower(sum)
	}
	return sums
}

// assetURL is the API download URL of asset a of s; with Accept: application/octet-stream it
// answers 200 or a 302 to the asset host.
func assetURL(s Source, a *GitHubAsset) string {
	return githubAPI(s, "releases", "assets", fmt.Sprint(a.ID))
}

// readChecksums downloads and parses the checksums asset a (at most 1 MiB).
func readChecksums(ctx context.Context, hc *http.Client, s Source, a *GitHubAsset) (map[string]string, error) {
	resp, err := githubGet(ctx, hc, assetURL(s, a), "application/octet-stream")
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", Sanitize(a.Name), err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", Sanitize(a.Name), withoutURL(err))
	}
	return ParseChecksums(b), nil
}

// readGitHubManifest returns ado-extension.yml of s at ref, parsed for name; a missing file
// is an empty manifest.
func readGitHubManifest(ctx context.Context, hc *http.Client, s Source, ref, name string) (*Manifest, error) {
	u := githubAPI(s, "contents", ManifestFile) + "?" + url.Values{"ref": {ref}}.Encode()
	resp, err := githubGet(ctx, hc, u, "application/vnd.github.raw+json")
	if errors.Is(err, errGitHubNotFound) {
		return ParseManifest(nil, name)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ManifestFile, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ManifestFile, withoutURL(err))
	}
	return ParseManifest(b, name)
}

// downloadBinary streams asset a into a new file named file in dir (see writeExecutable) and
// returns its SHA-256.
func downloadBinary(ctx context.Context, hc *http.Client, s Source, a *GitHubAsset, dir, file string) (string, error) {
	resp, err := githubGet(ctx, hc, assetURL(s, a), "application/octet-stream")
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", Sanitize(a.Name), err)
	}
	defer resp.Body.Close()
	sum, err := writeExecutable(resp.Body, dir, file)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", Sanitize(a.Name), withoutURL(err))
	}
	return sum, nil
}

// writeExecutable copies r into a new file named file in dir: created exclusively through
// os.Root, hashed while writing, made executable through the open handle. It returns the
// SHA-256 of exactly the bytes written, so what is verified is what gets installed.
func writeExecutable(r io.Reader, dir, file string) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	out, err := fsutil.CreateFile(root, file, os.O_EXCL)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), r)
	if err == nil {
		err = out.Chmod(0o755)
	}
	if err = errors.Join(err, out.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ErrNotBinary means a GitHub source has no release binaries: install it as a script.
var ErrNotBinary = errors.New("not a binary extension")

// BinaryOptions configures InstallGitHubBinary.
type BinaryOptions struct {
	// Root is the extensions root, Name the extension name (without ado-).
	Root, Name string
	Source     Source
	// Pin is a release tag; empty means the latest release.
	Pin   string
	Force bool
	Trust
	// GOOS and GOARCH pick the binary; empty means the running platform.
	GOOS, GOARCH string

	// resolved, when set, gets the release tag before anything is downloaded or confirmed;
	// an error stops the install. Upgrade uses it.
	resolved func(ref string) error
}

// InstallGitHubBinary installs extension o.Name from a GitHub release of o.Source: the latest
// one, or the release tagged o.Pin. A missing release or one without platform binaries is
// ErrNotBinary (gh installs those as scripts). It picks the asset for the platform (see
// SelectAsset), requires its SHA-256 in the release's checksums file unless
// o.AllowUnverified, confirms (see Confirm), then downloads the binary into a staging
// directory inside the root, verifies the bytes written, renames the staging directory into
// place and writes the install record.
func InstallGitHubBinary(ctx context.Context, f *cmdutil.Factory, o BinaryOptions) (Extension, error) {
	if err := ValidateName(o.Name); err != nil {
		return Extension{}, err
	}
	if strings.HasPrefix(o.Pin, "-") {
		return Extension{}, fmt.Errorf("invalid --pin %q", o.Pin)
	}
	ios := f.IOStreams
	if err := o.Check(ios); err != nil {
		return Extension{}, err
	}
	entry := filepath.Join(o.Root, Prefix+o.Name)
	_, err := os.Lstat(entry)
	exists := err == nil
	if exists && !o.Force {
		return Extension{}, fmt.Errorf("extension %q is already installed; use --force to replace it", o.Name)
	}
	if o.GOOS == "" {
		o.GOOS, o.GOARCH = runtime.GOOS, runtime.GOARCH
	}
	hc, err := GitHubClient(f)
	if err != nil {
		return Extension{}, err
	}
	rel, err := FetchRelease(ctx, hc, o.Source, o.Pin)
	if errors.Is(err, ErrNoRelease) {
		return Extension{}, fmt.Errorf("%w: %w", ErrNotBinary, err)
	}
	if err != nil {
		return Extension{}, err
	}
	if len(rel.Platforms()) == 0 {
		return Extension{}, fmt.Errorf("%w: release %s has no platform binaries", ErrNotBinary, Sanitize(rel.Tag))
	}
	asset, note, err := rel.SelectAsset(o.Name, o.GOOS, o.GOARCH)
	if err != nil {
		return Extension{}, err
	}
	if note != "" {
		fmt.Fprintf(ios.ErrOut, "%s %s\n", ios.ColorScheme().Yellow("!"), note)
	}
	if o.resolved != nil {
		if err := o.resolved(rel.Tag); err != nil {
			return Extension{}, err
		}
	}

	var want, status string
	if ca := rel.ChecksumAsset(); ca != nil {
		sums, err := readChecksums(ctx, hc, o.Source, ca)
		if err != nil {
			return Extension{}, err
		}
		if want = sums[asset.Name]; want == "" {
			return Extension{}, fmt.Errorf("%s has no SHA-256 for %s", Sanitize(ca.Name), Sanitize(asset.Name))
		}
		status = "SHA-256 from " + ca.Name
	} else if !o.AllowUnverified {
		return Extension{}, fmt.Errorf("release %s has no checksums.txt; use --allow-unverified to install %s without verifying it",
			Sanitize(rel.Tag), Sanitize(asset.Name))
	} else {
		status = "unverified: the release has no checksums.txt"
		if ios.IsStderrTTY() {
			fmt.Fprintf(ios.ErrOut, "%s release %s has no checksums.txt; %s is installed without verification\n",
				ios.ColorScheme().Yellow("!"), Sanitize(rel.Tag), Sanitize(asset.Name))
		}
	}

	m, err := readGitHubManifest(ctx, hc, o.Source, rel.Tag, o.Name)
	if err != nil {
		return Extension{}, err
	}
	src := "https://" + hostGitHub + "/" + o.Source.Owner + "/" + o.Source.Repo
	ref := rel.Tag + " (" + asset.Name + ")"
	if rel.Prerelease {
		ref = rel.Tag + " (prerelease, " + asset.Name + ")"
	}
	rec := &Record{Source: src, Host: hostGitHub, Kind: KindBinary, Tag: rel.Tag, Pinned: o.Pin != ""}
	if rec.TokenConsent, err = Confirm(f, Summary{Name: o.Name, Description: m.Description, Source: src, Ref: ref,
		Kind: KindBinary, Checksum: status, Token: m.Token}, o.Trust); err != nil {
		return Extension{}, err
	}

	if err := os.MkdirAll(o.Root, 0o755); err != nil {
		return Extension{}, err
	}
	// A dot name: List never shows it. Removed on failure; gone after the rename.
	stage, err := os.MkdirTemp(o.Root, ".install-")
	if err != nil {
		return Extension{}, err
	}
	defer os.RemoveAll(stage)
	file := Prefix + o.Name
	if o.GOOS == "windows" {
		file += ".exe"
	}
	got, err := downloadBinary(ctx, hc, o.Source, asset, stage, file)
	if err != nil {
		return Extension{}, err
	}
	if want != "" && got != want {
		return Extension{}, fmt.Errorf("checksum mismatch for %s: expected %s, got %s", Sanitize(asset.Name), want, got)
	}
	if want != "" {
		rec.Checksum = "sha256:" + got
	}
	if err := placeEntry(o.Root, o.Name, stage, file); err != nil {
		return Extension{}, err
	}
	if err := WriteRecord(o.Root, o.Name, rec); err != nil {
		return Extension{}, err
	}
	return Lookup(o.Root, o.Name)
}
