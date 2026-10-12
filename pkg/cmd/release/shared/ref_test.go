package shared

import (
	"context"
	"errors"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/release"
	"github.com/snake-in-prod/ado-gh/api"
	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseReleaseRef(t *testing.T) {
	dev := adocontext.Repo{Host: "dev.azure.com", Org: "org", Project: "My Proj"}
	vs := adocontext.Repo{Host: "fab.visualstudio.com", Org: "fab", Project: "p"}
	tests := []struct {
		ref     string
		id      int
		project adocontext.Repo
	}{
		{"45", 45, adocontext.Repo{}},
		{"https://dev.azure.com/org/My%20Proj/_release?releaseId=45&_a=release-summary", 45, dev},
		{"https://dev.azure.com/org/My%20Proj/_releaseProgress?_a=release-pipeline-progress&RELEASEID=45", 45, dev},
		{"https://vsrm.dev.azure.com/org/My%20Proj/_apis/Release/releases/45", 45, dev},
		{"https://fab.visualstudio.com/DefaultCollection/p/_release?releaseId=45", 45, vs},
		{"https://fab.vsrm.visualstudio.com/p/_apis/release/releases/45?api-version=7.1", 45, vs},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			id, project, err := ParseReleaseRef(tt.ref)
			require.NoError(t, err)
			assert.Equal(t, tt.id, id)
			assert.Equal(t, tt.project, project)
		})
	}
}

func TestParseReleaseRefInvalid(t *testing.T) {
	for _, ref := range []string{
		"", "abc", "0", "-1", "+4", "Release-1",
		"https://dev.azure.com/org/proj/_release?definitionId=4",
		"https://dev.azure.com/org/proj/_release?releaseId=x",
		"https://dev.azure.com/org/_release?releaseId=4",
		"https://dev.azure.com/org/proj/_build/results?buildId=4",
		"https://dev.azure.com/org/proj/_apis/release/definitions/4",
		"https://pat:secret@example.com/org/proj/_release?releaseId=4",
		"https://dev.azure.com/%zz",
	} {
		t.Run(ref, func(t *testing.T) {
			_, _, err := ParseReleaseRef(ref)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid release")
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

// relRefFakeClient answers GetReleaseDefinitions with defs, or err.
type relRefFakeClient struct {
	release.Client
	defs []release.ReleaseDefinition
	err  error
}

func (c relRefFakeClient) GetReleaseDefinitions(context.Context, release.GetReleaseDefinitionsArgs) (*release.GetReleaseDefinitionsResponseValue, error) {
	return &release.GetReleaseDefinitionsResponseValue{Value: c.defs}, c.err
}

func TestResolveDefinition(t *testing.T) {
	def := func(id int, name string) release.ReleaseDefinition {
		path := `\f`
		return release.ReleaseDefinition{Id: &id, Name: &name, Path: &path}
	}
	tests := []struct {
		name, ref string
		fake      relRefFakeClient
		id        int
		err       string
	}{
		{"id", "7", relRefFakeClient{}, 7, ""},
		{"name", "Rel", relRefFakeClient{defs: []release.ReleaseDefinition{def(2, "rel"), def(3, "rel2")}}, 2, ""},
		{"blank", " ", relRefFakeClient{}, 0, "invalid release definition"},
		{"none", "x", relRefFakeClient{}, 0, "no release definition with that name in project proj"},
		{"several", "rel", relRefFakeClient{defs: []release.ReleaseDefinition{def(2, "rel"), def(3, "REL")}}, 0,
			"several release definitions in project proj have that name; pass an ID:\n  \\f\\rel (ID 2)\n  \\f\\REL (ID 3)"},
		{"error", "x", relRefFakeClient{err: errors.New("boom")}, 0, "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := ResolveDefinition(context.Background(), &api.ReleaseClient{Project: "proj", Release: tt.fake}, tt.ref)
			if tt.err != "" {
				assert.ErrorContains(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.id, id)
		})
	}
}
