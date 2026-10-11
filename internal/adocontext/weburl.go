package adocontext

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// OrgURL returns the organization's base URL, ending in "/": https://{org}.visualstudio.com/
// for that host, https://dev.azure.com/{org}/ otherwise (incl. the SSH hosts).
func OrgURL(host, org string) string {
	if strings.HasSuffix(host, vsSuffix) && host != hostVSSSH {
		return "https://" + host + "/"
	}
	return "https://" + hostDevAzure + "/" + url.PathEscape(org) + "/"
}

// SplitOrgURL splits an https URL of an organization, https://dev.azure.com/ORG/... or
// https://ORG.visualstudio.com/[DefaultCollection/]..., into the Host, Org and Project it names
// and the path segments from the first one starting with "_" (e.g. _build, _apis), which no
// project name does. Project is the one segment before them, empty when there is none (an
// org-level URL). ok is false for any other URL, or one with no "_" segment.
func SplitOrgURL(u *url.URL) (repo Repo, rest []string, ok bool) {
	if u.Scheme != "https" {
		return Repo{}, nil, false
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	host := strings.ToLower(u.Hostname())
	repo.Host = host
	if host == hostDevAzure {
		repo.Org, segs = segs[0], segs[1:]
	} else if label, isVS := strings.CutSuffix(host, vsSuffix); isVS && label != "" && !strings.Contains(label, ".") {
		repo.Org = label
		if strings.EqualFold(segs[0], "DefaultCollection") {
			segs = segs[1:]
		}
	} else {
		return Repo{}, nil, false
	}
	i := slices.IndexFunc(segs, func(s string) bool { return strings.HasPrefix(s, "_") })
	if repo.Org == "" || strings.HasPrefix(repo.Org, "_") || i < 0 || i > 1 {
		return Repo{}, nil, false
	}
	if i == 1 {
		repo.Project = segs[0]
	}
	return repo, segs[i:], true
}

// ProjectURL returns the web URL of repo's project, e.g. https://dev.azure.com/org/proj.
func ProjectURL(repo Repo) string {
	return OrgURL(repo.Host, repo.Org) + url.PathEscape(repo.Project)
}

// RepoURL returns the web URL of repo, e.g. https://dev.azure.com/org/proj/_git/repo.
func RepoURL(repo Repo) string {
	return ProjectURL(repo) + "/_git/" + url.PathEscape(repo.Name)
}

// WorkItemURL returns the web URL of work item id in repo's project.
func WorkItemURL(repo Repo, id int) string {
	return ProjectURL(repo) + "/_workitems/edit/" + strconv.Itoa(id)
}

// QueryEscape escapes a query value with %20 for spaces: ADO's router may not decode "+".
func QueryEscape(v string) string {
	return strings.ReplaceAll(url.QueryEscape(v), "+", "%20")
}

// PRURL returns the web URL of pull request id of repo.
func PRURL(repo Repo, id int) string {
	return RepoURL(repo) + "/pullrequest/" + strconv.Itoa(id)
}

// PipelineURL returns the web URL of pipeline (build definition) id in project.
func PipelineURL(project Repo, id int) string {
	return ProjectURL(project) + "/_build?definitionId=" + strconv.Itoa(id)
}

// RunURL returns the web URL of run (build) id in project.
func RunURL(project Repo, id int) string {
	return ProjectURL(project) + "/_build/results?buildId=" + strconv.Itoa(id)
}

// TaskboardURL returns the web URL of team's taskboard for the iteration at path, e.g.
// {project}/_sprints/taskboard/{team}/{project}/{rest of the path, escaped as one segment}.
// ponytail: the URL form is undocumented (from microsoft/vsts-team-calendar); fix here if it moves.
func TaskboardURL(project Repo, team, path string) string {
	rest := strings.TrimPrefix(path, `\`)
	// The project name in the path has the server's casing, project.Project the user's.
	if n := len(project.Project); len(rest) >= n && strings.EqualFold(rest[:n], project.Project) {
		rest = rest[n:]
	}
	return ProjectURL(project) + "/_sprints/taskboard/" + url.PathEscape(team) + "/" +
		url.PathEscape(project.Project) + "/" + url.PathEscape(strings.TrimPrefix(rest, `\`))
}

// BoardURL returns the web URL of team's Kanban board for the backlog level levelID, e.g.
// {project}/_boards/board/t/{team}/Microsoft.RequirementCategory.
func BoardURL(project Repo, team, levelID string) string {
	return ProjectURL(project) + "/_boards/board/t/" + url.PathEscape(team) + "/" + url.PathEscape(levelID)
}
