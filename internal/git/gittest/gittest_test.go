package gittest

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunner(t *testing.T) {
	boom := errors.New("boom")
	r := &Runner{
		Replies: map[string]string{"remote -v": "origin\turl (fetch)\n", "rev-parse --verify main": "abc\n"},
		Fails:   map[string]error{"fetch origin": boom},
	}
	c := &git.Client{Run: r.Run}
	ctx := context.Background()

	out, err := c.Exec(ctx, "remote", "-v")
	require.NoError(t, err)
	assert.Equal(t, "origin\turl (fetch)\n", string(out))

	out, err = c.Exec(ctx, "rev-parse", "--verify", "main")
	require.NoError(t, err)
	assert.Equal(t, "abc\n", string(out))

	_, err = c.Exec(ctx, "fetch", "origin")
	assert.ErrorIs(t, err, boom)

	// Unanswered queries fail with exit 1, like git for a missing ref, branch or config key.
	for _, args := range [][]string{
		{"rev-parse", "--verify", "nope"},
		{"symbolic-ref", "--quiet", "HEAD"},
		{"config", "--get", "branch.x.merge"},
	} {
		_, err := c.Exec(ctx, args...)
		var gitErr *git.Error
		require.ErrorAs(t, err, &gitErr, "%v", args)
		assert.Equal(t, 1, gitErr.ExitCode)
		assert.Equal(t, args, gitErr.Args)
	}
	_, err = c.CurrentBranch(ctx)
	assert.ErrorIs(t, err, git.ErrNotOnAnyBranch)

	// Unanswered commands succeed with no output.
	for _, args := range [][]string{
		{"checkout", "main"}, {"config", "branch.x.merge", "refs/heads/x"}, {"branch", "-D", "x"},
		{"push", "origin", "x"}, {"merge", "--ff-only"}, {"reset", "--hard"}, {"branch", "--list"}, {"status"},
	} {
		out, err := c.Exec(ctx, args...)
		require.NoError(t, err, "%v", args)
		assert.Empty(t, out)
	}

	assert.Equal(t, []string{
		"remote -v", "rev-parse --verify main", "fetch origin",
		"rev-parse --verify nope", "symbolic-ref --quiet HEAD", "config --get branch.x.merge", "symbolic-ref --quiet HEAD",
		"checkout main", "config branch.x.merge refs/heads/x", "branch -D x",
		"push origin x", "merge --ff-only", "reset --hard", "branch --list", "status",
	}, r.Calls)
	assert.Equal(t, []string{
		"fetch origin", "checkout main", "config branch.x.merge refs/heads/x", "branch -D x",
		"push origin x", "merge --ff-only", "reset --hard",
	}, r.Mutations())
}

func TestRunnerZeroValue(t *testing.T) {
	var r Runner
	_, err := r.Run(context.Background(), "", "config", "--get", "x")
	require.Error(t, err)
	_, err = r.Run(context.Background(), "", "config", "--get-regexp", "^branch")
	require.NoError(t, err, "only --get fails when unanswered")
	out, err := r.Run(context.Background(), "", "fetch")
	require.NoError(t, err)
	assert.Empty(t, out)
	assert.Equal(t, []string{"fetch"}, r.Mutations())
}

func TestRunnerStreamAndDirs(t *testing.T) {
	r := &Runner{
		Replies: map[string]string{"rev-parse --git-dir": ".git\n"},
		Fails: map[string]error{
			"clone -- u d": &git.Error{Args: []string{"clone", "--", "u", "d"}, ExitCode: 128, Stderr: "fatal: nope\n"},
		},
	}
	ctx := context.Background()
	var out, errOut bytes.Buffer
	s := git.Streams{Out: &out, ErrOut: &errOut}

	err := (&git.Client{Dir: "x", Stream: r.Stream}).ExecStream(ctx, s, "clone", "--", "u", "d")
	var gitErr *git.Error
	require.ErrorAs(t, err, &gitErr)
	assert.Equal(t, 128, gitErr.ExitCode)
	assert.Empty(t, gitErr.Stderr, "stderr is streamed, not kept")
	assert.Equal(t, "fatal: nope\n", errOut.String())

	require.NoError(t, (&git.Client{Dir: "d", Stream: r.Stream}).ExecStream(ctx, s, "rev-parse", "--git-dir"))
	assert.Equal(t, ".git\n", out.String())
	_, err = (&git.Client{Run: r.Run}).Exec(ctx, "status")
	require.NoError(t, err)

	assert.Equal(t, []string{"clone -- u d", "rev-parse --git-dir", "status"}, r.Calls)
	assert.Equal(t, []string{"x", "d", ""}, r.Dirs)

	boom := errors.New("boom")
	r.Fails["fetch"] = boom
	require.ErrorIs(t, r.Stream(ctx, "", git.Streams{}, "fetch"), boom)
	require.NoError(t, r.Stream(ctx, "", git.Streams{}, "rev-parse", "--git-dir"), "nil streams are skipped")
	require.Error(t, r.Stream(ctx, "", git.Streams{}, "clone", "--", "u", "d"), "nil ErrOut is skipped")
}
