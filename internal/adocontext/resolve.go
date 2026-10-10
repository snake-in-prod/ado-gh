package adocontext

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/snake-in-prod/ado-gh/internal/git"
)

var errNoRepo = errors.New("not an Azure DevOps repository; use -R ORG/PROJECT/REPO or set ADO_REPO")

// ResolveBaseRepo picks the target repository: override (-R) > ADO_REPO >
// git remotes (origin > upstream > first ADO remote). -R and ADO_REPO take
// [ORG/]PROJECT/REPO on dev.azure.com, with ADO_ORG / ADO_PROJECT filling the
// missing parts. remotes is only called when neither is set.
func ResolveBaseRepo(override string, remotes func() ([]git.Remote, error)) (Repo, error) {
	if override != "" {
		return parseRepoArg("-R", override)
	}
	if v := os.Getenv("ADO_REPO"); v != "" {
		return parseRepoArg("ADO_REPO", v)
	}
	rs, err := remotes()
	if errors.Is(err, git.ErrNotGitRepo) {
		return Repo{}, errNoRepo
	}
	if err != nil {
		return Repo{}, err
	}
	var found []Repo
	byName := map[string]Repo{}
	for _, r := range rs {
		if repo, err := ParseRemoteURL(r.FetchURL); err == nil {
			found = append(found, repo)
			byName[r.Name] = repo
		}
	}
	for _, name := range []string{"origin", "upstream"} {
		if repo, ok := byName[name]; ok {
			return repo, nil
		}
	}
	if len(found) == 0 {
		return Repo{}, errNoRepo
	}
	return found[0], nil
}

// parseRepoArg parses [ORG/]PROJECT/REPO from source (-R or ADO_REPO).
func parseRepoArg(source, s string) (Repo, error) {
	segs := strings.Split(s, "/")
	if len(segs) > 3 || slices.ContainsFunc(segs, func(s string) bool { return !validSegment(s) }) {
		return Repo{}, fmt.Errorf("invalid %s %q: expected [ORG/]PROJECT/REPO", source, s)
	}
	if len(segs) < 3 {
		org := os.Getenv("ADO_ORG")
		if org == "" {
			return Repo{}, fmt.Errorf("%s %q has no organization; use ORG/PROJECT/REPO or set ADO_ORG", source, s)
		}
		segs = append([]string{org}, segs...)
	}
	if len(segs) < 3 {
		project := os.Getenv("ADO_PROJECT")
		if project == "" {
			return Repo{}, fmt.Errorf("%s %q has no project; use [ORG/]PROJECT/REPO or set ADO_PROJECT", source, s)
		}
		segs = []string{segs[0], project, segs[1]}
	}
	for i, env := range []string{"ADO_ORG", "ADO_PROJECT"} {
		if !validSegment(segs[i]) {
			return Repo{}, fmt.Errorf("invalid %s %q", env, segs[i])
		}
	}
	return Repo{Host: hostDevAzure, Org: segs[0], Project: segs[1], Name: segs[2]}, nil
}
