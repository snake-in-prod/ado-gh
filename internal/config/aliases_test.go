package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataDir(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	xdg := filepath.Join(home, "xdg") // absolute on every OS

	tests := []struct {
		name string
		goos string
		env  map[string]string
		want string
	}{
		{"xdg on linux", "linux", map[string]string{"XDG_DATA_HOME": xdg, "LocalAppData": `C:\local`}, filepath.Join(xdg, "ado")},
		{"xdg wins on windows", "windows", map[string]string{"XDG_DATA_HOME": xdg, "LocalAppData": `C:\local`}, filepath.Join(xdg, "ado")},
		{"localappdata on windows", "windows", map[string]string{"LocalAppData": `C:\local`}, filepath.Join(`C:\local`, "ado")},
		{"relative xdg ignored", "linux", map[string]string{"XDG_DATA_HOME": "data"}, filepath.Join(home, ".local", "share", "ado")},
		{"localappdata ignored on linux", "linux", map[string]string{"LocalAppData": `C:\local`}, filepath.Join(home, ".local", "share", "ado")},
		{"home fallback on darwin", "darwin", nil, filepath.Join(home, ".local", "share", "ado")},
		{"home fallback on windows", "windows", nil, filepath.Join(home, ".local", "share", "ado")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"XDG_DATA_HOME", "LocalAppData"} {
				t.Setenv(k, tt.env[k])
			}
			assert.Equal(t, tt.want, dataDir(tt.goos))
		})
	}
}

func TestDataDirUsesRuntimeOS(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	assert.Equal(t, filepath.Join(xdg, "ado"), DataDir())
}

func TestStateDir(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	xdg := filepath.Join(home, "xdg") // absolute on every OS

	tests := []struct {
		name string
		goos string
		env  map[string]string
		want string
	}{
		{"xdg on linux", "linux", map[string]string{"XDG_STATE_HOME": xdg, "LocalAppData": `C:\local`}, filepath.Join(xdg, "ado")},
		{"xdg wins on windows", "windows", map[string]string{"XDG_STATE_HOME": xdg, "LocalAppData": `C:\local`}, filepath.Join(xdg, "ado")},
		{"data home ignored", "linux", map[string]string{"XDG_DATA_HOME": "/data"}, filepath.Join(home, ".local", "state", "ado")},
		{"localappdata on windows", "windows", map[string]string{"LocalAppData": `C:\local`}, filepath.Join(`C:\local`, "ado")},
		{"relative xdg ignored", "windows", map[string]string{"XDG_STATE_HOME": "state", "LocalAppData": `C:\local`}, filepath.Join(`C:\local`, "ado")},
		{"localappdata ignored on linux", "linux", map[string]string{"LocalAppData": `C:\local`}, filepath.Join(home, ".local", "state", "ado")},
		{"home fallback on darwin", "darwin", nil, filepath.Join(home, ".local", "state", "ado")},
		{"home fallback on windows", "windows", nil, filepath.Join(home, ".local", "state", "ado")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"XDG_STATE_HOME", "XDG_DATA_HOME", "LocalAppData"} {
				t.Setenv(k, tt.env[k])
			}
			assert.Equal(t, tt.want, stateDir(tt.goos))
		})
	}
	t.Setenv("XDG_STATE_HOME", xdg)
	assert.Equal(t, filepath.Join(xdg, "ado"), StateDir())
}

func TestAliasesAndExtensionSourcesRoundTrip(t *testing.T) {
	d := t.TempDir()
	t.Setenv("ADO_CONFIG_DIR", d)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.Aliases())
	assert.Empty(t, cfg.ExtensionSources())

	cfg.SetAlias("prs", "pr list -a @me")
	cfg.SetAlias("co", "!ado pr checkout $1")
	cfg.SetAlias("gone", "pr view")
	cfg.DeleteAlias("gone")
	cfg.DeleteAlias("never-set")
	cfg.SetExtensionSources([]string{"github.com/myco/*", "local"})
	cfg.Set("", "editor", "vim")
	require.NoError(t, cfg.Write())

	raw, err := os.ReadFile(filepath.Join(d, "config.yml"))
	require.NoError(t, err)
	assert.Equal(t, `aliases:
    co: '!ado pr checkout $1'
    prs: pr list -a @me
extension_sources:
    - github.com/myco/*
    - local
editor: vim
`, string(raw))

	got, err := Load()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"prs": "pr list -a @me", "co": "!ado pr checkout $1"}, got.Aliases())
	assert.Equal(t, []string{"github.com/myco/*", "local"}, got.ExtensionSources())
	assert.Equal(t, "vim", got.Get("", "editor"))

	got.Aliases()["prs"] = "mutated"
	assert.Equal(t, "pr list -a @me", got.Aliases()["prs"], "Aliases returns a copy")
}

func TestLoadHandWrittenAliasesAndSources(t *testing.T) {
	d := t.TempDir()
	t.Setenv("ADO_CONFIG_DIR", d)
	require.NoError(t, os.WriteFile(filepath.Join(d, "config.yml"), []byte(`prompt: disabled
aliases:
  co: pr checkout
  mine: "!ado pr list --json id -q '.[0].id' | xargs ado pr checkout"
extension_sources: [dev.azure.com/myorg/**, local]
hosts:
  dev.azure.com:
    org: contoso
`), 0o600))

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"co":   "pr checkout",
		"mine": "!ado pr list --json id -q '.[0].id' | xargs ado pr checkout",
	}, cfg.Aliases())
	assert.Equal(t, []string{"dev.azure.com/myorg/**", "local"}, cfg.ExtensionSources())
	assert.Equal(t, "disabled", cfg.Get("", "prompt"))
	assert.Equal(t, "contoso", cfg.Get("dev.azure.com", "org"))
	assert.Empty(t, cfg.Get("", "aliases"), "aliases are not a plain global key")
}

func TestEmptyAliasesAndSourcesAreOmitted(t *testing.T) {
	d := t.TempDir()
	t.Setenv("ADO_CONFIG_DIR", d)
	cfg, err := Load()
	require.NoError(t, err)
	cfg.SetAlias("x", "pr list")
	cfg.DeleteAlias("x")
	cfg.SetExtensionSources(nil)
	cfg.Set("", "editor", "vim")
	require.NoError(t, cfg.Write())

	raw, err := os.ReadFile(filepath.Join(d, "config.yml"))
	require.NoError(t, err)
	assert.Equal(t, "editor: vim\n", string(raw))
}

func TestExtensionSourcesGetSetCommaSeparated(t *testing.T) {
	d := t.TempDir()
	t.Setenv("ADO_CONFIG_DIR", d)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.Get("", ExtensionSourcesKey))

	cfg.Set("", ExtensionSourcesKey, " github.com/myco/* ,, dev.azure.com/myorg/**,local ")
	assert.Equal(t, []string{"github.com/myco/*", "dev.azure.com/myorg/**", "local"}, cfg.ExtensionSources())
	assert.Equal(t, "github.com/myco/*,dev.azure.com/myorg/**,local", cfg.Get("", ExtensionSourcesKey))
	assert.Equal(t, "github.com/myco/*,dev.azure.com/myorg/**,local", cfg.Get("dev.azure.com", ExtensionSourcesKey), "global only")
	require.NoError(t, cfg.Write())

	raw, err := os.ReadFile(filepath.Join(d, "config.yml"))
	require.NoError(t, err)
	assert.Equal(t, "extension_sources:\n    - github.com/myco/*\n    - dev.azure.com/myorg/**\n    - local\n", string(raw), "stored as a YAML list")

	cfg.Set("", ExtensionSourcesKey, "")
	assert.Empty(t, cfg.ExtensionSources())
	assert.Empty(t, cfg.Get("", ExtensionSourcesKey))
}
