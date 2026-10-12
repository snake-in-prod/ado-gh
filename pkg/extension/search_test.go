package extension

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
)

func TestSearchQuery(t *testing.T) {
	kw := make([]string, 1, 4)
	kw[0] = "lint"
	q, err := SearchQuery(kw, "")
	require.NoError(t, err)
	assert.Equal(t, "lint topic:ado-extension", q)
	assert.Len(t, kw, 1)
	q, err = SearchQuery(nil, "My-Org")
	require.NoError(t, err)
	assert.Equal(t, "topic:ado-extension user:My-Org", q)
	_, err = SearchQuery(nil, "a user:b")
	assert.EqualError(t, err, `invalid owner "a user:b"`)
}

func TestSearchGitHubPages(t *testing.T) {
	reg := &httpmock.Registry{}
	page := func(n string, items int) {
		var list []map[string]any
		for i := range items {
			list = append(list, map[string]any{"full_name": fmt.Sprintf("o/ado-%s-%d", n, i), "stargazers_count": i})
		}
		reg.Register(func(r *http.Request) bool {
			return r.URL.Host == "api.github.com" && r.URL.Path == "/search/repositories" && r.URL.Query().Get("page") == n
		}, httpmock.JSONResponse(map[string]any{"total_count": 205, "items": list}))
	}
	page("1", 100)
	page("2", 100)
	page("3", 5)
	got, err := SearchGitHub(context.Background(), &http.Client{Transport: reg}, "topic:ado-extension", "updated", 1000)
	require.NoError(t, err)
	assert.Len(t, got, 205)
	assert.Equal(t, "o/ado-3-4", got[204].FullName)
	assert.Equal(t, "updated", reg.Requests[2].URL.Query().Get("sort"))
	reg.Verify(t)
}

func TestMarkInstalled(t *testing.T) {
	root := filepath.Join(t.TempDir(), "extensions")
	results := []SearchResult{{FullName: "O/ado-a"}, {FullName: "o/ado-b"}, {FullName: "o/ado-c"}}
	require.NoError(t, MarkInstalled(root, results), "missing root")
	for name, src := range map[string]string{"a": "https://github.com/o/ado-a", "b": "", "c": t.TempDir()} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, Prefix+name), 0o755))
		require.NoError(t, WriteRecord(root, name, &Record{Source: src}))
	}
	require.NoError(t, MarkInstalled(root, results))
	assert.Equal(t, []bool{true, false, false}, []bool{results[0].Installed, results[1].Installed, results[2].Installed})
}
