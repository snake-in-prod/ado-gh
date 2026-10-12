package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/feed"
)

// clientToolsAreaID is the clienttools resource area, which serves ArtifactTool releases from
// the org's vsblob host (https://{org}.vsblob.visualstudio.com/).
var clientToolsAreaID = uuid.MustParse("3fda18ba-dff2-42e6-8d10-c521b23b85fc")

// ErrUniversalPackagesUnavailable means the organization lists no clienttools area, as on Azure
// DevOps Server: Universal Packages exist only in Azure DevOps Services.
var ErrUniversalPackagesUnavailable = errors.New("universal packages (Azure Artifacts) need Azure DevOps Services: " +
	"this organization has no ArtifactTool")

// ClientToolRelease is an ArtifactTool build. URI points to a zip of it.
type ClientToolRelease struct {
	Name    string `json:"name"`
	Rid     string `json:"rid"`
	Version string `json:"version"`
	URI     string `json:"uri"`
}

// ArtifactToolRelease returns the current ArtifactTool build for osName (Linux, Darwin or
// Windows) and arch (amd64, arm64) from the organization at orgURL, or nil when there is none
// for that platform.
func ArtifactToolRelease(ctx context.Context, hc *http.Client, orgURL, osName, arch string) (*ClientToolRelease, error) {
	base, ok, err := areaURL(ctx, hc, orgURL, clientToolsAreaID)
	if err = mapErr(err); IsNotFound(err) || err == nil && !ok {
		return nil, ErrUniversalPackagesUnavailable
	} else if err != nil {
		return nil, err
	}
	q := url.Values{"osName": {osName}, "arch": {arch}, "api-version": {"5.0-preview"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(base, "/")+"/_apis/clienttools/ArtifactTool/release?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("looking up ArtifactTool: HTTP %d", resp.StatusCode)
	}
	var rel *ClientToolRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil || rel != nil && rel.URI == "" {
		return nil, errors.New("looking up ArtifactTool: unexpected response")
	}
	return rel, nil
}

var releaseVersion = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

// LatestUniversalVersion returns the highest version without a prerelease part of universal
// package name in feed feedID (name or ID; project "" means an org-scoped feed), which is what
// ArtifactTool's "*" resolves to. packageNameQuery is a contains filter, so the name is
// matched exactly (ignoring case) here.
func LatestUniversalVersion(ctx context.Context, hc *http.Client, orgURL, project, feedID, name string) (string, error) {
	c, _, err := areaClient(ctx, hc, orgURL, feed.ResourceAreaId)
	if err != nil {
		return "", mapErr(err)
	}
	upack, all := "upack", true
	args := feed.GetPackagesArgs{FeedId: &feedID, ProtocolType: &upack, PackageNameQuery: &name, IncludeAllVersions: &all}
	if project != "" {
		args.Project = &project
	}
	pkgs, err := (&feed.ClientImpl{Client: *c}).GetPackages(ctx, args)
	if err != nil {
		return "", mapErr(err)
	}
	var best string
	var bestN []int
	for _, p := range deref(pkgs) {
		if !strings.EqualFold(deref(p.Name), name) {
			continue
		}
		for _, v := range deref(p.Versions) {
			m := releaseVersion.FindStringSubmatch(deref(v.Version))
			if m == nil || deref(v.IsDeleted) {
				continue
			}
			n := make([]int, 3)
			for i := range n {
				if n[i], err = strconv.Atoi(m[i+1]); err != nil {
					break
				}
			}
			if err == nil && (best == "" || slices.Compare(n, bestN) > 0) {
				best, bestN = m[0], n
			}
		}
	}
	if best == "" {
		return "", fmt.Errorf("feed %s has no release version of package %s", feedID, name)
	}
	return best, nil
}
