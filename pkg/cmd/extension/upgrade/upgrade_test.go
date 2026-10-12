package upgrade

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	extpkg "github.com/snake-in-prod/ado-gh/pkg/extension"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// upgradeCmdEnv is a fresh data and config dir with GitHub stubbed through reg.
type upgradeCmdEnv struct {
	f   *cmdutil.Factory
	reg *httpmock.Registry
}

func newUpgradeCmdEnv(t *testing.T) *upgradeCmdEnv {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN", "ADO_TOKEN", "AZURE_DEVOPS_EXT_PAT"} {
		t.Setenv(k, "")
	}
	cfg, err := config.Load()
	require.NoError(t, err)
	reg := &httpmock.Registry{}
	ios, _, _, _ := iostreams.Test()
	return &upgradeCmdEnv{reg: reg, f: &cmdutil.Factory{
		IOStreams:  ios,
		Config:     func() (cmdutil.Config, error) { return cfg, nil },
		HttpClient: func() (*http.Client, error) { return &http.Client{Transport: reg}, nil },
	}}
}

func (e *upgradeCmdEnv) run(args ...string) (stdout, stderr string, err error) {
	ios, _, out, errOut := iostreams.Test()
	e.f.IOStreams = ios
	cmd := NewCmdUpgrade(e.f)
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// binary installs ado-<name> as a GitHub release binary at tag by hand.
func upgradeCmdBinary(t *testing.T, name string, rec extpkg.Record) {
	t.Helper()
	dir := filepath.Join(extpkg.Dir(), "ado-"+name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ado-"+name), []byte("OLD"), 0o755))
	rec.Kind = extpkg.KindBinary
	require.NoError(t, extpkg.WriteRecord(extpkg.Dir(), name, &rec))
}

func upgradeCmdLocal(t *testing.T, name string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ado-"+name)
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ado-"+name), []byte("#!/bin/sh\n"), 0o755))
	_, err := extpkg.InstallLocal(extpkg.Dir(), name, dir, false)
	require.NoError(t, err)
}

func TestUpgradeCmdArgs(t *testing.T) {
	e := newUpgradeCmdEnv(t)
	_, _, err := e.run()
	assert.EqualError(t, err, "specify an extension to upgrade or use --all")
	_, _, err = e.run("hello", "--all")
	assert.EqualError(t, err, "cannot use --all with an extension name")
	_, _, err = e.run("hello", "--yes")
	assert.EqualError(t, err, `extension "hello" is not installed`)
	upgradeCmdBinary(t, "hello", extpkg.Record{Source: "https://github.com/o/ado-hello", Host: "github.com", Tag: "v1"})
	_, _, err = e.run("hello")
	assert.EqualError(t, err, "--yes required when not running interactively")
}

func TestUpgradeCmdSkips(t *testing.T) {
	e := newUpgradeCmdEnv(t)
	upgradeCmdBinary(t, "hello", extpkg.Record{Source: "https://github.com/o/ado-hello", Host: "github.com", Tag: "v1", Pinned: true})
	upgradeCmdLocal(t, "loc")
	out, _, err := e.run("hello", "--yes")
	require.NoError(t, err)
	assert.Equal(t, "hello: pinned to v1; use --force to upgrade\n", out)
	out, _, err = e.run("loc", "--dry-run")
	require.NoError(t, err)
	assert.Equal(t, "loc: local extension, nothing to upgrade\n", out)
	assert.Empty(t, e.reg.Requests)
}

func TestUpgradeCmdDryRun(t *testing.T) {
	e := newUpgradeCmdEnv(t)
	upgradeCmdBinary(t, "hello", extpkg.Record{Source: "https://github.com/o/ado-hello", Host: "github.com", Tag: "v0.1.0"})
	asset := fmt.Sprintf("ado-hello-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	e.reg.Register(httpmock.REST(http.MethodGet, "repos/o/ado-hello/releases/latest"), httpmock.JSONResponse(map[string]any{
		"tag_name": "v0.2.0", "assets": []map[string]any{{"id": 1, "name": asset}}}))
	out, _, err := e.run("--all", "--dry-run")
	require.NoError(t, err)
	e.reg.Verify(t)
	assert.Equal(t, "hello: v0.1.0 → v0.2.0\n", out)
}

func TestUpgradeCmdAllPartialFailure(t *testing.T) {
	e := newUpgradeCmdEnv(t)
	upgradeCmdBinary(t, "broken", extpkg.Record{})
	upgradeCmdBinary(t, "hello", extpkg.Record{Source: "https://github.com/o/ado-hello", Host: "github.com", Tag: "v1", Pinned: true})
	upgradeCmdLocal(t, "loc")
	out, errOut, err := e.run("--all", "--yes")
	assert.ErrorIs(t, err, cmdutil.SilentError)
	assert.Equal(t, "hello: pinned to v1; use --force to upgrade\nloc: local extension, nothing to upgrade\n", out)
	assert.Equal(t, `broken: extension "broken" has no install record to upgrade from; reinstall it with --force`+"\n"+
		"Failed to upgrade 1 of 3 extensions: broken\n", errOut)

	_, _, err = e.run("broken", "--yes")
	assert.EqualError(t, err, `extension "broken" has no install record to upgrade from; reinstall it with --force`)
}

func TestUpgradeCmdAllNone(t *testing.T) {
	e := newUpgradeCmdEnv(t)
	out, errOut, err := e.run("--all")
	require.NoError(t, err)
	assert.Empty(t, out)
	assert.Empty(t, errOut)
}
