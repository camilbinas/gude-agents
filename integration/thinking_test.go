package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	pvdr "github.com/camilbinas/gude-agents/agent/provider"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

// Extended thinking integration tests.
//
// Run with:
//   go test -v -timeout=120s -run TestIntegration_Thinking ./...
//
// These tests require a provider that supports thinking (Claude 4-series on Bedrock).
// They are skipped when MODEL_PROVIDER is set to a provider without thinking support.

// collectingEventHook records thinking chunks for test assertions.
type collectingEventHook struct {
	mu     sync.Mutex
	chunks []string
}

func (h *collectingEventHook) OnToolCallStart(_ *agent.Context, _ string, _ json.RawMessage) {}
func (h *collectingEventHook) OnToolCallEnd(_ *agent.Context, _ string, _ string, _ error, _ time.Duration) {
}
func (h *collectingEventHook) OnThinking(_ *agent.Context, chunk string) {
	h.mu.Lock()
	h.chunks = append(h.chunks, chunk)
	h.mu.Unlock()
}
func (h *collectingEventHook) OnModelStart(_ *agent.Context)         {}
func (h *collectingEventHook) OnModelEnd(_ *agent.Context, _ string) {}

func TestIntegration_Thinking_CallbackFires(t *testing.T) {
	t.Parallel()
	// Thinking is only supported on Claude 4-series (Bedrock) and Gemini 2.5+.
	// Skip for providers that don't support it.
	providerName := os.Getenv("MODEL_PROVIDER")
	if providerName != "" && providerName != "bedrock" && providerName != "gemini" {
		t.Skipf("skipping thinking test for provider %q (not supported)", providerName)
	}

	// Use Bedrock Claude directly with thinking enabled — the registry tier
	// functions don't enable thinking by default.
	p, err := bedrock.GlobalClaudeSonnet4_6(bedrock.WithThinking(pvdr.ThinkingLow))
	if err != nil {
		t.Fatal(err)
	}
	tp := &trackingProvider{inner: p}

	hook := &collectingEventHook{}

	a, err := agent.New(tp,
		prompt.Text("You are a helpful assistant. Be brief."),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := agent.NewContext(ctx).WithEventHook(hook)

	result, err := a.Invoke(c, "What is 17 * 23? Show your reasoning.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	t.Logf("Response: %s", result)

	hook.mu.Lock()
	chunkCount := len(hook.chunks)
	thinkingText := strings.Join(hook.chunks, "")
	hook.mu.Unlock()

	t.Logf("Thinking chunks: %d, total length: %d chars", chunkCount, len(thinkingText))

	if chunkCount == 0 {
		t.Error("expected thinking callback to fire at least once")
	}
	if len(thinkingText) == 0 {
		t.Error("expected non-empty thinking text")
	}

	// The response should contain the answer (391).
	if !strings.Contains(result, "391") {
		t.Logf("Warning: expected response to contain '391', got: %s", result)
	}
}

func TestIntegration_Thinking_StreamingWithThinking(t *testing.T) {
	t.Parallel()
	providerName := os.Getenv("MODEL_PROVIDER")
	if providerName != "" && providerName != "bedrock" && providerName != "gemini" {
		t.Skipf("skipping thinking test for provider %q (not supported)", providerName)
	}

	p, err := bedrock.GlobalClaudeSonnet4_6(bedrock.WithThinking(pvdr.ThinkingLow))
	if err != nil {
		t.Fatal(err)
	}
	tp := &trackingProvider{inner: p}

	hook := &collectingEventHook{}
	var mu sync.Mutex
	var responseChunks []string

	a, err := agent.New(tp,
		prompt.Text("You are a helpful assistant. Be brief."),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := agent.NewContext(ctx).WithEventHook(hook)

	err = a.InvokeStream(c, "Explain why the sky is blue in one sentence.", func(chunk string) {
		mu.Lock()
		responseChunks = append(responseChunks, chunk)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("InvokeStream error: %v", err)
	}

	hook.mu.Lock()
	thinkingText := strings.Join(hook.chunks, "")
	thinkingCount := len(hook.chunks)
	hook.mu.Unlock()

	mu.Lock()
	responseText := strings.Join(responseChunks, "")
	responseCount := len(responseChunks)
	mu.Unlock()

	t.Logf("Thinking: %d chunks, %d chars", thinkingCount, len(thinkingText))
	t.Logf("Response: %d chunks, text: %s", responseCount, responseText)

	if thinkingCount == 0 {
		t.Error("expected thinking chunks during streaming")
	}
	if responseCount == 0 {
		t.Error("expected response chunks during streaming")
	}
	if responseText == "" {
		t.Error("expected non-empty streamed response")
	}
}
