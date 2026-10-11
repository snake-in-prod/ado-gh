package adocontext

import (
	"net/url"
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
