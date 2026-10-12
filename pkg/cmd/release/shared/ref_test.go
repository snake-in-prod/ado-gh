package shared

import (
	"testing"

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
