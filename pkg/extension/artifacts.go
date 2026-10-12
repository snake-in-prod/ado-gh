package extension

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/fsutil"
	"github.com/snake-in-prod/ado-gh/internal/tableprinter"
	authShared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// artifactToolPATVar is the env var ArtifactTool reads the token from (--patvar names it), so
// the token is never in its argv.
const artifactToolPATVar = "ADO_ARTIFACTTOOL_PAT"

// maxArtifactToolZip caps the ArtifactTool download.
const maxArtifactToolZip = 1 << 30

// artifactOSName maps GOOS to the osName the clienttools API takes (the az CLI's spelling).
var artifactOSName = map[string]string{"linux": "Linux", "darwin": "Darwin", "windows": "Windows"}

// packageVersion is a universal package version: lowercase SemVer without build metadata.
var packageVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9a-z-]+(\.[0-9a-z-]+)*)?$`)

// installArtifacts finishes InstallScript for a manifest naming an Azure Artifacts universal
// package (the clone only supplied the manifest and the pinned tag). The version is the pinned
// tag without its "v", else the latest release version. A feed in another organization than
// the source's must pass extension_sources too. ArtifactTool, fetched from the feed's
// organization into a cache, downloads the package with the user's token in its env; the
// platform binary and checksums.txt are read from its output through os.Root, the binary is
// verified as it is copied into a staging directory, which is renamed into place after the
// install is confirmed.
func installArtifacts(ctx context.Context, f *cmdutil.Factory, o ScriptOptions, m *Manifest, rec *Record) (Extension, error) {
	a := m.Binaries.AzureArtifacts
	ios := f.IOStreams
	goos, goarch := o.goos, o.goarch
	if goos == "" {
		goos, goarch = runtime.GOOS, runtime.GOARCH
	}
	var version string
	if o.Pin != "" {
		if rec.Tag == "" {
			return Extension{}, fmt.Errorf("--pin %q is not a tag: an Azure Artifacts extension is pinned to a tag naming its package version, such as v1.2.3", o.Pin)
		}
		if version = strings.TrimPrefix(rec.Tag, "v"); !packageVersion.MatchString(version) {
			return Extension{}, fmt.Errorf("tag %q does not name a package version such as v1.2.3", Sanitize(rec.Tag))
		}
	}
	feedSrc := Source{Host: hostADO, Owner: a.Organization, Project: a.Project, Repo: a.Feed}
	if o.Source.Host != hostADO || !strings.EqualFold(a.Organization, o.Source.Owner) {
		patterns, err := sourcePatterns(f)
		if err != nil {
			return Extension{}, err
		}
		if err := CheckAllowed(patterns, feedSrc); err != nil {
			return Extension{}, fmt.Errorf("the azure-artifacts feed in %s: %w", ManifestFile, err)
		}
	}
	tok, err := lookupToken(f, hostADO, a.Organization)
	if err != nil {
		return Extension{}, err
	}
	hc, err := authShared.HTTPClient(ctx, f, hostADO, a.Organization)
	if err != nil {
		return Extension{}, err
	}
	orgURL := "https://" + hostADO + "/" + url.PathEscape(a.Organization) + "/"
	toolRel, err := artifactToolRelease(ctx, f, orgURL, hc, goos, goarch)
	if err != nil {
		return Extension{}, err
	}
	if version == "" {
		if version, err = api.LatestUniversalVersion(ctx, hc, orgURL, a.Project, a.Feed, a.Package); err != nil {
			return Extension{}, err
		}
	}
	if o.resolved != nil {
		if err := o.resolved(version); err != nil {
			return Extension{}, err
		}
	}
	tool, err := artifactTool(ctx, f, toolRel, goos)
	if err != nil {
		return Extension{}, err
	}

	dl, err := os.MkdirTemp(o.Root, ".download-")
	if err != nil {
		return Extension{}, err
	}
	defer os.RemoveAll(dl)
	run := o.runTool
	if run == nil {
		run = (*exec.Cmd).Run
	}
	if err := downloadPackage(ctx, run, tool, orgURL, tok.Value, a, version, dl); err != nil {
		return Extension{}, err
	}
	pkg, err := os.OpenRoot(dl)
	if err != nil {
		return Extension{}, err
	}
	defer pkg.Close()
	files, err := fs.ReadDir(pkg.FS(), ".")
	if err != nil {
		return Extension{}, err
	}
	// The package's top-level regular files stand in for release assets.
	contents := &GitHubRelease{Tag: version}
	for _, e := range files {
		if e.Type().IsRegular() {
			contents.Assets = append(contents.Assets, GitHubAsset{Name: e.Name()})
		}
	}
	asset, note, err := contents.SelectAsset(o.Name, goos, goarch)
	if err != nil {
		return Extension{}, err
	}
	if note != "" {
		fmt.Fprintf(ios.ErrOut, "%s %s\n", ios.ColorScheme().Yellow("!"), note)
	}

	var want, status string
	if ca := contents.ChecksumAsset(); ca != nil {
		b, err := readPackageFile(pkg, ca.Name)
		if err != nil {
			return Extension{}, err
		}
		if want = ParseChecksums(b)[asset.Name]; want == "" {
			return Extension{}, fmt.Errorf("%s has no SHA-256 for %s", Sanitize(ca.Name), Sanitize(asset.Name))
		}
		status = "SHA-256 from " + ca.Name
	} else if !o.AllowUnverified {
		return Extension{}, fmt.Errorf("package %s %s has no checksums.txt; use --allow-unverified to install %s without verifying it",
			a.Package, version, Sanitize(asset.Name))
	} else {
		status = "unverified: the package has no checksums.txt"
		if ios.IsStderrTTY() {
			fmt.Fprintf(ios.ErrOut, "%s package %s %s has no checksums.txt; %s is installed without verification\n",
				ios.ColorScheme().Yellow("!"), a.Package, version, Sanitize(asset.Name))
		}
	}

	rec.Kind, rec.Version = KindBinary, version
	ref := fmt.Sprintf("%s %s (%s) from feed %s", a.Package, version, asset.Name, feedSrc)
	if rec.TokenConsent, err = Confirm(f, Summary{Name: o.Name, Description: m.Description, Source: o.URL, Ref: ref,
		Kind: KindBinary, Checksum: status, Token: m.Token}, o.Trust); err != nil {
		return Extension{}, err
	}

	stage, err := os.MkdirTemp(o.Root, ".install-")
	if err != nil {
		return Extension{}, err
	}
	defer os.RemoveAll(stage)
	file := Prefix + o.Name
	if goos == "windows" {
		file += ".exe"
	}
	src, err := openPackageFile(pkg, asset.Name)
	if err != nil {
		return Extension{}, err
	}
	got, err := writeExecutable(src, stage, file)
	src.Close()
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

// openPackageFile opens name in the package output, which must be a regular file both by name
// (no symlink) and through the open handle.
func openPackageFile(pkg *os.Root, name string) (*os.File, error) {
	notRegular := fmt.Errorf("%s in the package is not a regular file", Sanitize(name))
	if fi, err := pkg.Lstat(name); err != nil {
		return nil, err
	} else if !fi.Mode().IsRegular() {
		return nil, notRegular
	}
	f, err := pkg.Open(name)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, cmp.Or(err, notRegular)
	}
	return f, nil
}

// readPackageFile reads at most 1 MiB of name in the package output (see openPackageFile).
func readPackageFile(pkg *os.Root, name string) ([]byte, error) {
	f, err := openPackageFile(pkg, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

// downloadPackage runs `ArtifactTool universal download` of version of package a into dir,
// with the token only in the env var --patvar names. A failure carries the tool's Error and
// Fatal log lines.
func downloadPackage(ctx context.Context, run func(*exec.Cmd) error, tool, orgURL, token string, a *AzureArtifacts, version, dir string) error {
	args := []string{"universal", "download", "--service", orgURL, "--patvar", artifactToolPATVar,
		"--feed", a.Feed, "--package-name", a.Package, "--package-version", version, "--path", dir}
	if a.Project != "" {
		args = append(args, "--project", a.Project)
	}
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Env = append(os.Environ(), artifactToolPATVar+"="+token)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := run(cmd)
	if err == nil {
		return nil
	}
	msg := fmt.Sprintf("ArtifactTool could not download %s %s from feed %s (%v)", a.Package, version, a.Feed, err)
	if lines := serilogErrors(stderr.Bytes()); len(lines) > 0 {
		msg += ": " + strings.Join(lines, "; ")
	}
	return errors.New(msg)
}

// serilogErrors returns the messages of the Error, Fatal and Critical events in ArtifactTool's
// stderr, one Serilog compact JSON event per line; other lines are skipped.
func serilogErrors(b []byte) []string {
	var msgs []string
	for line := range strings.Lines(string(b)) {
		var e struct {
			Level     string `json:"@l"`
			Message   string `json:"@m"`
			Template  string `json:"@mt"`
			Exception string `json:"@x"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		switch e.Level {
		case "Error", "Fatal", "Critical":
			first, _, _ := strings.Cut(e.Exception, "\n")
			if msg := strings.TrimSpace(tableprinter.OneLine(Sanitize(cmp.Or(e.Message, e.Template, first)))); msg != "" {
				msgs = append(msgs, msg)
			}
		}
	}
	return msgs
}

// artifactToolRelease looks up the current ArtifactTool release for goos/goarch on the
// organization at orgURL (on every install); a darwin or windows arm64 without a native build
// falls back to amd64 with a warning.
func artifactToolRelease(ctx context.Context, f *cmdutil.Factory, orgURL string, hc *http.Client, goos, goarch string) (*api.ClientToolRelease, error) {
	osName, ok := artifactOSName[goos]
	if !ok {
		return nil, fmt.Errorf("ArtifactTool, needed for Azure Artifacts extensions, is not available for %s", goos)
	}
	rel, err := api.ArtifactToolRelease(ctx, hc, orgURL, osName, goarch)
	if err == nil && rel == nil && goarch == "arm64" && goos != "linux" {
		if rel, err = api.ArtifactToolRelease(ctx, hc, orgURL, osName, "amd64"); rel != nil {
			ios := f.IOStreams
			fmt.Fprintf(ios.ErrOut, "%s no %s-arm64 ArtifactTool; using the amd64 build, which needs x64 emulation\n",
				ios.ColorScheme().Yellow("!"), goos)
		}
	}
	if err != nil {
		return nil, err
	}
	if rel == nil {
		return nil, fmt.Errorf("no ArtifactTool for %s-%s, needed for Azure Artifacts extensions", goos, goarch)
	}
	return rel, nil
}

// artifactTool returns the executable of ArtifactTool release rel for goos, cached in
// <DataDir>/artifacttool/<rid>_<version>. A release not cached yet is downloaded from its URI
// without credentials, extracted into a temp dir and renamed into place. There is no hash to
// verify it against: it comes from the user's own organization, as for the az CLI.
func artifactTool(ctx context.Context, f *cmdutil.Factory, rel *api.ClientToolRelease, goos string) (string, error) {
	exeName := "artifacttool"
	if goos == "windows" {
		exeName = "ArtifactTool.exe"
	}
	cache := filepath.Join(config.DataDir(), "artifacttool")
	// ponytail: older versions stay cached; prune <rid>_* siblings if the cache grows a problem.
	dir := filepath.Join(cache, fsutil.SafeName(rel.Rid+"_"+rel.Version))
	exe := filepath.Join(dir, exeName)
	if fi, err := os.Lstat(exe); err == nil && fi.Mode().IsRegular() {
		return exe, nil
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(cache, ".extract-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := fetchArtifactTool(ctx, f, rel.URI, tmp); err != nil {
		return "", fmt.Errorf("downloading ArtifactTool: %w", err)
	}
	if fi, err := os.Lstat(filepath.Join(tmp, exeName)); err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("the ArtifactTool archive has no %s", exeName)
	}
	if err := os.Chmod(filepath.Join(tmp, exeName), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		// Another ado may have cached the same release meanwhile.
		if fi, serr := os.Lstat(exe); serr == nil && fi.Mode().IsRegular() {
			return exe, nil
		}
		return "", err
	}
	return exe, nil
}

// fetchArtifactTool downloads the ArtifactTool zip at uri with f.HttpClient's plain client (no
// Azure DevOps token, whatever the host) and extracts it into dir. Errors never hold the URI,
// which may be signed.
func fetchArtifactTool(ctx context.Context, f *cmdutil.Factory, uri, dir string) error {
	if u, err := url.Parse(uri); err != nil || u.Scheme != "https" {
		return errors.New("the release has no https download URL")
	}
	plain, err := f.HttpClient()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return withoutURL(err)
	}
	resp, err := plain.Do(req)
	if err != nil {
		return withoutURL(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	zf, err := os.CreateTemp(filepath.Dir(dir), ".artifacttool-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(zf.Name())
	defer zf.Close()
	n, err := io.Copy(zf, io.LimitReader(resp.Body, maxArtifactToolZip+1))
	if err != nil {
		return withoutURL(err)
	}
	if n > maxArtifactToolZip {
		return errors.New("the archive is too large")
	}
	zr, err := zip.NewReader(zf, n)
	if err != nil {
		return err
	}
	_, err = fsutil.ExtractZip(zr, dir, "", false)
	return err
}
