package config

import (
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

type memConfig map[string]string

func (m memConfig) Get(host, key string) string {
	if v, ok := m[host+"|"+key]; ok {
		return v
	}
	return m["|"+key]
}

func (m memConfig) Set(host, key, value string) { m[host+"|"+key] = value }

func (memConfig) Write() error { return nil }

func TestSetThenGet(t *testing.T) {
	ios, _, out, _ := iostreams.Test()
	cfg := memConfig{}
	f := &cmdutil.Factory{IOStreams: ios, Config: func() (cmdutil.Config, error) { return cfg, nil }}
	exec := func(args ...string) {
		t.Helper()
		cmd := NewCmdConfig(f)
		cmd.SetArgs(args)
		cmd.SetOut(out)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	exec("set", "editor", "nano")
	exec("set", "editor", "code --wait", "--host", "dev.azure.com")
	exec("get", "editor")
	exec("get", "editor", "--host", "dev.azure.com")
	if got, want := out.String(), "nano\ncode --wait\n"; got != want {
		t.Errorf("out = %q, want %q", got, want)
	}
}
