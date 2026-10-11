package shared

import (
	"encoding/json"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeThreads decodes a JSON thread list as the SDK does, so properties are map[string]any.
func decodeThreads(t *testing.T, s string) []git.GitPullRequestCommentThread {
	t.Helper()
	var out []git.GitPullRequestCommentThread
	require.NoError(t, json.Unmarshal([]byte(s), &out))
	return out
}

func TestVisibleThreads(t *testing.T) {
	all := decodeThreads(t, `[
		{"id": 1, "comments": [{"id": 1, "content": "keep"}, {"id": 2, "isDeleted": true}, {"id": 3, "commentType": "system"}]},
		{"id": 2, "isDeleted": true, "comments": [{"id": 1, "content": "deleted thread"}]},
		{"id": 3, "comments": [{"id": 1, "commentType": "system", "content": "voted"}]},
		{"id": 4, "properties": {"CodeReviewThreadType": {"$type": "System.String", "$value": "VoteUpdate"}},
		 "comments": [{"id": 1, "commentType": "text", "content": "system thread with a text comment"}]},
		{"id": 5, "properties": {"Microsoft.TeamFoundation.Discussion.SupportsMarkdown": {"$type": "System.Int32", "$value": 1}},
		 "comments": [{"id": 1, "content": "user thread"}]},
		{"id": 6},
		{"id": 7, "comments": [{"id": 1, "isDeleted": true}]}
	]`)

	got := VisibleThreads(all)
	ids := make([]int, len(got))
	for i, th := range got {
		ids[i] = Deref(th.Id)
	}
	assert.Equal(t, []int{1, 5}, ids)
	require.Len(t, *got[0].Comments, 1)
	assert.Equal(t, "keep", Deref((*got[0].Comments)[0].Content))
	assert.Empty(t, VisibleThreads(nil))
}

func TestFilePathAndThreadLines(t *testing.T) {
	ths := decodeThreads(t, `[
		{"id": 1},
		{"id": 2, "threadContext": {"filePath": "/a.go", "rightFileStart": {"line": 10, "offset": 1}, "rightFileEnd": {"line": 10, "offset": 5}}},
		{"id": 3, "threadContext": {"filePath": "/b.go", "rightFileStart": {"line": 4, "offset": 1}, "rightFileEnd": {"line": 8, "offset": 1}}},
		{"id": 4, "threadContext": {"filePath": "/c.go", "leftFileStart": {"line": 7, "offset": 1}, "leftFileEnd": {"line": 9, "offset": 1}}},
		{"id": 5, "threadContext": {"filePath": "/d.go"}},
		{"id": 6, "threadContext": {"filePath": "/e.go", "rightFileStart": {"line": 3, "offset": 1}}}
	]`)
	tests := []struct {
		file       string
		start, end int
	}{
		{"", 0, 0},
		{"/a.go", 10, 10},
		{"/b.go", 4, 8},
		{"/c.go", 7, 9},
		{"/d.go", 0, 0},
		{"/e.go", 3, 3},
	}
	for i, tt := range tests {
		assert.Equal(t, tt.file, FilePath(ths[i]), "thread %d", i+1)
		start, end := ThreadLines(ths[i])
		assert.Equal(t, [2]int{tt.start, tt.end}, [2]int{start, end}, "thread %d", i+1)
	}
}
