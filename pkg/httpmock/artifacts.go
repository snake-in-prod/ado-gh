package httpmock

import (
	"net/http"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/feed"
)

// ArtifactsAreas are the resource areas Azure Artifacts installs look up, for
// StubResourceAreas: clienttools (ArtifactTool releases) on https://{org}.vsblob.visualstudio.com/
// and the feed area on https://feeds.dev.azure.com/{org}/.
func ArtifactsAreas(org string) []ResourceArea {
	return []ResourceArea{
		{ID: "3fda18ba-dff2-42e6-8d10-c521b23b85fc", Name: "clienttools", LocationURL: "https://" + org + ".vsblob.visualstudio.com/"},
		{ID: feed.ResourceAreaId.String(), Name: "Packaging", LocationURL: "https://feeds.dev.azure.com/" + org + "/"},
	}
}

// StubFeedLocations registers the OPTIONS discovery call on feeds.dev.azure.com/{org} serving
// the feed packages location. Register the packages GET on {org}[/{project}]/_apis/packaging/
// Feeds/{feed}/packages.
func (r *Registry) StubFeedLocations(org string) {
	options := REST(http.MethodOptions, org+"/_apis")
	r.RegisterOptional(func(req *http.Request) bool { return req.URL.Host == "feeds.dev.azure.com" && options(req) },
		locationsResponse([]Location{{ID: "7a20d846-c929-4acc-9ea2-0d5a7df1b197", Area: "Packaging", ResourceName: "Packages",
			RouteTemplate: "{project}/_apis/packaging/Feeds/{feedId}/packages/{packageId}"}}))
}
