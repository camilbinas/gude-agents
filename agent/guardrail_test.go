package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
)

// capturingProvider records the ConverseParams it receives and returns scripted responses.
type capturingProvider struct {
	responses []*ProviderResponse
	callIndex int
	captured  []ConverseParams
}

func newCapturingProvider(responses ...*ProviderResponse) *capturingProvider {
	return &capturingProvider{responses: responses}
}

func (cp *capturingProvider) Converse(ctx context.Context, params ConverseParams) (*ProviderResponse, error) {
	cp.captured = append(cp.captured, params)
	if cp.callIndex >= len(cp.responses) {
		return nil, fmt.Errorf("capturingProvider: no more responses")
	}
	resp := cp.responses[cp.callIndex]
	cp.callIndex++
	return resp, nil
}

func (cp *capturingProvider) ConverseStream(ctx context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	cp.captured = append(cp.captured, params)
	if cp.callIndex >= len(cp.responses) {
		return nil, fmt.Errorf("capturingProvider: no more responses")
	}
	resp := cp.responses[cp.callIndex]
	cp.callIndex++

	if len(resp.ToolCalls) == 0 && resp.Text != "" && cb != nil {
		cb(resp.Text)
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// Guardrail tests (task 9.3)
// ---------------------------------------------------------------------------

func TestInputGuardrail_TransformsMessage(t *testing.T) {
	cp := newCapturingProvider(&ProviderResponse{Text: "reply"})

	upperGuardrail := func(_ context.Context, msg string) (string, error) {
		return strings.ToUpper(msg), nil
	}

	a, err := New(cp, prompt.Text("sys"), nil, WithInputGuardrail(upperGuardrail))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The provider should have received the uppercased message.
	if len(cp.captured) != 1 {
		t.Fatalf("expected 1 provider call, got %d", len(cp.captured))
	}
	msgs := cp.captured[0].Messages
	if len(msgs) == 0 {
		t.Fatal("expected at least one message sent to provider")
	}
	lastMsg := msgs[len(msgs)-1]
	if lastMsg.Role != RoleUser {
		t.Fatalf("expected last message role=user, got %s", lastMsg.Role)
	}
	if len(lastMsg.Content) == 0 {
		t.Fatal("expected content in user message")
	}
	tb, ok := lastMsg.Content[0].(TextBlock)
	if !ok {
		t.Fatalf("expected TextBlock, got %T", lastMsg.Content[0])
	}
	if tb.Text != "HELLO" {
		t.Errorf("expected provider to receive %q, got %q", "HELLO", tb.Text)
	}
}

func TestOutputGuardrail_TransformsResponse(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "raw response"})

	filterGuardrail := func(_ context.Context, resp string) (string, error) {
		return resp + " [filtered]", nil
	}

	a, err := New(sp, prompt.Text("sys"), nil, WithOutputGuardrail(filterGuardrail))
	if err != nil {
		t.Fatal(err)
	}

	result, _, err := a.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "raw response [filtered]" {
		t.Errorf("expected %q, got %q", "raw response [filtered]", result)
	}
}

func TestInputGuardrail_ErrorAbortsInvocation(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "should not reach"})

	blockGuardrail := func(_ context.Context, msg string) (string, error) {
		return "", fmt.Errorf("blocked content")
	}

	a, err := New(sp, prompt.Text("sys"), nil, WithInputGuardrail(blockGuardrail))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "bad input")
	if err == nil {
		t.Fatal("expected error from input guardrail, got nil")
	}
	if !strings.Contains(err.Error(), "input guardrail") {
		t.Errorf("expected error to contain 'input guardrail', got: %v", err)
	}
}

func TestOutputGuardrail_ErrorAbortsInvocation(t *testing.T) {
	sp := newScriptedProvider(&ProviderResponse{Text: "some response"})

	blockGuardrail := func(_ context.Context, resp string) (string, error) {
		return "", fmt.Errorf("response policy violation")
	}

	a, err := New(sp, prompt.Text("sys"), nil, WithOutputGuardrail(blockGuardrail))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error from output guardrail, got nil")
	}
	if !strings.Contains(err.Error(), "output guardrail") {
		t.Errorf("expected error to contain 'output guardrail', got: %v", err)
	}
}

func TestMultipleInputGuardrails_AppliedInOrder(t *testing.T) {
	cp := newCapturingProvider(&ProviderResponse{Text: "done"})

	appendA := func(_ context.Context, msg string) (string, error) {
		return msg + "-A", nil
	}
	appendB := func(_ context.Context, msg string) (string, error) {
		return msg + "-B", nil
	}

	a, err := New(cp, prompt.Text("sys"), nil,
		WithInputGuardrail(appendA),
		WithInputGuardrail(appendB),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = a.Invoke(context.Background(), "start")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cp.captured) != 1 {
		t.Fatalf("expected 1 provider call, got %d", len(cp.captured))
	}
	msgs := cp.captured[0].Messages
	lastMsg := msgs[len(msgs)-1]
	tb, ok := lastMsg.Content[0].(TextBlock)
	if !ok {
		t.Fatalf("expected TextBlock, got %T", lastMsg.Content[0])
	}
	if tb.Text != "start-A-B" {
		t.Errorf("expected %q, got %q", "start-A-B", tb.Text)
	}
}
