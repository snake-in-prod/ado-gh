package main

import (
	"strings"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/build"
	"github.com/snake-in-prod/ado-gh/internal/config"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

// stubUpdateCheck enables run's update check for a stamped build 1.0.0 and answers
// releases/latest with tag (no stub when tag is ""), with a fresh state dir.
func stubUpdateCheck(t *testing.T, tag string) *httpmock.Registry {
	t.Helper()
	for _, k := range []string{"ADO_NO_UPDATE_NOTIFIER", "ADO_EXTENSION_NAME", "CI", "TF_BUILD",
		"BUILD_NUMBER", "RUN_ID", "GITHUB_ACTIONS", "GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(k, "")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	oldVersion, oldTransport := build.Version, updateTransport
	t.Cleanup(func() { build.Version, updateTransport = oldVersion, oldTransport })
	build.Version = "1.0.0"
	reg := &httpmock.Registry{}
	if tag != "" {
		reg.Register(httpmock.REST("GET", "repos/snake-in-prod/ado-gh/releases/latest"),
			httpmock.JSONResponse(map[string]any{"tag_name": tag}))
	}
	updateTransport = reg
	return reg
}

func ttyTestStreams() (*iostreams.IOStreams, *strings.Builder) {
	ios, _, _, _ := iostreams.Test()
	ios.SetStdoutTTY(true)
	ios.SetStderrTTY(true)
	errOut := &strings.Builder{}
	ios.ErrOut = errOut
	return ios, errOut
}

func TestRunUpdateNotifier(t *testing.T) {
	const notice = "A new release of ado is available: 1.0.0 → 1.1.0"
	tests := []struct {
		name       string
		args       []string
		env        map[string]string
		tag        string
		wantCode   cmdutil.ExitCode
		wantNotice string // "" means stderr has no notice
		wantCalls  int
	}{
		{name: "after a command", args: []string{"version"}, tag: "v1.1.0", wantNotice: notice, wantCalls: 1},
		{name: "up to date", args: []string{"version"}, tag: "v1.0.0", wantCalls: 1},
		{name: "not after a failed command", args: []string{"nope"}, tag: "v1.1.0", wantCode: cmdutil.ExitError, wantCalls: 1},
		{name: "after an alias", args: []string{"ver"}, tag: "v1.1.0", wantNotice: notice, wantCalls: 1},
		{name: "after an extension", args: []string{"hello"}, env: map[string]string{"ADO_FAKE_HELLO": "echo"},
			tag: "v1.1.0", wantNotice: notice, wantCalls: 1},
		{name: "not after a failed extension", args: []string{"hello"}, env: map[string]string{"ADO_FAKE_HELLO": "1"},
			tag: "v1.1.0", wantCode: 3, wantCalls: 1},
		{name: "quiet inside an extension", args: []string{"version"}, env: map[string]string{"ADO_EXTENSION_NAME": "x"}, tag: "v1.1.0"},
		{name: "opted out", args: []string{"version"}, env: map[string]string{"ADO_NO_UPDATE_NOTIFIER": "1"}, tag: "v1.1.0"},
		{name: "azure pipelines", args: []string{"version"}, env: map[string]string{"TF_BUILD": "True"}, tag: "v1.1.0"},
		{name: "failure silent", args: []string{"version"}, wantCalls: 1},
		{name: "failure shown with --debug", args: []string{"--debug", "version"},
			wantNotice: "warning: checking for update failed", wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeExtension(t, isolate(t), "hello")
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			cfg.SetAlias("ver", "version")
			if err := cfg.Write(); err != nil {
				t.Fatal(err)
			}
			reg := stubUpdateCheck(t, tt.tag)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			ios, errOut := ttyTestStreams()

			if got := run(tt.args, ios); got != tt.wantCode {
				t.Errorf("code = %d, want %d, stderr = %q", got, tt.wantCode, errOut)
			}
			if tt.wantNotice == "" && strings.Contains(errOut.String(), "new release") {
				t.Errorf("stderr = %q, want no notice", errOut)
			}
			if !strings.Contains(errOut.String(), tt.wantNotice) {
				t.Errorf("stderr = %q, want %q", errOut, tt.wantNotice)
			}
			if len(reg.Requests) != tt.wantCalls {
				t.Errorf("requests = %d, want %d", len(reg.Requests), tt.wantCalls)
			}
		})
	}
}

func TestRunDevBuildSkipsUpdateCheck(t *testing.T) {
	isolate(t)
	reg := stubUpdateCheck(t, "v1.1.0")
	build.Version = "DEV"
	ios, errOut := ttyTestStreams()
	if got := run([]string{"version"}, ios); got != cmdutil.ExitOK || len(reg.Requests) != 0 || errOut.Len() != 0 {
		t.Errorf("code = %d, requests = %d, stderr = %q", got, len(reg.Requests), errOut)
	}
}
