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
	if err != nil {
		return 0, adocontext.Repo{}, errInvalidURL
	}
	repo, rest, ok := adocontext.SplitOrgURL(u)
	if !ok {
		return 0, adocontext.Repo{}, errInvalidURL
	}
	// PROJECT/_workitems/edit/ID (a web URL always names the project) or [PROJECT/]_apis/wit/workItems/ID.
	web := len(rest) == 3 && repo.Project != "" && strings.EqualFold(rest[0], "_workitems") && strings.EqualFold(rest[1], "edit")
	restAPI := len(rest) == 4 && strings.EqualFold(rest[0], "_apis") && strings.EqualFold(rest[1], "wit") && strings.EqualFold(rest[2], "workitems")
	if !web && !restAPI {
		return 0, adocontext.Repo{}, errInvalidURL
	}
	id, err := strconv.Atoi(rest[len(rest)-1])
	if err != nil || id <= 0 {
		return 0, adocontext.Repo{}, errInvalidURL
	}
	return id, repo, nil
}
