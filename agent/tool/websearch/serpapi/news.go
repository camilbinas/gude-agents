package serpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// NewNews creates a news_search tool that queries Google News via SerpAPI.
// It returns current news articles with titles, sources, links, and dates.
//
// Usage:
//
//	newsTool := serpapi.NewNews(os.Getenv("SERPAPI_API_KEY"))
//	newsTool := serpapi.NewNews(apiKey, serpapi.WithMaxResults(10))
func NewNews(apiKey string, opts ...Option) tool.Tool {
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

	maxChars := cfg.maxChars

	return tool.NewRaw(
		"news_search",
		"Search Google News for current news articles. Returns headlines, sources, links, and publication dates.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The news search query",
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
			log.Logf("searching news %q", req.Query)
			result, err := searchNews(ctx, client, apiKey, req.Query, maxChars)
			if err != nil {
				return "", err
			}
			log.Logf("got news results")
			return result, nil
		},
	)
}

func searchNews(ctx context.Context, client *http.Client, apiKey, query string, maxChars int) (string, error) {
	u := fmt.Sprintf("https://serpapi.com/search.json?engine=google_news&q=%s&api_key=%s",
		url.QueryEscape(query),
		url.QueryEscape(apiKey),
	)

	resp, err := doGet(ctx, client, u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", apiError(resp, "Google News")
	}

	var result struct {
		NewsResults []struct {
			Title   string `json:"title"`
			Stories []struct {
				Title  string `json:"title"`
				Link   string `json:"link"`
				Source struct {
					Name string `json:"name"`
				} `json:"source"`
				Date string `json:"date"`
			} `json:"stories"`
		} `json:"news_results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(result.NewsResults) == 0 {
		return "No news results found.", nil
	}

	var sb strings.Builder
	n := 0
	for _, section := range result.NewsResults {
		for _, story := range section.Stories {
			n++
			title := story.Title
			if maxChars > 0 && len(title) > maxChars {
				title = title[:maxChars] + "…"
			}
			date := story.Date
			// Trim timezone suffix for readability (e.g. "09/27/2025, 06:29 PM, +0200 CEST" → "09/27/2025, 06:29 PM").
			if parts := strings.SplitN(date, ",", 3); len(parts) >= 2 {
				date = strings.TrimSpace(parts[0] + "," + parts[1])
			}
			fmt.Fprintf(&sb, "%d. %s\n   %s | %s\n   %s\n\n", n, title, story.Source.Name, date, story.Link)
		}
	}

	if n == 0 {
		return "No news results found.", nil
	}

	return sb.String(), nil
}
