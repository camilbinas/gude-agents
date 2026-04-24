// Package brave provides a web search tool for agents using the Brave Search API.
//
// Brave Search (https://brave.com/search/api/) has its own independent web
// index and offers a generous free tier ($5/month in credits).
//
// Usage:
//
//	searchTool := brave.New(os.Getenv("BRAVE_API_KEY"))
//	searchTool := brave.New(apiKey, brave.WithMaxResults(3))
//
//	a, _ := agent.Default(provider, system, []tool.Tool{searchTool})
//
// Prerequisites:
//
//   - BRAVE_API_KEY: API key from https://brave.com/search/api/
package brave

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// defaults.
const (
	defaultMaxResults        = 5
	defaultTimeout           = 10 * time.Second
	defaultMaxCharsPerResult = 300
)

// Option configures the Brave search tool.
type Option func(*config)

type config struct {
	maxResults int
	timeout    time.Duration
	maxChars   int
	client     *http.Client
}

// WithMaxResults sets the maximum number of search results. Default: 5.
func WithMaxResults(n int) Option {
	return func(c *config) { c.maxResults = n }
}

// WithTimeout sets the HTTP request timeout. Default: 10s.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithMaxCharsPerResult sets the maximum characters per result snippet.
// Longer snippets are truncated. Default: 300.
func WithMaxCharsPerResult(n int) Option {
	return func(c *config) { c.maxChars = n }
}

// WithClient sets a custom HTTP client. When set, the timeout option
// is ignored — the caller is responsible for configuring it on the
// provided client.
func WithClient(client *http.Client) Option {
	return func(c *config) { c.client = client }
}

// New creates a web_search tool that queries the Brave Search API.
// The apiKey is required and can be obtained from https://brave.com/search/api/.
func New(apiKey string, opts ...Option) tool.Tool {
	cfg := &config{
		maxResults: defaultMaxResults,
		timeout:    defaultTimeout,
		maxChars:   defaultMaxCharsPerResult,
	}
	for _, o := range opts {
		o(cfg)
	}

	client := cfg.client
	if client == nil {
		client = &http.Client{Timeout: cfg.timeout}
	}

	maxResults := cfg.maxResults
	maxChars := cfg.maxChars

	return tool.NewRaw(
		"web_search",
		"Search the web for current information. Returns titles, URLs, and content snippets.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The search query",
				},
			},
			"required": []any{"query"},
		},
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var req struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(input, &req); err != nil {
				return "", err
			}
			return search(ctx, client, apiKey, req.Query, maxResults, maxChars)
		},
	)
}

func search(ctx context.Context, client *http.Client, apiKey, query string, maxResults, maxChars int) (string, error) {
	u := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query), maxResults)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Brave API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(result.Web.Results) == 0 {
		return "No results found.", nil
	}

	var sb strings.Builder
	for i, r := range result.Web.Results {
		desc := r.Description
		if maxChars > 0 && len(desc) > maxChars {
			desc = desc[:maxChars] + "…"
		}
		fmt.Fprintf(&sb, "%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, desc)
	}
	return sb.String(), nil
}
