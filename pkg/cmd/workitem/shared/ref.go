package shared

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
)

// errInvalidURL never includes the ref: a URL's userinfo may hold a PAT.
var errInvalidURL = errors.New("invalid work item URL: expected https://dev.azure.com/ORG/PROJECT/_workitems/edit/ID")

// ParseRef parses a work item reference: `123`, `#123`, a web URL
// (https://dev.azure.com/ORG/PROJECT/_workitems/edit/ID or the {org}.visualstudio.com form)
// or a REST URL (.../_apis/wit/workItems/ID). For a URL it also returns its Host, Org and
// Project (Project is empty for an org-level REST URL); otherwise the Repo is empty.
func ParseRef(ref string) (int, adocontext.Repo, error) {
	if strings.Contains(ref, "://") {
		return parseWorkItemURL(ref)
	}
	s := strings.TrimPrefix(ref, "#")
	if n, err := strconv.Atoi(s); err == nil && n > 0 && s[0] != '+' {
		return n, adocontext.Repo{}, nil
	}
	return 0, adocontext.Repo{}, fmt.Errorf("invalid work item reference %q: expected an ID, #ID or a work item URL", ref)
}

func parseWorkItemURL(ref string) (int, adocontext.Repo, error) {
	u, err := url.Parse(ref)
	if err != nil || u.Scheme != "https" {
		return 0, adocontext.Repo{}, errInvalidURL
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	host := strings.ToLower(u.Hostname())
	var org string
	if host == "dev.azure.com" {
		org, segs = segs[0], segs[1:]
	} else if label, ok := strings.CutSuffix(host, ".visualstudio.com"); ok && label != "" && !strings.Contains(label, ".") {
		org = label
		if len(segs) > 0 && strings.EqualFold(segs[0], "DefaultCollection") {
			segs = segs[1:]
		}
	} else {
		return 0, adocontext.Repo{}, errInvalidURL
	}
	// [PROJECT/]_workitems/edit/ID or [PROJECT/]_apis/wit/workItems/ID.
	n := len(segs)
	if n < 3 || org == "" {
		return 0, adocontext.Repo{}, errInvalidURL
	}
	id, err := strconv.Atoi(segs[n-1])
	web := strings.EqualFold(segs[n-3], "_workitems") && strings.EqualFold(segs[n-2], "edit")
	rest := n >= 4 && strings.EqualFold(segs[n-4], "_apis") && strings.EqualFold(segs[n-3], "wit") && strings.EqualFold(segs[n-2], "workitems")
	if err != nil || id <= 0 || !web && !rest {
		return 0, adocontext.Repo{}, errInvalidURL
	}
	prefix := n - 3
	if rest {
		prefix = n - 4
	}
	repo := adocontext.Repo{Host: host, Org: org}
	switch prefix {
	case 0:
		if web {
			return 0, adocontext.Repo{}, errInvalidURL // a web URL always names the project
		}
	case 1:
		repo.Project = segs[0]
	default:
		return 0, adocontext.Repo{}, errInvalidURL
	}
	return id, repo, nil
}
