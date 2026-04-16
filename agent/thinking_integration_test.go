//go:build integration

package agent_test

import (
	"context"
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
//   go test -tags=integration -v -timeout=120s -run TestIntegration_Thinking ./agent/...
//
// These tests require a provider that supports thinking (Claude 4-series on Bedrock).
// They are skipped when MODEL_PROVIDER is set to a provider without thinking support.

func TestIntegration_Thinking_CallbackFires(t *testing.T) {
	// Thinking is only supported on Claude 4-series (Bedrock) and Gemini 2.5+.
	// Skip for providers that don't support it.
	providerName := os.Getenv("MODEL_PROVIDER")
	if providerName != "" && providerName != "bedrock" && providerName != "gemini" {
		t.Skipf("skipping thinking test for provider %q (not supported)", providerName)
	}

	// Use Bedrock Claude directly with thinking enabled — the registry tier
	// functions don't enable thinking by default.
	p, err := bedrock.ClaudeSonnet4_6(bedrock.WithThinking(pvdr.ThinkingLow))
	if err != nil {
		t.Fatal(err)
	}
	tp := &trackingProvider{inner: p}

	var mu sync.Mutex
	var thinkingChunks []string

	a, err := agent.New(tp,
		prompt.Text("You are a helpful assistant. Be brief."),
		nil,
		agent.WithThinkingCallback(func(chunk string) {
			mu.Lock()
			thinkingChunks = append(thinkingChunks, chunk)
			mu.Unlock()
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, _, err := a.Invoke(ctx, "What is 17 * 23? Show your reasoning.")
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}

	t.Logf("Response: %s", result)

	mu.Lock()
	chunkCount := len(thinkingChunks)
	thinkingText := strings.Join(thinkingChunks, "")
	mu.Unlock()

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
	providerName := os.Getenv("MODEL_PROVIDER")
	if providerName != "" && providerName != "bedrock" && providerName != "gemini" {
		t.Skipf("skipping thinking test for provider %q (not supported)", providerName)
	}

	p, err := bedrock.ClaudeSonnet4_6(bedrock.WithThinking(pvdr.ThinkingLow))
	if err != nil {
		t.Fatal(err)
	}
	tp := &trackingProvider{inner: p}

	var mu sync.Mutex
	var thinkingChunks []string
	var responseChunks []string

	a, err := agent.New(tp,
		prompt.Text("You are a helpful assistant. Be brief."),
		nil,
		agent.WithThinkingCallback(func(chunk string) {
			mu.Lock()
			thinkingChunks = append(thinkingChunks, chunk)
			mu.Unlock()
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err = a.InvokeStream(ctx, "Explain why the sky is blue in one sentence.", func(chunk string) {
		mu.Lock()
		responseChunks = append(responseChunks, chunk)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("InvokeStream error: %v", err)
	}

	mu.Lock()
	thinkingText := strings.Join(thinkingChunks, "")
	responseText := strings.Join(responseChunks, "")
	mu.Unlock()

	t.Logf("Thinking: %d chunks, %d chars", len(thinkingChunks), len(thinkingText))
	t.Logf("Response: %d chunks, text: %s", len(responseChunks), responseText)

	if len(thinkingChunks) == 0 {
		t.Error("expected thinking chunks during streaming")
	}
	if len(responseChunks) == 0 {
		t.Error("expected response chunks during streaming")
	}
	if responseText == "" {
		t.Error("expected non-empty streamed response")
	}
}
