package list

import (
	"errors"
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

func run(tty bool, cfg cmdutil.Config, cfgErr error, args ...string) (string, error) {
	ios, _, out, _ := iostreams.Test()
	ios.SetStdoutTTY(tty)
	f := &cmdutil.Factory{IOStreams: ios, Config: func() (cmdutil.Config, error) { return cfg, cfgErr }}
	cmd := NewCmdList(f)
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(out)
	err := cmd.Execute()
	return out.String(), err
}

func TestList(t *testing.T) {
	cfg := memConfig{"|editor": "vim", "|org": "myorg", "dev.azure.com|prompt": "disabled"}
	tests := []struct {
		name string
		tty  bool
		args []string
		want string
	}{
		{"non-TTY", false, nil, "alias_shell=\nbrowser=\neditor=vim\nentra_client_id=\nextension_sources=\ngit_protocol=https\norg=myorg\nprompt=enabled\nteam=\n"},
		{"TTY marks unset", true, nil, "alias_shell=(not set)\nbrowser=(not set)\neditor=vim\nentra_client_id=(not set)\nextension_sources=(not set)\ngit_protocol=https\norg=myorg\nprompt=enabled\nteam=(not set)\n"},
		{"host", false, []string{"--host", "dev.azure.com"}, "alias_shell=\nbrowser=\neditor=vim\nentra_client_id=\nextension_sources=\ngit_protocol=https\norg=myorg\nprompt=disabled\nteam=\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := run(tt.tty, cfg, nil, tt.args...)
			if err != nil || out != tt.want {
				t.Errorf("out = %q, err = %v; want %q", out, err, tt.want)
			}
		})
	}
}

func TestListConfigError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := run(false, nil, boom); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}
