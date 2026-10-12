// Package scripts holds tests for the repository's shell scripts.
package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// git runs git in dir with the user's global and system config ignored.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestReleaseGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release-guard.sh runs on the ubuntu release runner")
	}
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	script, err := filepath.Abs("release-guard.sh")
	if err != nil {
		t.Fatal(err)
	}

	// origin/main is the first commit. v9.9.9 tags a commit on another branch,
	// v8.8.8 a local main commit that was never pushed.
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "first")
	git(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	git(t, repo, "checkout", "-q", "-b", "side")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "side")
	git(t, repo, "tag", "v9.9.9", "side")
	git(t, repo, "checkout", "-q", "main")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "not pushed")
	git(t, repo, "tag", "v8.8.8", "HEAD")

	type testCase struct {
		tag     string
		wantErr string // empty: accepted
	}
	tests := []testCase{
		{tag: "v1.2", wantErr: "not a release tag"},
		{tag: "v1.2.3.4", wantErr: "not a release tag"},
		{tag: "1.2.3", wantErr: "not a release tag"},
		{tag: "V1.2.3", wantErr: "not a release tag"},
		{tag: "release-1.2.3", wantErr: "not a release tag"},
		{tag: "v01.2.3", wantErr: "not a release tag"},
		{tag: "v1.02.3", wantErr: "not a release tag"},
		{tag: "v1.2.03", wantErr: "not a release tag"},
		{tag: "v1.2.3-rc.01", wantErr: "not a release tag"},
		{tag: "v1.2.3+build", wantErr: "not a release tag"},
		{tag: "v1.2.3-rc.1+build.5", wantErr: "not a release tag"},
		{tag: "v1.2.3-", wantErr: "not a release tag"},
		{tag: "v1.2.3-rc..1", wantErr: "not a release tag"},
		{tag: "v1.2.3-rc_1", wantErr: "not a release tag"},
		{tag: "v1.2.3\nv1.2.4", wantErr: "not a release tag"},
		{tag: "", wantErr: "not a release tag"},
		{tag: "v7.7.7", wantErr: "no tag"},
		{tag: "v9.9.9", wantErr: "not on origin/main"},
		{tag: "v8.8.8", wantErr: "not on origin/main"},
	}
	for _, tag := range []string{
		"v0.1.0", "v1.2.3", "v10.20.30", "v0.1.0-rc.1", "v1.0.0-alpha.beta",
		"v1.0.0-alpha-1", "v1.2.3-0a", "v1.2.3--", "v1.0.0-0",
	} {
		// Annotated, as release tags are: the guard must peel them to the commit.
		git(t, repo, "tag", "-a", "-m", tag, tag, "origin/main")
		tests = append(tests, testCase{tag: tag})
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			cmd := exec.Command("bash", script, tt.tag)
			cmd.Dir = repo
			out, err := cmd.CombinedOutput()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("rejected %q: %v\n%s", tt.tag, err, out)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted %q:\n%s", tt.tag, out)
			}
			if !strings.Contains(string(out), tt.wantErr) {
				t.Errorf("output %q does not contain %q", out, tt.wantErr)
			}
		})
	}
}
