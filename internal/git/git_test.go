package git

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stub returns a Client whose runner answers with out/err and records the args.
func stub(out string, err error) (*Client, *[]string) {
	var got []string
	return &Client{Run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		got = args
		return []byte(out), err
	}}, &got
}

var notRepo = &Error{ExitCode: 128, Stderr: "fatal: not a git repository (or any of the parent directories): .git"}

func TestRemotes(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want []Remote
	}{
		{name: "none", out: ""},
		{
			name: "fetch and push",
			out: "origin\thttps://dev.azure.com/o/p/_git/r (fetch)\n" +
				"origin\thttps://dev.azure.com/o/p/_git/r (push)\n" +
				"upstream\tgit@ssh.dev.azure.com:v3/o/p/r (fetch)\n" +
				"upstream\tgit@ssh.dev.azure.com:v3/o/p/other (push)\n",
			want: []Remote{
				{Name: "origin", FetchURL: "https://dev.azure.com/o/p/_git/r", PushURL: "https://dev.azure.com/o/p/_git/r"},
				{Name: "upstream", FetchURL: "git@ssh.dev.azure.com:v3/o/p/r", PushURL: "git@ssh.dev.azure.com:v3/o/p/other"},
			},
		},
		{
			name: "path with spaces, partial clone filter, CRLF",
			out:  "local\t/mnt/c/my projects/repo (fetch) [blob:none]\r\nlocal\t/mnt/c/my projects/repo (push)\r\n",
			want: []Remote{{Name: "local", FetchURL: "/mnt/c/my projects/repo", PushURL: "/mnt/c/my projects/repo"}},
		},
		{
			name: "first of several push URLs kept, junk skipped",
			out:  "o\ta (fetch)\no\tb (push)\no\tc (push)\ngarbage\n",
			want: []Remote{{Name: "o", FetchURL: "a", PushURL: "b"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, args := stub(tt.out, nil)
			got, err := c.Remotes(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, []string{"remote", "-v"}, *args)
		})
	}
}

func TestCurrentBranch(t *testing.T) {
	c, args := stub("refs/heads/feature/x\n", nil)
	got, err := c.CurrentBranch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "feature/x", got)
	assert.Equal(t, []string{"symbolic-ref", "--quiet", "HEAD"}, *args)
}

func TestCurrentBranchDetached(t *testing.T) {
	c, _ := stub("", &Error{ExitCode: 1})
	_, err := c.CurrentBranch(context.Background())
	assert.ErrorIs(t, err, ErrNotOnAnyBranch)
}

func TestNotAGitRepo(t *testing.T) {
	c, _ := stub("", notRepo)
	_, err := c.Remotes(context.Background())
	assert.ErrorIs(t, err, ErrNotGitRepo)
	_, err = c.CurrentBranch(context.Background())
	assert.ErrorIs(t, err, ErrNotGitRepo)
	assert.NotErrorIs(t, &Error{ExitCode: 128, Stderr: "fatal: bad config"}, ErrNotGitRepo)
}

func TestError(t *testing.T) {
	assert.Equal(t, "fatal: boom", (&Error{Args: []string{"fetch"}, ExitCode: 128, Stderr: "fatal: boom\n"}).Error())
	assert.Equal(t, "git fetch: exit status 1", (&Error{Args: []string{"fetch"}, ExitCode: 1}).Error())
}

// TestRealGit runs the default runner against the git binary.
func TestRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", dir)
	c := &Client{Dir: dir}

	_, err := c.Remotes(ctx)
	require.ErrorIs(t, err, ErrNotGitRepo)

	_, err = c.Exec(ctx, "init", "--quiet")
	require.NoError(t, err)
	_, err = c.Exec(ctx, "symbolic-ref", "HEAD", "refs/heads/trunk")
	require.NoError(t, err)
	_, err = c.Exec(ctx, "remote", "add", "origin", "https://dev.azure.com/o/p/_git/r")
	require.NoError(t, err)

	branch, err := c.CurrentBranch(ctx)
	require.NoError(t, err)
	assert.Equal(t, "trunk", branch)

	remotes, err := c.Remotes(ctx)
	require.NoError(t, err)
	assert.Equal(t, []Remote{{Name: "origin", FetchURL: "https://dev.azure.com/o/p/_git/r", PushURL: "https://dev.azure.com/o/p/_git/r"}}, remotes)

	_, err = c.Exec(ctx, "no-such-command")
	var gerr *Error
	require.ErrorAs(t, err, &gerr)
	assert.NotZero(t, gerr.ExitCode)
	assert.NotEmpty(t, gerr.Stderr)
}

func TestMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := (&Client{}).Exec(context.Background(), "status")
	assert.True(t, errors.Is(err, exec.ErrNotFound), err)
}
