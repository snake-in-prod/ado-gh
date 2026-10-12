package extension

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

var artOrgSeq atomic.Int64

const artZipHost = "artifacttool.blob.example"

// artFixture is a script install whose clone holds an azure-artifacts manifest, with the
// ADO APIs stubbed per test org (the SDK caches discovery per base URL) and a fake
// ArtifactTool that writes files into its --path.
type artFixture struct {
	s      *scriptFixture
	reg    *httpmock.Registry
	errOut *bytes.Buffer
	org    string
	opts   ScriptOptions
	// files are written by the fake ArtifactTool; toolErr and toolStderr make it fail.
	files      map[string]string
	toolErr    error
	toolStderr string
	cmds       []*exec.Cmd
	data       string
}

func newArtFixture(t *testing.T, sources string) *artFixture {
	t.Helper()
	s := newScriptFixture(t)
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("ADO_TOKEN", "ado-secret")
	org := fmt.Sprintf("artorg%d", artOrgSeq.Add(1))
	f, reg := sourceFactory(t, sources)
	ios, _, _, errOut := iostreams.Test()
	ios.SetStderrTTY(true)
	s.f.Config, s.f.HttpClient, s.f.IOStreams = f.Config, f.HttpClient, ios
	s.f.Prompter = &cmdutil.Prompter{IO: ios, Accessible: true}
	s.manifest = "binaries:\n  azure-artifacts:\n    organization: " + org + "\n    project: proj\n    feed: ado-live\n    package: ado-hello\n"
	a := &artFixture{s: s, reg: reg, errOut: errOut, org: org, data: data}
	a.opts = ScriptOptions{Root: s.root, Name: "hello", Source: Source{Host: hostADO, Owner: org, Project: "proj", Repo: "ado-hello"},
		URL: "https://dev.azure.com/" + org + "/proj/_git/ado-hello", Trust: Trust{Yes: true}, goos: "linux", goarch: "amd64"}
	a.opts.runTool = func(cmd *exec.Cmd) error {
		a.cmds = append(a.cmds, cmd)
		dir := cmd.Args[slices.Index(cmd.Args, "--path")+1]
		for name, body := range a.files {
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
		}
		if a.toolErr != nil {
			_, _ = cmd.Stderr.Write([]byte(a.toolStderr))
		}
		return a.toolErr
	}
	reg.StubLocations(org, httpmock.ResourceAreasLocation)
	return a
}

func (a *artFixture) install() (Extension, error) {
	return InstallScript(context.Background(), a.s.f, a.opts)
}

// areas registers one ResourceAreas lookup listing clienttools and the feed area.
func (a *artFixture) areas() { a.reg.StubResourceAreas(a.org, httpmock.ArtifactsAreas(a.org)...) }

// release registers the ArtifactTool release lookup for arch; empty rid answers null.
func (a *artFixture) release(t *testing.T, osName, arch, rid string) {
	a.areas()
	a.reg.Register(func(r *http.Request) bool {
		q := r.URL.Query()
		return r.URL.Host == a.org+".vsblob.visualstudio.com" && r.URL.Path == "/_apis/clienttools/ArtifactTool/release" &&
			q.Get("osName") == osName && q.Get("arch") == arch
	}, func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "Basic "+basicSecret, r.Header.Get("Authorization"), "the ADO token goes to the vsblob host")
		if rid == "" {
			return httpmock.StringResponse("null")(r)
		}
		return httpmock.JSONResponse(map[string]string{"name": "ArtifactTool", "rid": rid, "version": "0.2.1",
			"uri": "https://" + artZipHost + "/at.zip?sig=SECRETSIG"})(r)
	})
}

var basicSecret = "OmFkby1zZWNyZXQ=" // base64(":ado-secret")

// toolZip registers the ArtifactTool zip download holding files.
func (a *artFixture) toolZip(t *testing.T, files ...string) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = w.Write([]byte("tool " + name))
	}
	require.NoError(t, zw.Close())
	a.reg.Register(hostIs(artZipHost, "/at.zip"), func(r *http.Request) (*http.Response, error) {
		assert.Empty(t, r.Header.Get("Authorization"), "the zip is downloaded without the ADO token")
		return httpmock.StringResponse(buf.String())(r)
	})
}

// packages registers the feed packages query answering versions of ado-hello.
func (a *artFixture) packages(project string, versions ...string) {
	a.areas()
	a.reg.StubFeedLocations(a.org)
	vs := []map[string]any{}
	for _, v := range versions {
		vs = append(vs, map[string]any{"version": v})
	}
	p := "/" + a.org + "/_apis/packaging/Feeds/ado-live/packages"
	if project != "" {
		p = "/" + a.org + "/" + project + "/_apis/packaging/Feeds/ado-live/packages"
	}
	a.reg.Register(hostIs("feeds.dev.azure.com", p), httpmock.JSONResponse(map[string]any{"count": 1,
		"value": []map[string]any{{"name": "ado-hello", "versions": vs}}}))
}

// linuxPackage sets the package contents: linux and windows binaries plus checksums.txt.
func (a *artFixture) linuxPackage() {
	a.files = map[string]string{
		"ado-hello-linux-amd64":       "LINUX",
		"ado-hello-windows-amd64.exe": "WINDOWS",
		"checksums.txt":               ghSum("LINUX") + "  ado-hello-linux-amd64\n" + ghSum("WINDOWS") + "  ado-hello-windows-amd64.exe\n",
	}
}

func TestInstallArtifactsLatest(t *testing.T) {
	a := newArtFixture(t, "")
	a.s.manifest += "token: true\ndescription: Hello\n"
	a.opts.AllowToken = true
	a.release(t, "Linux", "amd64", "linux-x64")
	a.toolZip(t, "artifacttool", "lib/runtime.dll")
	a.packages("proj", "0.2.0", "0.10.0", "1.0.0-rc.1")
	a.linuxPackage()

	ext, err := a.install()
	require.NoError(t, err)
	a.reg.Verify(t)
	entry := filepath.Join(a.s.root, "ado-hello")
	assert.Equal(t, Extension{Name: "hello", Path: entry, Dir: entry, Kind: KindBinary}, ext)
	b, err := os.ReadFile(filepath.Join(entry, "ado-hello"))
	require.NoError(t, err)
	assert.Equal(t, "LINUX", string(b))
	assert.Equal(t, []string{".records", "ado-hello"}, a.s.rootEntries(t), "clone, download and staging dirs removed")

	tool := filepath.Join(a.data, "ado", "artifacttool", "linux-x64_0.2.1", "artifacttool")
	assert.FileExists(t, filepath.Join(filepath.Dir(tool), "lib", "runtime.dll"))
	if runtime.GOOS != "windows" {
		for _, p := range []string{tool, filepath.Join(entry, "ado-hello")} {
			fi, err := os.Stat(p)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm(), p)
		}
	}

	require.Len(t, a.cmds, 1)
	cmd := a.cmds[0]
	assert.Equal(t, tool, cmd.Path)
	dl := cmd.Args[slices.Index(cmd.Args, "--path")+1]
	assert.Equal(t, a.s.root, filepath.Dir(dl), "downloaded inside the extensions root")
	assert.Equal(t, []string{tool, "universal", "download", "--service", "https://dev.azure.com/" + a.org + "/",
		"--patvar", "ADO_ARTIFACTTOOL_PAT", "--feed", "ado-live", "--package-name", "ado-hello",
		"--package-version", "0.10.0", "--path", dl, "--project", "proj"}, cmd.Args)
	assert.Equal(t, "ADO_ARTIFACTTOOL_PAT=ado-secret", cmd.Env[len(cmd.Env)-1], "token only in the --patvar env")
	assert.NotContains(t, strings.Join(cmd.Args, " "), "ado-secret")

	rec, err := ReadRecord(a.s.root, "hello")
	require.NoError(t, err)
	assert.Equal(t, &Record{Source: a.opts.URL, Host: hostADO, Kind: KindBinary, Commit: scriptSHA, Version: "0.10.0",
		TokenConsent: true, Checksum: "sha256:" + ghSum("LINUX")}, rec)
	out := a.errOut.String()
	assert.Contains(t, out, "Ref:         ado-hello 0.10.0 (ado-hello-linux-amd64) from feed dev.azure.com/"+a.org+"/proj/ado-live\n")
	assert.Contains(t, out, "Kind:        binary\n")
	assert.Contains(t, out, "Checksum:    SHA-256 from checksums.txt\n")
	assert.NotContains(t, out, "SECRETSIG")

	// Reinstall: the cached ArtifactTool is reused (no zip download registered).
	a.cmds = nil
	a.release(t, "Linux", "amd64", "linux-x64")
	a.packages("proj", "0.10.0")
	a.opts.Force = true
	_, err = a.install()
	require.NoError(t, err)
	a.reg.Verify(t)
	require.Len(t, a.cmds, 1)
	assert.Equal(t, tool, a.cmds[0].Path)
}

func TestInstallArtifactsPinned(t *testing.T) {
	a := newArtFixture(t, "")
	a.s.manifest = strings.Replace(a.s.manifest, "    project: proj\n", "", 1) // org-scoped feed
	a.s.r.Replies["rev-parse --verify --quiet v0.2.0^{commit}"] = scriptSHA + "\n"
	a.s.r.Replies["rev-parse --verify --quiet refs/tags/v0.2.0"] = scriptSHA + "\n"
	a.opts.Pin = "v0.2.0"
	a.release(t, "Linux", "amd64", "linux-x64")
	a.toolZip(t, "artifacttool")
	a.linuxPackage()

	_, err := a.install()
	require.NoError(t, err)
	a.reg.Verify(t) // no packages query: the tag names the version
	require.Len(t, a.cmds, 1)
	args := a.cmds[0].Args
	assert.Equal(t, "0.2.0", args[slices.Index(args, "--package-version")+1])
	assert.NotContains(t, args, "--project")
	rec, err := ReadRecord(a.s.root, "hello")
	require.NoError(t, err)
	assert.Equal(t, &Record{Source: a.opts.URL, Host: hostADO, Kind: KindBinary, Commit: scriptSHA, Tag: "v0.2.0",
		Version: "0.2.0", Pinned: true, Checksum: "sha256:" + ghSum("LINUX")}, rec)
}

func TestInstallArtifactsPinErrors(t *testing.T) {
	tests := map[string]struct {
		pin     string
		isTag   bool
		wantErr string
	}{
		"commit":  {"2222222", false, `--pin "2222222" is not a tag: an Azure Artifacts extension is pinned to a tag naming its package version, such as v1.2.3`},
		"bad tag": {"release-1", true, `tag "release-1" does not name a package version such as v1.2.3`},
		"build":   {"v1.2.3+build", true, `tag "v1.2.3+build" does not name a package version such as v1.2.3`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			a := newArtFixture(t, "")
			a.s.r.Replies["rev-parse --verify --quiet "+tt.pin+"^{commit}"] = scriptSHA + "\n"
			if tt.isTag {
				a.s.r.Replies["rev-parse --verify --quiet refs/tags/"+tt.pin] = scriptSHA + "\n"
			}
			a.opts.Pin = tt.pin
			_, err := a.install()
			assert.EqualError(t, err, tt.wantErr)
			assert.Empty(t, a.reg.Requests)
		})
	}
}

func TestInstallArtifactsArm64Fallback(t *testing.T) {
	tests := map[string]struct {
		goos, osName, exe, binary string
	}{
		"darwin":  {"darwin", "Darwin", "artifacttool", "ado-hello-darwin-arm64"},
		"windows": {"windows", "Windows", "ArtifactTool.exe", "ado-hello-windows-arm64.exe"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			a := newArtFixture(t, "")
			a.opts.goos, a.opts.goarch, a.opts.AllowUnverified = tt.goos, "arm64", true
			a.release(t, tt.osName, "arm64", "")
			a.release(t, tt.osName, "amd64", tt.goos+"-x64")
			a.toolZip(t, tt.exe)
			a.packages("proj", "1.0.0")
			a.files = map[string]string{tt.binary: "BIN"}
			ext, err := a.install()
			require.NoError(t, err)
			a.reg.Verify(t)
			assert.Contains(t, a.errOut.String(), "! no "+tt.goos+"-arm64 ArtifactTool; using the amd64 build, which needs x64 emulation\n")
			assert.Equal(t, filepath.Join(a.data, "ado", "artifacttool", tt.goos+"-x64_0.2.1", tt.exe), a.cmds[0].Path)
			assert.FileExists(t, filepath.Join(ext.Dir, "ado-hello"+filepath.Ext(tt.binary)))
		})
	}

	a := newArtFixture(t, "")
	a.opts.goarch = "arm64"
	a.release(t, "Linux", "arm64", "")
	_, err := a.install()
	assert.EqualError(t, err, "no ArtifactTool for linux-arm64, needed for Azure Artifacts extensions")
}

func TestInstallArtifactsFeedOrg(t *testing.T) {
	tests := map[string]struct {
		sources string
		wantErr string
	}{
		"no allowlist":      {sources: ""},
		"feed org allowed":  {sources: "dev.azure.com/src/**,dev.azure.com/other/**"},
		"feed org refused":  {sources: "dev.azure.com/src/**", wantErr: `the azure-artifacts feed in ado-extension.yml: extension source "dev.azure.com/other/proj/ado-live" is not allowed by extension_sources (dev.azure.com/src/**)`},
		"github source too": {sources: "github.com/o/*", wantErr: "is not allowed by extension_sources"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			a := newArtFixture(t, tt.sources)
			a.opts.Source.Owner = "src"
			if name == "github source too" {
				a.opts.Source, a.opts.URL = ghSource, scriptGitHubURL
			}
			// The feed org is the fixture's own (named in the manifest); swap in "other".
			a.s.manifest = strings.Replace(a.s.manifest, "organization: "+a.org, "organization: other", 1)
			if tt.wantErr != "" {
				_, err := a.install()
				assert.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, a.reg.Requests, "refused before any request")
				return
			}
			a.reg.Register(httpmock.MatchAny, httpmock.StatusStringResponse(http.StatusTeapot, ""))
			_, err := a.install()
			require.Error(t, err)
			assert.Equal(t, "https://dev.azure.com/other/_apis", a.reg.Requests[0].URL.String(), "allowed: the feed org is contacted")
		})
	}
}

func TestInstallArtifactsChecksums(t *testing.T) {
	tests := map[string]struct {
		files      map[string]string
		unverified bool
		wantErr    string
		wantOut    string
	}{
		"mismatch": {files: map[string]string{"ado-hello-linux-amd64": "EVIL", "checksums.txt": ghSum("LINUX") + "  ado-hello-linux-amd64\n"},
			wantErr: "checksum mismatch for ado-hello-linux-amd64: expected " + ghSum("LINUX") + ", got " + ghSum("EVIL")},
		"no entry": {files: map[string]string{"ado-hello-linux-amd64": "LINUX", "checksums.txt": ghSum("X") + "  other\n"},
			wantErr: "checksums.txt has no SHA-256 for ado-hello-linux-amd64"},
		"missing": {files: map[string]string{"ado-hello-linux-amd64": "LINUX"},
			wantErr: "package ado-hello 1.0.0 has no checksums.txt; use --allow-unverified to install ado-hello-linux-amd64 without verifying it"},
		"unverified": {files: map[string]string{"ado-hello-linux-amd64": "LINUX"}, unverified: true,
			wantOut: "! package ado-hello 1.0.0 has no checksums.txt; ado-hello-linux-amd64 is installed without verification\n"},
		"goreleaser name": {files: map[string]string{"ado-hello-linux-amd64": "LINUX", "x_1.0.0_checksums.txt": ghSum("LINUX") + " *ado-hello-linux-amd64\n"},
			wantOut: "Checksum:    SHA-256 from x_1.0.0_checksums.txt\n"},
		"no platform binary": {files: map[string]string{"ado-hello-darwin-amd64": "MAC"},
			wantErr: "release 1.0.0 has no ado-hello binary for linux-amd64; available: darwin-amd64"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			a := newArtFixture(t, "")
			a.opts.AllowUnverified = tt.unverified
			a.release(t, "Linux", "amd64", "linux-x64")
			a.toolZip(t, "artifacttool")
			a.packages("proj", "1.0.0")
			a.files = tt.files
			_, err := a.install()
			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
				assert.Equal(t, []string{}, filterVisible(a.s.rootEntries(t)), "nothing installed")
				return
			}
			require.NoError(t, err)
			assert.Contains(t, a.errOut.String(), tt.wantOut)
		})
	}
}

func TestInstallArtifactsOutputConfined(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	a := newArtFixture(t, "")
	a.opts.AllowUnverified = true
	a.release(t, "Linux", "amd64", "linux-x64")
	a.toolZip(t, "artifacttool")
	a.packages("proj", "1.0.0")
	outside := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(outside, []byte("SECRET"), 0o644))
	run := a.opts.runTool
	a.opts.runTool = func(cmd *exec.Cmd) error {
		dir := cmd.Args[slices.Index(cmd.Args, "--path")+1]
		require.NoError(t, os.Symlink(outside, filepath.Join(dir, "ado-hello-linux-amd64")))
		require.NoError(t, os.Symlink(outside, filepath.Join(dir, "checksums.txt")))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "ado-hello-linux-amd64"), []byte("NESTED"), 0o755))
		return run(cmd)
	}
	_, err := a.install()
	assert.EqualError(t, err, "release 1.0.0 has no ado-hello binary for linux-amd64; available: none")

	root, err := os.OpenRoot(filepath.Dir(outside))
	require.NoError(t, err)
	defer root.Close()
	require.NoError(t, os.Symlink(outside, filepath.Join(filepath.Dir(outside), "link")))
	_, err = openPackageFile(root, "link")
	assert.EqualError(t, err, "link in the package is not a regular file")
	require.NoError(t, os.Mkdir(filepath.Join(filepath.Dir(outside), "dir"), 0o755))
	_, err = openPackageFile(root, "dir")
	assert.EqualError(t, err, "dir in the package is not a regular file")
	_, err = readPackageFile(root, "missing")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestInstallArtifactsToolFails(t *testing.T) {
	a := newArtFixture(t, "")
	a.release(t, "Linux", "amd64", "linux-x64")
	a.toolZip(t, "artifacttool")
	a.packages("proj", "1.0.0")
	a.toolErr = errors.New("exit status 1")
	a.toolStderr = `{"@t":"2026-10-12T10:00:00Z","@l":"Information","@m":"Downloading"}` + "\n" +
		`{"@l":"Error","@m":"Can't find the package 'ado-hello' with version '1.0.0'\n\u001b[31m"}` + "\n" +
		"not json\n" +
		`{"@l":"Fatal","@mt":"Unhandled {Error}"}` + "\n" +
		`{"@l":"Critical","@x":"System.Exception: boom\n   at X.Y()"}` + "\n" +
		`{"@l":"Warning","@m":"slow"}` + "\n"
	_, err := a.install()
	assert.EqualError(t, err, "ArtifactTool could not download ado-hello 1.0.0 from feed ado-live (exit status 1): "+
		"Can't find the package 'ado-hello' with version '1.0.0'; Unhandled {Error}; System.Exception: boom")
	assert.Equal(t, []string{}, filterVisible(a.s.rootEntries(t)))

	a = newArtFixture(t, "")
	a.release(t, "Linux", "amd64", "linux-x64")
	a.toolZip(t, "artifacttool")
	a.packages("proj", "1.0.0")
	a.toolErr = errors.New("exec: not started")
	_, err = a.install()
	assert.EqualError(t, err, "ArtifactTool could not download ado-hello 1.0.0 from feed ado-live (exec: not started)")
}

func TestInstallArtifactsToolDownloadErrors(t *testing.T) {
	connReset := func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") }
	tests := map[string]struct {
		setup   func(t *testing.T, a *artFixture)
		wantErr string
	}{
		"http status": {setup: func(t *testing.T, a *artFixture) {
			a.release(t, "Linux", "amd64", "linux-x64")
			a.reg.Register(hostIs(artZipHost, "/at.zip"), httpmock.StatusStringResponse(http.StatusForbidden, ""))
		}, wantErr: "downloading ArtifactTool: HTTP 403"},
		"connection": {setup: func(t *testing.T, a *artFixture) {
			a.release(t, "Linux", "amd64", "linux-x64")
			a.reg.Register(hostIs(artZipHost, "/at.zip"), connReset)
		}, wantErr: "downloading ArtifactTool: connection reset"},
		"not a zip": {setup: func(t *testing.T, a *artFixture) {
			a.release(t, "Linux", "amd64", "linux-x64")
			a.reg.Register(hostIs(artZipHost, "/at.zip"), httpmock.StringResponse("nope"))
		}, wantErr: "downloading ArtifactTool: zip: not a valid zip file"},
		"no executable": {setup: func(t *testing.T, a *artFixture) {
			a.release(t, "Linux", "amd64", "linux-x64")
			a.toolZip(t, "README")
		}, wantErr: "the ArtifactTool archive has no artifacttool"},
		"zip slip": {setup: func(t *testing.T, a *artFixture) {
			a.release(t, "Linux", "amd64", "linux-x64")
			a.toolZip(t, "../evil")
		}, wantErr: `downloading ArtifactTool: unsafe path in archive: "../evil"`},
		"http uri": {setup: func(_ *testing.T, a *artFixture) {
			a.areas()
			a.reg.Register(httpmock.REST(http.MethodGet, "_apis/clienttools/ArtifactTool/release"),
				httpmock.JSONResponse(map[string]string{"rid": "linux-x64", "version": "1", "uri": "http://x/at.zip"}))
		}, wantErr: "downloading ArtifactTool: the release has no https download URL"},
		"server": {setup: func(_ *testing.T, a *artFixture) {
			a.reg.StubResourceAreas(a.org)
		}, wantErr: api.ErrUniversalPackagesUnavailable.Error()},
		"unsupported os": {setup: func(_ *testing.T, a *artFixture) { a.opts.goos = "freebsd" },
			wantErr: "ArtifactTool, needed for Azure Artifacts extensions, is not available for freebsd"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			a := newArtFixture(t, "")
			tt.setup(t, a)
			a.packages("proj", "0.1.0") // the version is resolved before ArtifactTool is downloaded
			_, err := a.install()
			assert.EqualError(t, err, tt.wantErr)
			assert.NotContains(t, err.Error(), "SECRETSIG")
			entries, rerr := os.ReadDir(filepath.Join(a.data, "ado", "artifacttool"))
			if rerr == nil {
				assert.Empty(t, entries, "temp files removed, nothing cached")
			}
		})
	}
}

func TestInstallArtifactsNotLoggedIn(t *testing.T) {
	a := newArtFixture(t, "")
	clearTokenEnv(t)
	_, err := a.install()
	var authErr *cmdutil.AuthError
	assert.ErrorAs(t, err, &authErr)
	assert.Empty(t, a.reg.Requests)
}

func TestInstallArtifactsNoLatest(t *testing.T) {
	a := newArtFixture(t, "")
	a.release(t, "Linux", "amd64", "linux-x64")
	a.toolZip(t, "artifacttool")
	a.packages("proj", "1.0.0-beta")
	_, err := a.install()
	assert.EqualError(t, err, "feed ado-live has no release version of package ado-hello")
	assert.Empty(t, a.cmds)
}

func TestInstallArtifactsDeclined(t *testing.T) {
	a := newArtFixture(t, "")
	a.opts.Yes = false
	cf, _, _ := confirmEnv(true, "n\n")
	a.s.f.IOStreams, a.s.f.Prompter = cf.IOStreams, cf.Prompter
	a.release(t, "Linux", "amd64", "linux-x64")
	a.toolZip(t, "artifacttool")
	a.packages("proj", "1.0.0")
	a.linuxPackage()
	_, err := a.install()
	assert.ErrorIs(t, err, cmdutil.CancelError)
	assert.Equal(t, []string{}, filterVisible(a.s.rootEntries(t)))
}

// filterVisible drops dot entries (temp dirs, .records) from a root listing.
func filterVisible(names []string) []string {
	out := []string{}
	for _, n := range names {
		if !strings.HasPrefix(n, ".") {
			out = append(out, n)
		}
	}
	return out
}
