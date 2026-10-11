package comment

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// parseLines parses a --line value "<n>" or "<n>:<m>" (1 <= n <= m).
func parseLines(s string) (start, end int, err error) {
	a, b, isRange := strings.Cut(s, ":")
	start, err = strconv.Atoi(a)
	end = start
	if err == nil && isRange {
		end, err = strconv.Atoi(b)
	}
	if err != nil || start < 1 || end < start {
		return 0, 0, cmdutil.FlagErrorf("invalid value for --line: %q (want <n> or <n>:<m>, 1 <= n <= m)", s)
	}
	return start, end, nil
}

// repoPath turns p into a repository path with a leading "/": "./src\a.go" → "/src/a.go".
func repoPath(p string) string {
	return path.Clean("/" + strings.ReplaceAll(p, `\`, "/"))
}

// changePath is the current path of a changed file; Item is untyped in the SDK.
func changePath(ch git.GitPullRequestChange) string {
	m, _ := ch.Item.(map[string]any)
	p, _ := m["path"].(string)
	return p
}

// inlineContext anchors a new thread to lines o.start..o.end of o.file, on the side o.left picks
// (the only side of an added or deleted file), in the latest iteration of pull request id compared with the merge base (the Files tab).
func inlineContext(ctx context.Context, c *api.PRClient, o *options, id int) (*git.CommentThreadContext, *git.GitPullRequestCommentThreadContext, error) {
	its, err := c.Iterations(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	latest := 0
	for _, it := range its {
		latest = max(latest, shared.Deref(it.Id))
	}
	if latest == 0 {
		return nil, nil, fmt.Errorf("pull request !%d has no iterations", id)
	}
	changes, err := c.IterationChanges(ctx, id, latest)
	if err != nil {
		return nil, nil, err
	}
	want := repoPath(o.file)
	// The current path wins over a rename's original path.
	i := slices.IndexFunc(changes, func(ch git.GitPullRequestChange) bool { return changePath(ch) == want })
	if i < 0 {
		i = slices.IndexFunc(changes, func(ch git.GitPullRequestChange) bool { return shared.Deref(ch.OriginalPath) == want })
	}
	if i < 0 {
		return nil, nil, fmt.Errorf("%s is not changed in pull request !%d; list the changed files with: ado pr diff %d --name-only", o.file, id, id)
	}
	ch := changes[i]
	filePath, one := changePath(ch), 1
	// ponytail: end offset 1 (line length unknown without fetching the file); the thread still
	// anchors on the end line.
	start, end := &git.CommentPosition{Line: &o.start, Offset: &one}, &git.CommentPosition{Line: &o.end, Offset: &one}
	tc := &git.CommentThreadContext{FilePath: &filePath}
	// An added file has only a new (right) side, a deleted one only an old (left) side.
	left := o.left
	switch kinds := shared.ChangeKinds(ch.ChangeType); {
	case slices.Contains(kinds, "add"):
		left = false
	case slices.Contains(kinds, "delete"):
		left = true
	}
	if left {
		tc.LeftFileStart, tc.LeftFileEnd = start, end
	} else {
		tc.RightFileStart, tc.RightFileEnd = start, end
	}
	return tc, &git.GitPullRequestCommentThreadContext{
		ChangeTrackingId: ch.ChangeTrackingId,
		IterationContext: &git.CommentIterationContext{FirstComparingIteration: &latest, SecondComparingIteration: &latest},
	}, nil
}
