package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestNewContext_WrapsParent_Deadline(t *testing.T) {
	deadline := time.Now().Add(5 * time.Second)
	parent, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	c := NewContext(parent)

	got, ok := c.Deadline()
	if !ok {
		t.Fatal("expected deadline to be set")
	}
	if !got.Equal(deadline) {
		t.Fatalf("expected deadline %v, got %v", deadline, got)
	}
}

func TestNewContext_WrapsParent_Values(t *testing.T) {
	type ctxKey struct{}
	parent := context.WithValue(context.Background(), ctxKey{}, "hello")

	c := NewContext(parent)

	v := c.Value(ctxKey{})
	if v != "hello" {
		t.Fatalf("expected parent value %q, got %v", "hello", v)
	}
}

func TestNewContext_WrapsParent_Cancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())

	c := NewContext(parent)

	cancel()

	select {
	case <-c.Done():
		// expected
	case <-time.After(time.Second):
		t.Fatal("expected Done channel to close after parent cancel")
	}

	if c.Err() == nil {
		t.Fatal("expected non-nil Err after parent cancel")
	}
}

func TestBackground_EquivalentToNewContextBackground(t *testing.T) {
	c := Background()

	// Should have no deadline
	_, ok := c.Deadline()
	if ok {
		t.Fatal("Background() should not have a deadline")
	}

	// Should not be done
	select {
	case <-c.Done():
		t.Fatal("Background() should not be done")
	default:
		// expected
	}

	// Should have no error
	if c.Err() != nil {
		t.Fatalf("Background() Err should be nil, got %v", c.Err())
	}

	// Should have empty KV store
	_, ok = c.Get("anything")
	if ok {
		t.Fatal("Background() should have empty KV store")
	}
}

func TestNewContext_NilPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for nil parent, got none")
		}
	}()

	NewContext(nil)
}

func TestWithConversationID_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()

	got := c.WithConversationID("conv-123")

	if got != c {
		t.Fatal("WithConversationID should return the same pointer")
	}
	if c.ConversationID() != "conv-123" {
		t.Fatalf("expected %q, got %q", "conv-123", c.ConversationID())
	}
}

func TestWithImages_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()
	imgs := []ImageBlock{
		{Source: ImageSource{MIMEType: "image/png", Data: []byte{0x89}}},
	}

	got := c.WithImages(imgs)

	if got != c {
		t.Fatal("WithImages should return the same pointer")
	}
	if len(c.Images()) != 1 {
		t.Fatalf("expected 1 image, got %d", len(c.Images()))
	}
	if c.Images()[0].Source.MIMEType != "image/png" {
		t.Fatalf("expected image/png, got %q", c.Images()[0].Source.MIMEType)
	}
}

func TestWithDocuments_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()
	docs := []DocumentBlock{
		{Source: DocumentSource{MIMEType: "application/pdf", Data: []byte{0x25}}},
	}

	got := c.WithDocuments(docs)

	if got != c {
		t.Fatal("WithDocuments should return the same pointer")
	}
	if len(c.Documents()) != 1 {
		t.Fatalf("expected 1 document, got %d", len(c.Documents()))
	}
	if c.Documents()[0].Source.MIMEType != "application/pdf" {
		t.Fatalf("expected application/pdf, got %q", c.Documents()[0].Source.MIMEType)
	}
}

func TestWithInferenceConfig_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()
	temp := 0.7
	cfg := &InferenceConfig{Temperature: &temp}

	got := c.WithInferenceConfig(cfg)

	if got != c {
		t.Fatal("WithInferenceConfig should return the same pointer")
	}
	if c.InferenceConfig() != cfg {
		t.Fatal("expected same InferenceConfig pointer")
	}
	if *c.InferenceConfig().Temperature != 0.7 {
		t.Fatalf("expected temperature 0.7, got %f", *c.InferenceConfig().Temperature)
	}
}

func TestWithEventHook_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()
	hook := BaseEventHook{}

	got := c.WithEventHook(hook)

	if got != c {
		t.Fatal("WithEventHook should return the same pointer")
	}
	if c.EventHook() == nil {
		t.Fatal("expected non-nil EventHook")
	}
}

func TestWithIdentifier_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()

	got := c.WithIdentifier("user-42")

	if got != c {
		t.Fatal("WithIdentifier should return the same pointer")
	}
	if c.Identifier() != "user-42" {
		t.Fatalf("expected %q, got %q", "user-42", c.Identifier())
	}
}

func TestSetGet_RoundTrip(t *testing.T) {
	c := Background()

	c.Set("key1", "value1")
	c.Set(42, true)

	v, ok := c.Get("key1")
	if !ok || v != "value1" {
		t.Fatalf("expected (value1, true), got (%v, %v)", v, ok)
	}

	v, ok = c.Get(42)
	if !ok || v != true {
		t.Fatalf("expected (true, true), got (%v, %v)", v, ok)
	}
}

func TestSetGet_Overwrite(t *testing.T) {
	c := Background()

	c.Set("key", "first")
	c.Set("key", "second")

	v, ok := c.Get("key")
	if !ok || v != "second" {
		t.Fatalf("expected (second, true), got (%v, %v)", v, ok)
	}
}

func TestGet_NonExistentKey(t *testing.T) {
	c := Background()

	v, ok := c.Get("missing")
	if ok || v != nil {
		t.Fatalf("expected (nil, false), got (%v, %v)", v, ok)
	}
}

func TestWithMethods_Chaining(t *testing.T) {
	c := Background()
	temp := 0.5
	cfg := &InferenceConfig{Temperature: &temp}
	hook := BaseEventHook{}

	result := c.
		WithConversationID("conv-1").
		WithIdentifier("user-1").
		WithInferenceConfig(cfg).
		WithEventHook(hook)

	if result != c {
		t.Fatal("chained With* methods should return the same pointer")
	}
	if c.ConversationID() != "conv-1" {
		t.Fatalf("expected conv-1, got %q", c.ConversationID())
	}
	if c.Identifier() != "user-1" {
		t.Fatalf("expected user-1, got %q", c.Identifier())
	}
	if c.InferenceConfig() != cfg {
		t.Fatal("expected same InferenceConfig pointer")
	}
	if c.EventHook() == nil {
		t.Fatal("expected non-nil EventHook")
	}
}

func TestContext_SatisfiesContextInterface(t *testing.T) {
	c := Background()

	// Verify *Context is assignable to context.Context
	var _ context.Context = c
}

func TestContext_UsageDefaultsToZero(t *testing.T) {
	c := Background()

	usage := c.Usage()
	if usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Fatalf("expected zero usage, got %+v", usage)
	}
}

func TestContext_SetUsage(t *testing.T) {
	c := Background()

	c.setUsage(TokenUsage{InputTokens: 100, OutputTokens: 50})

	usage := c.Usage()
	if usage.InputTokens != 100 {
		t.Fatalf("expected InputTokens=100, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 50 {
		t.Fatalf("expected OutputTokens=50, got %d", usage.OutputTokens)
	}
}

// --- Minimal hook implementations for testing ---

// stubTracingHook is a minimal TracingHook implementation for testing.
type stubTracingHook struct {
	name string
}

func (s *stubTracingHook) OnInvokeStart(ctx context.Context, params InvokeSpanParams) (context.Context, func(err error, usage TokenUsage, response string)) {
	return ctx, func(error, TokenUsage, string) {}
}
func (s *stubTracingHook) OnIterationStart(ctx context.Context, iteration int) (context.Context, func(toolCount int, isFinal bool)) {
	return ctx, func(int, bool) {}
}
func (s *stubTracingHook) OnProviderCallStart(ctx context.Context, params ProviderCallParams) (context.Context, func(err error, usage TokenUsage, toolCallCount int, responseText string)) {
	return ctx, func(error, TokenUsage, int, string) {}
}
func (s *stubTracingHook) OnToolStart(ctx context.Context, toolName string, input json.RawMessage) (context.Context, func(err error, output string)) {
	return ctx, func(error, string) {}
}
func (s *stubTracingHook) OnGuardrailStart(ctx context.Context, direction string, input string) (context.Context, func(err error, output string)) {
	return ctx, func(error, string) {}
}
func (s *stubTracingHook) OnConversationStart(ctx context.Context, operation string, conversationID string) (context.Context, func(err error)) {
	return ctx, func(error) {}
}
func (s *stubTracingHook) OnRetrieverStart(ctx context.Context, query string) (context.Context, func(err error, docCount int)) {
	return ctx, func(error, int) {}
}
func (s *stubTracingHook) OnMaxIterationsExceeded(ctx context.Context, limit int) {}

// stubMetricsHook is a minimal MetricsHook implementation for testing.
type stubMetricsHook struct {
	name string
}

func (s *stubMetricsHook) OnInvokeStart() func(err error, usage TokenUsage) {
	return func(error, TokenUsage) {}
}
func (s *stubMetricsHook) OnIterationStart()                          {}
func (s *stubMetricsHook) OnIterationEnd(toolCount int, isFinal bool) {}
func (s *stubMetricsHook) OnProviderCallStart(modelID string) func(err error, usage TokenUsage) {
	return func(error, TokenUsage) {}
}
func (s *stubMetricsHook) OnToolStart(toolName string) func(err error) {
	return func(error) {}
}
func (s *stubMetricsHook) OnGuardrailComplete(direction string, blocked bool) {}
func (s *stubMetricsHook) OnImagesAttached(imageCount int)                    {}
func (s *stubMetricsHook) OnDocumentsAttached(docCount int)                   {}

// stubLoggingHook is a minimal LoggingHook implementation for testing.
type stubLoggingHook struct {
	name string
}

func (s *stubLoggingHook) OnInvokeStart(params InvokeSpanParams)                           {}
func (s *stubLoggingHook) OnInvokeEnd(err error, usage TokenUsage, duration time.Duration) {}
func (s *stubLoggingHook) OnIterationStart(iteration int)                                  {}
func (s *stubLoggingHook) OnIterationEnd(iteration int, toolCount int, isFinal bool, duration time.Duration) {
}
func (s *stubLoggingHook) OnProviderCallStart(modelID string) {}
func (s *stubLoggingHook) OnProviderCallEnd(err error, usage TokenUsage, toolCallCount int, duration time.Duration) {
}
func (s *stubLoggingHook) OnToolStart(toolName string)                                   {}
func (s *stubLoggingHook) OnToolEnd(toolName string, err error, duration time.Duration)  {}
func (s *stubLoggingHook) OnToolLog(toolName string, msg string)                         {}
func (s *stubLoggingHook) OnGuardrailComplete(direction string, blocked bool, err error) {}
func (s *stubLoggingHook) OnConversationStart(operation string, conversationID string)   {}
func (s *stubLoggingHook) OnConversationEnd(operation string, conversationID string, err error, messageCount int, duration time.Duration) {
}
func (s *stubLoggingHook) OnRetrieverStart(query string)                                  {}
func (s *stubLoggingHook) OnRetrieverEnd(err error, docCount int, duration time.Duration) {}
func (s *stubLoggingHook) OnImagesAttached(imageCount int)                                {}
func (s *stubLoggingHook) OnDocumentsAttached(docCount int)                               {}
func (s *stubLoggingHook) OnMaxIterationsExceeded(limit int)                              {}
func (s *stubLoggingHook) OnStreamChunk(text string)                                      {}
func (s *stubLoggingHook) OnResponse(text string)                                         {}

// --- Tests for WithTracingHook/WithMetricsHook/WithLoggingHook setters ---

func TestContextWithTracingHook_SetsAndReturns(t *testing.T) {
	c := Background()
	hook := &stubTracingHook{name: "tracing-1"}

	got := c.WithTracingHook(hook)

	if got != c {
		t.Fatal("WithTracingHook should return the same pointer")
	}
	if c.TracingHook() != hook {
		t.Fatal("TracingHook() should return the hook that was set")
	}
}

func TestContextWithMetricsHook_SetsAndReturns(t *testing.T) {
	c := Background()
	hook := &stubMetricsHook{name: "metrics-1"}

	got := c.WithMetricsHook(hook)

	if got != c {
		t.Fatal("WithMetricsHook should return the same pointer")
	}
	if c.MetricsHook() != hook {
		t.Fatal("MetricsHook() should return the hook that was set")
	}
}

func TestContextWithLoggingHook_SetsAndReturns(t *testing.T) {
	c := Background()
	hook := &stubLoggingHook{name: "logging-1"}

	got := c.WithLoggingHook(hook)

	if got != c {
		t.Fatal("WithLoggingHook should return the same pointer")
	}
	if c.LoggingHook() != hook {
		t.Fatal("LoggingHook() should return the hook that was set")
	}
}

func TestContextHookSetters_DefaultNil(t *testing.T) {
	c := Background()

	if c.TracingHook() != nil {
		t.Fatal("TracingHook() should be nil on fresh context")
	}
	if c.MetricsHook() != nil {
		t.Fatal("MetricsHook() should be nil on fresh context")
	}
	if c.LoggingHook() != nil {
		t.Fatal("LoggingHook() should be nil on fresh context")
	}
}

func TestContextHookSetters_Chaining(t *testing.T) {
	c := Background()
	th := &stubTracingHook{name: "tracing"}
	mh := &stubMetricsHook{name: "metrics"}
	lh := &stubLoggingHook{name: "logging"}

	result := c.WithTracingHook(th).WithMetricsHook(mh).WithLoggingHook(lh)

	if result != c {
		t.Fatal("chained hook setters should return the same pointer")
	}
	if c.TracingHook() != th {
		t.Fatal("TracingHook() mismatch after chaining")
	}
	if c.MetricsHook() != mh {
		t.Fatal("MetricsHook() mismatch after chaining")
	}
	if c.LoggingHook() != lh {
		t.Fatal("LoggingHook() mismatch after chaining")
	}
}

func TestContextHooks_TakePrecedenceOverAgentHooks(t *testing.T) {
	agentTracingHook := &stubTracingHook{name: "agent-tracing"}
	agentMetricsHook := &stubMetricsHook{name: "agent-metrics"}
	agentLoggingHook := &stubLoggingHook{name: "agent-logging"}

	a := &Agent{
		tracingHook: agentTracingHook,
		metricsHook: agentMetricsHook,
		loggingHook: agentLoggingHook,
	}

	ctxTracingHook := &stubTracingHook{name: "ctx-tracing"}
	ctxMetricsHook := &stubMetricsHook{name: "ctx-metrics"}
	ctxLoggingHook := &stubLoggingHook{name: "ctx-logging"}

	c := Background().
		WithTracingHook(ctxTracingHook).
		WithMetricsHook(ctxMetricsHook).
		WithLoggingHook(ctxLoggingHook)

	h := a.hooks(c)

	if h.tracing != ctxTracingHook {
		t.Fatalf("expected context tracing hook, got agent-level hook")
	}
	if h.metrics != ctxMetricsHook {
		t.Fatalf("expected context metrics hook, got agent-level hook")
	}
	if h.logging != ctxLoggingHook {
		t.Fatalf("expected context logging hook, got agent-level hook")
	}
}

func TestContextHooks_NilFallsBackToAgentHooks(t *testing.T) {
	agentTracingHook := &stubTracingHook{name: "agent-tracing"}
	agentMetricsHook := &stubMetricsHook{name: "agent-metrics"}
	agentLoggingHook := &stubLoggingHook{name: "agent-logging"}

	a := &Agent{
		tracingHook: agentTracingHook,
		metricsHook: agentMetricsHook,
		loggingHook: agentLoggingHook,
	}

	c := Background()
	h := a.hooks(c)

	if h.tracing != agentTracingHook {
		t.Fatalf("expected agent tracing hook as fallback")
	}
	if h.metrics != agentMetricsHook {
		t.Fatalf("expected agent metrics hook as fallback")
	}
	if h.logging != agentLoggingHook {
		t.Fatalf("expected agent logging hook as fallback")
	}
}

func TestContextHooks_PartialOverride(t *testing.T) {
	agentTracingHook := &stubTracingHook{name: "agent-tracing"}
	agentMetricsHook := &stubMetricsHook{name: "agent-metrics"}
	agentLoggingHook := &stubLoggingHook{name: "agent-logging"}

	a := &Agent{
		tracingHook: agentTracingHook,
		metricsHook: agentMetricsHook,
		loggingHook: agentLoggingHook,
	}

	ctxTracingHook := &stubTracingHook{name: "ctx-tracing"}
	c := Background().WithTracingHook(ctxTracingHook)

	h := a.hooks(c)

	if h.tracing != ctxTracingHook {
		t.Fatalf("expected context tracing hook")
	}
	if h.metrics != agentMetricsHook {
		t.Fatalf("expected agent metrics hook as fallback")
	}
	if h.logging != agentLoggingHook {
		t.Fatalf("expected agent logging hook as fallback")
	}
}

func TestContextHooks_BothNilReturnsNil(t *testing.T) {
	a := &Agent{}
	c := Background()
	h := a.hooks(c)

	if h.tracing != nil {
		t.Fatal("expected nil tracing hook")
	}
	if h.metrics != nil {
		t.Fatal("expected nil metrics hook")
	}
	if h.logging != nil {
		t.Fatal("expected nil logging hook")
	}
}

func TestContextHooks_ClonePreservesHooks(t *testing.T) {
	th := &stubTracingHook{name: "tracing"}
	mh := &stubMetricsHook{name: "metrics"}
	lh := &stubLoggingHook{name: "logging"}

	c := Background().
		WithTracingHook(th).
		WithMetricsHook(mh).
		WithLoggingHook(lh)

	cloned := c.Clone()

	if cloned.TracingHook() != th {
		t.Fatal("Clone should preserve TracingHook")
	}
	if cloned.MetricsHook() != mh {
		t.Fatal("Clone should preserve MetricsHook")
	}
	if cloned.LoggingHook() != lh {
		t.Fatal("Clone should preserve LoggingHook")
	}
}
