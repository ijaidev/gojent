package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ijaidev/gojent/core/tool"
)

// SearchResult represents an individual search result item.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// SearchProvider defines the interface for external search engines.
type SearchProvider interface {
	Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error)
}

// SearchInput defines the input parameters for web searches.
type SearchInput struct {
	Query      string `json:"query" description:"The search query string"`
	MaxResults int    `json:"max_results,omitempty" description:"Maximum number of search results to return (default 5)"`
}

// SearchOutput represents the search results.
type SearchOutput struct {
	Query   string         `json:"query"`
	Results []SearchResult `json:"results"`
	Total   int            `json:"total"`
}

// NewWebSearchTool creates a Tool for performing web searches using a SearchProvider.
func NewWebSearchTool(provider SearchProvider) (tool.Tool, error) {
	if provider == nil {
		return nil, fmt.Errorf("web_search: SearchProvider is required")
	}

	return tool.New(tool.Config[SearchInput, SearchOutput]{
		ID:          "web_search",
		Description: "Search the web for query terms and return relevant pages with titles, URLs, and snippets.",
		Execute: func(ctx context.Context, in SearchInput) (SearchOutput, error) {
			if in.Query == "" {
				return SearchOutput{}, fmt.Errorf("web_search: query cannot be empty")
			}
			maxResults := in.MaxResults
			if maxResults <= 0 {
				maxResults = 5
			}

			results, err := provider.Search(ctx, in.Query, maxResults)
			if err != nil {
				return SearchOutput{}, fmt.Errorf("web_search: %w", err)
			}

			return SearchOutput{
				Query:   in.Query,
				Results: results,
				Total:   len(results),
			}, nil
		},
	})
}

// MockSearchProvider provides deterministic search results for testing.
type MockSearchProvider struct {
	Results []SearchResult
	Err     error
}

// Search executes a mock search returning canned results.
func (m *MockSearchProvider) Search(_ context.Context, _ string, maxResults int) ([]SearchResult, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	if len(m.Results) <= maxResults {
		return m.Results, nil
	}
	return m.Results[:maxResults], nil
}

// TavilySearchProvider connects to the Tavily AI search API.
type TavilySearchProvider struct {
	APIKey string
	Client *http.Client
}

// Search performs a web search query through the Tavily API.
func (t *TavilySearchProvider) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	if t.APIKey == "" {
		return nil, fmt.Errorf("tavily: API key is required")
	}
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	payload := map[string]any{
		"api_key":        t.APIKey,
		"query":          query,
		"max_results":    maxResults,
		"include_answer": false,
		"search_depth":   "basic",
	}
	data, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tavily: api returned status %d", resp.StatusCode)
	}

	var tavilyResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tavilyResp); err != nil {
		return nil, err
	}

	var results []SearchResult
	for _, r := range tavilyResp.Results {
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
		})
	}
	return results, nil
}
