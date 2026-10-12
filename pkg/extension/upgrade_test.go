package extension

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// upgradeGH installs ado-hello v0.1.0 from a GitHub release by hand: the entry directory with
// its binary and the install record.
func upgradeGH(t *testing.T, g *ghFixture, rec Record) string {
	t.Helper()
	dir := filepath.Join(g.opts.Root, "ado-hello")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ado-hello"), []byte("OLD"), 0o755))
	if rec.Source == "" {
		rec.Source, rec.Host = "https://github.com/o/ado-hello", hostGitHub
	}
	rec.Kind = KindBinary
	if rec.Tag == "" {
		rec.Tag = "v0.1.0"
	}
	require.NoError(t, WriteRecord(g.opts.Root, "hello", &rec))
	return dir
}

func (g *ghFixture) upgrade(o UpgradeOptions) (UpgradeResult, error) {
	o.Root, o.Name, o.goos, o.goarch = g.opts.Root, "hello", "linux", "amd64"
	return Upgrade(context.Background(), g.f, o)
}

func TestUpgradeGitHubBinary(t *testing.T) {
	g := newGHFixture(t)
	dir := upgradeGH(t, g, Record{})
	before, err := os.Stat(dir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".old-1-ado-hello"), []byte("leftover"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stale"), nil, 0o644))
	g.release("", "v0.2.0", false, "ado-hello-linux-amd64", "checksums.txt")
	g.asset(2, ghSum("NEW")+"  ado-hello-linux-amd64\n")
	g.manifest("")
	g.asset(1, "NEW")

	res, err := g.upgrade(UpgradeOptions{Trust: Trust{Yes: true}})
	require.NoError(t, err)
	g.reg.Verify(t)
	assert.Equal(t, UpgradeResult{From: "v0.1.0", To: "v0.2.0"}, res)
	after, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after), "the entry directory is kept, only the binary is replaced")
	b, err := os.ReadFile(filepath.Join(dir, "ado-hello"))
	require.NoError(t, err)
	assert.Equal(t, "NEW", string(b))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "old binary, leftover aside copy and stale files removed")
	rec, err := ReadRecord(g.opts.Root, "hello")
	require.NoError(t, err)
	assert.Equal(t, &Record{Source: "https://github.com/o/ado-hello", Host: hostGitHub, Kind: KindBinary, Tag: "v0.2.0",
		Checksum: "sha256:" + ghSum("NEW")}, rec)
}

func TestReplaceBinaryRestoresOnFailure(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "ado-hello")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ado-hello"), []byte("OLD"), 0o755))
	err := placeEntry(root, "hello", t.TempDir(), "ado-hello") // the stage has no binary
	require.Error(t, err)
	b, err := os.ReadFile(filepath.Join(dir, "ado-hello"))
	require.NoError(t, err)
	assert.Equal(t, "OLD", string(b), "the old binary is moved back")
}

func TestUpgradeGitHubBinaryChecksumMismatch(t *testing.T) {
	g := newGHFixture(t)
	dir := upgradeGH(t, g, Record{})
	g.release("", "v0.2.0", false, "ado-hello-linux-amd64", "checksums.txt")
	g.asset(2, ghSum("OTHER")+"  ado-hello-linux-amd64\n")
	g.manifest("")
	g.asset(1, "NEW")
	_, err := g.upgrade(UpgradeOptions{Trust: Trust{Yes: true}})
	assert.ErrorContains(t, err, "checksum mismatch")
	b, err := os.ReadFile(filepath.Join(dir, "ado-hello"))
	require.NoError(t, err)
	assert.Equal(t, "OLD", string(b), "the installed binary is untouched")
}

func TestUpgradeGitHubBinaryUpToDateAndDryRun(t *testing.T) {
	g := newGHFixture(t)
	upgradeGH(t, g, Record{})
	g.release("", "v0.1.0", false, "ado-hello-linux-amd64")
	res, err := g.upgrade(UpgradeOptions{Trust: Trust{Yes: true}})
	require.NoError(t, err)
	assert.Equal(t, UpgradeResult{From: "v0.1.0", To: "v0.1.0", Skipped: "already up to date"}, res)

	g.f.IOStreams.SetStdinTTY(false) // a dry run needs no --yes off a terminal
	g.release("", "v0.2.0", false, "ado-hello-linux-amd64", "checksums.txt")
	res, err = g.upgrade(UpgradeOptions{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, UpgradeResult{From: "v0.1.0", To: "v0.2.0"}, res)
	g.reg.Verify(t)
	assert.Len(t, g.reg.Requests, 2, "only the release lookups: nothing downloaded")
	rec, err := ReadRecord(g.opts.Root, "hello")
	require.NoError(t, err)
	assert.Equal(t, "v0.1.0", rec.Tag)
}

func TestUpgradeSkipsPinnedAndLocal(t *testing.T) {
	g := newGHFixture(t)
	upgradeGH(t, g, Record{Pinned: true})
	res, err := g.upgrade(UpgradeOptions{Trust: Trust{Yes: true}})
	require.NoError(t, err)
	assert.Equal(t, UpgradeResult{From: "v0.1.0", Skipped: "pinned to v0.1.0; use --force to upgrade"}, res)
	assert.Empty(t, g.reg.Requests)

	// --force upgrades it and unpins it.
	g.release("", "v0.2.0", false, "ado-hello-linux-amd64")
	g.manifest("")
	g.asset(1, "NEW")
	res, err = g.upgrade(UpgradeOptions{Force: true, Trust: Trust{Yes: true, AllowUnverified: true}})
	require.NoError(t, err)
	assert.Equal(t, UpgradeResult{From: "v0.1.0", To: "v0.2.0"}, res)
	rec, err := ReadRecord(g.opts.Root, "hello")
	require.NoError(t, err)
	assert.False(t, rec.Pinned)
	assert.Equal(t, "v0.2.0", rec.Tag)

	local := t.TempDir()
	_, err = InstallLocal(local, "hello", localSrcDir(t, true), false)
	require.NoError(t, err)
	res, err = Upgrade(context.Background(), g.f, UpgradeOptions{Root: local, Name: "hello", Force: true})
	require.NoError(t, err)
	assert.Equal(t, "local extension, nothing to upgrade", res.Skipped)

	_, err = Upgrade(context.Background(), g.f, UpgradeOptions{Root: g.opts.Root, Name: "missing"})
	assert.EqualError(t, err, `extension "missing" is not installed`)
}

func TestUpgradeRecordErrors(t *testing.T) {
	g := newGHFixture(t)
	dir := upgradeGH(t, g, Record{})
	require.NoError(t, os.Remove(RecordPath(g.opts.Root, "hello")))
	_, err := g.upgrade(UpgradeOptions{Trust: Trust{Yes: true}})
	assert.EqualError(t, err, `extension "hello" has no install record to upgrade from; reinstall it with --force`)

	require.NoError(t, WriteRecord(g.opts.Root, "hello", &Record{Source: "/some/dir", Kind: KindBinary}))
	_, err = g.upgrade(UpgradeOptions{Trust: Trust{Yes: true}})
	assert.EqualError(t, err, `extension "hello" has no install record to upgrade from; reinstall it with --force`)

	require.NoError(t, os.WriteFile(RecordPath(g.opts.Root, "hello"), []byte(":\n:"), 0o600))
	_, err = g.upgrade(UpgradeOptions{Trust: Trust{Yes: true}})
	assert.ErrorContains(t, err, "parse install record")
	assert.DirExists(t, dir)
}

func TestUpgradeChecksAllowlist(t *testing.T) {
	g := newGHFixture(t)
	upgradeGH(t, g, Record{})
	cfg, err := g.f.Config()
	require.NoError(t, err)
	cfg.Set("", config.ExtensionSourcesKey, "github.com/other/*")
	_, err = g.upgrade(UpgradeOptions{Trust: Trust{Yes: true}})
	assert.EqualError(t, err, `extension source "github.com/o/ado-hello" is not allowed by extension_sources (github.com/other/*)`)
	assert.Empty(t, g.reg.Requests)
}

func TestUpgradeTokenConsent(t *testing.T) {
	t.Run("new token request asks again", func(t *testing.T) {
		g := newGHFixture(t)
		upgradeGH(t, g, Record{})
		out, _ := upgradeInput(g.f, "y\n")
		g.release("", "v0.2.0", false, "ado-hello-linux-amd64")
		g.manifest("token: true\n")
		g.asset(1, "NEW")
		_, err := g.upgrade(UpgradeOptions{Trust: Trust{Yes: true, AllowUnverified: true}})
		require.NoError(t, err)
		assert.Contains(t, out.String(), "Give hello your Azure DevOps token?")
		rec, err := ReadRecord(g.opts.Root, "hello")
		require.NoError(t, err)
		assert.True(t, rec.TokenConsent)
	})
	t.Run("granted consent is kept", func(t *testing.T) {
		g := newGHFixture(t)
		upgradeGH(t, g, Record{TokenConsent: true})
		g.release("", "v0.2.0", false, "ado-hello-linux-amd64")
		g.manifest("token: true\n")
		g.asset(1, "NEW")
		_, err := g.upgrade(UpgradeOptions{Trust: Trust{Yes: true, AllowUnverified: true}})
		require.NoError(t, err)
		assert.NotContains(t, g.errOut.String(), "was not given it")
		rec, err := ReadRecord(g.opts.Root, "hello")
		require.NoError(t, err)
		assert.True(t, rec.TokenConsent)
	})
}

// upgradeScriptFixture installs ado-hello as a script clone of the ADO repo by hand at
// scriptSHA; the runner answers FETCH_HEAD and ls-remote with next.
func upgradeScriptFixture(t *testing.T, next string, rec Record) *scriptFixture {
	t.Helper()
	s := newScriptFixture(t)
	t.Setenv("ADO_TOKEN", "ado-secret")
	require.NoError(t, os.MkdirAll(filepath.Join(s.root, "ado-hello", ".git"), 0o755))
	rec.Source, rec.Host, rec.Kind, rec.Commit = scriptADOURL, hostADO, KindScript, scriptSHA
	require.NoError(t, WriteRecord(s.root, "hello", &rec))
	s.r.Replies["rev-parse FETCH_HEAD"] = next + "\n"
	s.r.Replies["ls-remote origin HEAD"] = next + "\tHEAD\n"
	return s
}

// upgradeInput makes f's streams terminals whose prompter reads input; it returns stdout,
// where the prompts go, and stderr.
func upgradeInput(f *cmdutil.Factory, input string) (out, errOut *bytes.Buffer) {
	ios, _, out, errOut := iostreams.Test()
	ios.In = iotest.OneByteReader(bytes.NewBufferString(input))
	ios.SetStdinTTY(true)
	ios.SetStdoutTTY(true)
	ios.SetStderrTTY(true)
	f.IOStreams, f.Prompter = ios, &cmdutil.Prompter{IO: ios, Accessible: true}
	return out, errOut
}

func (s *scriptFixture) upgrade(o UpgradeOptions) (UpgradeResult, error) {
	o.Root, o.Name = s.root, "hello"
	return Upgrade(context.Background(), s.f, o)
}

const scriptSHA2 = "2222222222222222222222222222222222222222"

func TestUpgradeScript(t *testing.T) {
	s := upgradeScriptFixture(t, scriptSHA2, Record{})
	res, err := s.upgrade(UpgradeOptions{Trust: Trust{Yes: true}})
	require.NoError(t, err)
	assert.Equal(t, UpgradeResult{From: "11111111", To: "22222222"}, res)
	assert.Equal(t, []string{"version", "rev-parse HEAD", "fetch --quiet --no-recurse-submodules origin HEAD",
		"rev-parse FETCH_HEAD", "show FETCH_HEAD:ado-extension.yml", "merge --quiet --ff-only FETCH_HEAD"}, s.r.Calls)
	dir := filepath.Join(s.root, "ado-hello")
	for i, c := range s.r.Calls[1:] {
		assert.Equal(t, dir, s.r.Dirs[i+1], c)
		env := strings.Join(s.r.Envs[i+1], "\n")
		assert.Contains(t, env, "GIT_CONFIG_KEY_0=http."+scriptADOURL+".extraheader", "token env on "+c)
		assert.Contains(t, env, "GIT_TERMINAL_PROMPT=0")
		assert.NotContains(t, c, "ado-secret")
	}
	rec, err := ReadRecord(s.root, "hello")
	require.NoError(t, err)
	assert.Equal(t, &Record{Source: scriptADOURL, Host: hostADO, Kind: KindScript, Commit: scriptSHA2}, rec)
}

func TestUpgradeScriptNotFastForward(t *testing.T) {
	s := upgradeScriptFixture(t, scriptSHA2, Record{})
	s.r.Fails = map[string]error{"merge --quiet --ff-only FETCH_HEAD": &git.Error{Stderr: "fatal: Not possible to fast-forward, aborting."}}
	_, err := s.upgrade(UpgradeOptions{Trust: Trust{Yes: true}})
	assert.EqualError(t, err, "cannot fast-forward ado-hello to the remote default branch (fatal: Not possible to fast-forward, aborting.); "+
		"use --force to reset it to the remote, discarding local changes")
	rec, err := ReadRecord(s.root, "hello")
	require.NoError(t, err)
	assert.Equal(t, scriptSHA, rec.Commit, "record unchanged")

	s.r.Calls = nil
	_, err = s.upgrade(UpgradeOptions{Force: true, Trust: Trust{Yes: true}})
	require.NoError(t, err)
	assert.Equal(t, []string{"fetch --quiet --no-recurse-submodules origin HEAD", "reset --quiet --hard FETCH_HEAD"}, s.r.Mutations())
}

func TestUpgradeScriptPinnedForce(t *testing.T) {
	s := upgradeScriptFixture(t, scriptSHA2, Record{Pinned: true, Tag: "v1"})
	res, err := s.upgrade(UpgradeOptions{Trust: Trust{Yes: true}})
	require.NoError(t, err)
	assert.Equal(t, "pinned to v1; use --force to upgrade", res.Skipped)
	assert.Empty(t, s.r.Calls)

	res, err = s.upgrade(UpgradeOptions{Force: true, Trust: Trust{Yes: true}})
	require.NoError(t, err)
	assert.Equal(t, UpgradeResult{From: "11111111", To: "22222222"}, res)
	assert.Contains(t, s.r.Calls, "reset --quiet --hard FETCH_HEAD")
	rec, err := ReadRecord(s.root, "hello")
	require.NoError(t, err)
	assert.Equal(t, &Record{Source: scriptADOURL, Host: hostADO, Kind: KindScript, Commit: scriptSHA2}, rec)
}

func TestUpgradeScriptUpToDateAndDryRun(t *testing.T) {
	s := upgradeScriptFixture(t, scriptSHA, Record{})
	res, err := s.upgrade(UpgradeOptions{Trust: Trust{Yes: true}})
	require.NoError(t, err)
	assert.Equal(t, "already up to date", res.Skipped)
	assert.NotContains(t, s.r.Calls, "merge --quiet --ff-only FETCH_HEAD")

	s = upgradeScriptFixture(t, scriptSHA2, Record{})
	res, err = s.upgrade(UpgradeOptions{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, UpgradeResult{From: "11111111", To: "22222222"}, res)
	assert.Equal(t, []string{"version", "rev-parse HEAD", "ls-remote origin HEAD"}, s.r.Calls)
	assert.Contains(t, strings.Join(s.r.Envs[2], "\n"), "GIT_CONFIG_KEY_0=http."+scriptADOURL+".extraheader", "token env on ls-remote")
	assert.Empty(t, s.r.Mutations())

	s.r.Replies["ls-remote origin HEAD"] = ""
	_, err = s.upgrade(UpgradeOptions{DryRun: true})
	assert.EqualError(t, err, "checking "+scriptADOURL+": the remote has no HEAD")
	s.r.Fails = map[string]error{"ls-remote origin HEAD": errors.New("boom")}
	_, err = s.upgrade(UpgradeOptions{DryRun: true})
	assert.EqualError(t, err, "checking "+scriptADOURL+": boom")
	s.r.Fails = map[string]error{"fetch --quiet --no-recurse-submodules origin HEAD": errors.New("boom")}
	_, err = s.upgrade(UpgradeOptions{Trust: Trust{Yes: true}})
	assert.EqualError(t, err, "fetching "+scriptADOURL+": boom")
}

func TestUpgradeScriptNewTokenRequest(t *testing.T) {
	s := upgradeScriptFixture(t, scriptSHA2, Record{})
	s.r.Replies["show FETCH_HEAD:ado-extension.yml"] = "token: true\n"
	out, errOut := upgradeInput(s.f, "y\ny\n")
	_, err := s.upgrade(UpgradeOptions{})
	require.NoError(t, err)
	assert.Contains(t, errOut.String(), "Ref:         "+scriptSHA2+" (default branch)")
	assert.Contains(t, errOut.String(), "Token:       requests your Azure DevOps token (ADO_TOKEN)")
	assert.Contains(t, out.String(), "Give hello your Azure DevOps token?")
	rec, err := ReadRecord(s.root, "hello")
	require.NoError(t, err)
	assert.True(t, rec.TokenConsent)

	// Declining the upgrade leaves it as is.
	s = upgradeScriptFixture(t, scriptSHA2, Record{})
	upgradeInput(s.f, "n\n")
	_, err = s.upgrade(UpgradeOptions{})
	assert.ErrorIs(t, err, cmdutil.CancelError)
	assert.NotContains(t, s.r.Calls, "merge --quiet --ff-only FETCH_HEAD")
}

func TestUpgradeScriptToArtifacts(t *testing.T) {
	a := newArtFixture(t, "")
	require.NoError(t, os.MkdirAll(filepath.Join(a.s.root, "ado-hello", ".git"), 0o755))
	require.NoError(t, WriteRecord(a.s.root, "hello", &Record{Source: a.opts.URL, Host: hostADO, Kind: KindScript, Commit: scriptSHA}))
	a.s.r.Replies["rev-parse FETCH_HEAD"] = scriptSHA2 + "\n"
	a.s.r.Replies["show FETCH_HEAD:ado-extension.yml"] = a.s.manifest
	a.release(t, "Linux", "amd64", "linux-x64")
	a.toolZip(t, "artifacttool")
	a.packages("proj", "0.2.0")
	a.linuxPackage()
	res, err := Upgrade(context.Background(), a.s.f, UpgradeOptions{Root: a.s.root, Name: "hello", Trust: Trust{Yes: true},
		goos: "linux", goarch: "amd64", runTool: a.opts.runTool})
	require.NoError(t, err)
	assert.Equal(t, UpgradeResult{From: "11111111", To: "0.2.0"}, res)
	ext, err := Lookup(a.s.root, "hello")
	require.NoError(t, err)
	assert.Equal(t, KindBinary, ext.Kind, "the new version is an Azure Artifacts binary")
	assert.NotContains(t, a.s.r.Calls, "merge --quiet --ff-only FETCH_HEAD")
}

func TestUpgradeArtifacts(t *testing.T) {
	a := newArtFixture(t, "")
	a.release(t, "Linux", "amd64", "linux-x64")
	a.toolZip(t, "artifacttool")
	a.packages("proj", "0.1.0")
	a.linuxPackage()
	_, err := a.install()
	require.NoError(t, err)
	a.reg.Verify(t)
	up := UpgradeOptions{Root: a.s.root, Name: "hello", Trust: Trust{Yes: true}, goos: "linux", goarch: "amd64", runTool: a.opts.runTool}

	// Dry run: the version is resolved without downloading ArtifactTool or the package.
	require.NoError(t, os.RemoveAll(filepath.Join(a.data, "ado", "artifacttool")))
	a.release(t, "Linux", "amd64", "linux-x64")
	a.packages("proj", "0.1.0", "0.2.0")
	a.cmds = nil
	res, err := Upgrade(context.Background(), a.s.f, UpgradeOptions{Root: a.s.root, Name: "hello", DryRun: true})
	require.NoError(t, err)
	a.reg.Verify(t)
	assert.Equal(t, UpgradeResult{From: "0.1.0", To: "0.2.0"}, res)
	assert.Empty(t, a.cmds)
	assert.NoDirExists(t, filepath.Join(a.data, "ado", "artifacttool"))

	a.release(t, "Linux", "amd64", "linux-x64")
	a.packages("proj", "0.1.0")
	res, err = Upgrade(context.Background(), a.s.f, up)
	require.NoError(t, err)
	assert.Equal(t, "already up to date", res.Skipped)
	assert.Empty(t, a.cmds)

	a.release(t, "Linux", "amd64", "linux-x64")
	a.toolZip(t, "artifacttool")
	a.packages("proj", "0.1.0", "0.2.0")
	res, err = Upgrade(context.Background(), a.s.f, up)
	require.NoError(t, err)
	a.reg.Verify(t)
	assert.Equal(t, UpgradeResult{From: "0.1.0", To: "0.2.0"}, res)
	require.Len(t, a.cmds, 1)
	rec, err := ReadRecord(a.s.root, "hello")
	require.NoError(t, err)
	assert.Equal(t, "0.2.0", rec.Version)
	assert.False(t, rec.Pinned)
}
