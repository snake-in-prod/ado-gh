package get

import (
	"errors"
	"strings"
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

func run(cfg cmdutil.Config, cfgErr error, args ...string) (string, error) {
	ios, _, out, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios, Config: func() (cmdutil.Config, error) { return cfg, cfgErr }}
	cmd := NewCmdGet(f)
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(out)
	err := cmd.Execute()
	return out.String(), err
}

func TestGet(t *testing.T) {
	cfg := memConfig{"|editor": "vim", "dev.azure.com|editor": "code", "|org": "myorg"}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"global", []string{"editor"}, "vim\n"},
		{"host", []string{"editor", "--host", "dev.azure.com"}, "code\n"},
		{"host falls back to global", []string{"editor", "--host", "contoso.visualstudio.com"}, "vim\n"},
		{"default", []string{"prompt"}, "enabled\n"},
		{"unset", []string{"browser"}, "\n"},
		{"org", []string{"org"}, "myorg\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := run(cfg, nil, tt.args...)
			if err != nil || out != tt.want {
				t.Errorf("out = %q, err = %v; want %q", out, err, tt.want)
			}
		})
	}
}

func TestGetUnknownKey(t *testing.T) {
	_, err := run(memConfig{}, nil, "nope")
	if err == nil || !strings.Contains(err.Error(), `unknown config key "nope"`) {
		t.Errorf("err = %v", err)
	}
}

func TestGetConfigError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := run(nil, boom, "editor"); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestGetArgs(t *testing.T) {
	if _, err := run(memConfig{}, nil); err == nil {
		t.Error("want error without key")
	}
}
