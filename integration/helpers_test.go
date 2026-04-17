package integration_test

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/registry"
)

// testLogger adapts testing.T to the agent.Logger interface.
type tLogger struct{ t *testing.T }

func (l tLogger) Printf(format string, v ...any) { l.t.Logf(format, v...) }
func testLogger(t *testing.T) tLogger            { return tLogger{t} }

// ---------------------------------------------------------------------------
// Token usage tracking across all integration tests
// ---------------------------------------------------------------------------

var (
	totalInputTokens  atomic.Int64
	totalOutputTokens atomic.Int64
	totalLLMCalls     atomic.Int64
)

// trackingProvider wraps a real Provider and accumulates token usage globally.
type trackingProvider struct {
	inner agent.Provider
}

func (tp *trackingProvider) Converse(ctx context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	resp, err := tp.inner.Converse(ctx, params)
	if resp != nil {
		totalInputTokens.Add(int64(resp.Usage.InputTokens))
		totalOutputTokens.Add(int64(resp.Usage.OutputTokens))
		totalLLMCalls.Add(1)
	}
	return resp, err
}

func (tp *trackingProvider) ConverseStream(ctx context.Context, params agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	resp, err := tp.inner.ConverseStream(ctx, params, cb)
	if resp != nil {
		totalInputTokens.Add(int64(resp.Usage.InputTokens))
		totalOutputTokens.Add(int64(resp.Usage.OutputTokens))
		totalLLMCalls.Add(1)
	}
	return resp, err
}

// Forward capability interfaces so the agent doesn't log warnings.

func (tp *trackingProvider) Capabilities() agent.Capabilities {
	if cr, ok := tp.inner.(agent.CapabilityReporter); ok {
		return cr.Capabilities()
	}
	return agent.Capabilities{ToolUse: true, ToolChoice: true, TokenUsage: true}
}

func (tp *trackingProvider) ModelId() string {
	if mi, ok := tp.inner.(agent.ModelIdentifier); ok {
		return mi.ModelId()
	}
	return "unknown"
}

func newTestProvider(t *testing.T) agent.Provider {
	t.Helper()
	registry.RegisterBuiltins()
	p, err := registry.FromEnv()
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	t.Logf("Using provider: MODEL_PROVIDER=%s MODEL_TIER=%s",
		envOr("MODEL_PROVIDER", "bedrock"), envOr("MODEL_TIER", "standard"))
	return &trackingProvider{inner: p}
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

// TestMain runs all integration tests and prints a token usage summary.
func TestMain(m *testing.M) {
	registry.RegisterBuiltins()

	code := m.Run()

	in := totalInputTokens.Load()
	out := totalOutputTokens.Load()
	calls := totalLLMCalls.Load()

	if calls > 0 {
		fmt.Println()
		fmt.Println("━━━ Integration Test Token Usage ━━━")
		fmt.Printf("  LLM calls  : %d\n", calls)
		fmt.Printf("  Input      : %d tokens\n", in)
		fmt.Printf("  Output     : %d tokens\n", out)
		fmt.Printf("  Total      : %d tokens\n", in+out)
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	}

	os.Exit(code)
}
