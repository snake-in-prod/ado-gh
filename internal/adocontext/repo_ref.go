package adocontext

import (
	"errors"
	"net/url"
	"strings"
)

// errBadRepoURL never includes the URL: it may carry a PAT in its userinfo.
var errBadRepoURL = errors.New("invalid repository URL: expected https://dev.azure.com/ORG/PROJECT/_git/REPO or git@ssh.dev.azure.com:v3/ORG/PROJECT/REPO")

// ParseRepoRef parses a repository argument: REPO, PROJECT/REPO, ORG/PROJECT/REPO, a git remote
// URL or a repository web URL (query, fragment and sub-pages such as /pullrequest/N are
// ignored). Missing parts come from base (the current repository, incl. its host), else
// ADO_ORG / ADO_PROJECT, else configOrg (the `org` config key) for the organization. base and
// configOrg are only called when needed.
func ParseRepoRef(arg string, base func() (Repo, error), configOrg func() string) (Repo, error) {
	// ADO names can't contain ':', so any colon means a URL (scheme or scp-style SSH).
	if strings.Contains(arg, ":") {
		r, err := ParseRemoteURL(cleanWebURL(arg))
		if err != nil {
			return Repo{}, errBadRepoURL
		}
		return r, nil
	}
	return parseSegments("repository", arg, base, configOrg)
}

// cleanWebURL reduces an https URL copied from the browser to its .../_git/REPO part, dropping
// query, fragment, userinfo and trailing sub-paths. Other input is returned unchanged.
func cleanWebURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return raw
	}
	segs := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if i := indexFold(segs, "_git"); i >= 0 && i+2 < len(segs) {
		end := i + 2
		if strings.EqualFold(segs[i+1], "_full") || strings.EqualFold(segs[i+1], "_optimized") {
			end++
		}
		segs = segs[:end]
	}
	return "https://" + u.Host + "/" + strings.Join(segs, "/")
}
