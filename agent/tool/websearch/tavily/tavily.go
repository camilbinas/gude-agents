// Package tavily provides a web search tool for agents using the Tavily API.
//
// Tavily (https://tavily.com) is a search API designed for AI agents.
// It returns titles, URLs, content snippets, and relevance scores.
//
// Usage:
//
//	searchTool := tavily.New(os.Getenv("TAVILY_API_KEY"))
//	searchTool := tavily.New(apiKey, tavily.WithMaxResults(3))
//
// Prerequisites:
//
//   - TAVILY_API_KEY: free API key from https://app.tavily.com

package tavily

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// defaults.
const (
	defaultMaxResults        = 5
	defaultSearchDepth       = "basic"
	defaultTimeout           = 10 * time.Second
	defaultMaxCharsPerResult = 300
)

// Option configures the web search tool.
type Option func(*config)

type config struct {
	maxResults    int
	searchDepth   string
	timeout       time.Duration
	maxChars      int
	includeAnswer bool
	client        *http.Client
}

// WithMaxResults sets the maximum number of search results. Default: 5.
func WithMaxResults(n int) Option {
	return func(c *config) { c.maxResults = n }
}

// WithSearchDepth sets the Tavily search depth: "basic" or "advanced".
// Advanced provides higher quality results but uses more credits and is
// slower. Default: "basic".
func WithSearchDepth(depth string) Option {
	return func(c *config) { c.searchDepth = depth }
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

// WithIncludeAnswer requests Tavily to include an AI-generated short
// answer alongside the search results. Default: false.
func WithIncludeAnswer() Option {
	return func(c *config) { c.includeAnswer = true }
}

// WithClient sets a custom HTTP client. When set, the timeout option
// is ignored — the caller is responsible for configuring it on the
// provided client.
func WithClient(client *http.Client) Option {
	return func(c *config) { c.client = client }
}

// New creates a web_search tool that queries the Tavily Search API.
// The apiKey is required and can be obtained from https://app.tavily.com.
func New(apiKey string, opts ...Option) tool.Tool {
	cfg := &config{
		maxResults:  defaultMaxResults,
		searchDepth: defaultSearchDepth,
		timeout:     defaultTimeout,
		maxChars:    defaultMaxCharsPerResult,
	}
	for _, o := range opts {
		o(cfg)
	}

	client := cfg.client
	if client == nil {
		client = &http.Client{Timeout: cfg.timeout}
	}

	maxResults := cfg.maxResults
	searchDepth := cfg.searchDepth
	maxChars := cfg.maxChars
	includeAnswer := cfg.includeAnswer

	return tool.NewRaw(
		"web_search",
		"Search the web for current information. Returns titles, URLs, content snippets, and relevance scores.",
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
			return search(ctx, client, apiKey, req.Query, maxResults, searchDepth, maxChars, includeAnswer)
		},
	)
}

func search(ctx context.Context, client *http.Client, apiKey, query string, maxResults int, searchDepth string, maxChars int, includeAnswer bool) (string, error) {
	reqBody := map[string]any{
		"query":          query,
		"max_results":    maxResults,
		"search_depth":   searchDepth,
		"include_answer": includeAnswer,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Tavily API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title   string  `json:"title"`
			URL     string  `json:"url"`
			Content string  `json:"content"`
			Score   float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(result.Results) == 0 && result.Answer == "" {
		return "No results found.", nil
	}

	var sb strings.Builder

	if result.Answer != "" {
		fmt.Fprintf(&sb, "Answer: %s\n\n", result.Answer)
	}

	for i, r := range result.Results {
		content := r.Content
		if maxChars > 0 && len(content) > maxChars {
			content = content[:maxChars] + "…"
		}
		fmt.Fprintf(&sb, "%d. %s (score: %.2f)\n   %s\n   %s\n\n", i+1, r.Title, r.Score, r.URL, content)
	}

	return sb.String(), nil
}
