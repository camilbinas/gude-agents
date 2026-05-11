// Example: Rate limiting with shared RateLimiter.
//
// Shows how to enforce RPM and TPM limits on provider calls using a shared
// RateLimiter across multiple agents. Demonstrates:
//   - Creating a RateLimiter with RPM/TPM limits
//   - Sliding vs fixed window strategies
//   - Fail-fast vs block overflow behaviors
//   - Sharing a single limiter across multiple agents
//   - Handling ErrRateLimitExceeded
//
// Run:
//
//	go run ./rate-limiting

package main

import (
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	provider := bedrock.Must(bedrock.Cheapest())

	// ── Shared RateLimiter ────────────────────────────────────────────
	// Enforce 2 RPM across all agents using this provider.
	// With 5 concurrent calls and only 2 RPM capacity, some will be rejected.
	// Uses sliding window (default) and fail-fast to return errors immediately.
	rl, err := agent.NewRateLimiter(2, 0, agent.WithFailFast())
	if err != nil {
		log.Fatal(err)
	}

	// ── Create two agents sharing the same limiter ────────────────────
	a1, err := agent.Default(
		provider,
		prompt.Text("You are a concise assistant. One sentence max."),
		nil,
		agent.WithRateLimiter(rl),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	a2, err := agent.Default(
		provider,
		prompt.Text("You are a creative writer. One sentence max."),
		nil,
		agent.WithRateLimiter(rl),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// ── Run both agents concurrently ──────────────────────────────────
	// They share the same rate limiter, so their combined calls respect
	// the 2 RPM budget. Most calls will be rate-limited.
	var wg sync.WaitGroup
	questions := []string{
		"What is Go?",
		"What is Rust?",
		"What is Python?",
		"What is Java?",
		"What is TypeScript?",
	}

	for i, q := range questions {
		wg.Add(1)
		go func(a *agent.Agent, question string, idx int) {
			defer wg.Done()
			result, err := a.Invoke(agent.Background(), question)
			if errors.Is(err, agent.ErrRateLimitExceeded) {
				fmt.Printf("[%d] Rate limited: %s\n", idx, question)
				return
			}
			if err != nil {
				fmt.Printf("[%d] Error: %v\n", idx, err)
				return
			}
			fmt.Printf("[%d] %s\n", idx, result)
		}([]*agent.Agent{a1, a2}[i%2], q, i)
	}

	wg.Wait()

	// ── Block mode example ────────────────────────────────────────────
	// With block mode (default), Acquire waits until capacity is available
	// instead of returning an error. Useful for background batch processing.
	fmt.Println("\n── Block mode (waits for capacity) ──")
	rlBlock, err := agent.NewRateLimiter(3, 0) // 3 RPM, unlimited TPM, block mode
	if err != nil {
		log.Fatal(err)
	}

	aBlock, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. One sentence max."),
		nil,
		agent.WithRateLimiter(rlBlock),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, err := aBlock.Invoke(agent.Background(), "Hello!")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
