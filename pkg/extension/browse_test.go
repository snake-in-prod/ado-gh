package extension

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
)

// browseItems returns n search items named ado-ext<i> starting at from.
func browseItems(from, n int) []map[string]any {
	items := []map[string]any{}
	for i := from; i < from+n; i++ {
		items = append(items, map[string]any{"full_name": fmt.Sprintf("o/ado-ext%d", i), "stargazers_count": i})
	}
	return items
}

func TestSearchBrowsable(t *testing.T) {
	hc := &http.Client{}
	reg := httpmock.ReplaceTripper(hc)
	defer reg.Verify(t)
	reg.Register(hostIs("api.github.com", "/search/repositories"), httpmock.JSONResponse(map[string]any{
		"items": []map[string]any{
			{"full_name": "o/ado-hello", "description": "Says \x1b[31mhi\x1b[0m", "stargazers_count": 7,
				"html_url": "https://evil.example/o/ado-hello"},
			{"full_name": "o/gh-hello", "description": "not ours"},
			{"full_name": "o/ado-", "description": "no name"},
			{"full_name": "o/ado-bad name", "description": "invalid"},
			{"full_name": "O/Ado-Lint", "description": nil, "stargazers_count": 1},
		},
	}))
	repos, err := SearchBrowsable(context.Background(), hc, 100)
	require.NoError(t, err)
	assert.Equal(t, []BrowseRepo{
		{Source: Source{Host: "github.com", Owner: "o", Repo: "ado-hello"}, Name: "hello", Description: "Says hi", Stars: 7},
		{Source: Source{Host: "github.com", Owner: "O", Repo: "Ado-Lint"}, Name: "lint", Stars: 1},
	}, repos)
	assert.Equal(t, "https://github.com/o/ado-hello", repos[0].URL())

	req := reg.Requests[0]
	assert.Equal(t, "topic:ado-extension", req.URL.Query().Get("q"))
	assert.Equal(t, "100", req.URL.Query().Get("per_page"))
	assert.Equal(t, "1", req.URL.Query().Get("page"))
	assert.Equal(t, "application/vnd.github+json", req.Header.Get("Accept"))
}

func TestSearchBrowsablePages(t *testing.T) {
	hc := &http.Client{}
	reg := httpmock.ReplaceTripper(hc)
	defer reg.Verify(t)
	page := func(n string) httpmock.Matcher {
		return func(req *http.Request) bool {
			return hostIs("api.github.com", "/search/repositories")(req) && req.URL.Query().Get("page") == n
		}
	}
	reg.Register(page("1"), httpmock.JSONResponse(map[string]any{"items": browseItems(0, 100)}))
	reg.Register(page("2"), httpmock.JSONResponse(map[string]any{"items": browseItems(100, 100)}))
	repos, err := SearchBrowsable(context.Background(), hc, 150)
	require.NoError(t, err)
	assert.Len(t, repos, 150, "the last page is cut at the limit")
	assert.Equal(t, "ext149", repos[149].Name)

	// A short page ends the search before the limit.
	reg = httpmock.ReplaceTripper(hc)
	reg.Register(page("1"), httpmock.JSONResponse(map[string]any{"items": browseItems(0, 3)}))
	repos, err = SearchBrowsable(context.Background(), hc, 500)
	require.NoError(t, err)
	assert.Len(t, repos, 3)
	assert.Len(t, reg.Requests, 1)

	reg = httpmock.ReplaceTripper(hc)
	reg.Register(page("1"), httpmock.JSONResponse(map[string]any{"items": browseItems(0, 5)}))
	repos, err = SearchBrowsable(context.Background(), hc, 5)
	require.NoError(t, err)
	assert.Len(t, repos, 5)
	assert.Equal(t, "5", reg.Requests[0].URL.Query().Get("per_page"))
}

func TestSearchBrowsableErrors(t *testing.T) {
	hc := &http.Client{}
	reg := httpmock.ReplaceTripper(hc)
	reg.Register(httpmock.MatchAny, httpmock.WithHeader(httpmock.StatusStringResponse(http.StatusForbidden, ""), "X-RateLimit-Remaining", "0"))
	_, err := SearchBrowsable(context.Background(), hc, 100)
	assert.ErrorContains(t, err, "searching GitHub: GitHub API rate limit exceeded")
	assert.ErrorContains(t, err, "set GH_TOKEN")

	reg.Register(httpmock.MatchAny, httpmock.StringResponse("{"))
	_, err = SearchBrowsable(context.Background(), hc, 100)
	assert.ErrorContains(t, err, "searching GitHub:")
}

func TestFetchReadme(t *testing.T) {
	hc := &http.Client{}
	reg := httpmock.ReplaceTripper(hc)
	defer reg.Verify(t)
	src := Source{Host: "github.com", Owner: "o", Repo: "ado-hello"}
	reg.Register(hostIs("api.github.com", "/repos/o/ado-hello/readme"), httpmock.StringResponse("# Hello\n"))
	md, err := FetchReadme(context.Background(), hc, src)
	require.NoError(t, err)
	assert.Equal(t, "# Hello\n", md)
	assert.Equal(t, "application/vnd.github.raw+json", reg.Requests[0].Header.Get("Accept"))

	reg.Register(httpmock.MatchAny, httpmock.StatusStringResponse(http.StatusNotFound, `{"message":"Not Found"}`))
	md, err = FetchReadme(context.Background(), hc, src)
	require.NoError(t, err)
	assert.Empty(t, md, "no README is not an error")

	reg.Register(httpmock.MatchAny, httpmock.StatusStringResponse(http.StatusInternalServerError, ""))
	_, err = FetchReadme(context.Background(), hc, src)
	assert.EqualError(t, err, "README of o/ado-hello: HTTP 500")
}
