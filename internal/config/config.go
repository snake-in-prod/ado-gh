// Package config reads and writes ado's YAML config file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const fileName = "config.yml"

var defaults = map[string]string{
	"prompt": "enabled",
}

// Config holds global settings and per-host settings keyed by the original
// remote host (e.g. dev.azure.com, {org}.visualstudio.com).
type Config struct {
	path string
	data file
}

type file struct {
	Global map[string]string            `yaml:",inline"`
	Hosts  map[string]map[string]string `yaml:"hosts,omitempty"`
	// Aliases maps alias names to expansions; a leading "!" marks a shell alias.
	Aliases map[string]string `yaml:"aliases,omitempty"`
	// ExtensionSources are the glob patterns extensions may be installed from.
	ExtensionSources []string `yaml:"extension_sources,omitempty"`
}

// Dir returns the config directory: $ADO_CONFIG_DIR, else $XDG_CONFIG_HOME/ado,
// else %AppData%\ado on Windows, else ~/.config/ado.
func Dir() string { return dir(runtime.GOOS) }

func dir(goos string) string {
	if d := os.Getenv("ADO_CONFIG_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "ado")
	}
	if d := os.Getenv("AppData"); goos == "windows" && d != "" {
		return filepath.Join(d, "ado")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ado")
}

// DataDir returns the data directory: $XDG_DATA_HOME/ado, else %LocalAppData%\ado on
// Windows, else ~/.local/share/ado.
func DataDir() string { return dataDir(runtime.GOOS) }

func dataDir(goos string) string { return xdgDir(goos, "XDG_DATA_HOME", "share") }

// StateDir returns the state directory: $XDG_STATE_HOME/ado, else %LocalAppData%\ado on
// Windows, else ~/.local/state/ado.
func StateDir() string { return stateDir(runtime.GOOS) }

func stateDir(goos string) string { return xdgDir(goos, "XDG_STATE_HOME", "state") }

// xdgDir returns $env/ado, else %LocalAppData%\ado on Windows, else ~/.local/<sub>/ado. A
// relative $env is ignored, as the XDG Base Directory spec requires: it would put files under
// whatever directory ado runs in.
func xdgDir(goos, env, sub string) string {
	if d := os.Getenv(env); filepath.IsAbs(d) {
		return filepath.Join(d, "ado")
	}
	if d := os.Getenv("LocalAppData"); goos == "windows" && d != "" {
		return filepath.Join(d, "ado")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", sub, "ado")
}

// Load reads the config file. A missing file yields an empty config.
func Load() (*Config, error) {
	c := &Config{path: filepath.Join(Dir(), fileName)}
	b, err := os.ReadFile(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, &c.data); err != nil {
		return nil, fmt.Errorf("parse %s: %w", c.path, err)
	}
	return c, nil
}

// ExtensionSourcesKey is the global key of the extension_sources allowlist. It is stored as a
// YAML list; Get and Set present it as one comma-separated value.
const ExtensionSourcesKey = "extension_sources"

// SplitList splits a comma-separated value, trimming spaces and dropping empty items.
func SplitList(v string) []string {
	var items []string
	for s := range strings.SplitSeq(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			items = append(items, s)
		}
	}
	return items
}

// Get returns the host value, else the global value, else the default.
// Pass host "" for global settings.
func (c *Config) Get(host, key string) string {
	if key == ExtensionSourcesKey {
		return strings.Join(c.data.ExtensionSources, ",")
	}
	if v, ok := c.data.Hosts[host][key]; ok && host != "" {
		return v
	}
	if v, ok := c.data.Global[key]; ok {
		return v
	}
	return defaults[key]
}

// ProjectScope is the hosts-map key of per-project settings such as team:
// dev.azure.com/ORG/PROJECT, lowercased since Azure DevOps names are case-insensitive. Pass it
// as the host to Get and Set.
func ProjectScope(org, project string) string {
	return strings.ToLower("dev.azure.com/" + org + "/" + project)
}

// Set stores key under host, or globally when host is "". extension_sources is always global.
func (c *Config) Set(host, key, value string) {
	if key == ExtensionSourcesKey {
		c.data.ExtensionSources = SplitList(value)
		return
	}
	if host == "" {
		if c.data.Global == nil {
			c.data.Global = map[string]string{}
		}
		c.data.Global[key] = value
		return
	}
	if c.data.Hosts == nil {
		c.data.Hosts = map[string]map[string]string{}
	}
	if c.data.Hosts[host] == nil {
		c.data.Hosts[host] = map[string]string{}
	}
	c.data.Hosts[host][key] = value
}

// Unset removes key from host, or globally when host is "".
func (c *Config) Unset(host, key string) {
	if host == "" {
		delete(c.data.Global, key)
		return
	}
	delete(c.data.Hosts[host], key)
	if len(c.data.Hosts[host]) == 0 {
		delete(c.data.Hosts, host)
	}
}

// Hosts returns the configured hosts, sorted.
func (c *Config) Hosts() []string {
	hosts := make([]string, 0, len(c.data.Hosts))
	for h := range c.data.Hosts {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	return hosts
}

// Aliases returns a copy of the alias map.
func (c *Config) Aliases() map[string]string { return maps.Clone(c.data.Aliases) }

// SetAlias stores an alias expansion.
func (c *Config) SetAlias(name, expansion string) {
	if c.data.Aliases == nil {
		c.data.Aliases = map[string]string{}
	}
	c.data.Aliases[name] = expansion
}

// DeleteAlias removes an alias.
func (c *Config) DeleteAlias(name string) { delete(c.data.Aliases, name) }

// ExtensionSources returns the extension_sources allowlist; empty means any source.
func (c *Config) ExtensionSources() []string { return slices.Clone(c.data.ExtensionSources) }

// SetExtensionSources replaces the extension_sources allowlist.
func (c *Config) SetExtensionSources(patterns []string) { c.data.ExtensionSources = patterns }

// Write writes the config atomically with mode 0600, since it may hold a
// plaintext token when no keychain is available.
func (c *Config) Write() error {
	b, err := yaml.Marshal(&c.data)
	if err != nil {
		return err
	}
	return WriteFileAtomic(c.path, b)
}

// WriteFileAtomic writes b to path through a temporary file renamed into place, with mode
// 0600, creating the parent directory with mode 0700.
func WriteFileAtomic(path string, b []byte) error {
	d := filepath.Dir(path)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d, filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
