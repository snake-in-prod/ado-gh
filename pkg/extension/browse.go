package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// BrowseRepo is an extension repository found on GitHub by SearchBrowsable.
type BrowseRepo struct {
	Source Source
	// Name is the extension name (the repo name without ado-, lowercased).
	Name string
	// Description is sanitized for printing.
	Description string
	Stars       int
}

// URL is the repository's github.com page, built from its validated owner and name.
func (r BrowseRepo) URL() string {
	return "https://" + hostGitHub + "/" + r.Source.Owner + "/" + r.Source.Repo
}

// SearchBrowsable searches GitHub for up to limit repositories with the ado-extension topic,
// best match first, in pages of up to 100, and keeps those named ado-<valid name>.
func SearchBrowsable(ctx context.Context, hc *http.Client, limit int) ([]BrowseRepo, error) {
	perPage := min(limit, 100)
	var repos []BrowseRepo
	for page, fetched := 1, 0; fetched < limit; page++ {
		q := url.Values{"q": {"topic:ado-extension"}, "per_page": {strconv.Itoa(perPage)}, "page": {strconv.Itoa(page)}}
		resp, err := githubGet(ctx, hc, "https://"+hostGitHubAPI+"/search/repositories?"+q.Encode(), "application/vnd.github+json")
		if err != nil {
			return nil, fmt.Errorf("searching GitHub: %w", err)
		}
		var body struct {
			Items []struct {
				FullName    string `json:"full_name"`
				Description string `json:"description"`
				Stars       int    `json:"stargazers_count"`
			} `json:"items"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("searching GitHub: %w", withoutURL(err))
		}
		items := body.Items[:min(len(body.Items), limit-fetched)]
		fetched += len(items)
		for _, it := range items {
			owner, repo, _ := strings.Cut(it.FullName, "/")
			src, err := githubSource(owner, repo)
			if err != nil {
				continue
			}
			name, err := NameFromRepo(src.Repo)
			if err != nil {
				continue
			}
			repos = append(repos, BrowseRepo{Source: src, Name: name, Description: Sanitize(it.Description), Stars: it.Stars})
		}
		if len(body.Items) < perPage {
			break
		}
	}
	return repos, nil
}

// FetchReadme returns the raw preferred README of GitHub repository s (at most 4 MiB), or ""
// when it has none.
func FetchReadme(ctx context.Context, hc *http.Client, s Source) (string, error) {
	resp, err := githubGet(ctx, hc, githubAPI(s, "readme"), "application/vnd.github.raw+json")
	if errors.Is(err, errGitHubNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("README of %s/%s: %w", s.Owner, s.Repo, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("README of %s/%s: %w", s.Owner, s.Repo, withoutURL(err))
	}
	return string(b), nil
}
