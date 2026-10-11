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
	return parseSegments(source, s, nil, nil)
}

// parseSegments parses [ORG/]PROJECT/REPO from source. Missing parts come from base (when
// non-nil and it resolves), else ADO_ORG / ADO_PROJECT, else configOrg (when non-nil) for the
// organization.
func parseSegments(source, s string, base func() (Repo, error), configOrg func() string) (Repo, error) {
	segs := strings.Split(s, "/")
	if len(segs) > 3 || slices.ContainsFunc(segs, func(s string) bool { return !validSegment(s) }) {
		return Repo{}, fmt.Errorf("invalid %s %q: expected [ORG/]PROJECT/REPO", source, s)
	}
	r := Repo{Host: hostDevAzure, Name: segs[len(segs)-1]}
	switch len(segs) {
	case 3:
		r.Org, r.Project = segs[0], segs[1]
		return r, nil
	case 2:
		r.Project = segs[0]
	}
	if base != nil {
		if b, err := base(); err == nil {
			r.Host, r.Org = b.Host, b.Org
			if r.Project == "" {
				r.Project = b.Project
			}
			return r, nil
		}
	}
	orgSource, orgHint := "ADO_ORG", "use ORG/PROJECT/REPO or set ADO_ORG"
	r.Org = os.Getenv("ADO_ORG")
	if configOrg != nil {
		orgHint = "use ORG/PROJECT/REPO, set ADO_ORG or run `ado config set org <org>`"
		if r.Org == "" {
			orgSource, r.Org = "org config value", configOrg()
		}
	}
	if r.Org == "" {
		return Repo{}, fmt.Errorf("%s %q has no organization; %s", source, s, orgHint)
	}
	if r.Project == "" {
		if r.Project = os.Getenv("ADO_PROJECT"); r.Project == "" {
			return Repo{}, fmt.Errorf("%s %q has no project; use [ORG/]PROJECT/REPO or set ADO_PROJECT", source, s)
		}
	}
	if !validSegment(r.Org) {
		return Repo{}, fmt.Errorf("invalid %s %q", orgSource, r.Org)
	}
	if !validSegment(r.Project) { // only ADO_PROJECT can be invalid here
		return Repo{}, fmt.Errorf("invalid ADO_PROJECT %q", r.Project)
	}
	return r, nil
}
