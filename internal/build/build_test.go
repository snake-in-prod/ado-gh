package build

import (
	"runtime/debug"
	"testing"
)

func buildSettings(kv ...string) []debug.BuildSetting {
	var s []debug.BuildSetting
	for i := 0; i < len(kv); i += 2 {
		s = append(s, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
	}
	return s
}

func TestResolve(t *testing.T) {
	local := buildSettings("vcs", "git", "vcs.revision", "0123456789abcdef0123456789abcdef01234567", "vcs.time", "2026-10-01T12:00:00Z", "vcs.modified", "false")
	tests := []struct {
		name                        string
		ldVersion, ldCommit, ldDate string
		mainVersion                 string
		settings                    []debug.BuildSetting
		noInfo                      bool
		want                        Info
	}{
		{name: "ldflags win", ldVersion: "1.2.3", ldCommit: "abc", ldDate: "2026-10-10T00:00:00Z", mainVersion: "v9.9.9", settings: local,
			want: Info{Version: "1.2.3", Commit: "abc", Date: "2026-10-10T00:00:00Z"}},
		{name: "ldflags trim v", ldVersion: "v1.2.3", want: Info{Version: "1.2.3"}},
		{name: "no build info", noInfo: true, want: Info{Version: "DEV"}},
		{name: "no build info keeps ldflags", noInfo: true, ldVersion: "1.0.0", ldCommit: "c", want: Info{Version: "1.0.0", Commit: "c"}},
		{name: "local tagged clean", mainVersion: "v1.2.3", settings: local,
			want: Info{Version: "DEV", Commit: "0123456789abcdef0123456789abcdef01234567", Date: "2026-10-01T12:00:00Z"}},
		{name: "local tagged dirty", mainVersion: "v1.2.3+dirty", settings: buildSettings("vcs", "git", "vcs.revision", "abc", "vcs.time", "2026-10-01T12:00:00Z", "vcs.modified", "true"),
			want: Info{Version: "DEV", Commit: "abc", Date: "2026-10-01T12:00:00Z", Dirty: true}},
		{name: "local pseudo not parsed", mainVersion: "v1.2.4-0.20261001120000-0123456789ab", settings: buildSettings("vcs", "git"),
			want: Info{Version: "DEV"}},
		{name: "ldflags with dirty tree", ldVersion: "1.2.3", mainVersion: "v1.2.3", settings: buildSettings("vcs", "git", "vcs.modified", "true"),
			want: Info{Version: "1.2.3", Dirty: true}},
		{name: "devel", mainVersion: "(devel)", want: Info{Version: "DEV"}},
		{name: "empty main version", want: Info{Version: "DEV"}},
		{name: "go install tag", mainVersion: "v1.2.3", want: Info{Version: "1.2.3"}},
		{name: "go install pseudo", mainVersion: "v0.0.0-20261001120000-0123456789ab",
			want: Info{Version: "0.0.0-20261001120000-0123456789ab", Commit: "0123456789ab", Date: "2026-10-01T12:00:00Z"}},
		{name: "go install pseudo after tag", mainVersion: "v1.2.4-0.20261001120000-0123456789ab",
			want: Info{Version: "1.2.4-0.20261001120000-0123456789ab", Commit: "0123456789ab", Date: "2026-10-01T12:00:00Z"}},
		{name: "go install prerelease pseudo", mainVersion: "v1.2.4-rc.1.0.20261001120000-0123456789ab",
			want: Info{Version: "1.2.4-rc.1.0.20261001120000-0123456789ab", Commit: "0123456789ab", Date: "2026-10-01T12:00:00Z"}},
		{name: "go install prerelease", mainVersion: "v1.2.4-rc.1", want: Info{Version: "1.2.4-rc.1"}},
		{name: "ldflags commit wins over pseudo", ldCommit: "full", mainVersion: "v0.0.0-20261001120000-0123456789ab",
			want: Info{Version: "0.0.0-20261001120000-0123456789ab", Commit: "full", Date: "2026-10-01T12:00:00Z"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read := func() (*debug.BuildInfo, bool) {
				if tt.noInfo {
					return nil, false
				}
				bi := &debug.BuildInfo{Settings: tt.settings}
				bi.Main.Version = tt.mainVersion
				return bi, true
			}
			if got := resolve(tt.ldVersion, tt.ldCommit, tt.ldDate, read); got != tt.want {
				t.Errorf("resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestInitUnderTest(t *testing.T) {
	// Test binaries carry no ldflags, no vcs.* settings and no real Main.Version.
	if Version != "DEV" || Dirty || Stamped {
		t.Errorf("Version = %q, Dirty = %v, Stamped = %v; want DEV, false, false", Version, Dirty, Stamped)
	}
}
