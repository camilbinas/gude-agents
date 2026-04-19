// Example: Fallback provider.
//
// Demonstrates how to use fallback.New to chain providers so that if the
// primary fails, the next one in the chain is tried automatically.
//
// This example uses a fake provider that always returns an error to simulate
// an outage, with Bedrock as the backup that handles the request.
//
// Key concepts demonstrated:
//   - fallback.New        — chains providers, tries each in order on error
//   - fakeProvider        — a minimal Provider stub useful for testing
//   - transparent fallback — the agent has no knowledge of the switch
//
// Run:
//
//	go run ./fallback-provider

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/provider/fallback"
	"github.com/joho/godotenv"
)

// fakeProvider simulates a provider that is always unavailable.
type fakeProvider struct{ name string }

func (f *fakeProvider) Converse(_ context.Context, _ agent.ConverseParams) (*agent.ProviderResponse, error) {
	fmt.Printf("→ %s: ", f.name)
	fmt.Println("failed, falling back...")
	return nil, fmt.Errorf("%s: service unavailable", f.name)
}

func (f *fakeProvider) ConverseStream(_ context.Context, _ agent.ConverseParams, _ agent.StreamCallback) (*agent.ProviderResponse, error) {
	fmt.Printf("→ %s: ", f.name)
	fmt.Println("failed, falling back...")
	return nil, fmt.Errorf("%s: service unavailable", f.name)
}

func main() {
	godotenv.Load() //nolint

	// Primary provider is down — every call returns an error.
	primary := &fakeProvider{name: "fake-provider"}

	// Bedrock is the backup.
	backup := bedrock.Must(bedrock.Nova2Lite())

	// fallback.New tries primary first, then backup.
	// The agent doesn't know or care which one actually responds.
	provider := fallback.New(primary, backup)

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Be concise."),
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Sending request — primary will fail, backup (Bedrock) will handle it...")
	fmt.Println()

	result, usage, err := a.Invoke(context.Background(), "What is 2 + 2?")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result)
	fmt.Printf("\n[tokens: %d in / %d out]\n", usage.InputTokens, usage.OutputTokens)
}
