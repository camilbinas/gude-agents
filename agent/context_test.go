package agent

import (
	"context"
	"sync"
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

func TestWithDetailedEvents_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()
	got := c.WithDetailedEvents()
	if got != c {
		t.Fatal("WithDetailedEvents should return the same pointer")
	}
	if !c.cfg.detailedEvents {
		t.Fatal("expected detailed events enabled")
	}
}

func TestWithInstructions_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()
	if got := c.WithInstructions("override"); got != c {
		t.Fatal("WithInstructions should return the same pointer")
	}
	if c.Instructions() != "override" {
		t.Fatalf("Instructions() = %q, want override", c.Instructions())
	}
}

func TestWithIdentity_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()

	got := c.WithIdentity("user-42")

	if got != c {
		t.Fatal("WithIdentity should return the same pointer")
	}
	if c.Identity() != "user-42" {
		t.Fatalf("expected %q, got %q", "user-42", c.Identity())
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
	result := c.
		WithConversationID("conv-1").
		WithIdentity("user-1").
		WithInferenceConfig(cfg).
		WithDetailedEvents()

	if result != c {
		t.Fatal("chained With* methods should return the same pointer")
	}
	if c.ConversationID() != "conv-1" {
		t.Fatalf("expected conv-1, got %q", c.ConversationID())
	}
	if c.Identity() != "user-1" {
		t.Fatalf("expected user-1, got %q", c.Identity())
	}
	if c.InferenceConfig() != cfg {
		t.Fatal("expected same InferenceConfig pointer")
	}
	if !c.cfg.detailedEvents {
		t.Fatal("expected detailed events enabled")
	}
}

func TestContext_SatisfiesContextInterface(t *testing.T) {
	c := Background()

	// Verify *Context is assignable to context.Context
	var _ context.Context = c
}

// ---------------------------------------------------------------------------
// Unified observer configuration
// ---------------------------------------------------------------------------

type contextTestInvokeObserver struct{ name string }

func (o *contextTestInvokeObserver) ObserveInvoke(ctx context.Context, _ InvokeRecord) context.Context {
	return context.WithValue(ctx, contextObserverKey{}, o.name)
}

type contextObserverKey struct{}

func TestContextWithObservers_ReplacesAgentObservers(t *testing.T) {
	agentObserver := &contextTestInvokeObserver{name: "agent"}
	invocationObserver := &contextTestInvokeObserver{name: "invocation"}
	a := &Agent{observers: []any{agentObserver}}
	c := Background().WithObservers(invocationObserver)

	h := a.hooks(c)
	if len(h.observers) != 1 || h.observers[0] != invocationObserver {
		t.Fatalf("invocation observers did not replace agent observers: %#v", h.observers)
	}
}

func TestContextWithObservers_EmptyDisablesAgentObservers(t *testing.T) {
	a := &Agent{observers: []any{&contextTestInvokeObserver{name: "agent"}}}
	h := a.hooks(Background().WithObservers())
	if len(h.observers) != 0 {
		t.Fatalf("expected no observers, got %d", len(h.observers))
	}
}

func TestContextClonePreservesObservers(t *testing.T) {
	observer := &contextTestInvokeObserver{name: "invocation"}
	cloned := Background().WithObservers(observer).Clone()
	if !cloned.cfg.observersSet || len(cloned.cfg.observers) != 1 || cloned.cfg.observers[0] != observer {
		t.Fatal("Clone did not preserve invocation observers")
	}
}

// ---------------------------------------------------------------------------
// Multi-scope memory (8fc5f43)
// ---------------------------------------------------------------------------

func TestWithScope_SetsAndReturnsSamePointer(t *testing.T) {
	c := Background()
	got := c.WithScope("project", "p-123")
	if got != c {
		t.Fatal("WithScope should return the same pointer")
	}
	if v, ok := c.Scope("project"); !ok || v != "p-123" {
		t.Errorf("Scope(\"project\") = (%q, %v), want (\"p-123\", true)", v, ok)
	}
}

func TestScope_MultipleScopesAreIndependent(t *testing.T) {
	c := Background().
		WithScope("project", "p-1").
		WithScope("user", "u-1").
		WithScope("team", "t-1")
	cases := map[string]string{
		"project": "p-1",
		"user":    "u-1",
		"team":    "t-1",
	}
	for key, want := range cases {
		if got, ok := c.Scope(key); !ok || got != want {
			t.Errorf("Scope(%q) = (%q, %v), want %q", key, got, ok, want)
		}
	}
	if got, ok := c.Scope("missing"); ok || got != "" {
		t.Errorf("missing scope = (%q, %v), want (\"\", false)", got, ok)
	}
}

func TestScope_OverwriteSameKey(t *testing.T) {
	c := Background().WithScope("project", "p-1")
	c.WithScope("project", "p-2")
	if v, _ := c.Scope("project"); v != "p-2" {
		t.Errorf("Scope(\"project\") = %q, want \"p-2\" after overwrite", v)
	}
}

// ScopeFrom is strict: it never falls back to the identity.
func TestScopeFrom_NeverFallsBackToIdentity(t *testing.T) {
	c := Background().WithIdentity("default-user")
	if got, ok := ScopeFrom(c, "project"); ok || got != "" {
		t.Errorf("ScopeFrom unknown key = (%q, %v), want (\"\", false)", got, ok)
	}
	if got, ok := ScopeFrom(c, ""); ok || got != "" {
		t.Errorf("ScopeFrom empty key = (%q, %v), want (\"\", false)", got, ok)
	}
	c.WithScope("project", "p-1")
	if got, ok := ScopeFrom(c, "project"); !ok || got != "p-1" {
		t.Errorf("ScopeFrom(\"project\") = (%q, %v), want (\"p-1\", true)", got, ok)
	}
}

func TestScopeFrom_NoContextReturnsEmpty(t *testing.T) {
	if got, ok := ScopeFrom(context.Background(), "project"); ok || got != "" {
		t.Errorf("ScopeFrom on plain context = (%q, %v), want empty", got, ok)
	}
}

func TestScopeFrom_DerivedStdlibContext(t *testing.T) {
	c := Background().WithScope("project", "p-9").WithIdentity("u-9")
	derived, cancel := context.WithTimeout(c, time.Minute)
	defer cancel()
	if got, ok := ScopeFrom(derived, "project"); !ok || got != "p-9" {
		t.Errorf("ScopeFrom(derived) = (%q, %v), want p-9", got, ok)
	}
	if got := IdentityFrom(derived); got != "u-9" {
		t.Errorf("IdentityFrom(derived) = %q, want u-9", got)
	}
}

func TestScope_ClonePropagatesScopes(t *testing.T) {
	c := Background().
		WithScope("project", "p-1").
		WithScope("user", "u-1")
	cloned := c.Clone()
	if v, _ := cloned.Scope("project"); v != "p-1" {
		t.Errorf("clone lost project scope: got %q", v)
	}
	if v, _ := cloned.Scope("user"); v != "u-1" {
		t.Errorf("clone lost user scope: got %q", v)
	}
	// Clone scopes should be independent — mutating clone doesn't affect original.
	cloned.WithScope("project", "p-2")
	if v, _ := c.Scope("project"); v != "p-1" {
		t.Errorf("original mutated by clone: got %q, want p-1", v)
	}
}

type observerOrderKey struct{}

type orderedInvokeObserver struct {
	name   string
	events *[]string
}

func (o *orderedInvokeObserver) ObserveInvoke(ctx context.Context, record InvokeRecord) context.Context {
	seen, _ := ctx.Value(observerOrderKey{}).(string)
	*o.events = append(*o.events, string(record.Phase)+":"+o.name+":"+seen)
	return context.WithValue(ctx, observerOrderKey{}, o.name+"-"+string(record.Phase))
}

type filteredToolObserver struct{ calls int }

func (o *filteredToolObserver) ObserveTool(ctx context.Context, _ ToolCallRecord) context.Context {
	o.calls++
	return ctx
}

func TestObserverDispatcher_CapabilityFilteringAndContextOrder(t *testing.T) {
	var events []string
	first := &orderedInvokeObserver{name: "first", events: &events}
	toolOnly := &filteredToolObserver{}
	second := &orderedInvokeObserver{name: "second", events: &events}
	h := hooks{observers: []any{first, toolOnly, second}}

	ctx, finish := h.onInvokeStart(Background(), InvokeRecord{AgentName: "test"})
	if got := ctx.Value(observerOrderKey{}); got != "second-start" {
		t.Fatalf("threaded start context = %v, want second-start", got)
	}
	finish.finish(Result{Text: "final", StopReason: StopEndTurn}, nil)

	want := []string{
		"start:first:",
		"start:second:first-start",
		"end:second:second-start",
		"end:first:second-end",
	}
	if len(events) != len(want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events[%d] = %q, want %q", i, events[i], want[i])
		}
	}
	if toolOnly.calls != 0 {
		t.Fatalf("tool-only observer received invoke records: %d", toolOnly.calls)
	}
}

type streamIndependenceObserver struct {
	mu          sync.Mutex
	invokeCount int
	modelCount  int
}

func (o *streamIndependenceObserver) ObserveInvoke(ctx context.Context, _ InvokeRecord) context.Context {
	o.mu.Lock()
	o.invokeCount++
	o.mu.Unlock()
	return ctx
}

func (o *streamIndependenceObserver) ObserveModel(ctx context.Context, _ ModelCallRecord) context.Context {
	o.mu.Lock()
	o.modelCount++
	o.mu.Unlock()
	return ctx
}

func TestObserverDispatcher_DetailedStreamEventsAreIndependent(t *testing.T) {
	run := func(detailed bool) (invokeCount, modelCount, detailedEvents int) {
		observer := &streamIndependenceObserver{}
		provider := newScriptedProvider(&ModelResponse{Text: "done"})
		a, err := New(provider, "sys", WithObserver(observer))
		if err != nil {
			t.Fatal(err)
		}
		ctx := Background()
		if detailed {
			ctx.WithDetailedEvents()
		}
		for event, err := range a.Stream(ctx, "go") {
			if err != nil {
				t.Fatal(err)
			}
			switch event.Type {
			case EventIterationStart, EventIterationEnd, EventModelStart, EventModelEnd:
				detailedEvents++
			}
		}
		observer.mu.Lock()
		defer observer.mu.Unlock()
		return observer.invokeCount, observer.modelCount, detailedEvents
	}

	plainInvoke, plainModel, plainEvents := run(false)
	detailInvoke, detailModel, detailEvents := run(true)
	if plainInvoke != 2 || plainModel != 2 || detailInvoke != plainInvoke || detailModel != plainModel {
		t.Fatalf("observer counts changed with detailed events: plain=(%d,%d), detailed=(%d,%d)", plainInvoke, plainModel, detailInvoke, detailModel)
	}
	if plainEvents != 0 || detailEvents == 0 {
		t.Fatalf("stream detail gating failed: plain=%d detailed=%d", plainEvents, detailEvents)
	}
}

func TestContextClone_DeepCopiesInvocationConfiguration(t *testing.T) {
	temperature, topP := 0.2, 0.8
	topK, maxTokens := 5, 123
	firstObserver := &contextTestInvokeObserver{name: "first"}
	secondObserver := &contextTestInvokeObserver{name: "second"}
	original := Background().
		WithScope("project", "p1").
		WithPrincipal(Principal{ID: "user", Roles: []string{"admin"}, Attrs: map[string]string{"org": "one"}, Credentials: map[string]string{"token": "secret"}}).
		WithImages([]ImageBlock{{Source: ImageSource{Data: []byte{1, 2}, MIMEType: "image/png"}}}).
		WithDocuments([]DocumentBlock{{Source: DocumentSource{Data: []byte{3, 4}, MIMEType: "application/pdf"}}}).
		WithInferenceConfig(&InferenceConfig{Temperature: &temperature, TopP: &topP, TopK: &topK, MaxTokens: &maxTokens, StopSequences: []string{"STOP"}}).
		WithObservers(firstObserver, secondObserver)
	original.Set("parent", "value")
	original = original.forInvocation(original, &invocationRuntime{}).forToolCall(&toolCallRuntime{id: "call"})

	clone := original.Clone()
	if clone.Context != original.Context || clone.rt != nil || clone.call != nil || clone.kv == original.kv {
		t.Fatalf("clone runtime ownership is incorrect: %#v", clone)
	}
	if _, ok := clone.Get("parent"); ok {
		t.Fatal("clone inherited parent KV")
	}
	clone.Set("child", "value")
	if _, ok := original.Get("child"); ok {
		t.Fatal("original received clone KV")
	}

	clone.cfg.scopes["project"] = "p2"
	clone.cfg.principal.Roles[0] = "viewer"
	clone.cfg.principal.Attrs["org"] = "two"
	clone.cfg.principal.Credentials["token"] = "changed"
	clone.cfg.images[0].Source.Data[0] = 9
	clone.cfg.documents[0].Source.Data[0] = 8
	*clone.cfg.inferenceConfig.Temperature = 0.9
	*clone.cfg.inferenceConfig.TopP = 0.1
	*clone.cfg.inferenceConfig.TopK = 99
	*clone.cfg.inferenceConfig.MaxTokens = 456
	clone.cfg.inferenceConfig.StopSequences[0] = "CHANGED"
	clone.cfg.observers[0] = secondObserver

	if got, _ := original.Scope("project"); got != "p1" {
		t.Fatalf("original scope = %q", got)
	}
	principal, _ := original.Principal()
	if principal.Roles[0] != "admin" || principal.Attrs["org"] != "one" || principal.Credentials["token"] != "secret" {
		t.Fatalf("original principal mutated: %#v", principal)
	}
	if original.Images()[0].Source.Data[0] != 1 || original.Documents()[0].Source.Data[0] != 3 {
		t.Fatalf("original attachments mutated: images=%v docs=%v", original.Images(), original.Documents())
	}
	inference := original.InferenceConfig()
	if *inference.Temperature != 0.2 || *inference.TopP != 0.8 || *inference.TopK != 5 || *inference.MaxTokens != 123 || inference.StopSequences[0] != "STOP" {
		t.Fatalf("original inference mutated: %#v", inference)
	}
	if original.cfg.observers[0] != firstObserver {
		t.Fatal("original observer slice mutated")
	}

	original.cfg.scopes["project"] = "p3"
	original.cfg.images[0].Source.Data[1] = 7
	*original.cfg.inferenceConfig.MaxTokens = 777
	if got, _ := clone.Scope("project"); got != "p2" || clone.Images()[0].Source.Data[1] != 2 || *clone.InferenceConfig().MaxTokens != 456 {
		t.Fatalf("clone changed after original mutation: scope=%q image=%v inference=%d", got, clone.Images()[0].Source.Data, *clone.InferenceConfig().MaxTokens)
	}
}

func TestContextCloneStartsFreshExecutionIdentity(t *testing.T) {
	original := Background().WithConversationID("parent-conversation").WithExecutionID("parent-execution")
	original.cfg.executionResume = true
	original.cfg.executionVersion = 7

	clone := original.Clone()
	if clone.ExecutionID() != "" || clone.cfg.executionIDSet || clone.cfg.executionResume || clone.cfg.executionVersion != 0 {
		t.Fatalf("clone retained execution lifecycle state: %+v", clone.cfg)
	}
	if original.ExecutionID() != "parent-execution" || !original.cfg.executionResume || original.cfg.executionVersion != 7 {
		t.Fatalf("clone mutated parent execution state: %+v", original.cfg)
	}
	if clone.ConversationID() != "parent-conversation" {
		t.Fatalf("clone lost invocation configuration")
	}
	clone.WithExecutionID("child-execution")
	if clone.ExecutionID() != "child-execution" {
		t.Fatalf("clone explicit execution ID = %q", clone.ExecutionID())
	}
}
