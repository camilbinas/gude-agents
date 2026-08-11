package serpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// maxRawResponseChars is the maximum characters returned from the raw JSON
// response for the generic engine tool. This prevents excessive token usage
// when the response is large.
const maxRawResponseChars = 4000

// EngineConfig configures a generic SerpAPI engine tool.
type EngineConfig struct {
	// ToolName is the name exposed to the LLM (e.g. "flights_search").
	ToolName string

	// Description is the tool description shown to the LLM.
	Description string

	// Params are extra query parameters sent with every request
	// (e.g. {"departure_id": "FRA", "arrival_id": "JFK"}).
	// The "q" parameter is always set from the LLM's input.
	Params map[string]string
}

// NewEngine creates a generic SerpAPI tool for any engine. The response is
// returned as truncated raw JSON — use this as an escape hatch for engines
// that don't have a dedicated constructor (flights, hotels, trends, etc.).
//
// Usage:
//
//	flightsTool := serpapi.NewEngine(apiKey, "google_flights", serpapi.EngineConfig{
//	    ToolName:    "flights_search",
//	    Description: "Search Google Flights for routes and prices.",
//	    Params:      map[string]string{"departure_id": "FRA", "arrival_id": "JFK"},
//	})
//
//	trendsTool := serpapi.NewEngine(apiKey, "google_trends", serpapi.EngineConfig{
//	    ToolName:    "trends_search",
//	    Description: "Look up Google Trends data for keywords.",
//	})
func NewEngine(apiKey, engine string, ec EngineConfig, opts ...Option) tool.Tool {
	cfg := &config{
		timeout: defaultTimeout,
	}
	for _, o := range opts {
		o(cfg)
	}

	client := cfg.client
	if client == nil {
		client = &http.Client{Timeout: cfg.timeout}
	}

	toolName := ec.ToolName
	if toolName == "" {
		toolName = engine + "_search"
	}
	description := ec.Description
	if description == "" {
		description = fmt.Sprintf("Search using the SerpAPI %s engine. Returns raw JSON results.", engine)
	}

	return tool.NewRaw(
		toolName,
		description,
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
			log.Logf("searching %s %q", engine, req.Query)
			result, err := searchEngine(ctx, client, apiKey, engine, req.Query, ec.Params)
			if err != nil {
				return "", err
			}
			log.Logf("got %s results", engine)
			return result, nil
		},
	)
}

func searchEngine(ctx context.Context, client *http.Client, apiKey, engine, query string, extraParams map[string]string) (string, error) {
	params := url.Values{}
	params.Set("engine", engine)
	params.Set("q", query)
	params.Set("api_key", apiKey)
	for k, v := range extraParams {
		params.Set(k, v)
	}

	u := "https://serpapi.com/search.json?" + params.Encode()

	resp, err := doGet(ctx, client, u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", apiError(resp, engine)
	}

	// Decode into a generic map and re-encode as indented JSON.
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	// Remove metadata fields that waste tokens.
	delete(raw, "search_metadata")
	delete(raw, "search_parameters")
	delete(raw, "serpapi_pagination")
	delete(raw, "pagination")

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal response: %w", err)
	}

	result := string(out)
	if len(result) > maxRawResponseChars {
		result = result[:maxRawResponseChars] + "\n... (truncated)"
	}

	return result, nil
}
