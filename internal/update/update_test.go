package update

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/snake-in-prod/ado-gh/pkg/iostreams"
)

const latestPath = "repos/snake-in-prod/ado-gh/releases/latest"

// clearNotifierEnv unsets every variable that disables the notifier.
func clearNotifierEnv(t *testing.T) {
	t.Helper()
	for _, k := range disableEnv {
		t.Setenv(k, "")
	}
}

func ttyStreams() (*iostreams.IOStreams, *bytes.Buffer) {
	ios, _, _, errOut := iostreams.Test()
	ios.SetStdoutTTY(true)
	ios.SetStderrTTY(true)
	return ios, errOut
}

func releaseStub(reg *httpmock.Registry, tag string) {
	reg.Register(httpmock.REST("GET", latestPath), httpmock.JSONResponse(map[string]any{"tag_name": tag}))
}

func TestEnabled(t *testing.T) {
	tests := []struct {
		name           string
		env            string
		version        string
		stdout, stderr bool
		want           bool
	}{
		{name: "tty release build", version: "1.2.3", stdout: true, stderr: true, want: true},
		{name: "dev build", version: "DEV", stdout: true, stderr: true},
		{name: "stdout not tty", version: "1.2.3", stderr: true},
		{name: "stderr not tty", version: "1.2.3", stdout: true},
		{name: "opt out", env: "ADO_NO_UPDATE_NOTIFIER", version: "1.2.3", stdout: true, stderr: true},
		{name: "inside an extension", env: "ADO_EXTENSION_NAME", version: "1.2.3", stdout: true, stderr: true},
		{name: "CI", env: "CI", version: "1.2.3", stdout: true, stderr: true},
		{name: "azure pipelines", env: "TF_BUILD", version: "1.2.3", stdout: true, stderr: true},
		{name: "jenkins", env: "BUILD_NUMBER", version: "1.2.3", stdout: true, stderr: true},
		{name: "taskcluster", env: "RUN_ID", version: "1.2.3", stdout: true, stderr: true},
		{name: "github actions", env: "GITHUB_ACTIONS", version: "1.2.3", stdout: true, stderr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearNotifierEnv(t)
			if tt.env != "" {
				t.Setenv(tt.env, "True")
			}
			ios, _, _, _ := iostreams.Test()
			ios.SetStdoutTTY(tt.stdout)
			ios.SetStderrTTY(tt.stderr)
			assert.Equal(t, tt.want, Enabled(ios, tt.version))
		})
	}
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name    string
		current string
		tag     string
		want    string // newer version offered, "" for none
	}{
		{name: "newer", current: "1.2.3", tag: "v1.3.0", want: "1.3.0"},
		{name: "numeric not lexical", current: "1.9.0", tag: "v1.10.0", want: "1.10.0"},
		{name: "current with v prefix", current: "v1.2.3", tag: "v1.3.0", want: "1.3.0"},
		{name: "tag without v prefix", current: "1.2.3", tag: "1.3.0", want: "1.3.0"},
		{name: "same", current: "1.3.0", tag: "v1.3.0"},
		{name: "older", current: "2.0.0", tag: "v1.3.0"},
		{name: "rc never offered", current: "1.2.3", tag: "v1.3.0-rc.1"},
		{name: "stable newer than running rc", current: "1.3.0-rc.1", tag: "v1.3.0", want: "1.3.0"},
		{name: "pseudo-version past the tag", current: "1.3.1-0.20261010080000-abcdefabcdef", tag: "v1.3.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "state.yml")
			reg := &httpmock.Registry{}
			releaseStub(reg, tt.tag)
			rel, err := Check(context.Background(), &http.Client{Transport: reg}, file, tt.current, time.Now())
			require.NoError(t, err)
			reg.Verify(t)
			if tt.want == "" {
				assert.Nil(t, rel)
				return
			}
			require.NotNil(t, rel)
			assert.Equal(t, Release{Version: tt.want, URL: "https://github.com/snake-in-prod/ado-gh/releases/tag/v" + tt.want}, *rel)
		})
	}
}

func TestCheckOncePerDay(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state", "state.yml")
	now := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	reg := &httpmock.Registry{}
	hc := &http.Client{Transport: reg}

	releaseStub(reg, "v1.3.0")
	rel, err := Check(context.Background(), hc, file, "1.2.3", now)
	require.NoError(t, err)
	require.NotNil(t, rel)

	// Within 24 h: no request, no notice (once a day).
	rel, err = Check(context.Background(), hc, file, "1.2.3", now.Add(23*time.Hour))
	require.NoError(t, err)
	assert.Nil(t, rel)
	assert.Len(t, reg.Requests, 1)

	// After 24 h: asks again, and notifies as soon as releases/latest changed.
	releaseStub(reg, "v1.4.0")
	rel, err = Check(context.Background(), hc, file, "1.2.3", now.Add(24*time.Hour))
	require.NoError(t, err)
	require.NotNil(t, rel)
	assert.Equal(t, "1.4.0", rel.Version)
	assert.Len(t, reg.Requests, 2)
}

func TestCheckFailuresRetryNextCommand(t *testing.T) {
	tests := []struct {
		name string
		resp httpmock.Responder
	}{
		{"server error", httpmock.StatusStringResponse(http.StatusInternalServerError, "")},
		{"rate limited", httpmock.WithHeader(httpmock.StatusStringResponse(http.StatusForbidden, ""), "X-RateLimit-Remaining", "0")},
		{"no release", httpmock.StatusStringResponse(http.StatusNotFound, "")},
		{"invalid tag", httpmock.JSONResponse(map[string]any{"tag_name": "nightly\x1b[2J"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "state.yml")
			now := time.Now()
			reg := &httpmock.Registry{}
			hc := &http.Client{Transport: reg}
			reg.Register(httpmock.REST("GET", latestPath), tt.resp)

			rel, err := Check(context.Background(), hc, file, "1.2.3", now)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "\x1b")
			assert.Nil(t, rel)
			assert.NoFileExists(t, file, "checked_for_update_at is written only after a successful check")

			releaseStub(reg, "v1.3.0")
			rel, err = Check(context.Background(), hc, file, "1.2.3", now.Add(time.Minute))
			require.NoError(t, err)
			assert.NotNil(t, rel)
			reg.Verify(t)
		})
	}
}

func TestCheckSkipsInvalidCurrent(t *testing.T) {
	for _, v := range []string{"DEV", "", "(devel)"} {
		reg := &httpmock.Registry{}
		rel, err := Check(context.Background(), &http.Client{Transport: reg}, filepath.Join(t.TempDir(), "state.yml"), v, time.Now())
		assert.NoError(t, err)
		assert.Nil(t, rel)
		assert.Empty(t, reg.Requests, v)
	}
}

func TestCheckBrokenStateFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.yml")
	require.NoError(t, os.WriteFile(file, []byte("checked_for_update_at: [nope"), 0o600))
	reg := &httpmock.Registry{}
	releaseStub(reg, "v1.3.0")
	rel, err := Check(context.Background(), &http.Client{Transport: reg}, file, "1.2.3", time.Now())
	require.NoError(t, err)
	assert.NotNil(t, rel)
}

func TestStateFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "state.yml")
	now := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	reg := &httpmock.Registry{}
	releaseStub(reg, "v1.3.0")
	_, err := Check(context.Background(), &http.Client{Transport: reg}, file, "1.2.3", now)
	require.NoError(t, err)

	b, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, "checked_for_update_at: 2026-10-12T08:00:00Z\nlatest_release:\n"+
		"    version: 1.3.0\n    url: https://github.com/snake-in-prod/ado-gh/releases/tag/v1.3.0\n", string(b))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temporary file left behind")
}

func TestStartGitHubToken(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		wantAuth string
	}{
		{"unauthenticated", nil, ""},
		{"GH_TOKEN", map[string]string{"GH_TOKEN": "gh1", "GITHUB_TOKEN": "gh2"}, "Bearer gh1"},
		{"GITHUB_TOKEN", map[string]string{"GITHUB_TOKEN": "gh2"}, "Bearer gh2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearNotifierEnv(t)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv("GH_TOKEN", tt.env["GH_TOKEN"])
			t.Setenv("GITHUB_TOKEN", tt.env["GITHUB_TOKEN"])
			t.Setenv("ADO_TOKEN", "ado-secret")
			reg := &httpmock.Registry{}
			releaseStub(reg, "v1.3.0")
			ios, _ := ttyStreams()

			r := <-Start(context.Background(), ios, "1.2.3", reg)
			require.NoError(t, r.Err)
			require.NotNil(t, r.Release)
			require.Len(t, reg.Requests, 1)
			req := reg.Requests[0]
			assert.Equal(t, "api.github.com", req.URL.Host)
			assert.Equal(t, tt.wantAuth, req.Header.Get("Authorization"))
			assert.Equal(t, "ado-cli/1.2.3", req.Header.Get("User-Agent"))
			assert.FileExists(t, filepath.Join(os.Getenv("XDG_STATE_HOME"), "ado", "state.yml"))
		})
	}
}

func TestStartDisabled(t *testing.T) {
	clearNotifierEnv(t)
	t.Setenv("ADO_NO_UPDATE_NOTIFIER", "1")
	ios, _ := ttyStreams()
	reg := &httpmock.Registry{}
	assert.Nil(t, Start(context.Background(), ios, "1.2.3", reg))
	assert.Empty(t, reg.Requests)
}

func TestPrint(t *testing.T) {
	rel := &Release{Version: "1.3.0", URL: "https://github.com/snake-in-prod/ado-gh/releases/tag/v1.3.0"}
	tests := []struct {
		name    string
		result  *Result // nil: never arrives
		debug   bool
		wantOut string
	}{
		{name: "newer release", result: &Result{Release: rel}, wantOut: "A new release of ado is available: 1.2.3 → 1.3.0"},
		{name: "up to date", result: &Result{}},
		{name: "error silent", result: &Result{Err: assert.AnError}},
		{name: "error with debug", result: &Result{Err: assert.AnError}, debug: true, wantOut: "warning: checking for update failed: " + assert.AnError.Error()},
		{name: "timeout silent", result: nil},
		{name: "timeout with debug", result: nil, debug: true, wantOut: "checking for update failed: context deadline exceeded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, errOut := ttyStreams()
			ch := make(chan Result, 1)
			if tt.result != nil {
				ch <- *tt.result
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			start := time.Now()
			Print(ctx, ios, ch, "1.2.3", tt.debug)
			assert.Less(t, time.Since(start), time.Second, "never blocks past the deadline")
			if tt.wantOut == "" {
				assert.Empty(t, errOut.String())
			} else {
				assert.Contains(t, errOut.String(), tt.wantOut)
			}
		})
	}
	t.Run("disabled", func(t *testing.T) {
		ios, errOut := ttyStreams()
		Print(context.Background(), ios, nil, "1.2.3", true) // a nil channel never blocks
		assert.Empty(t, errOut.String())
	})
}

func TestMessage(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	rel := &Release{Version: "1.3.0", URL: "https://github.com/snake-in-prod/ado-gh/releases/tag/v1.3.0"}
	tests := []struct {
		name    string
		upgrade string
		want    string
	}{
		{"homebrew", "brew upgrade --cask ado", "To upgrade, run: brew upgrade --cask ado\n"},
		{"scoop", "scoop update ado", "To upgrade, run: scoop update ado\n"},
		{"winget", "winget upgrade --id SnakeInProd.Ado", "To upgrade, run: winget upgrade --id SnakeInProd.Ado\n"},
		{"go install", "go install github.com/snake-in-prod/ado-gh/cmd/ado@latest", "To upgrade, run: go install github.com/snake-in-prod/ado-gh/cmd/ado@latest\n"},
		{"unknown: release URL only", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Message(ios.ColorScheme(), "v1.2.3", rel, tt.upgrade)
			assert.Equal(t, "\nA new release of ado is available: 1.2.3 → 1.3.0\n"+tt.want+rel.URL+"\n", got)
		})
	}
}
