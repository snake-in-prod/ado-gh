package version

import (
	"encoding/json"
	"runtime"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/build"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

func TestFormat(t *testing.T) {
	platform := " · " + runtime.Version() + " · " + runtime.GOOS + "/" + runtime.GOARCH + "\n"
	tests := []struct {
		name string
		info build.Info
		want string
	}{
		{"release", build.Info{Version: "1.2.3", Commit: "abc1234def", Date: "2026-10-10T08:00:00Z"},
			"ado version 1.2.3 (2026-10-10)\ncommit abc1234" + platform +
				"https://github.com/snake-in-prod/ado-gh/releases/tag/v1.2.3\n"},
		{"dirty", build.Info{Version: "1.2.3", Commit: "abc1234def", Date: "2026-10-10T08:00:00Z", Dirty: true},
			"ado version 1.2.3 (2026-10-10)\ncommit abc1234-dirty" + platform +
				"https://github.com/snake-in-prod/ado-gh/releases/tag/v1.2.3\n"},
		{"no commit no date", build.Info{Version: "1.2.3"},
			"ado version 1.2.3\nhttps://github.com/snake-in-prod/ado-gh/releases/tag/v1.2.3\n"},
		{"dev omits url", build.Info{Version: "DEV", Commit: "abc1234", Date: "2026-10-10T08:00:00Z"},
			"ado version DEV (2026-10-10)\ncommit abc1234" + platform},
		{"pseudo-version omits url", build.Info{Version: "0.0.0-20261001120000-0123456789ab", Commit: "0123456789ab", Date: "2026-10-01T12:00:00Z"},
			"ado version 0.0.0-20261001120000-0123456789ab (2026-10-01)\ncommit 0123456" + platform},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := format(tt.info); got != tt.want {
				t.Errorf("format() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func runVersionCmd(t *testing.T, args ...string) string {
	t.Helper()
	oldCommit, oldDate, oldDirty := build.Commit, build.Date, build.Dirty
	build.Commit, build.Date, build.Dirty = "abc1234def", "2026-10-10T08:00:00Z", true
	t.Cleanup(func() { build.Commit, build.Date, build.Dirty = oldCommit, oldDate, oldDirty })
	ios, _, out, _ := iostreams.Test()
	cmd := NewCmdVersion(&cmdutil.Factory{IOStreams: ios, AppVersion: "1.2.3"})
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestVersionCmd(t *testing.T) {
	want := format(build.Info{Version: "1.2.3", Commit: "abc1234def", Date: "2026-10-10T08:00:00Z", Dirty: true})
	if got := runVersionCmd(t); got != want {
		t.Errorf("out = %q, want %q", got, want)
	}
}

func TestVersionCmdJSON(t *testing.T) {
	var got map[string]any
	if err := json.Unmarshal([]byte(runVersionCmd(t, "--json", "version,commit,date,dirty,goVersion,os,arch")), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"version": "1.2.3", "commit": "abc1234def", "date": "2026-10-10T08:00:00Z", "dirty": true,
		"goVersion": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH}
	if len(got) != len(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestText(t *testing.T) {
	f := &cmdutil.Factory{AppVersion: "1.2.3"}
	want := format(build.Info{Version: "1.2.3", Commit: build.Commit, Date: build.Date, Dirty: build.Dirty})
	if got := Text(f); got != want {
		t.Errorf("Text = %q, want %q", got, want)
	}
}
