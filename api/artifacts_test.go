package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/feed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
)

// newArtifactsReg returns a registry whose org (unique per test: the SDK caches discovery per
// base URL) lists the clienttools and feed areas.
func newArtifactsReg(t *testing.T, org string, areas bool) (*http.Client, *httpmock.Registry) {
	t.Helper()
	reg := &httpmock.Registry{}
	t.Cleanup(func() { reg.Verify(t) })
	reg.StubLocations(org, httpmock.ResourceAreasLocation)
	if areas {
		reg.StubResourceAreas(org, httpmock.ArtifactsAreas(org)...)
	} else {
		reg.StubResourceAreas(org)
	}
	return NewHTTPClient(ClientOptions{Transport: reg}), reg
}

func TestArtifactToolRelease(t *testing.T) {
	org := searchOrg("at-release")
	hc, reg := newArtifactsReg(t, org, true)
	reg.Register(func(r *http.Request) bool {
		return r.URL.Host == org+".vsblob.visualstudio.com" && r.URL.Path == "/_apis/clienttools/ArtifactTool/release"
	}, func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		assert.Equal(t, "Darwin", q.Get("osName"))
		assert.Equal(t, "arm64", q.Get("arch"))
		assert.Equal(t, "5.0-preview", q.Get("api-version"))
		return httpmock.JSONResponse(map[string]string{"name": "ArtifactTool", "rid": "osx-arm64", "version": "0.2.1", "uri": "https://x/a.zip"})(r)
	})
	rel, err := ArtifactToolRelease(context.Background(), hc, "https://dev.azure.com/"+org+"/", "Darwin", "arm64")
	require.NoError(t, err)
	assert.Equal(t, &ClientToolRelease{Name: "ArtifactTool", Rid: "osx-arm64", Version: "0.2.1", URI: "https://x/a.zip"}, rel)
}

func TestArtifactToolReleaseNone(t *testing.T) {
	org := searchOrg("at-none")
	hc, reg := newArtifactsReg(t, org, true)
	reg.Register(httpmock.REST(http.MethodGet, "_apis/clienttools/ArtifactTool/release"), httpmock.StringResponse("null"))
	rel, err := ArtifactToolRelease(context.Background(), hc, "https://dev.azure.com/"+org+"/", "Windows", "arm64")
	require.NoError(t, err)
	assert.Nil(t, rel)
}

func TestArtifactToolReleaseErrors(t *testing.T) {
	t.Run("server without clienttools", func(t *testing.T) {
		org := searchOrg("at-server")
		hc, _ := newArtifactsReg(t, org, false)
		_, err := ArtifactToolRelease(context.Background(), hc, "https://dev.azure.com/"+org+"/", "Linux", "amd64")
		assert.ErrorIs(t, err, ErrUniversalPackagesUnavailable)
	})
	t.Run("http error", func(t *testing.T) {
		org := searchOrg("at-500")
		hc, reg := newArtifactsReg(t, org, true)
		reg.Register(httpmock.REST(http.MethodGet, "_apis/clienttools/ArtifactTool/release"), httpmock.StatusStringResponse(http.StatusInternalServerError, ""))
		_, err := ArtifactToolRelease(context.Background(), hc, "https://dev.azure.com/"+org+"/", "Linux", "amd64")
		assert.EqualError(t, err, "looking up ArtifactTool: HTTP 500")
	})
	t.Run("no uri", func(t *testing.T) {
		org := searchOrg("at-nouri")
		hc, reg := newArtifactsReg(t, org, true)
		reg.Register(httpmock.REST(http.MethodGet, "_apis/clienttools/ArtifactTool/release"), httpmock.StringResponse(`{"rid":"linux-x64"}`))
		_, err := ArtifactToolRelease(context.Background(), hc, "https://dev.azure.com/"+org+"/", "Linux", "amd64")
		assert.EqualError(t, err, "looking up ArtifactTool: unexpected response")
	})
	t.Run("resource areas fail", func(t *testing.T) {
		org := searchOrg("at-areas")
		reg := &httpmock.Registry{}
		reg.StubLocations(org, httpmock.ResourceAreasLocation)
		reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/ResourceAreas"), httpmock.StatusStringResponse(http.StatusInternalServerError, `{"message":"boom"}`))
		hc := NewHTTPClient(ClientOptions{Transport: reg})
		_, err := ArtifactToolRelease(context.Background(), hc, "https://dev.azure.com/"+org+"/", "Linux", "amd64")
		assert.ErrorContains(t, err, "boom")
	})
}

func TestLatestUniversalVersion(t *testing.T) {
	versions := func(vs ...string) []map[string]any {
		out := []map[string]any{}
		for _, v := range vs {
			out = append(out, map[string]any{"version": v})
		}
		return out
	}
	tests := map[string]struct {
		project string
		path    string
		pkgs    []map[string]any
		want    string
		wantErr string
	}{
		"highest release, project feed": {project: "My Proj", path: "/My%20Proj/_apis/packaging/Feeds/ado-live/packages",
			pkgs: []map[string]any{
				{"name": "ado-hello-extra", "versions": versions("9.0.0")}, // packageNameQuery is "contains"
				{"name": "ado-hello", "versions": append(versions("0.2.0", "0.10.0", "1.0.0-rc.1", "0.9.9"),
					map[string]any{"version": "5.0.0", "isDeleted": true})},
			}, want: "0.10.0"},
		"org feed": {path: "/_apis/packaging/Feeds/ado-live/packages",
			pkgs: []map[string]any{{"name": "ADO-Hello", "versions": versions("1.2.3")}}, want: "1.2.3"},
		"only prereleases": {path: "/_apis/packaging/Feeds/ado-live/packages",
			pkgs:    []map[string]any{{"name": "ado-hello", "versions": versions("1.0.0-beta", "bogus")}},
			wantErr: "feed ado-live has no release version of package ado-hello"},
		"missing package": {path: "/_apis/packaging/Feeds/ado-live/packages", pkgs: []map[string]any{},
			wantErr: "feed ado-live has no release version of package ado-hello"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			org := searchOrg("upack")
			hc, reg := newArtifactsReg(t, org, true)
			reg.StubFeedLocations(org)
			reg.Register(func(r *http.Request) bool {
				return r.URL.Host == "feeds.dev.azure.com" && r.URL.EscapedPath() == "/"+org+tt.path
			}, func(r *http.Request) (*http.Response, error) {
				q := r.URL.Query()
				assert.Equal(t, "upack", q.Get("protocolType"))
				assert.Equal(t, "ado-hello", q.Get("packageNameQuery"))
				assert.Equal(t, "true", q.Get("includeAllVersions"))
				return httpmock.JSONResponse(map[string]any{"count": len(tt.pkgs), "value": tt.pkgs})(r)
			})
			got, err := LatestUniversalVersion(context.Background(), hc, "https://dev.azure.com/"+org+"/", tt.project, "ado-live", "ado-hello")
			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLatestUniversalVersionHTTPError(t *testing.T) {
	org := searchOrg("upack-err")
	hc, reg := newArtifactsReg(t, org, true)
	reg.StubFeedLocations(org)
	reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/packaging/Feeds/nope/packages"),
		httpmock.StatusJSONResponse(http.StatusNotFound, map[string]string{"message": "Feed nope not found."}))
	_, err := LatestUniversalVersion(context.Background(), hc, "https://dev.azure.com/"+org+"/", "", "nope", "ado-hello")
	assert.EqualError(t, err, "Feed nope not found. (HTTP 404)")
	assert.Equal(t, "7ab4e64e-c4d8-4f50-ae73-5ef2e21642a5", feed.ResourceAreaId.String())
}
