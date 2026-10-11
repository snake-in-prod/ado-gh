package shared

import (
	"strings"
	"testing"
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

func TestFind(t *testing.T) {
	if o, err := Find("prompt"); err != nil || o.Key != "prompt" {
		t.Errorf("Find(prompt) = %v, %v", o, err)
	}
	_, err := Find("nope")
	if err == nil || !strings.Contains(err.Error(), `unknown config key "nope"`) || !strings.Contains(err.Error(), "browser, editor, entra_client_id, git_protocol, org, prompt") {
		t.Errorf("err = %v", err)
	}
}

func TestValidate(t *testing.T) {
	prompt, _ := Find("prompt")
	editor, _ := Find("editor")
	gitProtocol, _ := Find("git_protocol")
	tests := []struct {
		o       Option
		v       string
		wantErr bool
	}{
		{prompt, "enabled", false},
		{prompt, "disabled", false},
		{prompt, "maybe", true},
		{editor, "anything goes", false},
		{gitProtocol, "https", false},
		{gitProtocol, "ssh", false},
		{gitProtocol, "git", true},
	}
	for _, tt := range tests {
		if err := tt.o.Validate(tt.v); (err != nil) != tt.wantErr {
			t.Errorf("%s=%q: err = %v", tt.o.Key, tt.v, err)
		}
	}
}

func TestValue(t *testing.T) {
	prompt, _ := Find("prompt")
	cfg := memConfig{}
	if got := prompt.Value(cfg, ""); got != "enabled" {
		t.Errorf("default = %q", got)
	}
	cfg.Set("dev.azure.com", "prompt", "disabled")
	if got := prompt.Value(cfg, "dev.azure.com"); got != "disabled" {
		t.Errorf("host value = %q", got)
	}
}

func TestGitProtocolValue(t *testing.T) {
	o, err := Find("git_protocol")
	if err != nil {
		t.Fatal(err)
	}
	cfg := memConfig{}
	if got := o.Value(cfg, "dev.azure.com"); got != "https" {
		t.Errorf("default = %q", got)
	}
	cfg.Set("dev.azure.com", "git_protocol", "ssh")
	if got := o.Value(cfg, "dev.azure.com"); got != "ssh" {
		t.Errorf("dev.azure.com = %q", got)
	}
	// Per-host keys are the original remote host: an SSH-derived context
	// (ssh.dev.azure.com) does not see a value set for dev.azure.com.
	if got := o.Value(cfg, "ssh.dev.azure.com"); got != "https" {
		t.Errorf("ssh.dev.azure.com = %q", got)
	}
	cfg.Set("ssh.dev.azure.com", "git_protocol", "ssh")
	if got := o.Value(cfg, "ssh.dev.azure.com"); got != "ssh" {
		t.Errorf("ssh.dev.azure.com after set = %q", got)
	}
}
