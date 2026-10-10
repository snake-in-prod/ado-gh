package gittest

import (
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
