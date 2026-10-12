package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/snake-in-prod/ado-gh/pkg/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var searchOrgSeq atomic.Int64

// searchOrg returns an org of its own per run for tests that stub the search area: the SDK caches
// resource-area discovery per base URL for the whole process, and StubSearch requires it (-count>1).
func searchOrg(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, searchOrgSeq.Add(1))
}

// newSearcher returns a searcher for its own org (the SDK caches discovery per base URL) with
// the org's locations stubbed; stub the search area with reg.StubSearch.
func newSearcher(t *testing.T, org string) (*WorkItemSearcher, *httpmock.Registry) {
	t.Helper()
	reg := &httpmock.Registry{}
	t.Cleanup(func() { reg.Verify(t) })
	reg.StubLocations(org, httpmock.ResourceAreasLocation)
	hc := NewHTTPClient(ClientOptions{Transport: reg})
	return &WorkItemSearcher{HTTP: hc, OrgURL: "https://dev.azure.com/" + org + "/", Project: "My Proj"}, reg
}

func TestWorkItemSearch(t *testing.T) {
	org := searchOrg("search-page")
	s, reg := newSearcher(t, org)
	reg.StubSearch(org)
	var body map[string]any
	reg.Register(httpmock.REST(http.MethodPost, org+"/My%20Proj/_apis/search/workitemsearchresults"), func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "almsearch.dev.azure.com", r.URL.Host)
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &body))
		return httpmock.JSONResponse(map[string]any{"count": 7, "infoCode": 8, "results": []map[string]any{
			{"fields": map[string]string{"system.id": "12", "system.title": "a"}},
			{"fields": map[string]string{"system.id": "oops"}},
			{"fields": map[string]string{"system.id": "3"}},
		}})(r)
	})

	page, err := s.Search(context.Background(), "crash a:@me", 1000, 500)
	require.NoError(t, err)
	assert.Equal(t, &WorkItemSearchPage{IDs: []int{12, 3}, Results: 3, Count: 7, InfoCode: 8}, page)
	assert.Equal(t, map[string]any{
		"searchText": "crash a:@me", "$skip": float64(1000), "$top": float64(500),
		"filters":  map[string]any{"System.TeamProject": []any{"My Proj"}},
		"$orderBy": []any{map[string]any{"field": "system.changeddate", "sortOrder": "DESC"}},
	}, body)
}

func TestWorkItemSearchUnavailable(t *testing.T) {
	t.Run("no search area", func(t *testing.T) {
		org := "search-noarea"
		s, reg := newSearcher(t, org)
		reg.StubResourceAreas(org)
		_, err := s.Search(context.Background(), "x", 0, 10)
		assert.ErrorIs(t, err, ErrSearchUnavailable)
		assert.EqualError(t, err, ErrSearchUnavailable.Error())
	})
	t.Run("no ResourceAreas", func(t *testing.T) {
		org := "search-noareas"
		s, reg := newSearcher(t, org)
		reg.Register(httpmock.REST(http.MethodGet, org+"/_apis/ResourceAreas"),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "not found"}))
		_, err := s.Search(context.Background(), "x", 0, 10)
		assert.ErrorIs(t, err, ErrSearchUnavailable)
	})
	t.Run("404", func(t *testing.T) {
		org := searchOrg("search-404")
		s, reg := newSearcher(t, org)
		reg.StubSearch(org)
		reg.Register(httpmock.REST(http.MethodPost, org+"/My%20Proj/_apis/search/workitemsearchresults"),
			httpmock.StatusJSONResponse(http.StatusNotFound, map[string]any{"message": "not found"}))
		_, err := s.Search(context.Background(), "x", 0, 10)
		assert.ErrorIs(t, err, ErrSearchUnavailable)
	})
	t.Run("other errors pass through", func(t *testing.T) {
		org := searchOrg("search-500")
		s, reg := newSearcher(t, org)
		reg.StubSearch(org)
		reg.Register(httpmock.REST(http.MethodPost, org+"/My%20Proj/_apis/search/workitemsearchresults"),
			httpmock.StatusJSONResponse(http.StatusBadRequest, map[string]any{"message": "bad query"}))
		_, err := s.Search(context.Background(), "x", 0, 10)
		var he *HTTPError
		require.ErrorAs(t, err, &he)
		assert.Equal(t, "bad query", he.Message)
		assert.False(t, errors.Is(err, ErrSearchUnavailable))
	})
}
