package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SearchTopic is the GitHub topic that marks a repository as an ado extension.
const SearchTopic = "ado-extension"

// SearchMaxResults is the most results GitHub returns for one search.
const SearchMaxResults = 1000

// SearchQuery returns the GitHub search query for extensions matching keywords, limited to
// repositories of owner when set.
func SearchQuery(keywords []string, owner string) (string, error) {
	q := append(slices.Clone(keywords), "topic:"+SearchTopic)
	if owner != "" {
		if !githubOwner.MatchString(owner) {
			return "", fmt.Errorf("invalid owner %q", owner)
		}
		q = append(q, "user:"+owner)
	}
	return strings.Join(q, " "), nil
}

// SearchResult is a repository found by SearchGitHub.
type SearchResult struct {
	FullName    string    `json:"fullName"`
	Description string    `json:"description"`
	Stars       int       `json:"stars"`
	UpdatedAt   time.Time `json:"updatedAt"`
	URL         string    `json:"url"`
	// Installed is set by MarkInstalled.
	Installed bool `json:"installed"`
}

// SearchGitHub runs the repository search q (see SearchQuery), sorted by sort ("stars",
// "updated" or "" for best match), and returns up to limit results, 100 per request.
func SearchGitHub(ctx context.Context, hc *http.Client, q, sort string, limit int) ([]SearchResult, error) {
	perPage := min(limit, 100)
	var out []SearchResult
	for page := 1; len(out) < limit; page++ {
		v := url.Values{"q": {q}, "per_page": {strconv.Itoa(perPage)}, "page": {strconv.Itoa(page)}}
		if sort != "" {
			v.Set("sort", sort)
			v.Set("order", "desc")
		}
		resp, err := githubGet(ctx, hc, "https://"+hostGitHubAPI+"/search/repositories?"+v.Encode(), "application/vnd.github+json")
		if err != nil {
			return nil, fmt.Errorf("searching GitHub: %w", err)
		}
		var body struct {
			Total int `json:"total_count"`
			Items []struct {
				FullName    string    `json:"full_name"`
				Description string    `json:"description"`
				Stars       int       `json:"stargazers_count"`
				UpdatedAt   time.Time `json:"updated_at"`
				URL         string    `json:"html_url"`
			} `json:"items"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("searching GitHub: %w", withoutURL(err))
		}
		for _, it := range body.Items {
			if len(out) == limit {
				break
			}
			out = append(out, SearchResult{FullName: it.FullName, Description: it.Description,
				Stars: it.Stars, UpdatedAt: it.UpdatedAt, URL: it.URL})
		}
		if len(body.Items) < perPage || len(out) >= body.Total {
			break
		}
	}
	return out, nil
}

// MarkInstalled sets Installed on the results whose repository is the recorded source of an
// extension installed under root.
func MarkInstalled(root string, results []SearchResult) error {
	exts, err := List(root)
	if err != nil {
		return err
	}
	installed := map[string]bool{}
	for _, e := range exts {
		if rec, err := ReadRecord(root, e.Name); err == nil && rec.Source != "" {
			if s, err := ParseSource(rec.Source); err == nil && !s.IsLocal() {
				installed[s.String()] = true
			}
		}
	}
	for i, r := range results {
		results[i].Installed = installed[strings.ToLower(hostGitHub+"/"+r.FullName)]
	}
	return nil
}
