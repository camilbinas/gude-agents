package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
)

// ---------------------------------------------------------------------------
// Task 5.4 — Inference config wiring tests
// ---------------------------------------------------------------------------

func TestInvoke_AgentLevelInferenceConfigForwarded(t *testing.T) {
	cp := newCapturingProvider(&ProviderResponse{Text: "ok"})

	a, err := New(cp, prompt.Text("sys"), nil,
		WithTemperature(0.7),
		WithTopP(0.9),
		WithTopK(50),
		WithStopSequences([]string{"STOP"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cp.captured) != 1 {
		t.Fatalf("expected 1 provider call, got %d", len(cp.captured))
	}

	cfg := cp.captured[0].InferenceConfig
	if cfg == nil {
		t.Fatal("expected InferenceConfig to be set, got nil")
	}
	if cfg.Temperature == nil || *cfg.Temperature != 0.7 {
		t.Errorf("Temperature: expected 0.7, got %v", cfg.Temperature)
	}
	if cfg.TopP == nil || *cfg.TopP != 0.9 {
		t.Errorf("TopP: expected 0.9, got %v", cfg.TopP)
	}
	if cfg.TopK == nil || *cfg.TopK != 50 {
		t.Errorf("TopK: expected 50, got %v", cfg.TopK)
	}
	if len(cfg.StopSequences) != 1 || cfg.StopSequences[0] != "STOP" {
		t.Errorf("StopSequences: expected [STOP], got %v", cfg.StopSequences)
	}
}

func TestInvoke_PerInvocationOverridesAgentLevel(t *testing.T) {
	cp := newCapturingProvider(&ProviderResponse{Text: "ok"})

	a, err := New(cp, prompt.Text("sys"), nil,
		WithTemperature(0.3),
		WithTopP(0.5),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Per-invocation override: temperature=0.9, leave TopP to fall back to agent-level.
	ctx := WithInferenceConfig(context.Background(), &InferenceConfig{
		Temperature: ptrFloat(0.9),
	})

	_, _, err = a.Invoke(ctx, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cp.captured) != 1 {
		t.Fatalf("expected 1 provider call, got %d", len(cp.captured))
	}

	cfg := cp.captured[0].InferenceConfig
	if cfg == nil {
		t.Fatal("expected InferenceConfig to be set, got nil")
	}
	if cfg.Temperature == nil || *cfg.Temperature != 0.9 {
		t.Errorf("Temperature: expected 0.9 (per-invocation), got %v", cfg.Temperature)
	}
	if cfg.TopP == nil || *cfg.TopP != 0.5 {
		t.Errorf("TopP: expected 0.5 (agent-level fallback), got %v", cfg.TopP)
	}
}

func TestInvoke_NilInferenceConfigWhenNoneSet(t *testing.T) {
	cp := newCapturingProvider(&ProviderResponse{Text: "ok"})

	a, err := New(cp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cp.captured) != 1 {
		t.Fatalf("expected 1 provider call, got %d", len(cp.captured))
	}

	if cp.captured[0].InferenceConfig != nil {
		t.Errorf("expected InferenceConfig to be nil, got %+v", cp.captured[0].InferenceConfig)
	}
}

func TestInvoke_InvalidPerInvocationConfigReturnsError(t *testing.T) {
	cp := newCapturingProvider(&ProviderResponse{Text: "should not reach"})

	a, err := New(cp, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Per-invocation config with invalid temperature.
	ctx := WithInferenceConfig(context.Background(), &InferenceConfig{
		Temperature: ptrFloat(2.0), // invalid: > 1.0
	})

	_, _, err = a.Invoke(ctx, "hello")
	if err == nil {
		t.Fatal("expected error for invalid per-invocation config, got nil")
	}
	if !strings.Contains(err.Error(), "inference config") {
		t.Errorf("expected error to mention 'inference config', got: %v", err)
	}

	// Provider should NOT have been called.
	if len(cp.captured) != 0 {
		t.Errorf("expected 0 provider calls (validation should block), got %d", len(cp.captured))
	}
}
