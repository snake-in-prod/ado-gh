// Package browse implements `ado browse`.
package browse

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	reposhared "github.com/snake-in-prod/ado-gh/pkg/cmd/repo/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// LastCommit is the --commit value meaning the local HEAD (the flag's NoOptDefVal).
const LastCommit = "HEAD"

// Options select the page `ado browse` opens.
type Options struct {
	// Target is the positional argument: "" (repo home), !N / N (PR), #N (work item),
	// a 7–40 hex SHA (commit) or a path[:line[-line]].
	Target string
	Branch string
	// Commit is "" when --commit is absent; LastCommit for --commit without a value.
	Commit string

	Settings, Wiki, Pipelines, Boards, PRs, Branches bool
}

var (
	shaRE  = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	lineRE = regexp.MustCompile(`^([0-9]+)(?:-([0-9]+))?$`)
)

// BuildURL returns the web URL opts selects in f's base repository. Only --settings makes a
// request (the repository ID); git is asked for the cwd prefix and to expand commits.
func BuildURL(ctx context.Context, f *cmdutil.Factory, opts Options) (string, error) {
	var set []string
	for _, s := range []struct {
		on   bool
		name string
	}{
		{opts.Settings, "--settings"}, {opts.Wiki, "--wiki"}, {opts.Pipelines, "--pipelines"},
		{opts.Boards, "--boards"}, {opts.PRs, "--prs"}, {opts.Branches, "--branches"},
	} {
		if s.on {
			set = append(set, s.name)
		}
	}
	if len(set) > 0 && opts.Target != "" {
		return "", cmdutil.FlagErrorf("%q is an invalid argument when using %s", opts.Target, set[0])
	}
	if opts.Branch != "" {
		set = append(set, "--branch")
	}
	if opts.Commit != "" {
		set = append(set, "--commit")
	}
	if len(set) > 1 {
		return "", cmdutil.FlagErrorf("specify only one of --branch, --commit, --settings, --wiki, --pipelines, --boards, --prs or --branches")
	}

	repo, err := f.BaseRepo()
	if err != nil {
		return "", err
	}
	repoURL := adocontext.RepoURL(repo)
	projectURL := adocontext.ProjectURL(repo)

	switch {
	case opts.Settings:
		return settingsURL(ctx, f, repo, projectURL), nil
	case opts.Wiki:
		return projectURL + "/_wiki", nil
	case opts.Pipelines:
		return projectURL + "/_build", nil
	case opts.Boards:
		return projectURL + "/_boards", nil
	case opts.PRs:
		return repoURL + "/pullrequests", nil
	case opts.Branches:
		return repoURL + "/branches", nil
	}

	versioned := opts.Branch != "" || opts.Commit != ""
	t := opts.Target
	if n, ok := number(strings.TrimPrefix(t, "!")); ok {
		if versioned {
			return "", invalidWithVersion(t)
		}
		return adocontext.PRURL(repo, n), nil
	}
	if n, ok := number(strings.TrimPrefix(t, "#")); ok && strings.HasPrefix(t, "#") {
		if versioned {
			return "", invalidWithVersion(t)
		}
		return adocontext.WorkItemURL(repo, n), nil
	}
	if shaRE.MatchString(t) {
		if versioned {
			return "", invalidWithVersion(t)
		}
		return repoURL + "/commit/" + expandCommit(ctx, f, t), nil
	}

	var p string
	var line []string
	if t != "" {
		var err error
		if p, line, err = repoPath(ctx, f, t); err != nil {
			return "", err
		}
	}
	version, err := versionParam(ctx, f, opts)
	if err != nil {
		return "", err
	}
	var q []string
	if t != "" {
		q = append(q, "path="+adocontext.QueryEscape(p))
	}
	q = append(q, version...)
	if t != "" {
		q = append(append(q, line...), "_a=contents")
	}
	if len(q) == 0 {
		return repoURL, nil
	}
	return repoURL + "?" + strings.Join(q, "&"), nil
}

// number reports whether s is a positive decimal number, so `1234567` is a PR, not a SHA.
func number(s string) (int, bool) {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0
}

func invalidWithVersion(arg string) error {
	return cmdutil.FlagErrorf("%q is an invalid argument when using --branch or --commit", arg)
}

// versionParam is the version=GB<branch>|GC<sha> query part, empty without -b/-c so ADO shows
// the default branch without an API call.
func versionParam(ctx context.Context, f *cmdutil.Factory, opts Options) ([]string, error) {
	switch {
	case opts.Branch != "":
		return []string{"version=" + adocontext.QueryEscape("GB"+opts.Branch)}, nil
	case opts.Commit != "":
		sha, err := commitSHA(ctx, f, opts.Commit)
		if err != nil {
			return nil, err
		}
		return []string{"version=GC" + sha}, nil
	}
	return nil, nil
}

// expandCommit expands a short SHA to the full one when the base repo is the local clone and
// git knows it; otherwise rev is returned as given.
func expandCommit(ctx context.Context, f *cmdutil.Factory, rev string) string {
	if f.BaseRepoFromGit() && f.GitClient != nil {
		out, err := f.GitClient.Exec(ctx, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
		if sha := strings.TrimSpace(string(out)); err == nil && shaRE.MatchString(sha) {
			return sha
		}
	}
	return rev
}

// commitSHA resolves a --commit value (LastCommit, a SHA or any local revision) to a SHA.
func commitSHA(ctx context.Context, f *cmdutil.Factory, rev string) (string, error) {
	sha := expandCommit(ctx, f, rev)
	if shaRE.MatchString(sha) {
		return sha, nil
	}
	if rev == LastCommit {
		return "", errors.New("--commit without a SHA needs a local clone of the repository")
	}
	return "", fmt.Errorf("cannot resolve commit %q; pass a commit SHA or run inside a clone that has it", rev)
}

// repoPath splits target into a repo-root path ("/a/b") and its line query parts. Inside a
// clone the path is relative to the cwd; with -R/ADO_REPO it is relative to the repo root.
func repoPath(ctx context.Context, f *cmdutil.Factory, target string) (string, []string, error) {
	p, lines, hasLines := strings.Cut(target, ":")
	var line []string
	if hasLines {
		m := lineRE.FindStringSubmatch(lines)
		if m == nil {
			return "", nil, cmdutil.FlagErrorf("invalid line number after colon in %q", target)
		}
		start, err := strconv.Atoi(m[1])
		end := start
		if err == nil && m[2] != "" {
			end, err = strconv.Atoi(m[2])
		}
		if err != nil || start < 1 || end < start || end == math.MaxInt {
			return "", nil, cmdutil.FlagErrorf("invalid line range in %q", target)
		}
		line = []string{
			"line=" + strconv.Itoa(start), "lineEnd=" + strconv.Itoa(end+1),
			"lineStartColumn=1", "lineEndColumn=1", "lineStyle=plain",
		}
	}
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") && f.BaseRepoFromGit() && f.GitClient != nil {
		out, err := f.GitClient.Exec(ctx, "rev-parse", "--show-prefix")
		if err != nil {
			return "", nil, fmt.Errorf("finding the path from the repository root: %w", err)
		}
		p = strings.TrimSpace(string(out)) + p
	}
	p = path.Clean(strings.TrimLeft(p, "/"))
	if p == ".." || strings.HasPrefix(p, "../") {
		return "", nil, cmdutil.FlagErrorf("%q is outside the repository", target)
	}
	if p == "." {
		p = ""
	}
	return "/" + p, line, nil
}

// settingsURL is the repository's settings page, or the project's repository list when its ID
// can't be fetched.
func settingsURL(ctx context.Context, f *cmdutil.Factory, repo adocontext.Repo, projectURL string) string {
	list := projectURL + "/_settings/repositories"
	client, err := reposhared.NewClient(ctx, f, repo)
	if err != nil {
		return list
	}
	r, err := client.Repository(ctx, repo.Name)
	if err != nil || r.ID == "" {
		return list
	}
	return list + "?repo=" + url.QueryEscape(r.ID)
}
