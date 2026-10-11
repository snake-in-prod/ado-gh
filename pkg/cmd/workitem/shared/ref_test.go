package shared

import (
	"testing"

	"github.com/snake-in-prod/ado-gh/internal/adocontext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		ref     string
		id      int
		project adocontext.Repo
	}{
		{"123", 123, adocontext.Repo{}},
		{"#123", 123, adocontext.Repo{}},
		{"https://dev.azure.com/org/proj/_workitems/edit/123", 123,
			adocontext.Repo{Host: "dev.azure.com", Org: "org", Project: "proj"}},
		{"https://dev.azure.com/org/My%20Proj/_workitems/edit/7/?fullScreen=true#x", 7,
			adocontext.Repo{Host: "dev.azure.com", Org: "org", Project: "My Proj"}},
		{"https://Fabrikam.VisualStudio.com/DefaultCollection/proj/_workitems/edit/9", 9,
			adocontext.Repo{Host: "fabrikam.visualstudio.com", Org: "fabrikam", Project: "proj"}},
		{"https://fabrikam.visualstudio.com/proj/_workitems/edit/9", 9,
			adocontext.Repo{Host: "fabrikam.visualstudio.com", Org: "fabrikam", Project: "proj"}},
		{"https://dev.azure.com/org/proj/_apis/wit/workItems/42", 42,
			adocontext.Repo{Host: "dev.azure.com", Org: "org", Project: "proj"}},
		{"https://dev.azure.com/org/_apis/wit/workItems/42", 42,
			adocontext.Repo{Host: "dev.azure.com", Org: "org"}},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			id, project, err := ParseRef(tt.ref)
			require.NoError(t, err)
			assert.Equal(t, tt.id, id)
			assert.Equal(t, tt.project, project)
		})
	}
}

func TestParseRefInvalid(t *testing.T) {
	for _, ref := range []string{
		"", "#", "0", "#0", "-1", "abc", "12a", "##1", "!12",
		"http://dev.azure.com/org/proj/_workitems/edit/1",
		"https://example.com/org/proj/_workitems/edit/1",
		"https://dev.azure.com/org/proj/_git/repo/pullrequest/1",
		"https://dev.azure.com/org/proj/_workitems/edit/x",
		"https://dev.azure.com/org/proj/_workitems/edit/0",
		"https://dev.azure.com/org/proj/_workitems/1",
		"https://dev.azure.com/_workitems/edit/1",
		"https://user:secret@dev.azure.com/org/proj/_boards/1",
	} {
		t.Run(ref, func(t *testing.T) {
			_, _, err := ParseRef(ref)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secret", "a URL ref is never echoed")
		})
	}
}
