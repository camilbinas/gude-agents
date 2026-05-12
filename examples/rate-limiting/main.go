// Example: Rate limiting with RateLimiter.
//
// Shows how to enforce RPM limits on provider calls. The same RateLimiter
// works in both shared and per-conversation modes. Demonstrates:
//   - Shared mode: multiple agents competing for one budget
//   - Per-conversation mode: each conversation ID gets independent limits
//   - Fail-fast vs block overflow behaviors
//   - Automatic TTL eviction of idle buckets
//
// Run:
//
//	go run ./rate-limiting

package main

import (
	"errors"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	provider := bedrock.Must(bedrock.Cheapest())

	// ── Shared mode: 2 RPM across all calls ──────────────────────────
	// Without conversation IDs, all calls share one budget.
	// FailFast is the default — returns ErrRateLimitExceeded immediately.
	fmt.Println("── Shared mode (2 RPM, fail-fast default) ──")

	rl, err := agent.NewRateLimiter(2, 0)
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a concise assistant. One sentence max."),
		nil,
		agent.WithRateLimiter(rl),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	for i, q := range []string{"What is Go?", "What is Rust?", "What is Python?"} {
		result, err := a.Invoke(agent.Background(), q)
		if errors.Is(err, agent.ErrRateLimitExceeded) {
			fmt.Printf("  [%d] Rate limited: %s\n", i+1, q)
			continue
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  [%d] %s\n", i+1, result)
	}

	// ── Per-conversation mode: 2 RPM per conversation ────────────────
	// With conversation IDs, each gets its own independent budget.
	// Use WithBlock() to wait for capacity instead of failing fast.
	fmt.Println("\n── Per-conversation mode (2 RPM each, block) ──")

	rl2, err := agent.NewRateLimiter(2, 0, agent.WithBlock())
	if err != nil {
		log.Fatal(err)
	}

	a2, err := agent.Default(
		provider,
		prompt.Text("You are a concise assistant. One sentence max."),
		nil,
		agent.WithRateLimiter(rl2),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Conversation A exhausts its budget — calls will block until capacity resets.
	fmt.Println("  Conversation A:")
	ctxA := agent.Background().WithConversationID("conv-a")
	for i, q := range []string{"What is Java?", "What is C++?", "What is Zig?"} {
		result, err := a2.Invoke(ctxA, q)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("    [%d] %s\n", i+1, result)
	}

	// Conversation B is unaffected — its own budget is fresh.
	fmt.Println("  Conversation B (independent):")
	ctxB := agent.Background().WithConversationID("conv-b")
	for i, q := range []string{"What is Haskell?", "What is OCaml?"} {
		result, err := a2.Invoke(ctxB, q)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("    [%d] %s\n", i+1, result)
	}

	// Cleanup (optional — stale buckets auto-evict after 60s of inactivity).
	rl2.Purge("conv-a")
	rl2.Purge("conv-b")
	fmt.Printf("\n  Active buckets after purge: %d\n", rl2.Len())
}
