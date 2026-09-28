// Package serper provides a web search tool for agents using the Serper.dev API.
//
// Serper (https://serper.dev) is a fast, affordable Google Search API that
// returns structured JSON results including organic listings, knowledge graphs,
// and "People Also Ask" data.
//
// Usage:
//
//	searchTool := serper.New(os.Getenv("SERPER_API_KEY"))
//	searchTool := serper.New(apiKey, serper.WithMaxResults(3))
//
// Prerequisites:
//
//   - SERPER_API_KEY: API key from https://serper.dev

package serper

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
	defaultTimeout           = 10 * time.Second
	defaultMaxCharsPerResult = 300
)

// Option configures the Serper search tool.
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

// New creates a web_search tool that queries the Serper.dev Google Search API.
// The apiKey is required and can be obtained from https://serper.dev.
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
			log := tool.LoggerFrom(ctx)
			log.Logf("searching %q", req.Query)
			result, err := search(ctx, client, apiKey, req.Query, maxResults, maxChars)
			if err != nil {
				return "", err
			}
			log.Logf("got results")
			return result, nil
		},
	)
}

func search(ctx context.Context, client *http.Client, apiKey, query string, maxResults, maxChars int) (string, error) {
	reqBody := map[string]any{
		"q":   query,
		"num": maxResults,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://google.serper.dev/search", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-KEY", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Serper API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		KnowledgeGraph *struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"knowledgeGraph"`
		Organic []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(result.Organic) == 0 && result.KnowledgeGraph == nil {
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

	for i, r := range result.Organic {
		snippet := r.Snippet
		if maxChars > 0 && len(snippet) > maxChars {
			snippet = snippet[:maxChars] + "…"
		}
		fmt.Fprintf(&sb, "%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.Link, snippet)
	}

	return sb.String(), nil
}
