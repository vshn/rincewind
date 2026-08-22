package hats

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Topic is the label a repository puts on itself to be findable. It is
// self-assigned — anyone can add it — so what comes back is discovery, not a
// registry, and the command says so.
const Topic = "rincewind-hats"

// GitHub is named in Go here, which nothing outside this package does — and
// this is the only remote system the binary reaches at all. A hat cannot say
// where hats are found, and a search that works everywhere would need a
// protocol nobody has. The exception is one constant and one URL; everything
// else stays a git remote.
const apiURL = "https://api.github.com"

// searchTimeout bounds the one request. Long enough for a slow connection,
// short enough that a captive portal does not look like a hang.
const searchTimeout = 15 * time.Second

// Repo is one result.
type Repo struct {
	Name        string
	Description string
	Stars       int
	URL         string
}

// Searcher queries GitHub. BaseURL and Client exist so the tests can point
// it at an httptest server: the parsing is worth covering, and covering it
// against the real API would spend a rate limit to prove nothing.
type Searcher struct {
	BaseURL string
	Client  *http.Client
}

// Search returns the repositories carrying the topic, narrowed by words the
// user added.
func (s Searcher) Search(ctx context.Context, query string) ([]Repo, error) {
	base := s.BaseURL
	if base == "" {
		base = apiURL
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: searchTimeout}
	}

	q := "topic:" + Topic
	if query = strings.TrimSpace(query); query != "" {
		q += " " + query
	}
	endpoint := base + "/search/repositories?q=" + url.QueryEscape(q) + "&per_page=50"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// GitHub refuses a request without one.
	req.Header.Set("User-Agent", "rincewind")
	// Unauthenticated search allows ten requests a minute, which is enough
	// until it is not; a token raises it and reaches private repositories.
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searching GitHub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}

	var body struct {
		Items []struct {
			FullName    string `json:"full_name"`
			Description string `json:"description"`
			Stars       int    `json:"stargazers_count"`
			HTMLURL     string `json:"html_url"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("reading GitHub's answer: %w", err)
	}

	out := make([]Repo, 0, len(body.Items))
	for _, it := range body.Items {
		out = append(out, Repo{
			Name:        it.FullName,
			Description: it.Description,
			Stars:       it.Stars,
			URL:         it.HTMLURL,
		})
	}
	return out, nil
}

// statusError turns a refusal into something one can act on. The rate limit
// is the one that will actually happen, and it has a fix worth naming.
func statusError(resp *http.Response) error {
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return fmt.Errorf(
			"GitHub's search rate limit is used up (ten a minute without a token)\n\nwait a minute, or set GITHUB_TOKEN")
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("GitHub refused the search: %s\n\nif GITHUB_TOKEN is set, check that it is still valid", resp.Status)
	default:
		return fmt.Errorf("GitHub answered %s", resp.Status)
	}
}
