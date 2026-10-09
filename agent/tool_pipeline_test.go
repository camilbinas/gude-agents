package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// recordingProvider records ModelRequest for each call and returns scripted responses.
type recordingProvider struct {
	mu           sync.Mutex
	calls        []ModelRequest
	responses    []*ModelResponse
	responseFunc func(ModelRequest) *ModelResponse
	callIndex    int
}

func (rp *recordingProvider) Name() string { return "mock" }

func (rp *recordingProvider) Stream(_ context.Context, params ModelRequest, cb func(ModelEvent)) (*ModelResponse, error) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.calls = append(rp.calls, params)
	var resp *ModelResponse
	if rp.responseFunc != nil {
		resp = rp.responseFunc(params)
	} else if rp.callIndex < len(rp.responses) {
		resp = rp.responses[rp.callIndex]
	} else {
		resp = &ModelResponse{Text: "no more responses"}
	}
	rp.callIndex++
	if len(resp.ToolCalls) == 0 && resp.Text != "" && cb != nil {
		words := strings.Fields(resp.Text)
		for i, w := range words {
			if i > 0 {
				cb(ModelEvent{Type: ModelEventText, Text: " "})
			}
			cb(ModelEvent{Type: ModelEventText, Text: w})
		}
	}
	return resp, nil
}

func TestWithToolFilter_FiltersToolSpecs(t *testing.T) {
	p := &recordingProvider{responses: []*ModelResponse{
		{Text: "done"},
	}}

	adminTool := tool.Tool{
		Spec: tool.Spec{
			Name:        "admin_delete",
			Description: "Delete a user account",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "deleted", nil
		},
	}
	publicTool := tool.Tool{
		Spec: tool.Spec{
			Name:        "get_info",
			Description: "Get public info",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "info", nil
		},
	}

	// Filter out admin tools.
	a, err := New(p, "test", WithTools(adminTool, publicTool),
		WithToolFilter(func(_ *Context, t tool.Tool) bool {
			return t.Spec.Name != "admin_delete"
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}

	// Verify only get_info was sent to the provider.
	if len(p.calls) != 1 {
		t.Fatalf("expected 1 provider call, got %d", len(p.calls))
	}
	specs := p.calls[0].Tools
	if len(specs) != 1 {
		t.Fatalf("expected 1 tool spec sent to provider, got %d", len(specs))
	}
	if specs[0].Name != "get_info" {
		t.Errorf("expected tool 'get_info', got %q", specs[0].Name)
	}
}

func TestWithToolFilter_DynamicViaContext(t *testing.T) {
	// Simulate: first call triggers a tool that sets a flag on the Context,
	// second loop iteration should see the new tool.
	callCount := 0
	p := &recordingProvider{responseFunc: func(params ModelRequest) *ModelResponse {
		callCount++
		if callCount == 1 {
			// First call: LLM calls unlock_tool.
			return &ModelResponse{
				ToolCalls: []tool.Call{{ToolUseID: "1", Name: "unlock", Input: json.RawMessage(`{}`)}},
			}
		}
		// Second call: should now see the "secret" tool.
		for _, s := range params.Tools {
			if s.Name == "secret" {
				return &ModelResponse{Text: "secret tool available"}
			}
		}
		return &ModelResponse{Text: "secret tool NOT available"}
	}}

	unlockTool := tool.Tool{
		Spec: tool.Spec{
			Name:        "unlock",
			Description: "Unlock the secret tool",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			// Type-assert to *Context to access the key-value store.
			c := ctx.(*Context)
			c.Set("unlocked", true)
			return "unlocked", nil
		},
	}
	secretTool := tool.Tool{
		Spec: tool.Spec{
			Name:        "secret",
			Description: "A secret tool",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "secret result", nil
		},
	}

	a, err := New(p, "test", WithTools(unlockTool, secretTool),
		WithToolFilter(func(c *Context, t tool.Tool) bool {
			if t.Spec.Name == "secret" {
				v, ok := c.Get("unlocked")
				return ok && v.(bool)
			}
			return true
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "unlock and use secret")
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "secret tool available" {
		t.Errorf("expected 'secret tool available', got %q", result.Text)
	}
}

func TestWithToolFilter_Nil_AllToolsAvailable(t *testing.T) {
	p := &recordingProvider{responses: []*ModelResponse{
		{Text: "done"},
	}}

	t1 := tool.Tool{
		Spec:    tool.Spec{Name: "a", Description: "a", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil },
	}
	t2 := tool.Tool{
		Spec:    tool.Spec{Name: "b", Description: "b", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil },
	}

	a, err := New(p, "test", WithTools(t1, t2))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}

	specs := p.calls[0].Tools
	if len(specs) != 2 {
		t.Fatalf("expected 2 tool specs, got %d", len(specs))
	}
}

func TestWithToolFilter_FilteredToolReturnsError(t *testing.T) {
	// If LLM somehow calls a filtered tool, it should get "unknown tool" error.
	p := &recordingProvider{responses: []*ModelResponse{
		{ToolCalls: []tool.Call{{ToolUseID: "1", Name: "blocked", Input: json.RawMessage(`{}`)}}},
		{Text: "ok"},
	}}

	blockedTool := tool.Tool{
		Spec:    tool.Spec{Name: "blocked", Description: "blocked", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) { return "should not run", nil },
	}

	a, err := New(p, "test", WithTools(blockedTool),
		WithToolFilter(func(_ *Context, _ tool.Tool) bool { return false }),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	// The agent should have recovered — LLM got an error for the blocked tool and responded with text.
	if result.Text != "ok" {
		t.Errorf("expected 'ok', got %q", result.Text)
	}
}
func TestWithToolFilter_MultipleFilters_ANDSemantics(t *testing.T) {
	p := &recordingProvider{responses: []*ModelResponse{
		{Text: "done"},
	}}

	t1 := tool.Tool{
		Spec:    tool.Spec{Name: "both_pass", Description: "passes both", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil },
	}
	t2 := tool.Tool{
		Spec:    tool.Spec{Name: "fails_second", Description: "passes first, fails second", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil },
	}
	t3 := tool.Tool{
		Spec:    tool.Spec{Name: "fails_first", Description: "fails first filter", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil },
	}

	// Filter 1: excludes "fails_first"
	filter1 := func(_ *Context, t tool.Tool) bool {
		return t.Spec.Name != "fails_first"
	}
	// Filter 2: excludes "fails_second"
	filter2 := func(_ *Context, t tool.Tool) bool {
		return t.Spec.Name != "fails_second"
	}

	a, err := New(p, "test", WithTools(t1, t2, t3),
		WithToolFilter(filter1, filter2),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}

	// Only "both_pass" should survive both filters.
	specs := p.calls[0].Tools
	if len(specs) != 1 {
		t.Fatalf("expected 1 tool spec (AND semantics), got %d: %v", len(specs), specs)
	}
	if specs[0].Name != "both_pass" {
		t.Errorf("expected 'both_pass', got %q", specs[0].Name)
	}
}

func TestWithToolFilter_AccumulatesAcrossMultipleCalls(t *testing.T) {
	p := &recordingProvider{responses: []*ModelResponse{
		{Text: "done"},
	}}

	t1 := tool.Tool{
		Spec:    tool.Spec{Name: "a", Description: "a", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil },
	}
	t2 := tool.Tool{
		Spec:    tool.Spec{Name: "b", Description: "b", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) { return "", nil },
	}

	// Two separate WithToolFilter calls should accumulate.
	a, err := New(p, "test", WithTools(t1, t2),
		WithToolFilter(func(_ *Context, t tool.Tool) bool {
			return t.Spec.Name != "a"
		}),
		WithToolFilter(func(_ *Context, t tool.Tool) bool {
			return t.Spec.Name != "b"
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}

	// Both filters exclude their respective tool — nothing should remain.
	specs := p.calls[0].Tools
	if len(specs) != 0 {
		t.Fatalf("expected 0 tool specs (both filtered out), got %d", len(specs))
	}
}

// TestNewSimple_RunsThroughCanonicalPipeline verifies NewSimple tools use the
// normal pipeline: role policy, approval interrupts, and handler execution.
func TestNewSimple_RunsThroughCanonicalPipeline(t *testing.T) {
	newAgent := func(t *testing.T, calls *atomic.Int32, opts ...tool.Option) *Agent {
		t.Helper()
		health := tool.NewSimple("health", "Check service health", func(context.Context) (string, error) {
			calls.Add(1)
			return "healthy", nil
		}, opts...)
		p := newScriptedProvider(
			&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "tc-1", Name: "health", Input: json.RawMessage(`{}`)}}},
			&ModelResponse{Text: "done"},
		)
		a, err := New(p, "sys", WithTools(health), WithRoleEnforcement())
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	t.Run("executes", func(t *testing.T) {
		var calls atomic.Int32
		res, err := newAgent(t, &calls).Invoke(Background(), "check")
		if err != nil || res.Text != "done" || calls.Load() != 1 {
			t.Fatalf("text=%q err=%v calls=%d", res.Text, err, calls.Load())
		}
	})

	t.Run("approval interrupts before handler", func(t *testing.T) {
		var calls atomic.Int32
		res, err := newAgent(t, &calls, tool.RequiresApproval()).Invoke(Background(), "check")
		if err != nil {
			t.Fatal(err)
		}
		if res.StopReason != StopInterrupt || res.Interrupt == nil || res.Interrupt.Type != InterruptApproval {
			t.Fatalf("expected approval interrupt, got %q", res.StopReason)
		}
		if calls.Load() != 0 {
			t.Fatal("handler ran before approval")
		}
	})

	t.Run("role policy denies", func(t *testing.T) {
		var calls atomic.Int32
		ctx := Background().WithPrincipal(Principal{ID: "u", Roles: []string{"guest"}})
		if _, err := newAgent(t, &calls, tool.AllowRoles("admin")).Invoke(ctx, "check"); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 0 {
			t.Fatal("handler ran for a denied role")
		}
	})
}

type recordingTransparentContextManager struct {
	recent []Message
}

func (m *recordingTransparentContextManager) HistoryBoundary(context.Context, string) (uint64, error) {
	return 0, nil
}

func (m *recordingTransparentContextManager) Prepare(_ context.Context, in ContextManagerInput) (ContextManagerOutput, error) {
	m.recent = append([]Message(nil), in.Recent...)
	messages := append([]Message(nil), in.Recent...)
	messages = append(messages, in.Current...)
	return ContextManagerOutput{Messages: messages}, nil
}

func toolResultIDs(messages []Message) ([]string, int) {
	var ids []string
	resultMessages := 0
	for _, message := range messages {
		containsResult := false
		for _, block := range message.Content {
			if result, ok := block.(ToolResultBlock); ok {
				ids = append(ids, result.ToolUseID)
				containsResult = true
			}
		}
		if containsResult {
			resultMessages++
		}
	}
	return ids, resultMessages
}

func assertToolResultIDs(t *testing.T, messages []Message, want ...string) {
	t.Helper()
	got, resultMessages := toolResultIDs(messages)
	if resultMessages != 1 {
		t.Fatalf("ToolResult message count = %d, want 1; messages=%#v", resultMessages, messages)
	}
	if len(got) != len(want) {
		t.Fatalf("ToolResult IDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ToolResult IDs = %v, want %v", got, want)
		}
	}
}

func TestProviderProjectionOrdersOutOfOrderCanonicalToolResults(t *testing.T) {
	for _, withContextManager := range []bool{false, true} {
		t.Run(map[bool]string{false: "without_context_manager", true: "transparent_context_manager"}[withContextManager], func(t *testing.T) {
			conversations := newTestMemoryStore()
			_, err := conversations.Append(context.Background(), "conversation", []Message{
				{Role: RoleAssistant, Content: []ContentBlock{
					ToolUseBlock{ToolUseID: "A", Name: "alpha", Input: json.RawMessage(`{}`)},
					ToolUseBlock{ToolUseID: "B", Name: "beta", Input: json.RawMessage(`{}`)},
				}},
				{Role: RoleUser, Content: []ContentBlock{ToolResultBlock{ToolUseID: "B", Content: "B completed first"}}},
				{Role: RoleUser, Content: []ContentBlock{ToolResultBlock{ToolUseID: "A", Content: "A completed second"}}},
			}, 0)
			if err != nil {
				t.Fatal(err)
			}

			provider := newCapturingProvider(&ModelResponse{Text: "done"})
			options := []Option{
				WithTools(dummyTool("alpha", "alpha"), dummyTool("beta", "beta")),
				WithConversationStore(conversations),
			}
			var manager *recordingTransparentContextManager
			if withContextManager {
				manager = &recordingTransparentContextManager{}
				options = append(options, WithContextManager(manager))
			}
			a, err := New(provider, "sys", options...)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.Invoke(Background().WithConversationID("conversation"), "continue"); err != nil {
				t.Fatal(err)
			}

			if len(provider.captured) != 1 {
				t.Fatalf("provider calls = %d, want 1", len(provider.captured))
			}
			assertToolResultIDs(t, provider.captured[0].Messages, "A", "B")
			if manager != nil {
				assertToolResultIDs(t, manager.recent, "A", "B")
			}

			snapshot, err := conversations.Load(context.Background(), "conversation")
			if err != nil {
				t.Fatal(err)
			}
			canonicalIDs, _ := toolResultIDs(snapshot.Messages[:3])
			if len(canonicalIDs) != 2 || canonicalIDs[0] != "B" || canonicalIDs[1] != "A" {
				t.Fatalf("canonical ToolResult IDs = %v, want [B A]", canonicalIDs)
			}
		})
	}
}

func TestReconcileRetryRecoversOnlyUnknownSiblingWithOriginalKey(t *testing.T) {
	conversations, executions := newTestMemoryStore(), newTestExecutionStore()
	var aCalls, bCalls int
	var bKeys []string
	alpha := tool.NewRaw("alpha", "alpha", nil, func(context.Context, json.RawMessage) (string, error) {
		aCalls++
		return "A completed", nil
	})
	beta := tool.NewRaw("beta", "beta", nil, func(ctx context.Context, _ json.RawMessage) (string, error) {
		key, _ := tool.IdempotencyKey(ctx)
		bKeys = append(bKeys, key)
		bCalls++
		if bCalls == 1 {
			return "", tool.OutcomeUnknown(errors.New("lost beta response"))
		}
		return "B recovered", nil
	})
	provider := newScriptedProvider(&ModelResponse{ToolCalls: []tool.Call{
		{ToolUseID: "A", Name: "alpha", Input: json.RawMessage(`{}`)},
		{ToolUseID: "B", Name: "beta", Input: json.RawMessage(`{}`)},
	}})
	a, err := New(provider, "sys", WithTools(alpha, beta), WithConversationStore(conversations), WithExecutionStore(executions))
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.Invoke(Background().WithConversationID("conversation"), "run both")
	if !errors.Is(err, ErrToolExecutionUncertain) {
		t.Fatalf("Invoke error = %v, want uncertainty", err)
	}
	execution, err := executions.Load(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.ToolBatch == nil || execution.ToolBatch.Calls[0].Status != ToolExecutionCompleted || execution.ToolBatch.Calls[1].Status != ToolExecutionInFlight {
		t.Fatalf("initial execution = %+v", execution)
	}
	originalKey := execution.ToolBatch.Calls[1].IdempotencyKey

	retried, err := a.ReconcileToolExecution(Background(), execution.ID, execution.Version, "B", ToolResolution{Outcome: ToolResolutionRetry})
	if err != nil {
		t.Fatal(err)
	}
	if retried.ToolBatch == nil || retried.ToolBatch.Calls[1].Status != ToolExecutionReady || retried.ToolBatch.Calls[1].IdempotencyKey != originalKey {
		t.Fatalf("retry execution = %+v, original B key=%q", retried, originalKey)
	}
	if _, err := a.RecoverExecution(Background(), execution.ID); err != nil {
		t.Fatal(err)
	}
	if aCalls != 1 || bCalls != 2 || len(bKeys) != 2 || bKeys[0] != originalKey || bKeys[1] != originalKey {
		t.Fatalf("alpha calls=%d beta calls=%d beta keys=%v, want alpha once and beta twice with %q", aCalls, bCalls, bKeys, originalKey)
	}
	if provider.callIndex != 1 {
		t.Fatalf("RecoverExecution made %d model calls, want 0", provider.callIndex-1)
	}

	snapshot, err := conversations.Load(context.Background(), "conversation")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 4 {
		t.Fatalf("canonical messages = %#v", snapshot.Messages)
	}
	uses := snapshot.Messages[1].Content
	if len(uses) != 2 || uses[0].(ToolUseBlock).ToolUseID != "A" || uses[1].(ToolUseBlock).ToolUseID != "B" {
		t.Fatalf("canonical ToolUse IDs = %#v", uses)
	}
	canonicalIDs, resultMessages := toolResultIDs(snapshot.Messages)
	if resultMessages != 2 || len(canonicalIDs) != 2 || canonicalIDs[0] != "A" || canonicalIDs[1] != "B" {
		t.Fatalf("canonical ToolResult IDs = %v in %#v, want [A B]", canonicalIDs, snapshot.Messages)
	}
}

func TestMiddleware_StandardContextAndToolCall(t *testing.T) {
	type key string
	ctx := context.WithValue(context.Background(), key("trace"), "abc")
	var got ToolCall
	mw := func(next ToolHandlerFunc) ToolHandlerFunc {
		return func(ctx context.Context, call ToolCall) (ToolResult, error) {
			if ctx.Value(key("trace")) != "abc" {
				t.Fatal("standard context value missing")
			}
			got = call
			return next(ctx, call)
		}
	}
	base := ToolHandlerFunc(func(_ context.Context, call ToolCall) (ToolResult, error) {
		return ToolResult{Text: call.Name}, nil
	})
	result, err := ChainMiddleware(base, mw)(ctx, ToolCall{ID: "call-42", Name: "echo", Input: json.RawMessage(`{"x":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "echo" || got.ID != "call-42" || got.Name != "echo" || string(got.Input) != `{"x":1}` {
		t.Fatalf("result=%#v call=%#v", result, got)
	}
}

func TestMiddleware_ChainExecutionOrder(t *testing.T) {
	var order []string
	wrap := func(name string) Middleware {
		return func(next ToolHandlerFunc) ToolHandlerFunc {
			return func(ctx context.Context, call ToolCall) (ToolResult, error) {
				order = append(order, "before-"+name)
				out, err := next(ctx, call)
				order = append(order, "after-"+name)
				return out, err
			}
		}
	}
	base := func(context.Context, ToolCall) (ToolResult, error) {
		order = append(order, "handler")
		return ToolResult{Text: "ok"}, nil
	}
	_, err := ChainMiddleware(base, wrap("A"), wrap("B"))(context.Background(), ToolCall{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"before-A", "before-B", "handler", "after-B", "after-A"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order=%v want=%v", order, want)
	}
}

func TestMiddleware_ShortCircuitToolResult(t *testing.T) {
	called := false
	mw := func(ToolHandlerFunc) ToolHandlerFunc {
		return func(context.Context, ToolCall) (ToolResult, error) {
			return ToolResult{Text: "blocked", IsError: true}, nil
		}
	}
	base := func(context.Context, ToolCall) (ToolResult, error) {
		called = true
		return ToolResult{}, nil
	}
	got, err := ChainMiddleware(base, mw)(context.Background(), ToolCall{})
	if err != nil || called || got.Text != "blocked" || !got.IsError {
		t.Fatalf("got=%#v called=%v err=%v", got, called, err)
	}
}

func TestMiddleware_IntegrationWithAgentIncludesProviderCallID(t *testing.T) {
	sp := newScriptedProvider(
		&ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "provider-id", Name: "greet", Input: json.RawMessage(`{}`)}}},
		&ModelResponse{Text: "done"},
	)
	greet := tool.NewRaw("greet", "says hello", nil, func(context.Context, json.RawMessage) (string, error) { return "hello", nil })
	var got ToolCall
	mw := func(next ToolHandlerFunc) ToolHandlerFunc {
		return func(ctx context.Context, call ToolCall) (ToolResult, error) {
			got = call
			if FromContext(ctx) == nil {
				t.Fatal("middleware did not receive invocation Context")
			}
			return next(ctx, call)
		}
	}
	a, err := New(sp, "sys", WithTools(greet), WithMiddleware(mw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if got.ID != "provider-id" || got.Name != "greet" {
		t.Fatalf("call=%#v", got)
	}
}
