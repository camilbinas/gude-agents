package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"
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

func TestContextWith_SetsAccessorValues(t *testing.T) {
	temp := 0.7
	cfg := &InferenceConfig{Temperature: &temp}

	c := Background()
	c.WithConversationID("conv-123")
	c.WithImages([]ImageBlock{{Source: ImageSource{MIMEType: "image/png", Data: []byte{0x89}}}})
	c.WithDocuments([]DocumentBlock{{Source: DocumentSource{MIMEType: "application/pdf", Data: []byte{0x25}}}})
	c.WithInferenceConfig(cfg)
	c.WithDetailedEvents()
	c.WithInstructions("override")
	c.WithIdentity("user-42")
	c.WithScope("project", "p-123")

	if got := c.ConversationID(); got != "conv-123" {
		t.Errorf("ConversationID() = %q, want conv-123", got)
	}
	if imgs := c.Images(); len(imgs) != 1 || imgs[0].Source.MIMEType != "image/png" {
		t.Errorf("Images() = %+v, want one image/png", imgs)
	}
	if docs := c.Documents(); len(docs) != 1 || docs[0].Source.MIMEType != "application/pdf" {
		t.Errorf("Documents() = %+v, want one application/pdf", docs)
	}
	if got := c.InferenceConfig(); got == nil || got.Temperature == nil || *got.Temperature != 0.7 {
		t.Errorf("InferenceConfig() = %+v, want temperature 0.7", got)
	}
	if !c.cfg.detailedEvents {
		t.Error("detailed events not enabled")
	}
	if got := c.Instructions(); got != "override" {
		t.Errorf("Instructions() = %q, want override", got)
	}
	if got := c.Identity(); got != "user-42" {
		t.Errorf("Identity() = %q, want user-42", got)
	}
	if v, ok := c.Scope("project"); !ok || v != "p-123" {
		t.Errorf("Scope(project) = (%q, %v), want (p-123, true)", v, ok)
	}
}

func TestContextWith_FluentChaining(t *testing.T) {
	temp := 0.5
	cfg := &InferenceConfig{Temperature: &temp}

	c := Background().
		WithConversationID("conv-1").
		WithIdentity("user-1").
		WithScope("project", "p-1").
		WithInferenceConfig(cfg).
		WithDetailedEvents()

	if c.ConversationID() != "conv-1" || c.Identity() != "user-1" {
		t.Fatalf("chained context lost values: conversation=%q identity=%q", c.ConversationID(), c.Identity())
	}
	if v, ok := c.Scope("project"); !ok || v != "p-1" {
		t.Fatalf("Scope(project) = (%q, %v), want (p-1, true)", v, ok)
	}
	if got := c.InferenceConfig(); got == nil || *got.Temperature != 0.5 {
		t.Fatalf("InferenceConfig() = %+v, want temperature 0.5", got)
	}
	if !c.cfg.detailedEvents {
		t.Fatal("detailed events not enabled")
	}
}

func TestContextClone_ScalarSettingsAreIndependent(t *testing.T) {
	original := Background().
		WithConversationID("conv-original").
		WithIdentity("user-original").
		WithInstructions("original instructions").
		WithScope("project", "p-original")

	clone := original.Clone()
	if clone.ConversationID() != "conv-original" || clone.Identity() != "user-original" || clone.Instructions() != "original instructions" {
		t.Fatalf("clone did not copy settings: conversation=%q identity=%q instructions=%q",
			clone.ConversationID(), clone.Identity(), clone.Instructions())
	}

	clone.WithConversationID("conv-clone").
		WithIdentity("user-clone").
		WithInstructions("clone instructions").
		WithDetailedEvents().
		WithScope("project", "p-clone")

	if original.ConversationID() != "conv-original" || original.Identity() != "user-original" || original.Instructions() != "original instructions" {
		t.Fatalf("clone mutation leaked into original: conversation=%q identity=%q instructions=%q",
			original.ConversationID(), original.Identity(), original.Instructions())
	}
	if original.cfg.detailedEvents {
		t.Fatal("clone detailed events leaked into original")
	}
	if v, _ := original.Scope("project"); v != "p-original" {
		t.Fatalf("original scope = %q, want p-original", v)
	}

	original.WithConversationID("conv-changed")
	if clone.ConversationID() != "conv-clone" {
		t.Fatalf("original mutation leaked into clone: %q", clone.ConversationID())
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

// structKey is a composite key type used to test struct keys in the KV store.
type structKey struct {
	Namespace string
	ID        int
}

// genKey generates a random key: string, int, or structKey.
func genKey(t *rapid.T, label string) any {
	kind := rapid.IntRange(0, 2).Draw(t, label+"_kind")
	switch kind {
	case 0:
		return rapid.String().Draw(t, label+"_str")
	case 1:
		return rapid.Int().Draw(t, label+"_int")
	default:
		return structKey{
			Namespace: rapid.StringMatching(`[a-z]{1,10}`).Draw(t, label+"_ns"),
			ID:        rapid.IntRange(0, 1000).Draw(t, label+"_id"),
		}
	}
}

// genValue generates a random value: string, int, bool, or nil.
func genValue(t *rapid.T, label string) any {
	kind := rapid.IntRange(0, 3).Draw(t, label+"_kind")
	switch kind {
	case 0:
		return rapid.String().Draw(t, label+"_str")
	case 1:
		return rapid.Int().Draw(t, label+"_int")
	case 2:
		return rapid.Bool().Draw(t, label+"_bool")
	default:
		return nil
	}
}

// TestProperty_KeyValueStoreRoundTrip verifies that for any key and value,
// Set then Get returns the same value with ok=true, and unset keys return (nil, false).
//
// **Validates: Requirements 1.2, 1.3**
func TestProperty_KeyValueStoreRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := Background()

		// Generate a random number of key-value pairs to set (1–20)
		n := rapid.IntRange(1, 20).Draw(rt, "numPairs")

		type kv struct {
			key   any
			value any
		}
		pairs := make([]kv, n)

		for i := 0; i < n; i++ {
			k := genKey(rt, "key")
			v := genValue(rt, "val")
			pairs[i] = kv{key: k, value: v}
			c.Set(k, v)
		}

		// Verify all set keys return the correct value.
		// Note: if duplicate keys were generated, the last write wins.
		// Build a map of final expected values.
		expected := make(map[any]any)
		for _, p := range pairs {
			expected[p.key] = p.value
		}

		for k, expectedVal := range expected {
			got, ok := c.Get(k)
			if !ok {
				rt.Fatalf("Get(%v) returned ok=false, expected ok=true", k)
			}
			if got != expectedVal {
				rt.Fatalf("Get(%v) = %v, expected %v", k, got, expectedVal)
			}
		}

		// Verify an unset key returns (nil, false)
		unsetKey := genKey(rt, "unset")
		// Make sure the unset key is actually not in our expected map
		if _, exists := expected[unsetKey]; !exists {
			got, ok := c.Get(unsetKey)
			if ok {
				rt.Fatalf("Get(unset key %v) returned ok=true, expected ok=false", unsetKey)
			}
			if got != nil {
				rt.Fatalf("Get(unset key %v) = %v, expected nil", unsetKey, got)
			}
		}
	})
}

// TestProperty_KeyValueStoreUnsetKey verifies that Get on a fresh context
// always returns (nil, false) for any key.
//
// **Validates: Requirements 1.2, 1.3**
func TestProperty_KeyValueStoreUnsetKey(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := Background()

		k := genKey(rt, "key")
		got, ok := c.Get(k)
		if ok {
			rt.Fatalf("Get(%v) on fresh context returned ok=true, expected ok=false", k)
		}
		if got != nil {
			rt.Fatalf("Get(%v) on fresh context = %v, expected nil", k, got)
		}
	})
}

// TestProperty_KeyValueStoreOverwrite verifies that setting the same key twice
// results in Get returning the last written value.
//
// **Validates: Requirements 1.2, 1.3**
func TestProperty_KeyValueStoreOverwrite(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := Background()

		k := genKey(rt, "key")
		v1 := genValue(rt, "val1")
		v2 := genValue(rt, "val2")

		c.Set(k, v1)
		c.Set(k, v2)

		got, ok := c.Get(k)
		if !ok {
			rt.Fatalf("Get(%v) returned ok=false after overwrite", k)
		}
		if got != v2 {
			rt.Fatalf("Get(%v) = %v after overwrite, expected %v", k, got, v2)
		}
	})
}

// TestProperty_WithAccessorRoundTrip verifies that for any *Context, calling
// any With* method makes the corresponding accessor return the value that was set.
//
// **Validates: Requirements 1.5, 1.6, 1.7, 1.8, 1.9, 2.3**
func TestProperty_WithAccessorRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := Background()

		// Pick a random subset of With* methods to call (1–6)
		numCalls := rapid.IntRange(1, 6).Draw(rt, "numCalls")

		for i := 0; i < numCalls; i++ {
			method := rapid.IntRange(0, 5).Draw(rt, "method")
			switch method {
			case 0: // WithConversationID
				id := rapid.String().Draw(rt, "conversationID")
				c.WithConversationID(id)
				if got := c.ConversationID(); got != id {
					rt.Fatalf("ConversationID() = %q, want %q", got, id)
				}

			case 1: // WithImages
				n := rapid.IntRange(0, 5).Draw(rt, "numImages")
				imgs := make([]ImageBlock, n)
				for j := range imgs {
					imgs[j] = ImageBlock{
						Source: ImageSource{
							Data:     rapid.SliceOfN(rapid.Byte(), 1, 100).Draw(rt, "imgData"),
							MIMEType: rapid.SampledFrom([]string{"image/png", "image/jpeg", "image/gif", "image/webp"}).Draw(rt, "imgMIME"),
						},
					}
				}
				c.WithImages(imgs)
				got := c.Images()
				if len(got) != len(imgs) {
					rt.Fatalf("Images() length = %d, want %d", len(got), len(imgs))
				}
				for j := range imgs {
					if got[j].Source.MIMEType != imgs[j].Source.MIMEType {
						rt.Fatalf("Images()[%d].Source.MIMEType = %q, want %q", j, got[j].Source.MIMEType, imgs[j].Source.MIMEType)
					}
				}

			case 2: // WithDocuments
				n := rapid.IntRange(0, 5).Draw(rt, "numDocs")
				docs := make([]DocumentBlock, n)
				for j := range docs {
					docs[j] = DocumentBlock{
						Source: DocumentSource{
							Data:     rapid.SliceOfN(rapid.Byte(), 1, 100).Draw(rt, "docData"),
							MIMEType: rapid.SampledFrom([]string{"application/pdf", "text/plain", "text/html", "text/csv", "text/markdown"}).Draw(rt, "docMIME"),
						},
					}
				}
				c.WithDocuments(docs)
				got := c.Documents()
				if len(got) != len(docs) {
					rt.Fatalf("Documents() length = %d, want %d", len(got), len(docs))
				}
				for j := range docs {
					if got[j].Source.MIMEType != docs[j].Source.MIMEType {
						rt.Fatalf("Documents()[%d].Source.MIMEType = %q, want %q", j, got[j].Source.MIMEType, docs[j].Source.MIMEType)
					}
				}

			case 3: // WithInferenceConfig
				temp := rapid.Float64Range(0.0, 2.0).Draw(rt, "temperature")
				cfg := &InferenceConfig{Temperature: &temp}
				c.WithInferenceConfig(cfg)
				got := c.InferenceConfig()
				if got.Temperature == nil || *got.Temperature != temp {
					rt.Fatalf("InferenceConfig().Temperature = %v, want %v", got.Temperature, temp)
				}

			case 4: // WithInstructions
				instr := rapid.String().Draw(rt, "instructions")
				c.WithInstructions(instr)
				if got := c.Instructions(); got != instr {
					rt.Fatalf("Instructions() = %q, want %q", got, instr)
				}
			case 5: // WithIdentity
				id := rapid.String().Draw(rt, "identifier")
				c.WithIdentity(id)
				if got := c.Identity(); got != id {
					rt.Fatalf("Identity() = %q, want %q", got, id)
				}
			}
		}
	})
}

// TestProperty_ConcurrentKeyValueSafety verifies that concurrent Set/Get operations
// across N goroutines produce no data races (verified with -race) and that all final
// values are consistent with the last write for each key.
//
// **Validates: Requirements 12.1, 12.2**
func TestProperty_ConcurrentKeyValueSafety(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := Background()

		// Generate number of goroutines (2–10)
		numGoroutines := rapid.IntRange(2, 10).Draw(rt, "numGoroutines")

		// Generate number of operations per goroutine (1–20)
		opsPerGoroutine := rapid.IntRange(1, 20).Draw(rt, "opsPerGoroutine")

		// Use a small key space to ensure contention on the same keys
		numKeys := rapid.IntRange(1, 5).Draw(rt, "numKeys")
		keys := make([]string, numKeys)
		for i := range keys {
			keys[i] = rapid.StringMatching(`[a-z]{1,5}`).Draw(rt, "key")
		}

		// Track the final expected value for each key.
		// We'll have each goroutine write its goroutine index as the value for a key.
		// After all goroutines complete, each key must hold a value that was written by
		// one of the goroutines (i.e., a valid goroutine index).
		type op struct {
			isSet bool
			key   string
			value int // goroutine index used as value for Set ops
		}

		// Pre-generate operations for each goroutine
		allOps := make([][]op, numGoroutines)
		for g := range allOps {
			ops := make([]op, opsPerGoroutine)
			for i := range ops {
				isSet := rapid.Bool().Draw(rt, "isSet")
				keyIdx := rapid.IntRange(0, numKeys-1).Draw(rt, "keyIdx")
				ops[i] = op{
					isSet: isSet,
					key:   keys[keyIdx],
					value: g, // use goroutine index as the written value
				}
			}
			allOps[g] = ops
		}

		// Execute all operations concurrently
		var wg sync.WaitGroup
		wg.Add(numGoroutines)
		for g := range allOps {
			go func(goroutineOps []op) {
				defer wg.Done()
				for _, o := range goroutineOps {
					if o.isSet {
						c.Set(o.key, o.value)
					} else {
						// Get should never panic; value may or may not be present
						c.Get(o.key)
					}
				}
			}(allOps[g])
		}
		wg.Wait()

		// After all goroutines complete, determine the set of keys that were written
		// and verify each one holds a valid value (one of the goroutine indices that wrote to it).
		writtenKeys := make(map[string]map[int]bool) // key -> set of goroutine indices that wrote to it
		for g, ops := range allOps {
			for _, o := range ops {
				if o.isSet {
					if writtenKeys[o.key] == nil {
						writtenKeys[o.key] = make(map[int]bool)
					}
					writtenKeys[o.key][g] = true
				}
			}
		}

		for key, validWriters := range writtenKeys {
			got, ok := c.Get(key)
			if !ok {
				rt.Fatalf("Get(%q) returned ok=false after concurrent writes", key)
			}
			gotInt, isInt := got.(int)
			if !isInt {
				rt.Fatalf("Get(%q) returned non-int value %v (%T)", key, got, got)
			}
			if !validWriters[gotInt] {
				rt.Fatalf("Get(%q) = %d, which is not a valid writer goroutine index (valid: %v)", key, gotInt, validWriters)
			}
		}
	})
}

// TestProperty_ParentCancellationPropagation verifies that when a parent context
// is cancelled, the *Context created via NewContext(parent) has Done() closed
// and Err() returns non-nil (context.Canceled).
//
// **Validates: Requirements 11.3**
func TestProperty_ParentCancellationPropagation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random delay before cancellation (0–500 microseconds)
		delayMicros := rapid.IntRange(0, 500).Draw(rt, "delayMicros")
		delay := time.Duration(delayMicros) * time.Microsecond

		// Create a cancellable parent context
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create the agent.Context wrapping the cancellable parent
		c := NewContext(parent)

		// Before cancellation, Err() should be nil
		if err := c.Err(); err != nil {
			rt.Fatalf("Err() before cancellation = %v, want nil", err)
		}

		// Wait the random delay, then cancel the parent
		if delay > 0 {
			time.Sleep(delay)
		}
		cancel()

		// After cancellation, Done() channel should be closed
		select {
		case <-c.Done():
			// expected: channel is closed
		case <-time.After(time.Second):
			rt.Fatalf("Done() channel not closed within 1s after parent cancellation")
		}

		// After cancellation, Err() should return non-nil (context.Canceled)
		if err := c.Err(); err == nil {
			rt.Fatalf("Err() after parent cancellation = nil, want non-nil")
		} else if err != context.Canceled {
			rt.Fatalf("Err() after parent cancellation = %v, want %v", err, context.Canceled)
		}
	})
}
