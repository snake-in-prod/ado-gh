package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/search"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/searchshared"
)

// ErrSearchUnavailable is returned when the organization has no Work Item Search, as on Azure
// DevOps Server without Search installed.
var ErrSearchUnavailable = errors.New("work item search not available: it needs Azure DevOps Services, " +
	"or Search installed on Azure DevOps Server")

// WorkItemSearchPage is one page of Work Item Search hits.
type WorkItemSearchPage struct {
	// IDs are the hits' work item IDs, in result order.
	IDs []int
	// Results is the number of hits on the page, also those without a readable ID.
	Results int
	// Count is the total number of hits.
	Count int
	// InfoCode is 0, or why the results may be incomplete (e.g. 1 reindexing, 8 $top trimmed).
	InfoCode int
}

// WorkItemSearcher runs Work Item Search queries in one project. Search has its own host
// (almsearch.dev.azure.com), found through ResourceAreas on first use.
type WorkItemSearcher struct {
	HTTP *http.Client
	// OrgURL is the organization URL ending in "/".
	OrgURL  string
	Project string

	client search.Client
}

// Search returns the page of hits for text (portal search syntax) after skipping skip hits,
// at most top, newest change first.
func (s *WorkItemSearcher) Search(ctx context.Context, text string, skip, top int) (*WorkItemSearchPage, error) {
	if s.client == nil {
		c, ok, err := areaClient(ctx, s.HTTP, s.OrgURL, search.ResourceAreaId)
		switch {
		case IsNotFound(mapErr(err)) || err == nil && !ok:
			return nil, ErrSearchUnavailable
		case err != nil:
			return nil, err
		}
		s.client = &search.ClientImpl{Client: *c}
	}
	field, order := "system.changeddate", "DESC"
	res, err := s.client.FetchWorkItemSearchResults(ctx, search.FetchWorkItemSearchResultsArgs{Project: &s.Project,
		Request: &search.WorkItemSearchRequest{
			SearchText: &text, Skip: &skip, Top: &top,
			Filters: &map[string][]string{"System.TeamProject": {s.Project}},
			OrderBy: &[]searchshared.SortOption{{Field: &field, SortOrder: &order}},
		}})
	if err = mapErr(err); IsNotFound(err) {
		return nil, ErrSearchUnavailable
	} else if err != nil {
		return nil, err
	}
	results := deref(res.Results)
	page := &WorkItemSearchPage{Results: len(results), Count: deref(res.Count), InfoCode: deref(res.InfoCode)}
	for _, r := range results {
		// Field keys are lowercase and values strings; Atoi also keeps the IDs safe for WIQL.
		if id, err := strconv.Atoi(deref(r.Fields)["system.id"]); err == nil {
			page.IDs = append(page.IDs, id)
		}
	}
	return page, nil
}
