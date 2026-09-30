package agent

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// collectStream drains a stream and returns all events and the final error.
func collectStream(seq func(func(Event, error) bool)) ([]Event, error) {
	var events []Event
	var final error
	for ev, err := range seq {
		events = append(events, ev)
		if err != nil {
			final = err
		}
	}
	return events, final
}

func eventTypes(events []Event) []EventType {
	out := make([]EventType, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

func countOf(events []Event, t EventType) int {
	n := 0
	for _, e := range events {
		if e.Type == t {
			n++
		}
	}
	return n
}

// errProvider returns a fixed error from Stream.
type errProvider struct{ err error }

func (errProvider) Name() string { return "err" }
func (p errProvider) Stream(_ context.Context, _ ModelRequest, _ func(ModelEvent)) (*ModelResponse, error) {
	return nil, p.err
}

// thinkingProvider emits thinking events during Stream.
type thinkingProvider struct {
	chunks   []string
	response *ModelResponse
}

func (tp *thinkingProvider) Name() string { return "mock" }
func (tp *thinkingProvider) Stream(_ context.Context, _ ModelRequest, emit func(ModelEvent)) (*ModelResponse, error) {
	if emit != nil {
		for _, chunk := range tp.chunks {
			emit(ModelEvent{Type: ModelEventThinking, Text: chunk})
		}
		if tp.response.Text != "" {
			for _, word := range strings.SplitAfter(tp.response.Text, " ") {
				emit(ModelEvent{Type: ModelEventText, Text: word})
			}
		}
	}
	return tp.response, nil
}

// chunkingProvider streams chunks one at a time, stopping when ctx is done.
type chunkingProvider struct {
	chunks    []string
	onChunk   func(i int)
	emitted   atomic.Int32
	sawCancel atomic.Bool
}

func (p *chunkingProvider) Name() string { return "chunking" }
func (p *chunkingProvider) Stream(ctx context.Context, _ ModelRequest, emit func(ModelEvent)) (*ModelResponse, error) {
	for i, chunk := range p.chunks {
		if err := ctx.Err(); err != nil {
			p.sawCancel.Store(true)
			return nil, err
		}
		emit(ModelEvent{Type: ModelEventText, Text: chunk})
		p.emitted.Add(1)
		if p.onChunk != nil {
			p.onChunk(i)
		}
	}
	return &ModelResponse{Text: strings.Join(p.chunks, ""), Usage: TokenUsage{InputTokens: 3, OutputTokens: 4}}, nil
}

func TestStream_TextOnlySequence(t *testing.T) {
	p := newScriptedProvider(&ModelResponse{Text: "Hello streaming world", Usage: TokenUsage{InputTokens: 5, OutputTokens: 3}})
	a, err := New(p, "sys")
	if err != nil {
		t.Fatal(err)
	}
	events, err := collectStream(a.Stream(Background(), "hi"))
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}
	types := eventTypes(events)
	if types[0] != EventStart || types[len(types)-1] != EventEnd {
		t.Fatalf("types = %v, want start ... end", types)
	}
	var text strings.Builder
	for _, ev := range events {
		if ev.Time.IsZero() {
			t.Fatalf("event %s has zero Time", ev.Type)
		}
		switch ev.Type {
		case EventText:
			text.WriteString(ev.Text.Content)
		case EventIterationStart, EventModelStart, EventModelEnd, EventIterationEnd:
			t.Fatalf("detailed event %s emitted without WithDetailedEvents", ev.Type)
		}
	}
	end := events[len(events)-1]
	if end.Result == nil || end.Error != nil {
		t.Fatalf("end = %+v", end)
	}
	if end.Result.Text != "Hello streaming world" || text.String() != end.Result.Text {
		t.Fatalf("result text %q, streamed %q", end.Result.Text, text.String())
	}
	if end.Result.Usage.InputTokens != 5 || end.Result.StopReason != StopEndTurn {
		t.Fatalf("result = %+v", end.Result)
	}
}

func TestInvokeAndStream_EquivalentResult(t *testing.T) {
	script := func() Provider {
		return newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{toolCall("t1", "echo")}, Usage: TokenUsage{InputTokens: 10, OutputTokens: 2}},
			&ModelResponse{Text: "final answer", Usage: TokenUsage{InputTokens: 12, OutputTokens: 4}, Metadata: map[string]any{"k": "v"}},
		)
	}
	newAgent := func() *Agent {
		a, err := New(script(), "sys", WithTools(dummyTool("echo", "echo")))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	invoked, err := newAgent().Invoke(Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	events, err := collectStream(newAgent().Stream(Background(), "go"))
	if err != nil {
		t.Fatal(err)
	}
	streamed := events[len(events)-1].Result
	if streamed == nil {
		t.Fatal("missing end result")
	}
	if invoked.Text != streamed.Text || invoked.Usage != streamed.Usage || invoked.StopReason != streamed.StopReason ||
		invoked.Metadata["k"] != streamed.Metadata["k"] {
		t.Fatalf("Invoke %+v != Stream %+v", invoked, *streamed)
	}
	if invoked.Usage != (TokenUsage{InputTokens: 22, OutputTokens: 6}) {
		t.Fatalf("usage = %+v", invoked.Usage)
	}
}

func TestTextStream_ConcatenationEqualsResultTextWithoutThinking(t *testing.T) {
	newAgent := func() *Agent {
		p := &thinkingProvider{chunks: []string{"let me think", "..."}, response: &ModelResponse{Text: "the final answer"}}
		a, err := New(p, "sys")
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	var chunks []string
	for chunk, err := range newAgent().TextStream(Background(), "q") {
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, chunk)
	}
	res, err := newAgent().Invoke(Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(chunks, "") != res.Text {
		t.Fatalf("TextStream %q != Result.Text %q", strings.Join(chunks, ""), res.Text)
	}
	for _, c := range chunks {
		if strings.Contains(c, "think") || c == "..." {
			t.Fatalf("TextStream yielded thinking chunk %q", c)
		}
	}
	if len(chunks) < 2 {
		t.Fatalf("expected live chunks, got %v", chunks)
	}

	// Thinking is still available on Stream.
	events, _ := collectStream(newAgent().Stream(Background(), "q"))
	if countOf(events, EventThinking) != 2 {
		t.Fatalf("thinking events = %d, want 2", countOf(events, EventThinking))
	}
}

func TestTextStream_YieldsError(t *testing.T) {
	a, err := New(errProvider{err: errors.New("boom")}, "sys")
	if err != nil {
		t.Fatal(err)
	}
	var gotErr error
	for _, err := range a.TextStream(Background(), "q") {
		gotErr = err
	}
	var pe *ProviderError
	if !errors.As(gotErr, &pe) {
		t.Fatalf("err = %v, want ProviderError", gotErr)
	}
}

func TestStream_ToolStartEndShareCallID(t *testing.T) {
	widgetTool := newTestRaw("chart", "draws", map[string]any{"type": "object"},
		func(ctx context.Context, _ json.RawMessage) (string, error) {
			if err := EmitWidget(ctx, WidgetBlock{Type: "chart", Payload: json.RawMessage(`{"x":1}`)}); err != nil {
				return "", err
			}
			return "drawn", nil
		})
	p := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{
			{ToolUseID: "call-a", Name: "chart", Input: json.RawMessage(`{"n":1}`)},
			{ToolUseID: "call-b", Name: "missing", Input: json.RawMessage(`{}`)},
		}},
		&ModelResponse{Text: "done"},
	)
	a, err := New(p, "sys", WithTools(widgetTool))
	if err != nil {
		t.Fatal(err)
	}
	events, err := collectStream(a.Stream(Background(), "draw"))
	if err != nil {
		t.Fatal(err)
	}
	starts := map[string]*ToolEvent{}
	ends := map[string]*ToolEvent{}
	var order []string
	for _, ev := range events {
		switch ev.Type {
		case EventToolStart:
			starts[ev.Tool.CallID] = ev.Tool
			order = append(order, "start:"+ev.Tool.CallID)
		case EventToolEnd:
			ends[ev.Tool.CallID] = ev.Tool
			order = append(order, "end:"+ev.Tool.CallID)
		case EventWidget:
			order = append(order, "widget:"+ev.Widget.CallID)
		}
	}
	want := []string{"start:call-a", "widget:call-a", "end:call-a", "start:call-b", "end:call-b"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if string(starts["call-a"].Input) != `{"n":1}` || starts["call-a"].Name != "chart" {
		t.Fatalf("start = %+v", starts["call-a"])
	}
	if ends["call-a"].Output != "drawn" || ends["call-a"].Error != nil {
		t.Fatalf("end = %+v", ends["call-a"])
	}
	if ends["call-b"].Error == nil || ends["call-b"].Error.Code != ErrorCodeUnknownTool {
		t.Fatalf("unknown tool end = %+v", ends["call-b"])
	}
}

func TestStream_BreakStopsProduction(t *testing.T) {
	p := &chunkingProvider{chunks: []string{"a", "b", "c", "d", "e"}}
	conv := newMemConversation()
	a, err := New(p, "sys", WithConversationStore(conv))
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for ev, err := range a.Stream(Background().WithConversationID("c1"), "go") {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ev.Type == EventText {
			seen++
			break
		}
	}
	if seen != 1 {
		t.Fatalf("seen = %d", seen)
	}
	if got := p.emitted.Load(); got != 1 {
		t.Fatalf("provider emitted %d chunks after break, want 1", got)
	}
	if !p.sawCancel.Load() {
		t.Fatal("provider did not observe cancellation")
	}
	if msgs, _ := testLoadMessages(context.Background(), conv, "c1"); len(msgs) != 0 {
		t.Fatalf("abandoned stream persisted %d messages", len(msgs))
	}
}

func TestStream_ContextCancellationStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &chunkingProvider{chunks: []string{"a", "b", "c"}, onChunk: func(i int) {
		if i == 0 {
			cancel()
		}
	}}
	a, err := New(p, "sys")
	if err != nil {
		t.Fatal(err)
	}
	events, err := collectStream(a.Stream(NewContext(ctx), "go"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	end := events[len(events)-1]
	if end.Type != EventEnd || end.Error == nil || end.Error.Code != ErrorCodeCanceled {
		t.Fatalf("end = %+v", end)
	}
	if p.emitted.Load() != 1 {
		t.Fatalf("emitted = %d, want 1", p.emitted.Load())
	}
	if countOf(events, EventText) != 1 {
		t.Fatalf("text events = %d, want 1", countOf(events, EventText))
	}
}

func TestStream_ProviderErrorEndCarriesError(t *testing.T) {
	a, err := New(errProvider{err: errors.New("model down")}, "sys")
	if err != nil {
		t.Fatal(err)
	}
	events, err := collectStream(a.Stream(Background(), "go"))
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want ProviderError", err)
	}
	end := events[len(events)-1]
	if end.Type != EventEnd || end.Error == nil || end.Error.Code != ErrorCodeProvider || end.Result == nil {
		t.Fatalf("end = %+v", end)
	}
	res, ierr := a.Invoke(Background(), "go")
	if !errors.As(ierr, &pe) || res.StopReason != "" {
		t.Fatalf("Invoke = %+v, %v", res, ierr)
	}
}

func TestStream_NilContext(t *testing.T) {
	a, _ := New(newScriptedProvider(), "sys")
	if _, err := a.Invoke(nil, "x"); !errors.Is(err, ErrNilContext) {
		t.Fatalf("err = %v, want ErrNilContext", err)
	}
}

func TestStream_DetailedEventsOnlyWhenEnabled(t *testing.T) {
	newAgent := func() *Agent {
		p := newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{toolCall("t1", "echo")}},
			&ModelResponse{Text: "done"},
		)
		a, err := New(p, "sys", WithTools(dummyTool("echo", "echo")))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	events, err := collectStream(newAgent().Stream(Background().WithDetailedEvents(), "go"))
	if err != nil {
		t.Fatal(err)
	}
	for typ, want := range map[EventType]int{EventIterationStart: 2, EventIterationEnd: 2, EventModelStart: 2, EventModelEnd: 2} {
		if got := countOf(events, typ); got != want {
			t.Errorf("%s count = %d, want %d", typ, got, want)
		}
	}
	var stopReasons []string
	for _, ev := range events {
		if ev.Type == EventModelEnd {
			stopReasons = append(stopReasons, ev.Lifecycle.StopReason)
		}
		if ev.Type == EventIterationEnd && ev.Lifecycle.Iteration == 2 && !ev.Lifecycle.IsFinal {
			t.Error("second iteration must be final")
		}
	}
	if len(stopReasons) != 2 || stopReasons[0] != StopReasonToolUse || stopReasons[1] != StopReasonEndTurn {
		t.Fatalf("stop reasons = %v", stopReasons)
	}

	plain, _ := collectStream(newAgent().Stream(Background(), "go"))
	for _, ev := range plain {
		if ev.Lifecycle != nil {
			t.Fatalf("lifecycle event %s without WithDetailedEvents", ev.Type)
		}
	}
}

func TestStream_MaxIterationsDetailedEvent(t *testing.T) {
	p := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{toolCall("t1", "echo")}},
		&ModelResponse{ToolCalls: []tool.Call{toolCall("t2", "echo")}},
	)
	a, err := New(p, "sys", WithTools(dummyTool("echo", "echo")), WithMaxIterations(2))
	if err != nil {
		t.Fatal(err)
	}
	events, err := collectStream(a.Stream(Background().WithDetailedEvents(), "go"))
	if !errors.Is(err, ErrMaxIterationsExceeded) {
		t.Fatalf("err = %v", err)
	}
	if countOf(events, EventMaxIterations) != 1 {
		t.Fatalf("types = %v", eventTypes(events))
	}
	if end := events[len(events)-1]; end.Error == nil || end.Error.Code != ErrorCodeMaxIterations {
		t.Fatalf("end = %+v", end)
	}
}

func TestStream_CustomEvents(t *testing.T) {
	emitter := newTestRaw("emit", "emits", map[string]any{"type": "object"},
		func(ctx context.Context, _ json.RawMessage) (string, error) {
			if err := EmitEvent(ctx, "rag.retrieved", map[string]int{"docs": 3}); err != nil {
				return "", err
			}
			if err := EmitEvent(ctx, "bad", func() {}); err == nil {
				return "", errors.New("expected marshal error")
			}
			return "ok", nil
		})
	p := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{toolCall("t1", "emit")}},
		&ModelResponse{Text: "done"},
	)
	a, err := New(p, "sys", WithTools(emitter))
	if err != nil {
		t.Fatal(err)
	}
	events, err := collectStream(a.Stream(Background(), "go"))
	if err != nil {
		t.Fatal(err)
	}
	var custom []*CustomEvent
	for _, ev := range events {
		if ev.Type == EventCustom {
			custom = append(custom, ev.Custom)
		}
	}
	if len(custom) != 1 || custom[0].Name != "rag.retrieved" || string(custom[0].Payload) != `{"docs":3}` {
		t.Fatalf("custom = %+v", custom)
	}
	// Outside an invocation EmitEvent is a no-op and EmitWidget fails.
	if err := EmitEvent(context.Background(), "x", 1); err != nil {
		t.Fatalf("EmitEvent outside invocation = %v", err)
	}
	if err := EmitWidget(Background(), WidgetBlock{Type: "x"}); !errors.Is(err, ErrNoToolCall) {
		t.Fatalf("EmitWidget outside tool call = %v", err)
	}
}

// TestStream_ParallelToolsIsolatedScratch verifies parallel tool calls get
// isolated per-call runtime (widgets, call ID) while sharing the user KV.
func TestStream_ParallelToolsIsolatedScratch(t *testing.T) {
	var arrived sync.WaitGroup
	arrived.Add(2)
	release := make(chan struct{})
	go func() {
		arrived.Wait()
		close(release)
	}()
	mk := func(name string) tool.Tool {
		return newTestRaw(name, name, map[string]any{"type": "object"}, func(ctx context.Context, _ json.RawMessage) (string, error) {
			arrived.Done()
			select {
			case <-release:
			case <-time.After(2 * time.Second):
				return "", errors.New("tools did not overlap")
			}
			if err := EmitWidget(ctx, WidgetBlock{Type: name}); err != nil {
				return "", err
			}
			FromContext(ctx).Set("seen-"+name, true)
			return name + " done", nil
		})
	}
	conv := newMemConversation()
	p := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{toolCall("id-a", "a"), toolCall("id-b", "b")}},
		&ModelResponse{Text: "both done"},
	)
	a, err := New(p, "sys", WithTools(mk("a"), mk("b")), WithConversationStore(conv))
	if err != nil {
		t.Fatal(err)
	}
	c := Background().WithConversationID("par")
	events, err := collectStream(a.Stream(c, "go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Type == EventWidget && ev.Widget.CallID != "id-"+ev.Widget.Type {
			t.Fatalf("widget %q attributed to %q", ev.Widget.Type, ev.Widget.CallID)
		}
	}
	if countOf(events, EventWidget) != 2 || countOf(events, EventToolStart) != 2 || countOf(events, EventToolEnd) != 2 {
		t.Fatalf("types = %v", eventTypes(events))
	}
	for _, k := range []string{"seen-a", "seen-b"} {
		if _, ok := c.Get(k); !ok {
			t.Fatalf("user KV value %q set by a tool is not visible to the caller", k)
		}
	}
	msgs, _ := testLoadMessages(context.Background(), conv, "par")
	var assistant Message
	for _, m := range msgs {
		if m.Role == RoleAssistant && len(m.Content) == 4 {
			assistant = m
		}
	}
	if len(assistant.Content) != 4 {
		t.Fatalf("assistant tool message not found in %#v", msgs)
	}
	for i, id := range []string{"id-a", "id-b"} {
		tu, ok := assistant.Content[2*i].(ToolUseBlock)
		w, wok := assistant.Content[2*i+1].(WidgetBlock)
		if !ok || !wok || tu.ToolUseID != id || "id-"+w.Type != id {
			t.Fatalf("content %d = %#v %#v", i, assistant.Content[2*i], assistant.Content[2*i+1])
		}
	}
}

// TestStream_ParallelToolsIsolatedGuardState verifies that overlapping guard
// evaluations of parallel calls do not leak decisions or widgets across calls:
// the denied call reports a denial and no widget, the allowed call runs and
// its widget is attributed to its own CallID.
func TestStream_ParallelToolsIsolatedGuardState(t *testing.T) {
	var arrived sync.WaitGroup
	arrived.Add(2)
	release := make(chan struct{})
	go func() {
		arrived.Wait()
		close(release)
	}()
	var handlerRuns atomic.Int32
	guarded := newTestRaw("g", "guarded", map[string]any{"type": "object"}, func(ctx context.Context, _ json.RawMessage) (string, error) {
		handlerRuns.Add(1)
		c := FromContext(ctx)
		if err := EmitWidget(ctx, WidgetBlock{Type: "w", Payload: json.RawMessage(`"` + c.call.id + `"`)}); err != nil {
			return "", err
		}
		return "ran " + c.call.id, nil
	})
	guarded.Guard = func(_ context.Context, input json.RawMessage) (tool.Decision, error) {
		arrived.Done()
		select {
		case <-release:
		case <-time.After(2 * time.Second):
			return tool.Decision{}, errors.New("guards did not overlap")
		}
		var in struct {
			Deny bool `json:"deny"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Deny {
			return tool.Deny("blocked"), nil
		}
		return tool.Allow(), nil
	}
	p := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{
			{ToolUseID: "deny-me", Name: "g", Input: json.RawMessage(`{"deny":true}`)},
			{ToolUseID: "allow-me", Name: "g", Input: json.RawMessage(`{"deny":false}`)},
		}},
		&ModelResponse{Text: "done"},
	)
	a, err := New(p, "sys", WithTools(guarded))
	if err != nil {
		t.Fatal(err)
	}
	events, err := collectStream(a.Stream(Background(), "go"))
	if err != nil {
		t.Fatal(err)
	}
	ends := map[string]*ToolEvent{}
	var widgets []*WidgetEvent
	for _, ev := range events {
		switch ev.Type {
		case EventToolEnd:
			ends[ev.Tool.CallID] = ev.Tool
		case EventWidget:
			widgets = append(widgets, ev.Widget)
		}
	}
	if handlerRuns.Load() != 1 {
		t.Fatalf("handler runs = %d, want 1", handlerRuns.Load())
	}
	if e := ends["deny-me"]; e == nil || e.Error == nil || e.Error.Code != ErrorCodeToolDenied {
		t.Fatalf("deny-me end = %+v, want denied", e)
	}
	if e := ends["allow-me"]; e == nil || e.Error != nil || e.Output != "ran allow-me" {
		t.Fatalf("allow-me end = %+v, want output", e)
	}
	if len(widgets) != 1 || widgets[0].CallID != "allow-me" || string(widgets[0].Payload) != `"allow-me"` {
		t.Fatalf("widgets = %+v, want one widget for allow-me", widgets)
	}
}

func TestStream_NoGoroutineLeak(t *testing.T) {
	p := &chunkingProvider{chunks: []string{"a", "b", "c"}}
	a, err := New(p, "sys")
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		_, _ = a.Invoke(Background(), "hi")
	}
	runtime.GC()
	baseline := runtime.NumGoroutine()
	for i := range 200 {
		for ev := range a.Stream(Background(), "hi") {
			if i%2 == 0 && ev.Type == EventText {
				break
			}
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	got := runtime.NumGoroutine()
	for got > baseline+2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		got = runtime.NumGoroutine()
	}
	if got > baseline+2 {
		t.Fatalf("goroutine leak: baseline=%d after=%d", baseline, got)
	}
}

func TestEvent_JSONDurationMS(t *testing.T) {
	ev := Event{Type: EventToolEnd, Tool: &ToolEvent{CallID: "c1", Name: "n", Output: "o", Duration: 1500 * time.Millisecond}}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"duration_ms":1500`) || strings.Contains(s, `"Duration"`) || !strings.Contains(s, `"call_id":"c1"`) {
		t.Fatalf("json = %s", s)
	}
	var back Event
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Tool.Duration != 1500*time.Millisecond || back.Tool.CallID != "c1" {
		t.Fatalf("round trip = %+v", back.Tool)
	}
	lb, _ := json.Marshal(LifecycleEvent{Iteration: 1, Duration: 2 * time.Second})
	if !strings.Contains(string(lb), `"duration_ms":2000`) {
		t.Fatalf("lifecycle json = %s", lb)
	}
	eb, _ := json.Marshal(Event{Type: EventEnd, Error: errorInfo(ErrTokenBudgetExceeded), Result: &Result{}})
	if !strings.Contains(string(eb), `"code":"token_budget_exceeded"`) {
		t.Fatalf("end json = %s", eb)
	}
}

func TestDeriveStopReason(t *testing.T) {
	if deriveStopReason(0, errors.New("x")) != StopReasonError ||
		deriveStopReason(2, nil) != StopReasonToolUse ||
		deriveStopReason(0, nil) != StopReasonEndTurn {
		t.Fatal("unexpected stop reason derivation")
	}
}
