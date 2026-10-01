package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// ---------------------------------------------------------------------------
// Bench harness
// ---------------------------------------------------------------------------

// benchProvider is a stripped-down Provider designed for tight benchmark loops.
// Unlike scriptedProvider, it has no mutex, no slice popping, and no per-call
// allocation. It returns the same response on every call.
type benchProvider struct {
	resp *ModelResponse
	// streamChunks, when non-nil, are emitted via cb during Stream
	// so we exercise the streaming path. Each string becomes a single chunk.
	streamChunks []string
}

func (benchProvider) Name() string { return "bench" }

func (p *benchProvider) Stream(_ context.Context, _ ModelRequest, cb func(ModelEvent)) (*ModelResponse, error) {
	if cb != nil {
		for _, c := range p.streamChunks {
			cb(ModelEvent{Type: ModelEventText, Text: c})
		}
	}
	return p.resp, nil
}

// fixedTextProvider yields a single text response with no streaming.
func fixedTextProvider(text string) *benchProvider {
	return &benchProvider{
		resp: &ModelResponse{Text: text},
	}
}

// streamingTextProvider yields text and emits each rune-prefix as a chunk so
// streaming benchmarks see realistic chunk volume.
func streamingTextProvider(text string, chunks int) *benchProvider {
	if chunks <= 0 {
		chunks = 1
	}
	step := len(text) / chunks
	if step < 1 {
		step = 1
	}
	pieces := make([]string, 0, chunks)
	for i := 0; i < len(text); i += step {
		end := i + step
		if end > len(text) {
			end = len(text)
		}
		pieces = append(pieces, text[i:end])
	}
	return &benchProvider{
		resp:         &ModelResponse{Text: text},
		streamChunks: pieces,
	}
}

// ---------------------------------------------------------------------------
// Agent loop overhead — the cheapest happy path.
// ---------------------------------------------------------------------------

func BenchmarkInvoke_NoTools_NoHooks(b *testing.B) {
	p := fixedTextProvider("ok")
	a, err := New(p, "sys")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := a.Invoke(Background(), "hi"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTextStream_NoTools_NoHooks(b *testing.B) {
	p := streamingTextProvider("the quick brown fox jumps over the lazy dog", 8)
	a, err := New(p, "sys")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, err := range a.TextStream(Background(), "hi") {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkStream_NoTools_NoHooks measures the per-invocation overhead of the
// full event stream against the same workload as BenchmarkTextStream.
func BenchmarkStream_NoTools_NoHooks(b *testing.B) {
	p := streamingTextProvider("the quick brown fox jumps over the lazy dog", 8)
	a, err := New(p, "sys")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var last EventType
		for ev, err := range a.Stream(Background(), "hi") {
			if err != nil {
				b.Fatal(err)
			}
			last = ev.Type
		}
		if last != EventEnd {
			b.Fatalf("missing terminal event, got %s", last)
		}
	}
}

// BenchmarkStream_DetailedEvents measures the cost of detailed lifecycle events.
func BenchmarkStream_DetailedEvents(b *testing.B) {
	p := fixedTextProvider("ok")
	a, err := New(p, "sys")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for range a.Stream(Background().WithDetailedEvents(), "hi") {
		}
	}
}

// ---------------------------------------------------------------------------
// Tool dispatch — the cost of one tool round-trip on the hot path.
// ---------------------------------------------------------------------------

// benchToolProvider returns one tool call on the first invocation and
// final text on the second.
type benchToolProvider struct {
	calls atomic.Int64
}

func (*benchToolProvider) Name() string { return "bench-tool" }

func (p *benchToolProvider) Stream(_ context.Context, _ ModelRequest, _ func(ModelEvent)) (*ModelResponse, error) {
	if p.calls.Add(1) == 1 {
		return &ModelResponse{
			ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "echo", Input: json.RawMessage(`{}`)}},
		}, nil
	}
	return &ModelResponse{Text: "done"}, nil
}

func newEchoTool() tool.Tool {
	return tool.NewRaw(
		"echo",
		"echo",
		nil,
		func(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil },
	)
}

// resetBenchToolProvider primes a fresh two-step provider for each iteration.
// We allocate inside the loop so each Invoke gets a clean two-step script.
func BenchmarkInvoke_OneToolCall_NoMiddleware(b *testing.B) {
	echo := newEchoTool()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := &benchToolProvider{}
		a, err := New(p, "sys", WithTools(echo))
		if err != nil {
			b.Fatal(err)
		}
		if _, err := a.Invoke(Background(), "go"); err != nil {
			b.Fatal(err)
		}
	}
}

// noopMW is a middleware that does nothing but call next.
func noopMW(next ToolHandlerFunc) ToolHandlerFunc {
	return func(ctx context.Context, call ToolCall) (ToolResult, error) {
		return next(ctx, call)
	}
}

func BenchmarkInvoke_OneToolCall_ThreeMiddlewares(b *testing.B) {
	echo := newEchoTool()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := &benchToolProvider{}
		a, err := New(p, "sys", WithTools(echo),
			WithMiddleware(noopMW, noopMW, noopMW),
		)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := a.Invoke(Background(), "go"); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// Event-stream throughput — fast vs. slow consumer.
// ---------------------------------------------------------------------------

func BenchmarkStream_FastConsumer(b *testing.B) {
	p := streamingTextProvider("the quick brown fox", 16)
	a, err := New(p, "sys")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var n int
		for range a.Stream(Background(), "hi") {
			n++
		}
		if n == 0 {
			b.Fatal("no events received")
		}
	}
}

// BenchmarkStream_SlowConsumer simulates a UI consumer that takes 100µs per
// event. The engine runs on the consumer's goroutine, so a slow consumer
// applies back-pressure directly.
func BenchmarkStream_SlowConsumer(b *testing.B) {
	p := streamingTextProvider("the quick brown fox jumps over the lazy dog", 32)
	a, err := New(p, "sys")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for range a.Stream(Background(), "hi") {
			time.Sleep(100 * time.Microsecond)
		}
	}
}
