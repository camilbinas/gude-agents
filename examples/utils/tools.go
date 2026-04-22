package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// weather conditions for random generation.
var (
	conditions = []string{"sunny", "partly cloudy", "cloudy", "rainy", "stormy", "snowy", "foggy", "windy"}
	condIcons  = []string{"☀️", "⛅", "☁️", "🌧️", "⛈️", "🌨️", "🌫️", "💨"}
)

// WeatherTool returns a mock weather tool for examples.
// Returns randomized temperature and conditions for each call.
func WeatherTool() tool.Tool {
	type input struct {
		City string `json:"city" description:"The city to get weather for" required:"true"`
	}
	return tool.New("get_weather", "Get the current weather for a city", func(_ context.Context, in input) (string, error) {
		i := rand.IntN(len(conditions))
		temp := rand.IntN(35) - 5      // -5 to 29°C
		humidity := 30 + rand.IntN(60) // 30-89%
		wind := rand.IntN(40)          // 0-39 km/h
		return fmt.Sprintf(`{"city": %q, "temperature": "%d°C", "condition": "%s %s", "humidity": "%d%%", "wind": "%d km/h"}`,
			in.City, temp, conditions[i], condIcons[i], humidity, wind), nil
	})
}

// TimeTool returns a mock time/timezone tool for examples.
func TimeTool() tool.Tool {
	type input struct {
		Timezone string `json:"timezone" description:"IANA timezone name (e.g. America/New_York)" required:"true"`
	}
	return tool.New("get_time", "Get the current time in a timezone", func(_ context.Context, in input) (string, error) {
		loc, err := time.LoadLocation(in.Timezone)
		if err != nil {
			return "", fmt.Errorf("unknown timezone: %s", in.Timezone)
		}
		return fmt.Sprintf(`{"timezone": %q, "time": %q}`, in.Timezone, time.Now().In(loc).Format(time.RFC3339)), nil
	})
}

// CalculateTool returns a mock calculator tool for examples.
func CalculateTool() tool.Tool {
	type input struct {
		Expression string `json:"expression" description:"Math expression to evaluate (e.g. '7 * 6')" required:"true"`
	}
	return tool.New("calculate", "Evaluate a math expression", func(_ context.Context, in input) (string, error) {
		// Fake calculator — returns a canned result for demo purposes.
		return fmt.Sprintf(`{"expression": %q, "result": 42}`, in.Expression), nil
	})
}

// CheckBalanceTool returns a mock account balance tool for support examples.
func CheckBalanceTool() tool.Tool {
	type input struct {
		AccountID string `json:"account_id" description:"The customer account ID" required:"true"`
	}
	return tool.New("check_balance", "Look up account balance and billing info", func(_ context.Context, in input) (string, error) {
		return fmt.Sprintf(`{"account_id": %q, "balance": "$42.50", "plan": "Pro", "next_billing": "2026-05-01"}`, in.AccountID), nil
	})
}

// SearchDocsTool returns a mock documentation search tool for support examples.
func SearchDocsTool() tool.Tool {
	type input struct {
		Query string `json:"query" description:"Search query for documentation" required:"true"`
	}
	return tool.New("search_docs", "Search technical documentation", func(_ context.Context, in input) (string, error) {
		return fmt.Sprintf(`{"results": [{"title": "Troubleshooting Guide", "snippet": "For '%s': Check your config file at ~/.app/config.yaml and ensure all required fields are set."}]}`, in.Query), nil
	})
}

// LookupOrderTool returns a mock order lookup tool for handoff examples.
func LookupOrderTool() tool.Tool {
	return tool.NewRaw("lookup_order", "Look up order details by ID", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"order_id": map[string]any{"type": "string", "description": "The order ID"},
		},
		"required": []string{"order_id"},
	}, func(_ context.Context, input json.RawMessage) (string, error) {
		return `{"order_id": "ORD-1234", "status": "delivered", "total": "$49.99", "item": "Wireless Headphones"}`, nil
	})
}

// ProcessRefundTool returns a mock refund tool for handoff/support examples.
func ProcessRefundTool() tool.Tool {
	return tool.NewRaw("process_refund", "Process a refund for an order", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"order_id": map[string]any{"type": "string", "description": "The order ID"},
			"amount":   map[string]any{"type": "string", "description": "Refund amount"},
		},
		"required": []string{"order_id", "amount"},
	}, func(_ context.Context, input json.RawMessage) (string, error) {
		return `{"status": "refunded", "amount": "$49.99"}`, nil
	})
}
