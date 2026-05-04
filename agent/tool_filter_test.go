package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// recordingProvider records ConverseParams for each call and returns scripted responses.
type recordingProvider struct {
	mu           sync.Mutex
	calls        []ConverseParams
	responses    []*ProviderResponse
	responseFunc func(ConverseParams) *ProviderResponse
	callIndex    int
}

func (rp *recordingProvider) Name() string { return "mock" }

func (rp *recordingProvider) Converse(_ context.Context, params ConverseParams) (*ProviderResponse, error) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.calls = append(rp.calls, params)
	if rp.responseFunc != nil {
		resp := rp.responseFunc(params)
		rp.callIndex++
		return resp, nil
	}
	if rp.callIndex >= len(rp.responses) {
		return &ProviderResponse{Text: "no more responses"}, nil
	}
	resp := rp.responses[rp.callIndex]
	rp.callIndex++
	return resp, nil
}

func (rp *recordingProvider) ConverseStream(_ context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.calls = append(rp.calls, params)
	var resp *ProviderResponse
	if rp.responseFunc != nil {
		resp = rp.responseFunc(params)
	} else if rp.callIndex < len(rp.responses) {
		resp = rp.responses[rp.callIndex]
	} else {
		resp = &ProviderResponse{Text: "no more responses"}
	}
	rp.callIndex++
	if len(resp.ToolCalls) == 0 && resp.Text != "" && cb != nil {
		words := strings.Fields(resp.Text)
		for i, w := range words {
			if i > 0 {
				cb(" ")
			}
			cb(w)
		}
	}
	return resp, nil
}

func TestWithToolFilter_FiltersToolSpecs(t *testing.T) {
	p := &recordingProvider{responses: []*ProviderResponse{
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
	a, err := New(p, prompt.Text("test"), []tool.Tool{adminTool, publicTool},
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
	specs := p.calls[0].ToolConfig
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
	p := &recordingProvider{responseFunc: func(params ConverseParams) *ProviderResponse {
		callCount++
		if callCount == 1 {
			// First call: LLM calls unlock_tool.
			return &ProviderResponse{
				ToolCalls: []tool.Call{{ToolUseID: "1", Name: "unlock", Input: json.RawMessage(`{}`)}},
			}
		}
		// Second call: should now see the "secret" tool.
		for _, s := range params.ToolConfig {
			if s.Name == "secret" {
				return &ProviderResponse{Text: "secret tool available"}
			}
		}
		return &ProviderResponse{Text: "secret tool NOT available"}
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

	a, err := New(p, prompt.Text("test"), []tool.Tool{unlockTool, secretTool},
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
	if result != "secret tool available" {
		t.Errorf("expected 'secret tool available', got %q", result)
	}
}

func TestWithToolFilter_Nil_AllToolsAvailable(t *testing.T) {
	p := &recordingProvider{responses: []*ProviderResponse{
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

	a, err := New(p, prompt.Text("test"), []tool.Tool{t1, t2})
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.Invoke(Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}

	specs := p.calls[0].ToolConfig
	if len(specs) != 2 {
		t.Fatalf("expected 2 tool specs, got %d", len(specs))
	}
}

func TestWithToolFilter_FilteredToolReturnsError(t *testing.T) {
	// If LLM somehow calls a filtered tool, it should get "unknown tool" error.
	p := &recordingProvider{responses: []*ProviderResponse{
		{ToolCalls: []tool.Call{{ToolUseID: "1", Name: "blocked", Input: json.RawMessage(`{}`)}}},
		{Text: "ok"},
	}}

	blockedTool := tool.Tool{
		Spec:    tool.Spec{Name: "blocked", Description: "blocked", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) { return "should not run", nil },
	}

	a, err := New(p, prompt.Text("test"), []tool.Tool{blockedTool},
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
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
}
func TestWithToolFilter_MultipleFilters_ANDSemantics(t *testing.T) {
	p := &recordingProvider{responses: []*ProviderResponse{
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

	a, err := New(p, prompt.Text("test"), []tool.Tool{t1, t2, t3},
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
	specs := p.calls[0].ToolConfig
	if len(specs) != 1 {
		t.Fatalf("expected 1 tool spec (AND semantics), got %d: %v", len(specs), specs)
	}
	if specs[0].Name != "both_pass" {
		t.Errorf("expected 'both_pass', got %q", specs[0].Name)
	}
}

func TestWithToolFilter_AccumulatesAcrossMultipleCalls(t *testing.T) {
	p := &recordingProvider{responses: []*ProviderResponse{
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
	a, err := New(p, prompt.Text("test"), []tool.Tool{t1, t2},
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
	specs := p.calls[0].ToolConfig
	if len(specs) != 0 {
		t.Fatalf("expected 0 tool specs (both filtered out), got %d", len(specs))
	}
}
