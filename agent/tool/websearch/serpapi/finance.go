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

// NewFinance creates a finance_search tool that queries Google Finance via SerpAPI.
// It returns stock prices, market indices, and price movements.
//
// The query format is TICKER:EXCHANGE (e.g. "GOOGL:NASDAQ", "DAX:INDEXDB",
// "AAPL:NASDAQ"). For market overviews, use index tickers like ".DJI:INDEXDJX".
//
// Usage:
//
//	financeTool := serpapi.NewFinance(os.Getenv("SERPAPI_API_KEY"))
func NewFinance(apiKey string, opts ...Option) tool.Tool {
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

	return tool.NewRaw(
		"finance_search",
		"Look up stock prices and market data from Google Finance. Query format: TICKER:EXCHANGE (e.g. GOOGL:NASDAQ, AAPL:NASDAQ, DAX:INDEXDB).",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Stock ticker in TICKER:EXCHANGE format (e.g. GOOGL:NASDAQ, AAPL:NASDAQ, TSLA:NASDAQ)",
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
			log.Logf("searching finance %q", req.Query)
			result, err := searchFinance(ctx, client, apiKey, req.Query)
			if err != nil {
				return "", err
			}
			log.Logf("got finance results")
			return result, nil
		},
	)
}

func searchFinance(ctx context.Context, client *http.Client, apiKey, query string) (string, error) {
	u := fmt.Sprintf("https://serpapi.com/search.json?engine=google_finance&q=%s&api_key=%s",
		url.QueryEscape(query),
		url.QueryEscape(apiKey),
	)

	resp, err := doGet(ctx, client, u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", apiError(resp, "Google Finance")
	}

	// Decode into a generic map because SerpAPI returns price fields
	// inconsistently — sometimes as numbers, sometimes as strings,
	// sometimes as currency-prefixed strings (e.g. "USD278.09").
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	var sb strings.Builder

	// Format the stock summary if available.
	if summary, ok := raw["summary"].(map[string]any); ok {
		title := str(summary, "title")
		stock := str(summary, "stock")
		exchange := str(summary, "exchange")
		price := str(summary, "price")
		currency := str(summary, "currency")

		arrow := "→"
		if pm, ok := summary["price_movement"].(map[string]any); ok {
			if str(pm, "movement") == "Up" {
				arrow = "↑"
			} else if str(pm, "movement") == "Down" {
				arrow = "↓"
			}
			fmt.Fprintf(&sb, "%s (%s:%s)\n", title, stock, exchange)
			fmt.Fprintf(&sb, "Price: %s %s %s %s (%s%%)\n\n",
				price, currency, arrow, str(pm, "value"), str(pm, "percentage"))
		} else {
			fmt.Fprintf(&sb, "%s (%s:%s)\n", title, stock, exchange)
			fmt.Fprintf(&sb, "Price: %s %s\n\n", price, currency)
		}
	}

	// Format market indices if available.
	if markets, ok := raw["markets"].(map[string]any); ok {
		for region, v := range markets {
			indices, ok := v.([]any)
			if !ok {
				continue
			}
			fmt.Fprintf(&sb, "Market: %s\n", strings.ToUpper(region))
			for _, item := range indices {
				idx, ok := item.(map[string]any)
				if !ok {
					continue
				}
				arrow := "→"
				if pm, ok := idx["price_movement"].(map[string]any); ok {
					if str(pm, "movement") == "Up" {
						arrow = "↑"
					} else if str(pm, "movement") == "Down" {
						arrow = "↓"
					}
					fmt.Fprintf(&sb, "  %s: %s %s %s%% (%s)\n",
						str(idx, "name"), str(idx, "price"), arrow, str(pm, "percentage"), str(idx, "stock"))
				} else {
					fmt.Fprintf(&sb, "  %s: %s (%s)\n",
						str(idx, "name"), str(idx, "price"), str(idx, "stock"))
				}
			}
			sb.WriteString("\n")
		}
	}

	if sb.Len() == 0 {
		return "No finance data found.", nil
	}

	return sb.String(), nil
}

// str extracts a string representation from a map value regardless of
// whether the underlying JSON value was a string or a number.
func str(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%.2f", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}
