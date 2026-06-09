package agent

import (
	"context"
	"testing"
)

// Benchmarks measure Acquire+Record overhead for different rate limiter configs.
// Run: go test ./agent -bench=BenchmarkRateLimiter -benchmem -count=3
//
// The high limits (1M) ensure we never actually hit the rate cap during the
// benchmark — we're measuring the per-call overhead of checking limits, not
// the cost of being rate-limited. The release function returned from Acquire
// must always be called to free the per-key concurrency slot when MaxConcurrent
// is configured; calling it in every iteration also matches real production use.

func BenchmarkRateLimiter_Baseline(b *testing.B) {
	rl, err := NewRateLimiter(RPM(1_000_000), TPM(1_000_000_000))
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	usage := TokenUsage{InputTokens: 10, OutputTokens: 20}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		release, _ := rl.Acquire(ctx, "key")
		_ = rl.Record("key", usage)
		release()
	}
}

func BenchmarkRateLimiter_ConfigurableWindow(b *testing.B) {
	rl, err := NewRateLimiter(
		RequestRateLimit(1_000_000, 30),
		TokenRateLimit(1_000_000_000, 30),
	)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	usage := TokenUsage{InputTokens: 10, OutputTokens: 20}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		release, _ := rl.Acquire(ctx, "key")
		_ = rl.Record("key", usage)
		release()
	}
}

func BenchmarkRateLimiter_WithGlobalLimits(b *testing.B) {
	rl, err := NewRateLimiter(
		RequestRateLimit(1_000_000, 60),
		TokenRateLimit(1_000_000_000, 60),
		WithGlobalRequestLimit(10_000_000, 60),
		WithGlobalTokenLimit(10_000_000_000, 60),
	)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	usage := TokenUsage{InputTokens: 10, OutputTokens: 20}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		release, _ := rl.Acquire(ctx, "key")
		_ = rl.Record("key", usage)
		release()
	}
}

func BenchmarkRateLimiter_WithMaxConcurrent(b *testing.B) {
	rl, err := NewRateLimiter(
		RequestRateLimit(1_000_000, 60),
		MaxConcurrent(1000),
	)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	usage := TokenUsage{InputTokens: 10, OutputTokens: 20}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		release, _ := rl.Acquire(ctx, "key")
		_ = rl.Record("key", usage)
		release()
	}
}

func BenchmarkRateLimiter_WithMemoryStore(b *testing.B) {
	store := NewMemoryStore()
	rl, err := NewRateLimiter(
		RequestRateLimit(1_000_000, 60),
		TokenRateLimit(1_000_000_000, 60),
		WithStore(store),
	)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	usage := TokenUsage{InputTokens: 10, OutputTokens: 20}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		release, _ := rl.Acquire(ctx, "key")
		_ = rl.Record("key", usage)
		release()
	}
}

func BenchmarkRateLimiter_FullStack(b *testing.B) {
	rl, err := NewRateLimiter(
		RequestRateLimit(1_000_000, 30),
		TokenRateLimit(1_000_000_000, 30),
		WithGlobalRequestLimit(10_000_000, 60),
		WithGlobalTokenLimit(10_000_000_000, 60),
		MaxConcurrent(1000),
	)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	usage := TokenUsage{InputTokens: 10, OutputTokens: 20}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		release, _ := rl.Acquire(ctx, "key")
		_ = rl.Record("key", usage)
		release()
	}
}

// --- Parallel benchmarks (contention) ---

func BenchmarkRateLimiter_Baseline_Parallel(b *testing.B) {
	rl, err := NewRateLimiter(RPM(1_000_000), TPM(1_000_000_000))
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	usage := TokenUsage{InputTokens: 10, OutputTokens: 20}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			release, _ := rl.Acquire(ctx, "key")
			_ = rl.Record("key", usage)
			release()
		}
	})
}

func BenchmarkRateLimiter_FullStack_Parallel(b *testing.B) {
	rl, err := NewRateLimiter(
		RequestRateLimit(1_000_000, 30),
		TokenRateLimit(1_000_000_000, 30),
		WithGlobalRequestLimit(10_000_000, 60),
		WithGlobalTokenLimit(10_000_000_000, 60),
		MaxConcurrent(1000),
	)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	usage := TokenUsage{InputTokens: 10, OutputTokens: 20}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			release, _ := rl.Acquire(ctx, "key")
			_ = rl.Record("key", usage)
			release()
		}
	})
}

func BenchmarkRateLimiter_FullStack_MultiKey_Parallel(b *testing.B) {
	rl, err := NewRateLimiter(
		RequestRateLimit(1_000_000, 30),
		TokenRateLimit(1_000_000_000, 30),
		WithGlobalRequestLimit(10_000_000, 60),
		WithGlobalTokenLimit(10_000_000_000, 60),
		MaxConcurrent(1000),
	)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	usage := TokenUsage{InputTokens: 10, OutputTokens: 20}
	keys := []string{"key-0", "key-1", "key-2", "key-3", "key-4", "key-5", "key-6", "key-7"}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := keys[i%len(keys)]
			release, _ := rl.Acquire(ctx, k)
			_ = rl.Record(k, usage)
			release()
			i++
		}
	})
}

func BenchmarkRateLimiter_MemoryStore_Parallel(b *testing.B) {
	store := NewMemoryStore()
	rl, err := NewRateLimiter(
		RequestRateLimit(1_000_000, 60),
		TokenRateLimit(1_000_000_000, 60),
		WithStore(store),
	)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	usage := TokenUsage{InputTokens: 10, OutputTokens: 20}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			release, _ := rl.Acquire(ctx, "key")
			_ = rl.Record("key", usage)
			release()
		}
	})
}

// --- Window strategy comparison ---

func BenchmarkRateLimiter_SlidingWindow(b *testing.B) {
	rl, err := NewRateLimiter(RPM(1_000_000), TPM(1_000_000_000), WithSlidingWindow())
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	usage := TokenUsage{InputTokens: 10, OutputTokens: 20}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		release, _ := rl.Acquire(ctx, "key")
		_ = rl.Record("key", usage)
		release()
	}
}

func BenchmarkRateLimiter_FixedWindow(b *testing.B) {
	rl, err := NewRateLimiter(RPM(1_000_000), TPM(1_000_000_000), WithFixedWindow())
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	usage := TokenUsage{InputTokens: 10, OutputTokens: 20}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		release, _ := rl.Acquire(ctx, "key")
		_ = rl.Record("key", usage)
		release()
	}
}
