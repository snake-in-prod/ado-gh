package httpmock

import (
	"net/http"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/identity"
)

// Location is one API resource location served by the SDK's OPTIONS {base}/_apis discovery call.
type Location struct {
	ID            string `json:"id"`
	Area          string `json:"area"`
	ResourceName  string `json:"resourceName"`
	RouteTemplate string `json:"routeTemplate"`
}

// ResourceAreasLocation is the location the SDK resolves before GET {base}/_apis/ResourceAreas.
var ResourceAreasLocation = Location{
	ID:            "e81700f7-3be2-46de-8624-2eb35882fcaa",
	Area:          "Location",
	ResourceName:  "ResourceAreas",
	RouteTemplate: "_apis/{resource}/{areaId}",
}

// ResourceArea is one entry of the GET {base}/_apis/ResourceAreas response.
type ResourceArea struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	LocationURL string `json:"locationUrl"`
}

type sdkLocation struct {
	Location
	ResourceVersion int    `json:"resourceVersion"`
	MinVersion      string `json:"minVersion"`
	MaxVersion      string `json:"maxVersion"`
	ReleasedVersion string `json:"releasedVersion"`
}

type collection[T any] struct {
	Count int `json:"count"`
	Value []T `json:"value"`
}

// StubLocations registers the OPTIONS {basePath}/_apis call every SDK client makes before its
// first request. basePath has no leading slash, e.g. "org", or is "" for a root base URL such as
// https://{org}.visualstudio.com/. The SDK caches the result per base URL for the whole process,
// so tests that register this stub must use a base URL of their own.
func (r *Registry) StubLocations(basePath string, locs ...Location) {
	out := make([]sdkLocation, len(locs))
	for i, l := range locs {
		// The SDK dereferences these during API version negotiation.
		out[i] = sdkLocation{Location: l, ResourceVersion: 3, MinVersion: "1.0", MaxVersion: "7.2", ReleasedVersion: "7.1"}
	}
	r.Register(REST(http.MethodOptions, strings.TrimPrefix(basePath+"/_apis", "/")), JSONResponse(collection[sdkLocation]{len(out), out}))
}

// StubResourceAreas registers GET {basePath}/_apis/ResourceAreas, which the SDK's NewClient
// constructors call to find an area's host. Include ResourceAreasLocation in StubLocations too.
func (r *Registry) StubResourceAreas(basePath string, areas ...ResourceArea) {
	r.Register(REST(http.MethodGet, basePath+"/_apis/ResourceAreas"), JSONResponse(collection[ResourceArea]{len(areas), areas}))
}

// StubIMS registers the discovery calls the identity client makes before its first request:
// GET {org}/_apis/ResourceAreas, then OPTIONS on the vssps host. The org's own StubLocations
// must include ResourceAreasLocation.
func (r *Registry) StubIMS(org string) {
	r.StubResourceAreas(org, ResourceArea{
		ID: identity.ResourceAreaId.String(), Name: "IMS", LocationURL: "https://vssps.dev.azure.com/" + org + "/",
	})
	r.StubLocations(org, Location{
		ID: "28010c54-d0c0-4c89-a5b0-1c9e188b9fb7", Area: "IMS", ResourceName: "Identities",
		RouteTemplate: "_apis/{resource}/{identityId}",
	})
}
