// Package diff implements `ado pr diff`.
package diff

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/snake-in-prod/ado-gh/pkg/cmd/pr/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// cloneDiff writes pull request pr's diff computed by the local clone: from the latest
// iteration's common commit to its source commit (the web Files tab), only the changed names
// with nameOnly, or the source commits as format-patch mails with patch. The source and target
// branches are fetched first. git never colours or runs external diff tools; writeDiff colours.
func cloneDiff(ctx context.Context, f *cmdutil.Factory, c *api.PRClient, repo adocontext.Repo, pr *git.GitPullRequest, nameOnly, patch, color bool) error {
	id := *pr.PullRequestId
	base, src, tgt, err := iterationCommits(ctx, c, pr)
	if err != nil {
		return err
	}
	head, headURL, fork, err := shared.SourceRepo(ctx, c, repo, pr)
	if err != nil {
		return err
	}
	gc := f.GitClient
	remotes, err := gc.Remotes(ctx)
	if err != nil {
		return err
	}

	// Each side is fetched from its local remote, else by URL (a fork without a remote).
	targetRemote := shared.MatchRemote(remotes, repo)
	sourceRemote := targetRemote
	if fork {
		sourceRemote = shared.MatchRemote(remotes, head)
	}
	targetFrom, sourceFrom := cmp.Or(targetRemote, shared.RepoURL(repo)), cmp.Or(sourceRemote, headURL)

	var fetchErr error
	fetch := func(from string, specs ...string) bool {
		if len(specs) == 0 {
			return false
		}
		_, err := gc.Exec(ctx, append(append([]string{"fetch", from}, specs...), "--no-tags")...)
		if err != nil {
			fetchErr = err
		}
		return err == nil
	}
	// A failed branch fetch is not fatal: a completed PR's source branch is often deleted,
	// while its commits are still local or fetchable by SHA.
	srcSpecs, tgtSpecs := refspec(sourceRemote, pr.SourceRefName), refspec(targetRemote, pr.TargetRefName)
	if sourceFrom == targetFrom {
		fetch(sourceFrom, append(srcSpecs, tgtSpecs...)...)
	} else {
		fetch(targetFrom, tgtSpecs...)
		fetch(sourceFrom, srcSpecs...)
	}
	has := func(sha string) bool {
		_, err := gc.Exec(ctx, "cat-file", "-e", sha+"^{commit}")
		return err == nil
	}
	ensure := func(sha, from string) error {
		if has(sha) || fetch(from, sha) {
			return nil
		}
		return fmt.Errorf("commit %s of pull request !%d is not available locally or from the remote: %w", sha[:7], id, fetchErr)
	}

	if err := ensure(src, sourceFrom); err != nil {
		return err
	}
	var args []string
	if patch && !nameOnly {
		if err := ensure(tgt, targetFrom); err != nil {
			return err
		}
		args = []string{"format-patch", "--no-color", "--no-ext-diff", "--stdout", tgt + ".." + src}
	} else {
		if base == "" || !has(base) && !fetch(targetFrom, base) {
			if err := ensure(tgt, targetFrom); err != nil {
				return err
			}
			out, err := gc.Exec(ctx, "merge-base", tgt, src)
			if err != nil {
				return fmt.Errorf("cannot find the merge base of pull request !%d: %w", id, err)
			}
			base = strings.TrimSpace(string(out))
		}
		args = []string{"diff", "--no-color", "--no-ext-diff"}
		if nameOnly {
			args = append(args, "--name-only")
		}
		args = append(args, base, src)
	}
	out, err := gc.Exec(ctx, args...)
	if err != nil {
		return err
	}
	return writeDiff(f.IOStreams.Out, out, color && !nameOnly)
}

// refspec returns the fetch refspec for branch ref: into the remote's tracking ref, or just the
// ref (FETCH_HEAD) when fetching by URL. A nil or empty ref has none.
func refspec(remote string, ref *string) []string {
	switch {
	case ref == nil || *ref == "":
		return nil
	case remote == "":
		return []string{*ref}
	}
	return []string{"+" + *ref + ":refs/remotes/" + remote + "/" + strings.TrimPrefix(*ref, "refs/heads/")}
}

// iterationCommits returns the latest iteration's common, source and target commits. Without
// iterations it falls back to the pull request's last merge commits and no common commit.
func iterationCommits(ctx context.Context, c *api.PRClient, pr *git.GitPullRequest) (base, src, tgt string, err error) {
	its, err := c.Iterations(ctx, *pr.PullRequestId)
	if err != nil {
		return "", "", "", err
	}
	src, tgt = commitID(pr.LastMergeSourceCommit), commitID(pr.LastMergeTargetCommit)
	if len(its) > 0 {
		it := slices.MaxFunc(its, func(a, b git.GitPullRequestIteration) int { return cmp.Compare(intOr0(a.Id), intOr0(b.Id)) })
		base = commitID(it.CommonRefCommit)
		src, tgt = cmp.Or(commitID(it.SourceRefCommit), src), cmp.Or(commitID(it.TargetRefCommit), tgt)
	}
	// IDs become git arguments: only accept SHA-1 hex. No common commit means merge-base.
	for i, sha := range []string{src, tgt, base} {
		if i == 2 && sha == "" {
			continue
		}
		if len(sha) != 40 || strings.Trim(sha, "0123456789abcdef") != "" {
			return "", "", "", fmt.Errorf("invalid commit ID %q in pull request !%d", sha, *pr.PullRequestId)
		}
	}
	return base, src, tgt, nil
}

func commitID(c *git.GitCommitRef) string {
	if c == nil || c.CommitId == nil {
		return ""
	}
	return *c.CommitId
}

func intOr0(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
