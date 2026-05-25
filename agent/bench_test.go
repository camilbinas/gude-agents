package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// ---------------------------------------------------------------------------
// Bench harness
// ---------------------------------------------------------------------------

// benchProvider is a stripped-down Provider designed for tight benchmark loops.
// Unlike scriptedProvider, it has no mutex, no slice popping, and no per-call
// allocation. It returns the same response on every call.
type benchProvider struct {
	resp *ProviderResponse
	// streamChunks, when non-nil, are emitted via cb during ConverseStream
	// so we exercise the streaming path. Each string becomes a single chunk.
	streamChunks []string
}

func (benchProvider) Name() string { return "bench" }

func (p *benchProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	return p.resp, nil
}

func (p *benchProvider) ConverseStream(_ context.Context, _ ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	if cb != nil {
		for _, c := range p.streamChunks {
			cb(c)
		}
	}
	return p.resp, nil
}

// fixedTextProvider yields a single text response with no streaming.
func fixedTextProvider(text string) *benchProvider {
	return &benchProvider{
		resp: &ProviderResponse{Text: text},
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
		resp:         &ProviderResponse{Text: text},
		streamChunks: pieces,
	}
}

// noopHook is a recording-free EventHook that does nothing on every callback.
// Useful for measuring "hook installed but does no work" overhead.
type noopHook struct {
	BaseEventHook
}

// countingHook records nothing but increments an atomic counter so the
// compiler can't optimize the call away. Used to measure realistic hook
// dispatch cost.
type countingHook struct {
	BaseEventHook
	calls atomic.Uint64
}

func (h *countingHook) OnModelStart(_ *Context)         { h.calls.Add(1) }
func (h *countingHook) OnModelEnd(_ *Context, _ string) { h.calls.Add(1) }

// ---------------------------------------------------------------------------
// Agent loop overhead — the cheapest happy path.
// ---------------------------------------------------------------------------

func BenchmarkInvoke_NoTools_NoHooks(b *testing.B) {
	p := fixedTextProvider("ok")
	a, err := New(p, prompt.Text("sys"), nil)
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

func BenchmarkInvokeStream_NoTools_NoHooks(b *testing.B) {
	p := streamingTextProvider("the quick brown fox jumps over the lazy dog", 8)
	a, err := New(p, prompt.Text("sys"), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := a.InvokeStream(Background(), "hi", func(_ string) {}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkInvokeEventStream_NoTools_NoHooks measures the per-invocation
// overhead of the channel-based event stream against the same workload as
// BenchmarkInvokeStream. Subtract the two to get the channel + clone + hook
// fan-out cost.
func BenchmarkInvokeEventStream_NoTools_NoHooks(b *testing.B) {
	p := streamingTextProvider("the quick brown fox jumps over the lazy dog", 8)
	a, err := New(p, prompt.Text("sys"), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		events := a.InvokeEventStream(Background(), "hi")
		var last EventType
		for ev := range events {
			last = ev.Type
		}
		if last != EventInvokeEnd {
			b.Fatalf("missing terminal event, got %s", last)
		}
	}
}

// ---------------------------------------------------------------------------
// Hook overhead — the "zero overhead when nil" claim, pinned.
// ---------------------------------------------------------------------------

// BenchmarkAgent_HookOverhead_None is the baseline: no EventHook on the
// context. Should be identical to BenchmarkInvokeStream_NoTools_NoHooks.
func BenchmarkAgent_HookOverhead_None(b *testing.B) {
	p := fixedTextProvider("ok")
	a, err := New(p, prompt.Text("sys"), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := a.InvokeStream(Background(), "hi", nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAgent_HookOverhead_Base measures cost when an EventHook is set
// but every callback is a no-op (BaseEventHook). Difference vs None pins the
// dispatch overhead.
func BenchmarkAgent_HookOverhead_Base(b *testing.B) {
	p := fixedTextProvider("ok")
	a, err := New(p, prompt.Text("sys"), nil)
	if err != nil {
		b.Fatal(err)
	}
	hook := noopHook{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c := Background().WithEventHook(hook)
		if err := a.InvokeStream(c, "hi", nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAgent_HookOverhead_Counting puts a tiny atomic increment in the
// hottest two callbacks (OnModelStart, OnModelEnd). Realistic floor for any
// hook that does actual work.
func BenchmarkAgent_HookOverhead_Counting(b *testing.B) {
	p := fixedTextProvider("ok")
	a, err := New(p, prompt.Text("sys"), nil)
	if err != nil {
		b.Fatal(err)
	}
	hook := &countingHook{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c := Background().WithEventHook(hook)
		if err := a.InvokeStream(c, "hi", nil); err != nil {
			b.Fatal(err)
		}
	}
	if hook.calls.Load() == 0 {
		b.Fatal("hook never fired — OnModelStart/End wiring may have regressed")
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

func (p *benchToolProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	if p.calls.Add(1) == 1 {
		return &ProviderResponse{
			ToolCalls: []tool.Call{{ToolUseID: "t1", Name: "echo", Input: json.RawMessage(`{}`)}},
		}, nil
	}
	return &ProviderResponse{Text: "done"}, nil
}

func (p *benchToolProvider) ConverseStream(_ context.Context, _ ConverseParams, _ StreamCallback) (*ProviderResponse, error) {
	return p.Converse(context.Background(), ConverseParams{})
}

func newEchoTool() tool.Tool {
	return tool.NewRaw("echo", "echo",
		map[string]any{"type": "object"},
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
		a, err := New(p, prompt.Text("sys"), []tool.Tool{echo})
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
	return func(c *Context, name string, input json.RawMessage) (string, error) {
		return next(c, name, input)
	}
}

func BenchmarkInvoke_OneToolCall_ThreeMiddlewares(b *testing.B) {
	echo := newEchoTool()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := &benchToolProvider{}
		a, err := New(p, prompt.Text("sys"), []tool.Tool{echo},
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

// BenchmarkInvokeEventStream_FastConsumer drains the channel as fast as
// possible. Combined with BenchmarkInvokeStream_NoTools_NoHooks this gives
// a clean number for stream overhead vs. callback streaming.
func BenchmarkInvokeEventStream_FastConsumer(b *testing.B) {
	p := streamingTextProvider("the quick brown fox", 16)
	a, err := New(p, prompt.Text("sys"), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var n int
		for range a.InvokeEventStream(Background(), "hi") {
			n++
		}
		if n == 0 {
			b.Fatal("no events received")
		}
	}
}

// BenchmarkInvokeEventStream_SlowConsumer simulates a UI consumer that takes
// 100µs per event. With the default buffer of 64 the engine should make
// progress until the buffer fills, then back-pressure cleanly. The benchmark
// is mostly checking we don't pathologically burn CPU under back-pressure.
func BenchmarkInvokeEventStream_SlowConsumer(b *testing.B) {
	p := streamingTextProvider("the quick brown fox jumps over the lazy dog", 32)
	a, err := New(p, prompt.Text("sys"), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for range a.InvokeEventStream(Background(), "hi") {
			time.Sleep(100 * time.Microsecond)
		}
	}
}
