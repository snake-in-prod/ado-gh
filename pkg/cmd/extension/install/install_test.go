package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/snake-in-prod/ado-gh/internal/git/gittest"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	extpkg "github.com/snake-in-prod/ado-gh/pkg/extension"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// runInstallLocal runs `ado install <args>` with a fresh config dir holding the given
// extension_sources allowlist. Set XDG_DATA_HOME first.
func runInstallLocal(t *testing.T, sources string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	cfg, err := config.Load()
	require.NoError(t, err)
	cfg.Set("", config.ExtensionSourcesKey, sources)
	ios, _, out, errOut := iostreams.Test()
	ios.SetStderrTTY(true)
	f := &cmdutil.Factory{IOStreams: ios, Config: func() (cmdutil.Config, error) { return cfg, nil }}
	root := &cobra.Command{Use: "ado", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(&cobra.Command{Use: "workitem", Aliases: []string{"wi"}, Run: func(*cobra.Command, []string) {}})
	root.AddCommand(NewCmdInstall(f))
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(append([]string{"install"}, args...))
	err = root.Execute()
	return errOut.String(), err
}

// localExtDir creates <tmp>/ado-<name> with an ado-<name> script when withExe.
func localExtDir(t *testing.T, name string, withExe bool) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ado-"+name)
	require.NoError(t, os.Mkdir(dir, 0o755))
	if withExe {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "ado-"+name), []byte("#!/bin/sh\n"), 0o755))
	}
	return dir
}

func TestInstallLocalCmd(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := localExtDir(t, "hello", true)
	t.Chdir(dir)
	errOut, err := runInstallLocal(t, "", ".")
	require.NoError(t, err)
	assert.Contains(t, errOut, "Installed extension hello")
	ext, err := extpkg.Lookup(extpkg.Dir(), "hello")
	require.NoError(t, err)
	assert.Equal(t, extpkg.KindLocal, ext.Kind)
	assert.FileExists(t, ext.Executable())
}

func TestInstallLocalCmdMissingExecutable(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := localExtDir(t, "hello", false)
	errOut, err := runInstallLocal(t, "local", dir)
	require.NoError(t, err)
	assert.Contains(t, errOut, "! Installed extension hello, but "+dir+" has no executable ado-hello yet")
	_, err = extpkg.Lookup(extpkg.Dir(), "hello")
	assert.NoError(t, err, "link kept")
}

func TestInstallLocalCmdForce(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := localExtDir(t, "hello", true)
	_, err := runInstallLocal(t, "", dir)
	require.NoError(t, err)
	_, err = runInstallLocal(t, "", dir)
	assert.ErrorContains(t, err, `extension "hello" is already installed; use --force to replace it`)

	dir2 := localExtDir(t, "hello", true)
	errOut, err := runInstallLocal(t, "", dir2, "--force")
	require.NoError(t, err)
	assert.Contains(t, errOut, "Installed extension hello")
	rec, err := extpkg.ReadRecord(extpkg.Dir(), "hello")
	require.NoError(t, err)
	assert.Equal(t, dir2, rec.Source)
}

func TestInstallLocalCmdErrors(t *testing.T) {
	tests := map[string]struct {
		sources string
		arg     func(t *testing.T) string
		wantErr string
	}{
		"bad name": {arg: func(t *testing.T) string { return t.TempDir() }, wantErr: "must be named ado-<name>"},
		"collision": {arg: func(t *testing.T) string { return localExtDir(t, "wi", true) },
			wantErr: `"wi" collides with a core command`},
		"not allowed": {sources: "github.com/myco/*", arg: func(t *testing.T) string { return localExtDir(t, "hello", true) },
			wantErr: `extension source "local" is not allowed by extension_sources`},
		"missing dir": {arg: func(t *testing.T) string { return filepath.Join(t.TempDir(), "ado-hello") },
			wantErr: "no such directory"},
		"bad source": {arg: func(*testing.T) string { return "nope" }, wantErr: "unsupported extension source"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			_, err := runInstallLocal(t, tt.sources, tt.arg(t))
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestInstallLocalCmdPinRefused(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	_, err := runInstallLocal(t, "", localExtDir(t, "hello", true), "--pin", "v1")
	assert.EqualError(t, err, "local extensions cannot be pinned")
}

// runInstallScriptCmd runs `ado install <args>` against a stubbed git whose clone creates an
// executable ado-<name> checkout.
func runInstallScriptCmd(t *testing.T, args ...string) (*gittest.Runner, string, error) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	for _, k := range []string{"ADO_TOKEN", "AZURE_DEVOPS_EXT_PAT", "GIT_CONFIG_COUNT"} {
		t.Setenv(k, "")
	}
	cfg, err := config.Load()
	require.NoError(t, err)
	r := &gittest.Runner{Replies: map[string]string{
		"rev-parse HEAD":                          "abc\n",
		"rev-parse --verify --quiet v1^{commit}":  "abc\n",
		"rev-parse --verify --quiet refs/tags/v1": "abc\n",
		"ls-files -s -- ado-hello":                "100755 abc 0\tado-hello\n",
	}}
	ios, _, out, errOut := iostreams.Test()
	ios.SetStderrTTY(true)
	f := &cmdutil.Factory{IOStreams: ios, Config: func() (cmdutil.Config, error) { return cfg, nil },
		GitClient: &git.Client{Run: func(ctx context.Context, dir string, a ...string) ([]byte, error) {
			if a[0] == "clone" {
				dst := a[len(a)-1]
				require.NoError(t, os.MkdirAll(filepath.Join(dst, ".git"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dst, "ado-hello"), []byte("#!/bin/sh\n"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dst, extpkg.ManifestFile), []byte("token: true\n"), 0o644))
			}
			return r.Run(ctx, dir, a...)
		}}}
	root := &cobra.Command{Use: "ado", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(NewCmdInstall(f))
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(append([]string{"install"}, args...))
	err = root.Execute()
	return r, errOut.String(), err
}

func TestInstallScriptCmd(t *testing.T) {
	r, errOut, err := runInstallScriptCmd(t, "https://dev.azure.com/Org/Proj/_git/ado-Hello", "--pin", "v1", "-y", "--allow-token")
	require.NoError(t, err)
	assert.Contains(t, errOut, "Installed extension hello")
	assert.Contains(t, errOut, "Token:       requests your Azure DevOps token")
	assert.Contains(t, r.Calls[0], "-- https://dev.azure.com/Org/Proj/_git/ado-Hello ")
	ext, err := extpkg.Lookup(extpkg.Dir(), "hello")
	require.NoError(t, err)
	assert.Equal(t, extpkg.KindScript, ext.Kind)
	rec, err := extpkg.ReadRecord(extpkg.Dir(), "hello")
	require.NoError(t, err)
	assert.Equal(t, &extpkg.Record{Source: "https://dev.azure.com/Org/Proj/_git/ado-Hello", Host: "dev.azure.com",
		Kind: extpkg.KindScript, Tag: "v1", Commit: "abc", Pinned: true, TokenConsent: true}, rec)
}

func TestInstallScriptCmdTrust(t *testing.T) {
	r, _, err := runInstallScriptCmd(t, "myco/ado-hello")
	assert.EqualError(t, err, "--yes required when not running interactively")
	assert.Empty(t, r.Calls, "nothing cloned")

	_, errOut, err := runInstallScriptCmd(t, "myco/ado-hello", "--yes")
	require.NoError(t, err)
	assert.Contains(t, errOut, "hello requests your Azure DevOps token but was not given it")
	rec, err := extpkg.ReadRecord(extpkg.Dir(), "hello")
	require.NoError(t, err)
	assert.False(t, rec.TokenConsent)
}

func TestInstallScriptCmdErrors(t *testing.T) {
	_, _, err := runInstallScriptCmd(t, "myco/hello")
	assert.ErrorContains(t, err, "must be named ado-<name>")
	_, _, err = runInstallScriptCmd(t, "myco/ado-hello", "--pin=-x", "--yes")
	assert.EqualError(t, err, `invalid --pin "-x"`)
}
