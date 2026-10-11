package shared

import (
	"testing"

	"github.com/snake-in-prod/ado-gh/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var linkTestTypes = []api.RelationType{
	{Name: "Parent", ReferenceName: "System.LinkTypes.Hierarchy-Reverse", Usage: "workItemLink"},
	{Name: "Tested By", ReferenceName: "Microsoft.VSTS.Common.TestedBy-Forward", Usage: "workItemLink"},
	{Name: "Hyperlink", ReferenceName: "Hyperlink", Usage: "resourceLink"},
	{Name: "Artifact Link", ReferenceName: "ArtifactLink", Usage: "resourceLink"},
}

func TestParseLink(t *testing.T) {
	const org = "https://dev.azure.com/o/"
	tests := []struct {
		arg     string
		want    Link
		wantErr string
	}{
		{"parent:7", Link{Rel: "System.LinkTypes.Hierarchy-Reverse", URL: org + "_apis/wit/workItems/7", ID: 7}, ""},
		{"tested by:#8", Link{Rel: "Microsoft.VSTS.Common.TestedBy-Forward", URL: org + "_apis/wit/workItems/8", ID: 8}, ""},
		{"System.LinkTypes.Hierarchy-Reverse: 9", Link{Rel: "System.LinkTypes.Hierarchy-Reverse", URL: org + "_apis/wit/workItems/9", ID: 9}, ""},
		{"url:https://x.test/a:b", Link{Rel: "Hyperlink", URL: "https://x.test/a:b"}, ""},
		{"Hyperlink:https://x.test", Link{Rel: "Hyperlink", URL: "https://x.test"}, ""},
		{"url:nope", Link{}, "a hyperlink needs a URL"},
		{"parent", Link{}, "expected <type>:<target>"},
		{"parent:", Link{}, "expected <type>:<target>"},
		{"parent:x", Link{}, "invalid work item reference"},
		{"ArtifactLink:1", Link{}, `unknown link type "ArtifactLink"; valid types: Parent, Tested By, url`},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			got, err := ParseLink(tt.arg, linkTestTypes, org)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLinkMatches(t *testing.T) {
	wi := Link{Rel: "System.LinkTypes.Related", URL: "https://dev.azure.com/o/_apis/wit/workItems/5", ID: 5}
	assert.True(t, wi.Matches(api.WorkItemRelation{Rel: "system.linktypes.related", URL: "https://o.visualstudio.com/_apis/wit/workitems/5"}))
	assert.False(t, wi.Matches(api.WorkItemRelation{Rel: "System.LinkTypes.Related", URL: "https://dev.azure.com/o/_apis/wit/workItems/15"}))
	assert.False(t, wi.Matches(api.WorkItemRelation{Rel: "System.LinkTypes.Hierarchy-Forward", URL: wi.URL}))
	assert.Equal(t, "System.LinkTypes.Related:5", wi.String())

	h := Link{Rel: "Hyperlink", URL: "https://x.test/A"}
	assert.True(t, h.Matches(api.WorkItemRelation{Rel: "Hyperlink", URL: "https://x.test/a"}))
	assert.False(t, h.Matches(api.WorkItemRelation{Rel: "Hyperlink", URL: "https://x.test/b"}))
	assert.Equal(t, "Hyperlink:https://x.test/A", h.String())
}
