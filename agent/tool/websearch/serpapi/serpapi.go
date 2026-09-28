// Package serpapi provides a web search tool for agents using the SerpAPI service.
//
// SerpAPI (https://serpapi.com) scrapes and parses Google (and 80+ other
// engines) in real time, returning richly structured JSON results including
// organic listings, knowledge graphs, local packs, and more.
//
// Usage:
//
//	searchTool := serpapi.New(os.Getenv("SERPAPI_API_KEY"))
//	searchTool := serpapi.New(apiKey, serpapi.WithMaxResults(3))
//
// Prerequisites:
//
//   - SERPAPI_API_KEY: API key from https://serpapi.com/manage-api-key

package serpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// defaults.
const (
	defaultMaxResults        = 5
	defaultTimeout           = 10 * time.Second
	defaultMaxCharsPerResult = 300
	defaultEngine            = "google"
)

// Option configures the SerpAPI search tool.
type Option func(*config)

type config struct {
	maxResults int
	timeout    time.Duration
	maxChars   int
	engine     string
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

// WithEngine sets the search engine to use. Default: "google".
// See https://serpapi.com for a list of supported engines (bing, yahoo,
// baidu, yandex, etc.).
func WithEngine(engine string) Option {
	return func(c *config) { c.engine = engine }
}

// WithClient sets a custom HTTP client. When set, the timeout option
// is ignored — the caller is responsible for configuring it on the
// provided client.
func WithClient(client *http.Client) Option {
	return func(c *config) { c.client = client }
}

// New creates a web_search tool that queries the SerpAPI Search API.
// The apiKey is required and can be obtained from https://serpapi.com/manage-api-key.
func New(apiKey string, opts ...Option) tool.Tool {
	cfg := &config{
		maxResults: defaultMaxResults,
		timeout:    defaultTimeout,
		maxChars:   defaultMaxCharsPerResult,
		engine:     defaultEngine,
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
	engine := cfg.engine

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
			log := tool.LoggerFrom(ctx)
			log.Logf("searching %q", req.Query)
			result, err := search(ctx, client, apiKey, req.Query, engine, maxResults, maxChars)
			if err != nil {
				return "", err
			}
			log.Logf("got results")
			return result, nil
		},
	)
}

func search(ctx context.Context, client *http.Client, apiKey, query, engine string, maxResults, maxChars int) (string, error) {
	u := fmt.Sprintf("https://serpapi.com/search.json?engine=%s&q=%s&api_key=%s&num=%s",
		url.QueryEscape(engine),
		url.QueryEscape(query),
		url.QueryEscape(apiKey),
		strconv.Itoa(maxResults),
	)

	resp, err := doGet(ctx, client, u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", apiError(resp, "Google Search")
	}

	var result struct {
		KnowledgeGraph *struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"knowledge_graph"`
		OrganicResults []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic_results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(result.OrganicResults) == 0 && result.KnowledgeGraph == nil {
		return "No results found.", nil
	}

	var sb strings.Builder

	if result.KnowledgeGraph != nil && result.KnowledgeGraph.Description != "" {
		desc := result.KnowledgeGraph.Description
		if maxChars > 0 && len(desc) > maxChars {
			desc = desc[:maxChars] + "…"
		}
		fmt.Fprintf(&sb, "Knowledge Graph: %s\n%s\n\n", result.KnowledgeGraph.Title, desc)
	}

	for i, r := range result.OrganicResults {
		snippet := r.Snippet
		if maxChars > 0 && len(snippet) > maxChars {
			snippet = snippet[:maxChars] + "…"
		}
		fmt.Fprintf(&sb, "%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.Link, snippet)
	}

	return sb.String(), nil
}
