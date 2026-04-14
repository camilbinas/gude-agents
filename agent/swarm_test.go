package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// mockSwarmProvider is a simple mock that returns canned responses in sequence.
type mockSwarmProvider struct {
	responses []*ProviderResponse
	callIndex int
}

func (m *mockSwarmProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	return m.next(), nil
}

func (m *mockSwarmProvider) ConverseStream(_ context.Context, _ ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	resp := m.next()
	if cb != nil && resp.Text != "" {
		cb(resp.Text)
	}
	return resp, nil
}

func (m *mockSwarmProvider) next() *ProviderResponse {
	if m.callIndex >= len(m.responses) {
		return &ProviderResponse{Text: "no more responses"}
	}
	r := m.responses[m.callIndex]
	m.callIndex++
	return r
}

func TestNewSwarm_MinimumMembers(t *testing.T) {
	p := &mockSwarmProvider{}
	a1, _ := New(p, prompt.Text("agent1"), nil)

	_, err := NewSwarm([]SwarmMember{
		{Name: "only", Description: "solo", Agent: a1},
	})
	if err == nil {
		t.Fatal("expected error for <2 members")
	}
}

func TestNewSwarm_DuplicateNames(t *testing.T) {
	p := &mockSwarmProvider{}
	a1, _ := New(p, prompt.Text("a1"), nil)
	a2, _ := New(p, prompt.Text("a2"), nil)

	_, err := NewSwarm([]SwarmMember{
		{Name: "same", Description: "first", Agent: a1},
		{Name: "same", Description: "second", Agent: a2},
	})
	if err == nil {
		t.Fatal("expected error for duplicate names")
	}
}

func TestSwarm_HandoffToolsInjected(t *testing.T) {
	p := &mockSwarmProvider{responses: []*ProviderResponse{{Text: "hi"}}}
	a1, _ := New(p, prompt.Text("a1"), nil)
	a2, _ := New(p, prompt.Text("a2"), nil)

	sw, err := NewSwarm([]SwarmMember{
		{Name: "alpha", Description: "first agent", Agent: a1},
		{Name: "beta", Description: "second agent", Agent: a2},
	})
	if err != nil {
		t.Fatal(err)
	}

	// alpha should have transfer_to_beta
	if _, ok := sw.members["alpha"].member.Agent.tools["transfer_to_beta"]; !ok {
		t.Error("alpha missing transfer_to_beta tool")
	}
	// beta should have transfer_to_alpha
	if _, ok := sw.members["beta"].member.Agent.tools["transfer_to_alpha"]; !ok {
		t.Error("beta missing transfer_to_alpha tool")
	}
}

func TestSwarm_DirectResponse(t *testing.T) {
	// Agent responds directly without handoff.
	p := &mockSwarmProvider{responses: []*ProviderResponse{
		{Text: "Hello from alpha", Usage: TokenUsage{InputTokens: 10, OutputTokens: 5}},
	}}
	a1, _ := New(p, prompt.Text("a1"), nil)
	a2, _ := New(&mockSwarmProvider{}, prompt.Text("a2"), nil)

	sw, _ := NewSwarm([]SwarmMember{
		{Name: "alpha", Description: "first", Agent: a1},
		{Name: "beta", Description: "second", Agent: a2},
	})

	result, err := sw.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAgent != "alpha" {
		t.Errorf("expected final agent alpha, got %s", result.FinalAgent)
	}
	if result.Response != "Hello from alpha" {
		t.Errorf("unexpected response: %s", result.Response)
	}
	if len(result.HandoffHistory) != 0 {
		t.Errorf("expected no handoffs, got %d", len(result.HandoffHistory))
	}
}

func TestSwarm_SingleHandoff(t *testing.T) {
	// Alpha calls transfer_to_beta, then beta responds.
	handoffInput, _ := json.Marshal(map[string]string{"summary": "user needs beta"})

	alphaProvider := &mockSwarmProvider{responses: []*ProviderResponse{
		// Alpha's first response: call transfer_to_beta
		{
			ToolCalls: []tool.Call{
				{ToolUseID: "tc1", Name: "transfer_to_beta", Input: handoffInput},
			},
			Usage: TokenUsage{InputTokens: 10, OutputTokens: 5},
		},
	}}
	betaProvider := &mockSwarmProvider{responses: []*ProviderResponse{
		{Text: "Hello from beta", Usage: TokenUsage{InputTokens: 8, OutputTokens: 4}},
	}}

	a1, _ := New(alphaProvider, prompt.Text("I am alpha"), nil)
	a2, _ := New(betaProvider, prompt.Text("I am beta"), nil)

	sw, _ := NewSwarm([]SwarmMember{
		{Name: "alpha", Description: "first", Agent: a1},
		{Name: "beta", Description: "second", Agent: a2},
	})

	result, err := sw.Invoke(context.Background(), "help me")
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAgent != "beta" {
		t.Errorf("expected final agent beta, got %s", result.FinalAgent)
	}
	if result.Response != "Hello from beta" {
		t.Errorf("unexpected response: %s", result.Response)
	}
	if len(result.HandoffHistory) != 1 {
		t.Fatalf("expected 1 handoff, got %d", len(result.HandoffHistory))
	}
	if result.HandoffHistory[0].From != "alpha" || result.HandoffHistory[0].To != "beta" {
		t.Errorf("unexpected handoff: %+v", result.HandoffHistory[0])
	}
}

func TestSwarm_MaxHandoffsExceeded(t *testing.T) {
	// Both agents keep handing off to each other.
	handoffToBeta, _ := json.Marshal(map[string]string{"summary": "go to beta"})
	handoffToAlpha, _ := json.Marshal(map[string]string{"summary": "go to alpha"})

	alphaProvider := &mockSwarmProvider{responses: make([]*ProviderResponse, 10)}
	betaProvider := &mockSwarmProvider{responses: make([]*ProviderResponse, 10)}

	for i := range 10 {
		alphaProvider.responses[i] = &ProviderResponse{
			ToolCalls: []tool.Call{{ToolUseID: "tc", Name: "transfer_to_beta", Input: handoffToBeta}},
		}
		betaProvider.responses[i] = &ProviderResponse{
			ToolCalls: []tool.Call{{ToolUseID: "tc", Name: "transfer_to_alpha", Input: handoffToAlpha}},
		}
	}

	a1, _ := New(alphaProvider, prompt.Text("alpha"), nil)
	a2, _ := New(betaProvider, prompt.Text("beta"), nil)

	sw, _ := NewSwarm([]SwarmMember{
		{Name: "alpha", Description: "first", Agent: a1},
		{Name: "beta", Description: "second", Agent: a2},
	}, WithSwarmMaxHandoffs(3))

	_, err := sw.Invoke(context.Background(), "loop")
	if err == nil {
		t.Fatal("expected max handoffs error")
	}
}

func TestSwarm_AgentWithOwnTools(t *testing.T) {
	// Agent uses its own tool, then responds — no handoff.
	lookupTool := tool.NewRaw("lookup", "look things up", map[string]any{
		"type": "object", "properties": map[string]any{},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "found it", nil
	})

	alphaProvider := &mockSwarmProvider{responses: []*ProviderResponse{
		// First: call lookup tool
		{ToolCalls: []tool.Call{{ToolUseID: "tc1", Name: "lookup", Input: json.RawMessage("{}")}}},
		// Second: final response
		{Text: "Here's what I found"},
	}}

	a1, _ := New(alphaProvider, prompt.Text("alpha"), []tool.Tool{lookupTool})
	a2, _ := New(&mockSwarmProvider{}, prompt.Text("beta"), nil)

	sw, _ := NewSwarm([]SwarmMember{
		{Name: "alpha", Description: "first", Agent: a1},
		{Name: "beta", Description: "second", Agent: a2},
	})

	result, err := sw.Invoke(context.Background(), "find something")
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAgent != "alpha" {
		t.Errorf("expected alpha, got %s", result.FinalAgent)
	}
	if len(result.HandoffHistory) != 0 {
		t.Errorf("expected no handoffs")
	}
}

// inMemoryStore is a simple in-memory Memory implementation for testing.
type inMemoryStore struct {
	data map[string][]Message
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{data: make(map[string][]Message)}
}

func (m *inMemoryStore) Load(_ context.Context, id string) ([]Message, error) {
	return m.data[id], nil
}

func (m *inMemoryStore) Save(_ context.Context, id string, msgs []Message) error {
	m.data[id] = msgs
	return nil
}

func TestSwarm_MemoryPersistsConversation(t *testing.T) {
	// Turn 1: alpha responds directly.
	// Turn 2: alpha sees the history and responds again.
	alphaProvider := &mockSwarmProvider{responses: []*ProviderResponse{
		{Text: "Hello from turn 1", Usage: TokenUsage{InputTokens: 10, OutputTokens: 5}},
		{Text: "Hello from turn 2", Usage: TokenUsage{InputTokens: 20, OutputTokens: 8}},
	}}

	a1, _ := New(alphaProvider, prompt.Text("alpha"), nil)
	a2, _ := New(&mockSwarmProvider{}, prompt.Text("beta"), nil)

	mem := newInMemoryStore()
	sw, _ := NewSwarm([]SwarmMember{
		{Name: "alpha", Description: "first", Agent: a1},
		{Name: "beta", Description: "second", Agent: a2},
	}, WithSwarmMemory(mem, "conv1"))

	// Turn 1
	r1, err := sw.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if r1.Response != "Hello from turn 1" {
		t.Errorf("turn 1: unexpected response: %s", r1.Response)
	}

	// Memory should have the conversation saved.
	saved := mem.data["conv1"]
	if len(saved) != 2 { // user + assistant
		t.Fatalf("expected 2 messages in memory, got %d", len(saved))
	}

	// Turn 2 — should load history and append new user message.
	r2, err := sw.Invoke(context.Background(), "follow up")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Response != "Hello from turn 2" {
		t.Errorf("turn 2: unexpected response: %s", r2.Response)
	}

	// Memory should now have 4 messages: user1, assistant1, user2, assistant2.
	saved = mem.data["conv1"]
	if len(saved) != 4 {
		t.Fatalf("expected 4 messages in memory, got %d", len(saved))
	}
}

func TestSwarm_MemoryPersistsActiveAgent(t *testing.T) {
	// Turn 1: alpha hands off to beta, beta responds.
	// Turn 2: beta should be the starting agent (remembered from turn 1).
	handoffInput, _ := json.Marshal(map[string]string{"summary": "go to beta"})

	alphaProvider := &mockSwarmProvider{responses: []*ProviderResponse{
		{ToolCalls: []tool.Call{{ToolUseID: "tc1", Name: "transfer_to_beta", Input: handoffInput}}},
	}}
	betaProvider := &mockSwarmProvider{responses: []*ProviderResponse{
		{Text: "Beta turn 1"},
		{Text: "Beta turn 2"},
	}}

	a1, _ := New(alphaProvider, prompt.Text("alpha"), nil)
	a2, _ := New(betaProvider, prompt.Text("beta"), nil)

	mem := newInMemoryStore()
	sw, _ := NewSwarm([]SwarmMember{
		{Name: "alpha", Description: "first", Agent: a1},
		{Name: "beta", Description: "second", Agent: a2},
	}, WithSwarmMemory(mem, "conv2"))

	// Turn 1: triage → beta
	r1, err := sw.Invoke(context.Background(), "help")
	if err != nil {
		t.Fatal(err)
	}
	if r1.FinalAgent != "beta" {
		t.Errorf("turn 1: expected beta, got %s", r1.FinalAgent)
	}

	// Turn 2: should start at beta (not alpha).
	// If it started at alpha, alpha's provider has no more responses and would fail.
	r2, err := sw.Invoke(context.Background(), "more help")
	if err != nil {
		t.Fatal(err)
	}
	if r2.FinalAgent != "beta" {
		t.Errorf("turn 2: expected beta, got %s", r2.FinalAgent)
	}
	if r2.Response != "Beta turn 2" {
		t.Errorf("turn 2: unexpected response: %s", r2.Response)
	}
}

// TestNewSwarm_Idempotency verifies that calling NewSwarm twice with the same
// agent instances does not produce duplicate handoff tools on any agent and
// does not return an error on the second call.
// Requirements: 5.1, 5.2
func TestNewSwarm_Idempotency(t *testing.T) {
	p := &mockSwarmProvider{}
	a1, _ := New(p, prompt.Text("a1"), nil)
	a2, _ := New(p, prompt.Text("a2"), nil)

	members := []SwarmMember{
		{Name: "alpha", Description: "first agent", Agent: a1},
		{Name: "beta", Description: "second agent", Agent: a2},
	}

	// First call.
	_, err := NewSwarm(members)
	if err != nil {
		t.Fatalf("first NewSwarm call failed: %v", err)
	}

	// Second call with the same agent instances — must not error.
	_, err = NewSwarm(members)
	if err != nil {
		t.Fatalf("second NewSwarm call failed: %v", err)
	}

	// Assert no duplicate tool names on alpha.
	seen := make(map[string]int)
	for _, spec := range a1.toolSpecs {
		seen[spec.Name]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("alpha has duplicate tool %q (%d times)", name, count)
		}
	}

	// Assert no duplicate tool names on beta.
	seen = make(map[string]int)
	for _, spec := range a2.toolSpecs {
		seen[spec.Name]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("beta has duplicate tool %q (%d times)", name, count)
		}
	}
}
