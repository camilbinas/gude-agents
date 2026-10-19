package integration_test

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	pvdr "github.com/camilbinas/gude-agents/agent/provider"
	"github.com/camilbinas/gude-agents/agent/provider/anthropic"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/provider/ollama"
	"github.com/camilbinas/gude-agents/agent/provider/openai"
	"github.com/camilbinas/gude-agents/agent/provider/vllm"
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

func (tp *trackingProvider) Name() string { return tp.inner.Name() }

func (tp *trackingProvider) Stream(ctx context.Context, req agent.ModelRequest, emit func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	resp, err := tp.inner.Stream(ctx, req, emit)
	if resp != nil {
		totalInputTokens.Add(int64(resp.Usage.InputTokens))
		totalOutputTokens.Add(int64(resp.Usage.OutputTokens))
		totalLLMCalls.Add(1)
	}
	return resp, err
}

func (tp *trackingProvider) ModelID() string {
	if mi, ok := tp.inner.(agent.ModelIdentifier); ok {
		return mi.ModelID()
	}
	return "unknown"
}

// liveConfig is the explicit provider and model a live run was asked to use.
type liveConfig struct {
	provider string
	model    string
}

// liveRequested reports whether the environment asks for live provider tests.
// Setting GUDE_LIVE_TESTS=1, MODEL_PROVIDER or MODEL_ID requests them.
func liveRequested() bool {
	return os.Getenv("GUDE_LIVE_TESTS") == "1" ||
		os.Getenv("MODEL_PROVIDER") != "" || os.Getenv("MODEL_ID") != ""
}

// requireLiveConfig returns the explicitly selected provider and model.
//
// There is no default provider and no default model. When live testing is not
// requested the test is skipped, so a plain `go test ./...` never calls a paid
// API. When it is requested, a missing or invalid MODEL_PROVIDER / MODEL_ID is
// a configuration error and fails the test with a message that says what to
// set.
func requireLiveConfig(t *testing.T) liveConfig {
	t.Helper()
	if !liveRequested() {
		t.Skip("live provider test skipped: set MODEL_PROVIDER=openai|anthropic|bedrock|ollama|vllm and MODEL_ID=<model identifier> to run it")
	}
	cfg := liveConfig{provider: os.Getenv("MODEL_PROVIDER"), model: os.Getenv("MODEL_ID")}
	if cfg.provider == "" || cfg.model == "" {
		t.Fatalf("live provider tests need both MODEL_PROVIDER (openai|anthropic|bedrock|ollama|vllm) and MODEL_ID (an explicit model identifier); got MODEL_PROVIDER=%q MODEL_ID=%q. There is no default provider or model", cfg.provider, cfg.model)
	}
	return cfg
}

// buildLiveProvider constructs the selected adapter directly. The test module
// imports every provider module; the runtime module does not.
func buildLiveProvider(cfg liveConfig) (agent.Provider, error) {
	switch cfg.provider {
	case "openai":
		return openai.New(cfg.model)
	case "anthropic":
		return anthropic.New(cfg.model)
	case "bedrock":
		return bedrock.New(cfg.model)
	case "ollama":
		return ollama.New(cfg.model)
	case "vllm":
		return vllm.New(cfg.model)
	default:
		return nil, fmt.Errorf("unknown MODEL_PROVIDER %q: want openai, anthropic, bedrock, ollama or vllm", cfg.provider)
	}
}

func newTestProvider(t *testing.T) agent.Provider {
	t.Helper()
	cfg := requireLiveConfig(t)
	p, err := buildLiveProvider(cfg)
	if err != nil {
		t.Fatalf("live provider configuration: %v", err)
	}
	t.Logf("Using provider %s with model %s", cfg.provider, cfg.model)
	return &trackingProvider{inner: p}
}

// newThinkingProvider returns the selected provider with thinking enabled. Only
// Bedrock and Anthropic models support thinking here; other selections skip.
func newThinkingProvider(t *testing.T, effort pvdr.ThinkingEffort) agent.Provider {
	t.Helper()
	cfg := requireLiveConfig(t)
	switch cfg.provider {
	case "bedrock":
		p, err := bedrock.New(cfg.model, bedrock.WithThinking(effort))
		if err != nil {
			t.Fatal(err)
		}
		return &trackingProvider{inner: p}
	case "anthropic":
		p, err := anthropic.New(cfg.model, anthropic.WithThinking(effort))
		if err != nil {
			t.Fatal(err)
		}
		return &trackingProvider{inner: p}
	default:
		t.Skipf("thinking tests need MODEL_PROVIDER=bedrock or anthropic, got %q", cfg.provider)
		return nil
	}
}

// TestMain runs all integration tests and prints a token usage summary.
func TestMain(m *testing.M) {

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
