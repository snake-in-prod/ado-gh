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

// RepoURL returns the web URL of repo, e.g. https://dev.azure.com/org/proj/_git/repo.
func RepoURL(repo Repo) string {
	return OrgURL(repo.Host, repo.Org) + url.PathEscape(repo.Project) + "/_git/" + url.PathEscape(repo.Name)
}

// PRURL returns the web URL of pull request id of repo.
func PRURL(repo Repo, id int) string {
	return RepoURL(repo) + "/pullrequest/" + strconv.Itoa(id)
}
