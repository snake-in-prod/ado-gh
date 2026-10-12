package download

import (
	"archive/zip"
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const artifactsPath = "/My%20Proj/_apis/build/builds/812/artifacts"

type plDownloadEnv struct {
	org         string
	reg         *httpmock.Registry
	ios         *iostreams.IOStreams
	in          *bytes.Buffer
	out, errOut *bytes.Buffer
	tmp         string
}

// newPLDownloadEnv returns an env for its own org (the SDK caches discovery per base URL), with
// TMPDIR pointing at an empty directory so temp-file cleanup can be checked.
func newPLDownloadEnv(t *testing.T, tty bool) *plDownloadEnv {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	t.Setenv("ADO_TOKEN", "pat")
	t.Setenv("AZURE_DEVOPS_EXT_PAT", "")
	t.Setenv("ADO_REPO", "")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	keyring.MockInit()
	ios, in, out, errOut := iostreams.Test()
	ios.SetStdinTTY(tty)
	ios.SetStdoutTTY(tty)
	ios.In = iotest.OneByteReader(in)
	org := "pldownload-" + strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	e := &plDownloadEnv{org: org, reg: &httpmock.Registry{}, ios: ios, in: in, out: out, errOut: errOut, tmp: tmp}
	e.reg.StubLocations(org, httpmock.PipelineLocations...)
	return e
}

func (e *plDownloadEnv) run(t *testing.T, args ...string) error {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	f := &cmdutil.Factory{
		IOStreams:  e.ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: e.reg}, nil },
		BaseRepo: func() (adocontext.Repo, error) {
			return adocontext.Repo{Host: "dev.azure.com", Org: e.org, Project: "My Proj", Name: "web"}, nil
		},
		Prompter: &cmdutil.Prompter{IO: e.ios, Accessible: true},
	}
	cmd := NewCmdDownload(f)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.Execute()
}

// containerURL is the downloadUrl of a Container artifact, served by stubContainer.
func (e *plDownloadEnv) containerURL(name string) string {
	return "https://dev.azure.com/" + e.org + artifactsPath + "?artifactName=" + name + "&api-version=7.1&%24format=zip"
}

// pipelineURL is the signed-looking downloadUrl of a PipelineArtifact, served by stubPipeline.
func pipelineURL(name string) string {
	return "https://artprodcus3.artifacts.visualstudio.com/A1/proj/_apis/artifact/" + name + "/content?format=zip&sig=SECRETSIG"
}

// stubList answers the artifact list of run 812 with arts.
func (e *plDownloadEnv) stubList(arts ...map[string]any) {
	e.reg.Register(func(r *http.Request) bool {
		return r.Method == http.MethodGet && r.URL.EscapedPath() == "/"+e.org+artifactsPath && r.URL.Query().Get("$format") == ""
	}, httpmock.JSONResponse(map[string]any{"count": len(arts), "value": arts}))
}

// stubContainer serves the zip of Container artifact name.
func (e *plDownloadEnv) stubContainer(name string, body []byte) {
	e.reg.Register(func(r *http.Request) bool {
		q := r.URL.Query()
		return r.URL.EscapedPath() == "/"+e.org+artifactsPath && q.Get("$format") == "zip" && q.Get("artifactName") == name
	}, httpmock.StringResponse(string(body)))
}

// stubPipeline serves PipelineArtifact name with responder.
func (e *plDownloadEnv) stubPipeline(name string, responder httpmock.Responder) {
	e.reg.Register(httpmock.REST(http.MethodGet, "A1/proj/_apis/artifact/"+name+"/content"), responder)
}

func artifact(id int, name, typ, data, url string) map[string]any {
	res := map[string]any{"data": data, "downloadUrl": url}
	if typ != "" {
		res["type"] = typ
	}
	return map[string]any{"id": id, "name": name, "resource": res}
}

// zipOf builds an archive of name → body pairs; a name ending in "/" is a directory.
func zipOf(t *testing.T, kv ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := 0; i < len(kv); i += 2 {
		fw, err := w.Create(kv[i])
		require.NoError(t, err)
		_, err = fw.Write([]byte(kv[i+1]))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

// tree returns the regular files under dir as slash path → content.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	got := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		got[filepath.ToSlash(rel)] = string(b)
		return err
	}))
	return got
}

// assertNoSecrets checks that no signed URL leaked into the output and the temp files are gone.
func (e *plDownloadEnv) assertNoSecrets(t *testing.T, err error) {
	t.Helper()
	all := e.out.String() + e.errOut.String()
	if err != nil {
		all += err.Error()
	}
	assert.NotContains(t, all, "SECRETSIG")
	assert.NotContains(t, all, "format=zip")
	left, _ := os.ReadDir(e.tmp)
	assert.Empty(t, left, "temp zips removed")
	for _, r := range e.reg.Requests {
		assert.NotContains(t, strings.ToLower(r.URL.RawQuery), "signedcontent", "never $expand=signedContent")
	}
}

func TestPLDownloadSingleNameIntoDir(t *testing.T) {
	e := newPLDownloadEnv(t, false)
	e.stubList(
		artifact(1, "drop", "PipelineArtifact", "AA01", pipelineURL("drop")),
		artifact(2, "other", "Container", "#/1/other", e.containerURL("other")),
	)
	var auth string
	e.stubPipeline("drop", func(r *http.Request) (*http.Response, error) {
		auth = r.Header.Get("Authorization")
		return httpmock.StringResponse(string(zipOf(t, "drop/", "", "drop/hello.txt", "hi")))(r)
	})
	dir := filepath.Join(t.TempDir(), "out")

	err := e.run(t, "812", "-n", "drop", "-D", dir)
	require.NoError(t, err)
	e.reg.Verify(t)
	assert.Equal(t, map[string]string{"hello.txt": "hi"}, tree(t, dir), "single -n goes straight into <dir>, top folder stripped")
	assert.Equal(t, "Basic OnBhdA==", auth, "fetched with the authenticated client")
	assert.Contains(t, e.errOut.String(), "Downloaded drop")
	e.assertNoSecrets(t, err)
}

func TestPLDownloadAllIntoSubdirs(t *testing.T) {
	e := newPLDownloadEnv(t, false)
	e.stubList(
		artifact(1, "drop", "container", "#/1/drop", e.containerURL("drop")),
		artifact(2, "webapp", "PIPELINEARTIFACT", "AA01", pipelineURL("webapp")),
		artifact(3, "share", "FilePath", `\\fileserver\drops\101`, "file://fileserver/drops/101/share"),
		artifact(0, "legacy", "", `\\old\drops`, ""),
		artifact(5, "sym", "SymbolStore", "x", "https://example.com/x"),
	)
	e.stubContainer("drop", zipOf(t, "drop/a.txt", "a"))
	e.stubPipeline("webapp", httpmock.StringResponse(string(zipOf(t, "index.html", "<p>"))))
	dir := t.TempDir()

	err := e.run(t, "812", "--dir", dir)
	require.NoError(t, err)
	e.reg.Verify(t)
	assert.Equal(t, map[string]string{"drop/a.txt": "a", "webapp/index.html": "<p>"}, tree(t, dir))
	errOut := e.errOut.String()
	assert.Contains(t, errOut, `skipping share: file share artifact; copy it from \\fileserver\drops\101`)
	assert.Contains(t, errOut, `skipping legacy: file share artifact; copy it from \\old\drops`)
	assert.Contains(t, errOut, "skipping sym: unsupported artifact type SymbolStore")
	e.assertNoSecrets(t, err)
}

func TestPLDownloadSelection(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]string
	}{
		{"several names", []string{"-n", "drop", "-n", "logs"}, map[string]string{"drop/d": "d", "logs/l": "l"}},
		{"pattern", []string{"-p", "lo*"}, map[string]string{"logs/l": "l"}},
		{"name and pattern", []string{"-n", "drop", "-p", "l?gs"}, map[string]string{"drop/d": "d", "logs/l": "l"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPLDownloadEnv(t, false)
			e.stubList(
				artifact(1, "drop", "Container", "#/1/drop", e.containerURL("drop")),
				artifact(2, "logs", "Container", "#/1/logs", e.containerURL("logs")),
				artifact(3, "webapp", "Container", "#/1/webapp", e.containerURL("webapp")),
			)
			if _, ok := tc.want["drop/d"]; ok {
				e.stubContainer("drop", zipOf(t, "drop/d", "d"))
			}
			e.stubContainer("logs", zipOf(t, "logs/l", "l"))
			dir := t.TempDir()

			require.NoError(t, e.run(t, append([]string{"812", "-D", dir}, tc.args...)...))
			e.reg.Verify(t)
			assert.Equal(t, tc.want, tree(t, dir))
		})
	}
}

// TestPLDownloadCaseOnlyDuplicates: "Drop" and "drop" would share a directory on Windows and
// macOS, where extracting the second removes the first; that's an error before any download.
func TestPLDownloadCaseOnlyDuplicates(t *testing.T) {
	e := newPLDownloadEnv(t, false)
	e.stubList(
		artifact(1, "Drop", "Container", "#/1/Drop", e.containerURL("Drop")),
		artifact(2, "drop", "Container", "#/1/drop", e.containerURL("drop")),
	)
	dir := t.TempDir()
	err := e.run(t, "812", "-D", dir)
	require.EqualError(t, err, "artifacts Drop and drop would extract to the same directory on case-insensitive file systems; pick one with --name")
	e.reg.Verify(t)
	assert.Empty(t, tree(t, dir), "nothing downloaded")
}

// TestSelectsBadPattern: a malformed pattern is an error, never a silent mismatch.
func TestSelectsBadPattern(t *testing.T) {
	o := &options{patterns: []string{"ok*", "[bad"}}
	ok, err := o.selects("okay")
	require.NoError(t, err)
	assert.True(t, ok, "a match before the bad pattern")
	_, err = o.selects("other")
	var fe *cmdutil.FlagError
	require.ErrorAs(t, err, &fe)
	assert.EqualError(t, err, `invalid pattern "[bad": syntax error in pattern`)
}

func TestPLDownloadNoMatch(t *testing.T) {
	e := newPLDownloadEnv(t, false)
	e.stubList(artifact(1, "drop", "Container", "#/1/drop", e.containerURL("drop")))
	err := e.run(t, "812", "-n", "nope", "-D", t.TempDir())
	assert.EqualError(t, err, "no artifact of run 812 matches the given names or patterns")

	e = newPLDownloadEnv(t, false)
	e.stubList()
	err = e.run(t, "812", "-D", t.TempDir())
	assert.EqualError(t, err, "run 812 has no artifacts")

	e = newPLDownloadEnv(t, false)
	err = e.run(t, "812", "-p", "[", "-D", t.TempDir())
	assert.ErrorContains(t, err, "invalid pattern")
}

func TestPLDownloadNothingDownloadable(t *testing.T) {
	e := newPLDownloadEnv(t, false)
	e.stubList(artifact(3, "share", "filepath", `\\fs\d`, "file://fs/d"))
	err := e.run(t, "812", "-n", "share", "-D", t.TempDir())
	assert.EqualError(t, err, "no artifacts downloaded")
	assert.Contains(t, e.errOut.String(), `skipping share: file share artifact; copy it from \\fs\d`)
}

func TestPLDownloadExistingArtifactDir(t *testing.T) {
	e := newPLDownloadEnv(t, false)
	e.stubList(artifact(1, "drop", "Container", "#/1/drop", e.containerURL("drop")))
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "drop"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "drop", "stale.txt"), []byte("old"), 0o644))

	err := e.run(t, "812", "-D", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), filepath.Join(dir, "drop")+" already exists; use --clobber")
	for _, r := range e.reg.Requests {
		assert.Empty(t, r.URL.Query().Get("$format"), "nothing downloaded")
	}

	e = newPLDownloadEnv(t, false)
	e.stubList(artifact(1, "drop", "Container", "#/1/drop", e.containerURL("drop")))
	e.stubContainer("drop", zipOf(t, "drop/new.txt", "n"))
	require.NoError(t, e.run(t, "812", "-D", dir, "--clobber"))
	assert.Equal(t, map[string]string{"drop/new.txt": "n"}, tree(t, dir), "removed and re-extracted")
}

func TestPLDownloadSingleNameExistingFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("old"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o644))

	e := newPLDownloadEnv(t, false)
	e.stubList(artifact(1, "drop", "Container", "#/1/drop", e.containerURL("drop")))
	e.stubContainer("drop", zipOf(t, "drop/a.txt", "new"))
	err := e.run(t, "812", "-n", "drop", "-D", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a.txt already exists; use --clobber")
	assert.Equal(t, map[string]string{"a.txt": "old", "keep.txt": "keep"}, tree(t, dir))
	e.assertNoSecrets(t, err)

	e = newPLDownloadEnv(t, false)
	e.stubList(artifact(1, "drop", "Container", "#/1/drop", e.containerURL("drop")))
	e.stubContainer("drop", zipOf(t, "drop/a.txt", "new"))
	require.NoError(t, e.run(t, "812", "-n", "drop", "-D", dir, "--clobber"))
	assert.Equal(t, map[string]string{"a.txt": "new", "keep.txt": "keep"}, tree(t, dir), "<dir> is never removed")
}

func TestPLDownloadUnsafe(t *testing.T) {
	e := newPLDownloadEnv(t, false)
	e.stubList(artifact(1, "../evil", "Container", "#/1/x", e.containerURL("x")))
	parent := t.TempDir()
	dir := filepath.Join(parent, "out")
	err := e.run(t, "812", "-D", dir)
	assert.ErrorContains(t, err, "not a safe directory name")
	assert.NoDirExists(t, filepath.Join(parent, "evil"))

	e = newPLDownloadEnv(t, false)
	e.stubList(artifact(1, "drop", "Container", "#/1/drop", e.containerURL("drop")))
	e.stubContainer("drop", zipOf(t, "../x", "evil"))
	err = e.run(t, "812", "-D", dir)
	assert.ErrorContains(t, err, `artifact drop: unsafe path in archive: "../x"`)
	assert.NoFileExists(t, filepath.Join(dir, "x"))
	assert.NoFileExists(t, filepath.Join(parent, "x"))
	e.assertNoSecrets(t, err)
}

func TestPLDownloadErrors(t *testing.T) {
	e := newPLDownloadEnv(t, false)
	e.stubList(artifact(1, "drop", "PipelineArtifact", "AA01", pipelineURL("drop")))
	e.stubPipeline("drop", httpmock.StatusStringResponse(http.StatusForbidden, "AuthenticationFailed "+pipelineURL("drop")))
	err := e.run(t, "812", "-D", t.TempDir())
	assert.EqualError(t, err, "downloading drop: HTTP 403")
	e.assertNoSecrets(t, err)

	e = newPLDownloadEnv(t, false)
	e.stubList(artifact(1, "drop", "PipelineArtifact", "AA01", pipelineURL("drop")))
	e.stubPipeline("drop", httpmock.StringResponse("not a zip"))
	err = e.run(t, "812", "-D", t.TempDir())
	assert.ErrorContains(t, err, "artifact drop: zip:")
	e.assertNoSecrets(t, err)

	e = newPLDownloadEnv(t, false)
	e.stubList(artifact(1, "drop", "Container", "#/1/drop", ""))
	err = e.run(t, "812", "-D", t.TempDir())
	assert.EqualError(t, err, "artifact drop has no download URL")

	e = newPLDownloadEnv(t, false)
	err = e.run(t, "812", "-D", t.TempDir())
	assert.ErrorContains(t, err, "no registered HTTP stubs matched GET", "the artifact list request fails")
	assert.ErrorContains(t, err, "/_apis/build/builds/812/artifacts")
}

func TestPLDownloadSymlinkEntryWarns(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: "drop/link"}
	h.SetMode(fs.ModeSymlink | 0o777)
	fw, err := w.CreateHeader(h)
	require.NoError(t, err)
	_, _ = fw.Write([]byte("/etc/passwd"))
	fw, err = w.Create("drop/a.txt")
	require.NoError(t, err)
	_, _ = fw.Write([]byte("a"))
	require.NoError(t, w.Close())

	e := newPLDownloadEnv(t, false)
	e.stubList(artifact(1, "drop", "Container", "#/1/drop", e.containerURL("drop")))
	e.stubContainer("drop", buf.Bytes())
	dir := t.TempDir()
	require.NoError(t, e.run(t, "812", "-n", "drop", "-D", dir))
	assert.Equal(t, map[string]string{"a.txt": "a"}, tree(t, dir))
	assert.Contains(t, e.errOut.String(), `warning: skipped "drop/link" in drop: not a regular file`)
}

func TestPLDownloadRunURLAndNoArg(t *testing.T) {
	e := newPLDownloadEnv(t, false)
	e.stubList(artifact(1, "drop", "Container", "#/1/drop", e.containerURL("drop")))
	e.stubContainer("drop", zipOf(t, "drop/a", "a"))
	dir := t.TempDir()
	require.NoError(t, e.run(t, "https://dev.azure.com/"+e.org+"/My%20Proj/_build/results?buildId=812", "-D", dir))
	assert.Equal(t, map[string]string{"drop/a": "a"}, tree(t, dir))

	e = newPLDownloadEnv(t, false)
	err := e.run(t, "-D", t.TempDir())
	assert.EqualError(t, err, "<run> argument required when not running interactively")

	e = newPLDownloadEnv(t, false)
	assert.Error(t, e.run(t, "nope"))
}
