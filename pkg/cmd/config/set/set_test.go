package set

import (
	"errors"
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

type fakeConfig struct {
	values   map[string]string
	writes   int
	writeErr error
}

func (c *fakeConfig) Get(host, key string) string { return c.values[host+"|"+key] }

func (c *fakeConfig) Set(host, key, value string) { c.values[host+"|"+key] = value }

func (c *fakeConfig) Write() error {
	c.writes++
	return c.writeErr
}

func run(cfg cmdutil.Config, cfgErr error, args ...string) (string, error) {
	ios, _, out, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios, Config: func() (cmdutil.Config, error) { return cfg, cfgErr }}
	cmd := NewCmdSet(f)
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(out)
	err := cmd.Execute()
	return out.String(), err
}

func TestSet(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantKey string
		wantVal string
	}{
		{"global", []string{"editor", "vim -n"}, "|editor", "vim -n"},
		{"org", []string{"org", "contoso"}, "|org", "contoso"},
		{"host", []string{"prompt", "disabled", "--host", "dev.azure.com"}, "dev.azure.com|prompt", "disabled"},
		{"git_protocol host", []string{"git_protocol", "ssh", "--host", "ssh.dev.azure.com"}, "ssh.dev.azure.com|git_protocol", "ssh"},
		{"other host", []string{"browser", "firefox", "--host", "contoso.visualstudio.com"}, "contoso.visualstudio.com|browser", "firefox"},
		{"team global", []string{"team", "Web Team"}, "|team", "Web Team"},
		{"team per project", []string{"team", "Web Team", "--project", "Contoso/Fabrikam"}, "dev.azure.com/contoso/fabrikam|team", "Web Team"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &fakeConfig{values: map[string]string{}}
			out, err := run(cfg, nil, tt.args...)
			if err != nil || out != "" {
				t.Fatalf("out = %q, err = %v", out, err)
			}
			if got := cfg.values[tt.wantKey]; got != tt.wantVal || cfg.writes != 1 {
				t.Errorf("%s = %q (writes %d); want %q", tt.wantKey, got, cfg.writes, tt.wantVal)
			}
		})
	}
}

func TestSetErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name    string
		cfg     *fakeConfig
		cfgErr  error
		args    []string
		wantErr string
	}{
		{"unknown key", &fakeConfig{}, nil, []string{"nope", "x"}, `unknown config key "nope"`},
		{"invalid value", &fakeConfig{}, nil, []string{"prompt", "maybe"}, `invalid value "maybe" for prompt; valid values: enabled, disabled`},
		{"invalid git_protocol", &fakeConfig{}, nil, []string{"git_protocol", "git"}, `invalid value "git" for git_protocol; valid values: https, ssh`},
		{"config error", nil, boom, []string{"editor", "vim"}, "boom"},
		{"write error", &fakeConfig{values: map[string]string{}, writeErr: boom}, nil, []string{"editor", "vim"}, "failed to write config: boom"},
		{"missing value", &fakeConfig{}, nil, []string{"editor"}, "accepts 2 arg(s)"},
		{"bad project", &fakeConfig{}, nil, []string{"team", "T", "--project", "proj"}, `invalid --project "proj": expected ORG/PROJECT`},
		{"project for a host key", &fakeConfig{}, nil, []string{"editor", "vim", "--project", "o/p"}, "--project is only supported for: team"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg cmdutil.Config
			if tt.cfg != nil {
				cfg = tt.cfg
			}
			_, err := run(cfg, tt.cfgErr, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v; want %q", err, tt.wantErr)
			}
			if tt.cfg != nil && tt.cfg.writeErr == nil && tt.cfg.writes != 0 {
				t.Error("config written despite error")
			}
		})
	}
}
