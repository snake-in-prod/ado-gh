// Package shared holds code shared by the release commands.
package shared

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	authshared "github.com/snake-in-prod/ado-gh/pkg/cmd/auth/shared"
	"github.com/snake-in-prod/ado-gh/pkg/cmdutil"
)

// errInvalidRelease never includes the ref: a URL's userinfo may hold a PAT.
var errInvalidRelease = errors.New("invalid release: expected an ID or a URL such as https://dev.azure.com/ORG/PROJECT/_release?releaseId=ID")

// NewClient returns a Classic Release client for project.Project, authenticated for its
// organization.
func NewClient(ctx context.Context, f *cmdutil.Factory, project adocontext.Repo) (*api.ReleaseClient, error) {
	hc, err := authshared.HTTPClient(ctx, f, project.Host, project.Org)
	if err != nil {
		return nil, err
	}
	return api.NewReleaseClient(hc, adocontext.OrgURL(project.Host, project.Org), project.Project), nil
}

// ParseReleaseRef parses a release argument: a release ID, a web URL
// (_release?releaseId=N or _releaseProgress?releaseId=N) or a REST URL
// (_apis/release/releases/N, also on the vsrm host). For a URL it also returns the project it
// names; otherwise the project is empty.
func ParseReleaseRef(ref string) (int, adocontext.Repo, error) {
	if !strings.Contains(ref, "://") {
		if id, ok := parseID(ref); ok {
			return id, adocontext.Repo{}, nil
		}
		return 0, adocontext.Repo{}, errInvalidRelease
	}
	u, err := url.Parse(ref)
	if err != nil {
		return 0, adocontext.Repo{}, errInvalidRelease
	}
	// REST URLs are on the Release host: vsrm.dev.azure.com/ORG or ORG.vsrm.visualstudio.com.
	host := strings.ToLower(u.Hostname())
	if host == "vsrm.dev.azure.com" {
		u.Host = "dev.azure.com"
	} else if org, ok := strings.CutSuffix(host, ".vsrm.visualstudio.com"); ok {
		u.Host = org + ".visualstudio.com"
	}
	project, rest, ok := adocontext.SplitOrgURL(u)
	if !ok || project.Project == "" {
		return 0, adocontext.Repo{}, errInvalidRelease
	}
	var id int
	switch n := len(rest); {
	case n == 1 && strings.HasPrefix(strings.ToLower(rest[0]), "_release"):
		for k, v := range u.Query() {
			if strings.EqualFold(k, "releaseId") {
				id, _ = parseID(v[0])
			}
		}
	case n == 4 && strings.EqualFold(rest[0], "_apis") && strings.EqualFold(rest[1], "release") && strings.EqualFold(rest[2], "releases"):
		id, _ = parseID(rest[3])
	}
	if id == 0 {
		return 0, adocontext.Repo{}, errInvalidRelease
	}
	return id, project, nil
}

// parseID parses a positive decimal ID without sign; it returns 0 and false for anything else.
func parseID(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 || s[0] == '+' {
		return 0, false
	}
	return n, true
}
