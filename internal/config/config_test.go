package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDir(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	tests := []struct {
		name string
		goos string
		env  map[string]string
		want string
	}{
		{"override wins", "linux", map[string]string{"ADO_CONFIG_DIR": "/custom", "XDG_CONFIG_HOME": "/xdg"}, "/custom"},
		{"override wins on windows", "windows", map[string]string{"ADO_CONFIG_DIR": `C:\custom`, "AppData": `C:\roaming`}, `C:\custom`},
		{"xdg on linux", "linux", map[string]string{"XDG_CONFIG_HOME": "/xdg"}, filepath.Join("/xdg", "ado")},
		{"xdg on darwin", "darwin", map[string]string{"XDG_CONFIG_HOME": "/xdg"}, filepath.Join("/xdg", "ado")},
		{"appdata on windows", "windows", map[string]string{"AppData": `C:\roaming`}, filepath.Join(`C:\roaming`, "ado")},
		{"home fallback on linux", "linux", nil, filepath.Join(home, ".config", "ado")},
		{"home fallback on darwin", "darwin", nil, filepath.Join(home, ".config", "ado")},
		{"home fallback on windows without appdata", "windows", nil, filepath.Join(home, ".config", "ado")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"ADO_CONFIG_DIR", "XDG_CONFIG_HOME", "AppData"} {
				t.Setenv(k, tt.env[k])
			}
			assert.Equal(t, tt.want, dir(tt.goos))
		})
	}
}

func TestDirUsesRuntimeOS(t *testing.T) {
	t.Setenv("ADO_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AppData", "")
	assert.Equal(t, dir(runtime.GOOS), Dir())
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "enabled", cfg.Get("", "prompt"))
	assert.Empty(t, cfg.Get("", "editor"))
	assert.Empty(t, cfg.Get("dev.azure.com", "org"))
	assert.Empty(t, cfg.Hosts())
}

func TestWriteLoadRoundTrip(t *testing.T) {
	d := filepath.Join(t.TempDir(), "nested")
	t.Setenv("ADO_CONFIG_DIR", d)

	cfg, err := Load()
	require.NoError(t, err)
	cfg.Set("", "editor", "vim")
	cfg.Set("", "prompt", "disabled")
	cfg.Set("dev.azure.com", "org", "contoso")
	cfg.Set("dev.azure.com", "auth_method", "pat")
	cfg.Set("fabrikam.visualstudio.com", "auth_method", "entra")
	require.NoError(t, cfg.Write())

	info, err := os.Stat(filepath.Join(d, "config.yml"))
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	got, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "vim", got.Get("", "editor"))
	assert.Equal(t, "disabled", got.Get("", "prompt"))
	assert.Equal(t, "contoso", got.Get("dev.azure.com", "org"))
	assert.Equal(t, "pat", got.Get("dev.azure.com", "auth_method"))
	assert.Equal(t, "entra", got.Get("fabrikam.visualstudio.com", "auth_method"))
	assert.Equal(t, []string{"dev.azure.com", "fabrikam.visualstudio.com"}, got.Hosts())

	raw, err := os.ReadFile(filepath.Join(d, "config.yml"))
	require.NoError(t, err)
	assert.Equal(t, `hosts:
    dev.azure.com:
        auth_method: pat
        org: contoso
    fabrikam.visualstudio.com:
        auth_method: entra
editor: vim
prompt: disabled
`, string(raw))
}

func TestPerHostKeys(t *testing.T) {
	t.Setenv("ADO_CONFIG_DIR", t.TempDir())
	cfg, err := Load()
	require.NoError(t, err)

	cfg.Set("", "editor", "nano")
	cfg.Set("dev.azure.com", "editor", "code")

	assert.Equal(t, "code", cfg.Get("dev.azure.com", "editor"), "host value wins")
	assert.Equal(t, "nano", cfg.Get("other.visualstudio.com", "editor"), "falls back to global")
	assert.Equal(t, "nano", cfg.Get("", "editor"))
	assert.Equal(t, "enabled", cfg.Get("dev.azure.com", "prompt"), "falls back to default")

	cfg.Unset("dev.azure.com", "editor")
	assert.Equal(t, "nano", cfg.Get("dev.azure.com", "editor"))
	assert.Empty(t, cfg.Hosts(), "empty host section removed")
	cfg.Unset("", "editor")
	assert.Empty(t, cfg.Get("", "editor"))
}

func TestLoadInvalidYAML(t *testing.T) {
	d := t.TempDir()
	t.Setenv("ADO_CONFIG_DIR", d)
	require.NoError(t, os.WriteFile(filepath.Join(d, "config.yml"), []byte("hosts: [nope"), 0o600))

	_, err := Load()
	assert.ErrorContains(t, err, "config.yml")
}

func TestLoadEmptyFile(t *testing.T) {
	d := t.TempDir()
	t.Setenv("ADO_CONFIG_DIR", d)
	require.NoError(t, os.WriteFile(filepath.Join(d, "config.yml"), nil, 0o600))

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "enabled", cfg.Get("", "prompt"))
}

func TestLoadUnreadable(t *testing.T) {
	d := t.TempDir()
	t.Setenv("ADO_CONFIG_DIR", d)
	// A directory where the file should be makes ReadFile fail on every OS.
	require.NoError(t, os.Mkdir(filepath.Join(d, "config.yml"), 0o700))

	_, err := Load()
	assert.Error(t, err)
}

func TestWriteFailsWhenDirIsAFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(f, nil, 0o600))

	cfg := &Config{path: filepath.Join(f, "config.yml")}
	cfg.Set("", "editor", "vim")
	assert.Error(t, cfg.Write())
}
