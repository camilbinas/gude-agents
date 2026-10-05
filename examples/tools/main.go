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
	Timezone string `json:"timezone" description:"IANA timezone such as Asia/Tokyo" required:"true"`
}

func main() {
	lookupOrder := tool.New("lookup_order", "Look up an order's shipping status", func(_ context.Context, in orderInput) (string, error) {
		return fmt.Sprintf(`{"order_id":%q,"status":"shipped","total":"$42.00"}`, in.OrderID), nil
	})
	localTime := tool.New("local_time", "Get the current time in an IANA timezone", func(_ context.Context, in timeInput) (string, error) {
		loc, err := time.LoadLocation(in.Timezone)
		if err != nil {
			return "", fmt.Errorf("load timezone %q: %w", in.Timezone, err)
		}
		return fmt.Sprintf(`{"timezone":%q,"time":%q}`, in.Timezone, time.Now().In(loc).Format(time.RFC3339)), nil
	})

	a, err := agent.New(
		bedrock.Must(bedrock.Standard()),
		"You are a customer-support assistant. Use tools when their data helps answer the customer.",
		agent.WithTools(lookupOrder, localTime),
	)
	if err != nil {
		log.Fatal(err)
	}
	result, err := a.Invoke(agent.Background(), "Where is order 1234, and what time is it in the Asia/Tokyo timezone?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Text)
}
