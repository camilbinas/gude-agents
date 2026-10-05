// Run: go run ./tools
//
// A customer-support agent with typed tools. Independent tool calls run in
// parallel by default, so the model can look up an order and local time at
// once when both help answer a request.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

type orderInput struct {
	OrderID string `json:"order_id" description:"Customer order ID" required:"true"`
}

type timeInput struct {
	City string `json:"city" description:"City used for the support case" required:"true"`
}

func main() {
	lookupOrder := tool.New("lookup_order", "Look up an order's shipping status", func(_ context.Context, in orderInput) (string, error) {
		return fmt.Sprintf(`{"order_id":%q,"status":"shipped","total":"$42.00"}`, in.OrderID), nil
	})
	localTime := tool.New("local_time", "Get the current support-center time for a city", func(_ context.Context, in timeInput) (string, error) {
		return fmt.Sprintf(`{"city":%q,"time":%q}`, in.City, time.Now().Format(time.RFC3339)), nil
	})

	a, err := agent.New(
		bedrock.Must(bedrock.Standard()),
		"You are a customer-support assistant. Use tools when their data helps answer the customer.",
		agent.WithTools(lookupOrder, localTime),
	)
	if err != nil {
		log.Fatal(err)
	}
	result, err := a.Invoke(agent.Background(), "Where is order 1234, and what time is it in Tokyo?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Text)
}
