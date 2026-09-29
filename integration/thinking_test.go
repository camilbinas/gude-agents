package integration_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
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

func TestIntegration_Thinking_CallbackFires(t *testing.T) {
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

	a, err := agent.New(tp, "You are a helpful assistant. Be brief.")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var thinkingChunks []string
	var result *agent.Result
	for event, streamErr := range a.Stream(agent.NewContext(ctx), "What is 17 * 23? Show your reasoning.") {
		if streamErr != nil {
			t.Fatalf("Stream error: %v", streamErr)
		}
		switch event.Type {
		case agent.EventThinking:
			thinkingChunks = append(thinkingChunks, event.Thinking.Content)
		case agent.EventEnd:
			result = event.Result
		}
	}
	if result == nil {
		t.Fatal("stream ended without a result")
	}

	thinkingText := strings.Join(thinkingChunks, "")
	t.Logf("Response: %s", result.Text)
	t.Logf("Thinking chunks: %d, total length: %d chars", len(thinkingChunks), len(thinkingText))
	if len(thinkingChunks) == 0 {
		t.Error("expected thinking events to be emitted at least once")
	}
	if thinkingText == "" {
		t.Error("expected non-empty thinking text")
	}
	if !strings.Contains(result.Text, "391") {
		t.Logf("Warning: expected response to contain '391', got: %s", result.Text)
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

	a, err := agent.New(tp, "You are a helpful assistant. Be brief.")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var thinkingChunks []string
	var responseChunks []string
	for event, streamErr := range a.Stream(agent.NewContext(ctx), "Explain why the sky is blue in one sentence.") {
		if streamErr != nil {
			t.Fatalf("Stream error: %v", streamErr)
		}
		switch event.Type {
		case agent.EventThinking:
			thinkingChunks = append(thinkingChunks, event.Thinking.Content)
		case agent.EventText:
			responseChunks = append(responseChunks, event.Text.Content)
		}
	}

	thinkingText := strings.Join(thinkingChunks, "")
	responseText := strings.Join(responseChunks, "")
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
