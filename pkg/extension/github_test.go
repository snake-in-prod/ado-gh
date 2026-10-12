package extension

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

func TestParseChecksums(t *testing.T) {
	a := strings.Repeat("a", 64)
	b := strings.Repeat("B", 64)
	c := strings.Repeat("c", 64)
	data := a + "  ado-hello-linux-amd64\n" +
		"\n" +
		b + " *ado-hello-windows-amd64.exe\r\n" +
		c + "  dist/darwin-arm64\n" +
		"   \n" +
		"nothex  ignored\n" +
		strings.Repeat("d", 63) + "  short\n" +
		a + "\n"
	assert.Equal(t, map[string]string{
		"ado-hello-linux-amd64":       a,
		"ado-hello-windows-amd64.exe": strings.ToLower(b),
		"darwin-arm64":                c,
	}, ParseChecksums([]byte(data)))
	assert.Empty(t, ParseChecksums(nil))
}

func ghRelease(tag string, names ...string) *GitHubRelease {
	rel := &GitHubRelease{Tag: tag}
	for i, n := range names {
		rel.Assets = append(rel.Assets, GitHubAsset{ID: int64(i + 1), Name: n})
	}
	return rel
}

func TestSelectAsset(t *testing.T) {
	tests := []struct {
		name         string
		assets       []string
		goos, goarch string
		want, note   string
	}{
		{"exact name first", []string{"other-linux-amd64", "ado-hello-linux-amd64"}, "linux", "amd64", "ado-hello-linux-amd64", ""},
		{"suffix fallback", []string{"checksums.txt", "linux-amd64", "darwin-amd64"}, "linux", "amd64", "linux-amd64", ""},
		{"arm is not arm64", []string{"ado-hello-linux-arm", "ado-hello-linux-arm64"}, "linux", "arm64", "ado-hello-linux-arm64", ""},
		{"windows needs .exe", []string{"ado-hello-windows-amd64", "ado-hello-windows-amd64.exe"}, "windows", "amd64", "ado-hello-windows-amd64.exe", ""},
		{"native darwin-arm64 wins", []string{"ado-hello-darwin-amd64", "ado-hello-darwin-arm64"}, "darwin", "arm64", "ado-hello-darwin-arm64", ""},
		{"rosetta fallback", []string{"ado-hello-darwin-amd64", "ado-hello-linux-arm64"}, "darwin", "arm64", "ado-hello-darwin-amd64",
			"no darwin-arm64 binary in v1; installing the darwin-amd64 one (runs under Rosetta 2)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, note, err := ghRelease("v1", tt.assets...).SelectAsset("hello", tt.goos, tt.goarch)
			require.NoError(t, err)
			assert.Equal(t, tt.want, a.Name)
			assert.Equal(t, tt.note, note)
		})
	}

	rel := ghRelease("v1", "ado-hello-linux-amd64", "ado-hello-darwin-amd64", "linux-amd64", "ado-hello-windows-arm64.exe", "checksums.txt")
	_, _, err := rel.SelectAsset("hello", "linux", "riscv64")
	assert.EqualError(t, err, "release v1 has no ado-hello binary for linux-riscv64; available: darwin-amd64, linux-amd64, windows-arm64")
	_, _, err = rel.SelectAsset("hello", "darwin", "386")
	assert.ErrorContains(t, err, "no ado-hello binary for darwin-386", "only darwin-arm64 falls back")
	_, _, err = ghRelease("v1", "notes.md").SelectAsset("hello", "linux", "amd64")
	assert.ErrorContains(t, err, "available: none")
}

func TestChecksumAsset(t *testing.T) {
	assert.Equal(t, "checksums.txt", ghRelease("v1", "x_1.0_checksums.txt", "checksums.txt").ChecksumAsset().Name)
	assert.Equal(t, "ado-hello_1.0.0_checksums.txt", ghRelease("v1", "a-linux-amd64", "ado-hello_1.0.0_checksums.txt").ChecksumAsset().Name)
	assert.Nil(t, ghRelease("v1", "a_checksums.txt", "b_checksums.txt").ChecksumAsset(), "ambiguous")
	assert.Nil(t, ghRelease("v1", "a-linux-amd64", "checksums.txt.sig").ChecksumAsset())
}

// ghFixture is a GitHub binary install against httpmock, logging requests (--debug) to errOut.
type ghFixture struct {
	f      *cmdutil.Factory
	reg    *httpmock.Registry
	errOut *bytes.Buffer
	opts   BinaryOptions
}

func newGHFixture(t *testing.T) *ghFixture {
	t.Helper()
	clearTokenEnv(t)
	t.Setenv("GH_TOKEN", "ghtoken")
	t.Setenv("ADO_TOKEN", "adotoken")
	f, reg := sourceFactory(t, "")
	ios, _, _, errOut := iostreams.Test()
	ios.SetStderrTTY(true)
	f.IOStreams, f.Debug, f.AppVersion = ios, true, "1.2.3"
	return &ghFixture{f: f, reg: reg, errOut: errOut, opts: BinaryOptions{
		Root: filepath.Join(t.TempDir(), "extensions"), Name: "hello", Source: ghSource,
		Trust: Trust{Yes: true}, GOOS: "linux", GOARCH: "amd64",
	}}
}

const ghRepoPath = "/repos/o/ado-hello"

// release registers the release endpoint (latest, or the tag of pin) answering with assets
// numbered from 1.
func (g *ghFixture) release(pin, tag string, prerelease bool, names ...string) {
	p := ghRepoPath + "/releases/latest"
	if pin != "" {
		p = ghRepoPath + "/releases/tags/" + pin
	}
	assets := []map[string]any{}
	for i, n := range names {
		assets = append(assets, map[string]any{"id": i + 1, "name": n,
			"url": fmt.Sprintf("https://api.github.com%s/releases/assets/%d", ghRepoPath, i+1)})
	}
	g.reg.Register(hostIs("api.github.com", p), httpmock.JSONResponse(map[string]any{
		"tag_name": tag, "prerelease": prerelease, "draft": false, "assets": assets}))
}

// asset registers the download of asset id: a 302 to the asset host with a signed query.
func (g *ghFixture) asset(id int, body string) {
	g.reg.Register(hostIs("api.github.com", fmt.Sprintf("%s/releases/assets/%d", ghRepoPath, id)),
		httpmock.WithHeader(httpmock.StatusStringResponse(http.StatusFound, ""), "Location",
			fmt.Sprintf("https://release-assets.githubusercontent.com/github-production-release-asset/%d?sig=SECRETSIG&se=2026", id)))
	g.reg.Register(hostIs("release-assets.githubusercontent.com", fmt.Sprintf("/github-production-release-asset/%d", id)),
		httpmock.StringResponse(body))
}

// manifest registers ado-extension.yml at ref; empty body means 404.
func (g *ghFixture) manifest(body string) {
	m := hostIs("api.github.com", ghRepoPath+"/contents/ado-extension.yml")
	if body == "" {
		g.reg.Register(m, httpmock.StatusStringResponse(http.StatusNotFound, `{"message":"Not Found"}`))
		return
	}
	g.reg.Register(m, httpmock.StringResponse(body))
}

func (g *ghFixture) install() (Extension, error) {
	return InstallGitHubBinary(context.Background(), g.f, g.opts)
}

// ghConnReset fails like a dropped connection; unlike httpmock's no-match error it doesn't
// echo the URL, so it shows whether ado's own messages leak it.
func ghConnReset(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") }

func ghSum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestInstallGitHubBinaryLatest(t *testing.T) {
	g := newGHFixture(t)
	bin := "\x7fELF binary"
	g.release("", "v1.0.0", false, "ado-hello-darwin-amd64", "ado-hello-linux-amd64", "checksums.txt")
	g.asset(3, ghSum("other")+"  ado-hello-darwin-amd64\n"+ghSum(bin)+"  ado-hello-linux-amd64\n")
	g.manifest("description: Says hello\ntoken: true\n")
	g.asset(2, bin)
	g.opts.AllowToken = true

	ext, err := g.install()
	require.NoError(t, err)
	g.reg.Verify(t)
	entry := filepath.Join(g.opts.Root, "ado-hello")
	assert.Equal(t, Extension{Name: "hello", Path: entry, Dir: entry, Kind: KindBinary}, ext)
	b, err := os.ReadFile(filepath.Join(entry, "ado-hello"))
	require.NoError(t, err)
	assert.Equal(t, bin, string(b))
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(entry, "ado-hello"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm())
	}
	rec, err := ReadRecord(g.opts.Root, "hello")
	require.NoError(t, err)
	assert.Equal(t, &Record{Source: "https://github.com/o/ado-hello", Host: "github.com", Kind: KindBinary, Tag: "v1.0.0",
		TokenConsent: true, Checksum: "sha256:" + ghSum(bin)}, rec)
	entries, err := os.ReadDir(g.opts.Root)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "only ado-hello and .records: the staging dir was renamed")

	out := g.errOut.String()
	assert.Contains(t, out, "Description: Says hello")
	assert.Contains(t, out, "Ref:         v1.0.0 (ado-hello-linux-amd64)")
	assert.Contains(t, out, "Kind:        binary")
	assert.Contains(t, out, "Checksum:    SHA-256 from checksums.txt")
	assert.Contains(t, out, "> GET https://release-assets.githubusercontent.com/github-production-release-asset/2\n")
	assert.NotContains(t, out, "SECRETSIG", "no signed query in the log")
	assert.NotContains(t, out, "ghtoken")

	for _, req := range g.reg.Requests {
		want := ""
		if req.URL.Host == "api.github.com" {
			want = "Bearer ghtoken"
		}
		assert.Equal(t, want, req.Header.Get("Authorization"), req.URL.String())
		assert.Equal(t, "ado-cli/1.2.3", req.Header.Get("User-Agent"))
	}
	assert.Equal(t, "application/vnd.github+json", g.reg.Requests[0].Header.Get("Accept"))
	assert.Equal(t, "application/octet-stream", g.reg.Requests[1].Header.Get("Accept"))
	assert.Equal(t, "ref=v1.0.0", g.reg.Requests[3].URL.RawQuery, "manifest read at the release tag")
	assert.Equal(t, "application/vnd.github.raw+json", g.reg.Requests[3].Header.Get("Accept"))
}

func TestInstallGitHubBinaryPinnedPrerelease(t *testing.T) {
	g := newGHFixture(t)
	g.opts.Pin, g.opts.GOOS = "v2.0.0-rc.1", "windows"
	bin := "MZ exe"
	g.release("v2.0.0-rc.1", "v2.0.0-rc.1", true, "windows-amd64.exe", "gh-hello_2.0.0_checksums.txt")
	g.asset(2, ghSum(bin)+" *dist/windows-amd64.exe\n")
	g.manifest("")
	g.asset(1, bin)

	ext, err := g.install()
	require.NoError(t, err)
	g.reg.Verify(t)
	b, err := os.ReadFile(filepath.Join(ext.Dir, "ado-hello.exe"))
	require.NoError(t, err)
	assert.Equal(t, bin, string(b))
	rec, err := ReadRecord(g.opts.Root, "hello")
	require.NoError(t, err)
	assert.True(t, rec.Pinned)
	assert.Equal(t, "v2.0.0-rc.1", rec.Tag)
	assert.False(t, rec.TokenConsent)
	assert.Contains(t, g.errOut.String(), "Ref:         v2.0.0-rc.1 (prerelease, windows-amd64.exe)")
	assert.Contains(t, g.errOut.String(), "Checksum:    SHA-256 from gh-hello_2.0.0_checksums.txt")
}

func TestInstallGitHubBinaryPathEscape(t *testing.T) {
	g := newGHFixture(t)
	g.opts.Pin = "a/../b?c#d"
	g.reg.Register(func(req *http.Request) bool {
		return req.URL.EscapedPath() == ghRepoPath+"/releases/tags/a%2F..%2Fb%3Fc%23d" && req.URL.RawQuery == ""
	}, httpmock.StatusStringResponse(http.StatusNotFound, ""))
	_, err := g.install()
	assert.ErrorIs(t, err, ErrNotBinary)
	assert.ErrorIs(t, err, ErrNoRelease)
	assert.ErrorContains(t, err, `o/ado-hello has no published release "a/../b?c#d"`)
	g.reg.Verify(t)
}

func TestInstallGitHubBinaryRosetta(t *testing.T) {
	g := newGHFixture(t)
	g.opts.GOOS, g.opts.GOARCH = "darwin", "arm64"
	g.release("", "v1", false, "ado-hello-darwin-amd64", "checksums.txt")
	g.asset(2, ghSum("x")+"  ado-hello-darwin-amd64\n")
	g.manifest("")
	g.asset(1, "x")
	_, err := g.install()
	require.NoError(t, err)
	assert.Contains(t, g.errOut.String(), "! no darwin-arm64 binary in v1; installing the darwin-amd64 one (runs under Rosetta 2)")
}

func TestInstallGitHubBinaryChecksumMismatch(t *testing.T) {
	g := newGHFixture(t)
	g.release("", "v1", false, "ado-hello-linux-amd64", "checksums.txt")
	g.asset(2, ghSum("expected")+"  ado-hello-linux-amd64\n")
	g.manifest("")
	g.asset(1, "tampered")
	_, err := g.install()
	assert.EqualError(t, err, fmt.Sprintf("checksum mismatch for ado-hello-linux-amd64: expected %s, got %s", ghSum("expected"), ghSum("tampered")))
	entries, err := os.ReadDir(g.opts.Root)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing installed, staging removed")
}

func TestInstallGitHubBinaryChecksumMissingEntry(t *testing.T) {
	g := newGHFixture(t)
	g.release("", "v1", false, "ado-hello-linux-amd64", "checksums.txt")
	g.asset(2, ghSum("x")+"  ado-hello-darwin-amd64\n")
	_, err := g.install()
	assert.EqualError(t, err, "checksums.txt has no SHA-256 for ado-hello-linux-amd64")
	assert.NoDirExists(t, g.opts.Root)
}

func TestInstallGitHubBinaryUnverified(t *testing.T) {
	g := newGHFixture(t)
	g.release("", "v1", false, "ado-hello-linux-amd64")
	_, err := g.install()
	assert.EqualError(t, err, "release v1 has no checksums.txt; use --allow-unverified to install ado-hello-linux-amd64 without verifying it")
	assert.NoDirExists(t, g.opts.Root)

	g = newGHFixture(t)
	g.opts.AllowUnverified = true
	g.release("", "v1", false, "ado-hello-linux-amd64")
	g.manifest("")
	g.asset(1, "bin")
	_, err = g.install()
	require.NoError(t, err)
	out := g.errOut.String()
	assert.Contains(t, out, "! release v1 has no checksums.txt; ado-hello-linux-amd64 is installed without verification")
	assert.Contains(t, out, "Checksum:    unverified: the release has no checksums.txt")
	rec, err := ReadRecord(g.opts.Root, "hello")
	require.NoError(t, err)
	assert.Empty(t, rec.Checksum)
}

func TestInstallGitHubBinaryNotBinary(t *testing.T) {
	g := newGHFixture(t)
	g.reg.Register(hostIs("api.github.com", ghRepoPath+"/releases/latest"), httpmock.StatusStringResponse(http.StatusNotFound, ""))
	_, err := g.install()
	assert.ErrorIs(t, err, ErrNotBinary)
	assert.ErrorContains(t, err, "o/ado-hello has no published release")

	g = newGHFixture(t)
	g.release("", "v1", false, "source.tar.gz", "checksums.txt")
	_, err = g.install()
	assert.ErrorIs(t, err, ErrNotBinary)
	assert.ErrorContains(t, err, "release v1 has no platform binaries")
}

func TestInstallGitHubBinaryMissingPlatform(t *testing.T) {
	g := newGHFixture(t)
	g.opts.GOARCH = "riscv64"
	g.release("", "v1", false, "ado-hello-linux-amd64", "ado-hello-windows-amd64.exe")
	_, err := g.install()
	assert.EqualError(t, err, "release v1 has no ado-hello binary for linux-riscv64; available: linux-amd64, windows-amd64")
	assert.NotErrorIs(t, err, ErrNotBinary)
}

func TestInstallGitHubBinaryHTTPErrors(t *testing.T) {
	tests := map[string]struct {
		resp    httpmock.Responder
		wantErr string
	}{
		"rate limit 403": {httpmock.WithHeader(httpmock.StatusStringResponse(http.StatusForbidden, ""), "X-RateLimit-Remaining", "0"),
			"GitHub release of o/ado-hello: GitHub API rate limit exceeded; set GH_TOKEN"},
		"rate limit 429": {httpmock.WithHeader(httpmock.StatusStringResponse(http.StatusTooManyRequests, ""), "X-RateLimit-Remaining", "0"),
			"rate limit exceeded; set GH_TOKEN"},
		"forbidden":    {httpmock.StatusStringResponse(http.StatusForbidden, ""), "GitHub release of o/ado-hello: HTTP 403"},
		"unauthorized": {httpmock.StatusStringResponse(http.StatusUnauthorized, ""), "check GH_TOKEN or GITHUB_TOKEN"},
		"bad json":     {httpmock.StringResponse("{"), "GitHub release of o/ado-hello: unexpected EOF"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			g := newGHFixture(t)
			g.reg.Register(hostIs("api.github.com", ghRepoPath+"/releases/latest"), tt.resp)
			_, err := g.install()
			assert.ErrorContains(t, err, tt.wantErr)
			var authErr *cmdutil.AuthError
			assert.NotErrorAs(t, err, &authErr, "a bad GH_TOKEN is not an ado login problem")
		})
	}
}

func TestInstallGitHubBinaryDownloadErrors(t *testing.T) {
	g := newGHFixture(t)
	g.release("", "v1", false, "ado-hello-linux-amd64", "checksums.txt")
	g.reg.Register(hostIs("api.github.com", ghRepoPath+"/releases/assets/2"), httpmock.StatusStringResponse(http.StatusInternalServerError, ""))
	_, err := g.install()
	assert.EqualError(t, err, "downloading checksums.txt: HTTP 500")

	g = newGHFixture(t)
	g.opts.AllowUnverified = true
	g.release("", "v1", false, "ado-hello-linux-amd64")
	g.manifest("")
	g.reg.Register(hostIs("api.github.com", ghRepoPath+"/releases/assets/1"),
		httpmock.WithHeader(httpmock.StatusStringResponse(http.StatusFound, ""), "Location", "https://release-assets.githubusercontent.com/x?sig=SECRETSIG"))
	g.reg.Register(hostIs("release-assets.githubusercontent.com", "/x"), ghConnReset)
	_, err = g.install()
	assert.EqualError(t, err, "downloading ado-hello-linux-amd64: connection reset")
	assert.NotContains(t, err.Error(), "SECRETSIG", "the signed URL never reaches the error")

	g = newGHFixture(t)
	g.release("", "v1", false, "ado-hello-linux-amd64")
	g.reg.Register(hostIs("api.github.com", ghRepoPath+"/contents/ado-extension.yml"), httpmock.StatusStringResponse(http.StatusBadGateway, ""))
	g.opts.AllowUnverified = true
	_, err = g.install()
	assert.EqualError(t, err, "reading ado-extension.yml: HTTP 502")

	g = newGHFixture(t)
	g.release("", "v1", false, "ado-hello-linux-amd64")
	g.manifest("name: other\n")
	g.opts.AllowUnverified = true
	_, err = g.install()
	assert.ErrorContains(t, err, `name "other" does not match`)
}

func TestInstallGitHubBinaryExisting(t *testing.T) {
	g := newGHFixture(t)
	old := filepath.Join(g.opts.Root, "ado-hello")
	require.NoError(t, os.MkdirAll(old, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(old, "stale"), nil, 0o644))
	_, err := g.install()
	assert.EqualError(t, err, `extension "hello" is already installed; use --force to replace it`)
	assert.Empty(t, g.reg.Requests, "refused before any request")

	g.opts.Force, g.opts.AllowUnverified = true, true
	g.release("", "v1", false, "ado-hello-linux-amd64")
	g.manifest("")
	g.asset(1, "new")
	_, err = g.install()
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(old, "stale"))
	assert.FileExists(t, filepath.Join(old, "ado-hello"))
}

func TestInstallGitHubBinaryRefusedEarly(t *testing.T) {
	g := newGHFixture(t)
	g.opts.Pin = "-x"
	_, err := g.install()
	assert.EqualError(t, err, `invalid --pin "-x"`)

	g.opts.Pin, g.opts.Name = "", "../x"
	_, err = g.install()
	assert.ErrorContains(t, err, "invalid extension name")

	g.opts.Name, g.opts.Yes = "hello", false
	_, err = g.install()
	assert.EqualError(t, err, "--yes required when not running interactively")
	assert.Empty(t, g.reg.Requests)
}

func TestGitHubClientDebugLogsErrorsWithoutURL(t *testing.T) {
	g := newGHFixture(t)
	hc, err := GitHubClient(g.f)
	require.NoError(t, err)
	g.reg.Register(hostIs("api.github.com", "/x"), ghConnReset)
	resp, err := hc.Get("https://api.github.com/x?sig=SECRETSIG")
	if err == nil {
		resp.Body.Close()
	}
	require.Error(t, err)
	assert.Equal(t, "> GET https://api.github.com/x\n< error: connection reset\n\n", g.errOut.String())
	assert.NotContains(t, g.errOut.String(), "SECRETSIG")
}
