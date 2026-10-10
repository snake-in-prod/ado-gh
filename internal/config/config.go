// Package config reads and writes ado's YAML config file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"

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

// Get returns the host value, else the global value, else the default.
// Pass host "" for global settings.
func (c *Config) Get(host, key string) string {
	if v, ok := c.data.Hosts[host][key]; ok && host != "" {
		return v
	}
	if v, ok := c.data.Global[key]; ok {
		return v
	}
	return defaults[key]
}

// Set stores key under host, or globally when host is "".
func (c *Config) Set(host, key, value string) {
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

// Write writes the config atomically with mode 0600, since it may hold a
// plaintext token when no keychain is available.
func (c *Config) Write() error {
	b, err := yaml.Marshal(&c.data)
	if err != nil {
		return err
	}
	d := filepath.Dir(c.path)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d, fileName+".*")
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
	return os.Rename(tmp.Name(), c.path)
}
