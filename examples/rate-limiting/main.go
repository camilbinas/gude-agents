// Example: Rate limiting with RateLimiter.
//
// Shows how to enforce RPM limits on provider calls. The same RateLimiter
// works in both shared and per-conversation modes. Demonstrates:
//   - Shared mode: multiple agents competing for one budget
//   - Per-conversation mode: each conversation ID gets independent limits
//   - Fail-fast vs block overflow behaviors
//   - Automatic TTL eviction of idle buckets
//   - Configurable time windows (v2)
//   - Per-key concurrency limiting (v2)
//   - Global (cross-key) rate limits (v2)
//   - Pluggable store backend (v2)
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

	// ── Shared mode: 2 RPM across all calls ──────────────────────────
	// Without conversation IDs, all calls share one budget.
	// FailFast is the default — returns ErrRateLimitExceeded immediately.
	fmt.Println("── Shared mode (2 RPM, fail-fast default) ──")

	rl, err := agent.NewRateLimiter(agent.RPM(2))
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

	rl2, err := agent.NewRateLimiter(agent.RPM(2), agent.WithBlock())
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

	// ── Configurable time windows (v2) ───────────────────────────────
	// Use RequestRateLimit/TokenRateLimit for custom windows instead of per-minute.
	// RPM() and TPM() are convenient aliases for 60-second windows.
	fmt.Println("\n── Configurable windows (10 requests per 30 seconds) ──")

	rl3, err := agent.NewRateLimiter(
		agent.RequestRateLimit(10, 30), // 10 requests per 30-second window
		agent.TokenRateLimit(5000, 30), // 5000 tokens per 30-second window
		agent.WithFailFast(),
	)
	if err != nil {
		log.Fatal(err)
	}

	a3, err := agent.Default(
		provider,
		prompt.Text("You are a concise assistant. One sentence max."),
		nil,
		agent.WithRateLimiter(rl3),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	for i, q := range []string{"What is 1+1?", "What is 2+2?", "What is 3+3?"} {
		result, err := a3.Invoke(agent.Background(), q)
		if errors.Is(err, agent.ErrRateLimitExceeded) {
			fmt.Printf("  [%d] Rate limited: %s\n", i+1, q)
			continue
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  [%d] %s\n", i+1, result)
	}

	// ── Concurrency limiting (v2) ────────────────────────────────────
	// MaxConcurrent(n) limits in-flight calls per key. Useful for providers
	// that throttle based on concurrent connections, not just request rate.
	fmt.Println("\n── Concurrency limiting (max 2 in-flight per key) ──")

	rl4, err := agent.NewRateLimiter(
		agent.RPM(100),         // high rate limit so it doesn't interfere
		agent.MaxConcurrent(2), // at most 2 calls in-flight per conversation
		agent.WithBlock(),      // block if at capacity (instead of failing fast)
	)
	if err != nil {
		log.Fatal(err)
	}

	a4, err := agent.Default(
		provider,
		prompt.Text("You are a concise assistant. One sentence max."),
		nil,
		agent.WithRateLimiter(rl4),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Launch 4 concurrent requests — only 2 proceed at a time, rest block.
	var wg sync.WaitGroup
	questions := []string{"What is a mutex?", "What is a channel?", "What is a goroutine?", "What is a WaitGroup?"}
	for i, q := range questions {
		wg.Add(1)
		go func(idx int, question string) {
			defer wg.Done()
			result, err := a4.Invoke(agent.Background(), question)
			if err != nil {
				fmt.Printf("  [%d] Error: %v\n", idx+1, err)
				return
			}
			fmt.Printf("  [%d] %s\n", idx+1, result)
		}(i, q)
	}
	wg.Wait()

	// ── Global (cross-key) rate limits (v2) ──────────────────────────
	// Enforce an account-wide budget shared across all conversations.
	// Both per-key AND global limits must pass for a call to proceed.
	fmt.Println("\n── Global limits (5 RPM per key, 8 RPM global) ──")

	rl5, err := agent.NewRateLimiter(
		agent.RPM(5),                // 5 requests/min per conversation
		agent.WithGlobalRPM(8),      // 8 requests/min total across all keys
		agent.WithGlobalTPM(10_000), // 10k tokens/min total
		agent.WithFailFast(),
	)
	if err != nil {
		log.Fatal(err)
	}

	a5, err := agent.Default(
		provider,
		prompt.Text("You are a concise assistant. One sentence max."),
		nil,
		agent.WithRateLimiter(rl5),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Three conversations compete for the global budget of 8.
	convIDs := []string{"user-alice", "user-bob", "user-charlie"}
	for round := 0; round < 4; round++ {
		for _, cid := range convIDs {
			ctx := agent.Background().WithConversationID(cid)
			_, err := a5.Invoke(ctx, "Say hi in one word.")
			if errors.Is(err, agent.ErrRateLimitExceeded) {
				fmt.Printf("  [%s] round %d: rate limited (global budget exhausted)\n", cid, round+1)
				continue
			}
			if err != nil {
				log.Fatal(err)
			}
			fmt.Printf("  [%s] round %d: OK\n", cid, round+1)
		}
	}

	fmt.Println("\n── Done ──")

	// ── Pluggable store backend (v2) ─────────────────────────────────
	// By default the RateLimiter uses in-memory counters. For multi-instance
	// deployments, plug in a distributed store with WithStore(). Example
	// using Redis (requires github.com/camilbinas/gude-agents/agent/ratelimit/redis):
	//
	//   import redistore "github.com/camilbinas/gude-agents/agent/ratelimit/redis"
	//   import goredis "github.com/redis/go-redis/v9"
	//
	//   client := goredis.NewClient(&goredis.Options{Addr: "localhost:6379"})
	//   store := redistore.NewStore(client, redistore.WithPrefix("myapp"))
	//   rl, _ := agent.NewRateLimiter(
	//       agent.RPM(100),
	//       agent.WithGlobalRPM(500),
	//       agent.WithStore(store),
	//   )
}
