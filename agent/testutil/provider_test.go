package testutil_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/testutil"
)

func TestProvider_ScriptedResponses(t *testing.T) {
	p := testutil.NewMockProvider(
		testutil.WithResponses(
			&agent.ModelResponse{Text: "first"},
			&agent.ModelResponse{Text: "second"},
		),
	)

	r1, err := p.Stream(context.Background(), agent.ModelRequest{}, nil)
	if err != nil || r1.Text != "first" {
		t.Fatalf("expected first, got %v / %v", r1, err)
	}
	r2, err := p.Stream(context.Background(), agent.ModelRequest{}, nil)
	if err != nil || r2.Text != "second" {
		t.Fatalf("expected second, got %v / %v", r2, err)
	}
	_, err = p.Stream(context.Background(), agent.ModelRequest{}, nil)
	if err == nil {
		t.Fatal("expected error when queue exhausted")
	}
}

func TestProvider_Capture(t *testing.T) {
	p := testutil.NewMockProvider(
		testutil.WithCapture(),
		testutil.WithResponses(&agent.ModelResponse{Text: "ok"}),
	)

	req := agent.ModelRequest{System: "test-system"}
	_, _ = p.Stream(context.Background(), req, nil)

	captured := p.Captured()
	if len(captured) != 1 || captured[0].System != "test-system" {
		t.Fatalf("unexpected captured: %+v", captured)
	}
}

func TestProvider_FixedError(t *testing.T) {
	boom := errors.New("boom")
	p := testutil.NewMockProvider(testutil.WithError(boom))

	_, err := p.Stream(context.Background(), agent.ModelRequest{}, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
}

func TestProvider_FailFirst(t *testing.T) {
	transient := errors.New("transient")
	p := testutil.NewMockProvider(
		testutil.WithFailFirst(2, transient),
		testutil.WithResponses(&agent.ModelResponse{Text: "ok"}),
	)

	_, err := p.Stream(context.Background(), agent.ModelRequest{}, nil)
	if !errors.Is(err, transient) {
		t.Fatalf("call 1: expected transient, got %v", err)
	}
	_, err = p.Stream(context.Background(), agent.ModelRequest{}, nil)
	if !errors.Is(err, transient) {
		t.Fatalf("call 2: expected transient, got %v", err)
	}
	r, err := p.Stream(context.Background(), agent.ModelRequest{}, nil)
	if err != nil || r.Text != "ok" {
		t.Fatalf("call 3: expected ok, got %v / %v", r, err)
	}
}

func TestProvider_Delay_Respects_Cancellation(t *testing.T) {
	p := testutil.NewMockProvider(
		testutil.WithDelay(5*time.Second),
		testutil.WithResponses(&agent.ModelResponse{Text: "slow"}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := p.Stream(ctx, agent.ModelRequest{}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestProvider_StreamWords(t *testing.T) {
	p := testutil.NewMockProvider(
		testutil.WithStreamWords(),
		testutil.WithResponses(&agent.ModelResponse{Text: "hello world"}),
	)

	var events []agent.ModelEvent
	_, err := p.Stream(context.Background(), agent.ModelRequest{}, func(event agent.ModelEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	var chunks []string
	for _, event := range events {
		if event.Type != agent.ModelEventText {
			t.Fatalf("event type = %q, want %q", event.Type, agent.ModelEventText)
		}
		chunks = append(chunks, event.Text)
	}
	if joined := strings.Join(chunks, ""); joined != "hello world" {
		t.Fatalf("expected 'hello world', got %q", joined)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected word-level chunks, got %d chunks", len(chunks))
	}
}

func TestProvider_ModelID(t *testing.T) {
	p := testutil.NewMockProvider(testutil.WithModelID("test-model-v1"))
	if p.ModelID() != "test-model-v1" {
		t.Fatalf("expected test-model-v1, got %s", p.ModelID())
	}
}

func TestProvider_Reset(t *testing.T) {
	p := testutil.NewMockProvider(
		testutil.WithCapture(),
		testutil.WithResponses(
			&agent.ModelResponse{Text: "a"},
			&agent.ModelResponse{Text: "b"},
		),
	)

	_, _ = p.Stream(context.Background(), agent.ModelRequest{}, nil)
	if p.CallCount() != 1 {
		t.Fatalf("expected 1 call, got %d", p.CallCount())
	}

	p.Reset()
	if p.CallCount() != 0 {
		t.Fatalf("expected 0 after reset, got %d", p.CallCount())
	}
	if len(p.Captured()) != 0 {
		t.Fatalf("expected empty captured after reset")
	}

	// Can reuse responses from the beginning.
	r, err := p.Stream(context.Background(), agent.ModelRequest{}, nil)
	if err != nil || r.Text != "a" {
		t.Fatalf("expected a after reset, got %v / %v", r, err)
	}
}
