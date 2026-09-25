package serpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// NewScholar creates a scholar_search tool that queries Google Scholar via SerpAPI.
// It returns academic papers with titles, authors, snippets, citation counts,
// and links.
//
// Usage:
//
//	scholarTool := serpapi.NewScholar(os.Getenv("SERPAPI_API_KEY"))
//	scholarTool := serpapi.NewScholar(apiKey, serpapi.WithMaxResults(10))
func NewScholar(apiKey string, opts ...Option) tool.Tool {
	cfg := &config{
		maxResults: defaultMaxResults,
		timeout:    20 * time.Second,
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
		"scholar_search",
		"Search Google Scholar for academic papers and publications. Returns paper titles, authors, citation counts, snippets, and links.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The academic search query",
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
			log.Logf("searching scholar %q", req.Query)
			result, err := searchScholar(ctx, client, apiKey, req.Query, maxResults, maxChars)
			if err != nil {
				return "", err
			}
			log.Logf("got scholar results")
			return result, nil
		},
	)
}

func searchScholar(ctx context.Context, client *http.Client, apiKey, query string, maxResults, maxChars int) (string, error) {
	u := fmt.Sprintf("https://serpapi.com/search.json?engine=google_scholar&q=%s&api_key=%s&num=%d",
		url.QueryEscape(query),
		url.QueryEscape(apiKey),
		maxResults,
	)

	resp, err := doGet(ctx, client, u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", apiError(resp, "Google Scholar")
	}

	var result struct {
		OrganicResults []struct {
			Title           string `json:"title"`
			Link            string `json:"link"`
			Snippet         string `json:"snippet"`
			PublicationInfo struct {
				Summary string `json:"summary"`
			} `json:"publication_info"`
			InlineLinks struct {
				CitedBy *struct {
					Total int `json:"total"`
				} `json:"cited_by"`
			} `json:"inline_links"`
			Resources []struct {
				Title      string `json:"title"`
				FileFormat string `json:"file_format"`
				Link       string `json:"link"`
			} `json:"resources"`
		} `json:"organic_results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(result.OrganicResults) == 0 {
		return "No scholar results found.", nil
	}

	var sb strings.Builder
	for i, r := range result.OrganicResults {
		snippet := r.Snippet
		if maxChars > 0 && len(snippet) > maxChars {
			snippet = snippet[:maxChars] + "…"
		}

		fmt.Fprintf(&sb, "%d. %s\n", i+1, r.Title)
		if r.PublicationInfo.Summary != "" {
			fmt.Fprintf(&sb, "   %s\n", r.PublicationInfo.Summary)
		}
		if r.InlineLinks.CitedBy != nil {
			fmt.Fprintf(&sb, "   Cited by: %d\n", r.InlineLinks.CitedBy.Total)
		}
		if r.Link != "" {
			fmt.Fprintf(&sb, "   %s\n", r.Link)
		}
		if len(r.Resources) > 0 {
			fmt.Fprintf(&sb, "   [%s] %s\n", r.Resources[0].FileFormat, r.Resources[0].Link)
		}
		if snippet != "" {
			fmt.Fprintf(&sb, "   %s\n", snippet)
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}
