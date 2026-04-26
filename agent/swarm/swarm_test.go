package swarm_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/swarm"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// mockSwarmProvider is a simple mock that returns canned responses in sequence.
type mockSwarmProvider struct {
	responses []*agent.ProviderResponse
	callIndex int
}

func (m *mockSwarmProvider) Converse(_ context.Context, _ agent.ConverseParams) (*agent.ProviderResponse, error) {
	return m.next(), nil
}

func (m *mockSwarmProvider) ConverseStream(_ context.Context, _ agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	resp := m.next()
	if cb != nil && resp.Text != "" {
		cb(resp.Text)
	}
	return resp, nil
}

func (m *mockSwarmProvider) next() *agent.ProviderResponse {
	if m.callIndex >= len(m.responses) {
		return &agent.ProviderResponse{Text: "no more responses"}
	}
	r := m.responses[m.callIndex]
	m.callIndex++
	return r
}

func TestNew_MinimumMembers(t *testing.T) {
	p := &mockSwarmProvider{}
	a1, _ := agent.New(p, prompt.Text("agent1"), nil)

	_, err := swarm.New([]swarm.Member{
		{Name: "only", Description: "solo", Agent: a1},
	})
	if err == nil {
		t.Fatal("expected error for <2 members")
	}
}

func TestNew_DuplicateNames(t *testing.T) {
	p := &mockSwarmProvider{}
	a1, _ := agent.New(p, prompt.Text("a1"), nil)
	a2, _ := agent.New(p, prompt.Text("a2"), nil)

	_, err := swarm.New([]swarm.Member{
		{Name: "same", Description: "first", Agent: a1},
		{Name: "same", Description: "second", Agent: a2},
	})
	if err == nil {
		t.Fatal("expected error for duplicate names")
	}
}

func TestSwarm_HandoffToolsInjected(t *testing.T) {
	p := &mockSwarmProvider{responses: []*agent.ProviderResponse{{Text: "hi"}}}
	a1, _ := agent.New(p, prompt.Text("a1"), nil)
	a2, _ := agent.New(p, prompt.Text("a2"), nil)

	_, err := swarm.New([]swarm.Member{
		{Name: "alpha", Description: "first agent", Agent: a1},
		{Name: "beta", Description: "second agent", Agent: a2},
	})
	if err != nil {
		t.Fatal(err)
	}

	// alpha should have transfer_to_beta
	if !a1.HasTool("transfer_to_beta") {
		t.Error("alpha missing transfer_to_beta tool")
	}
	// beta should have transfer_to_alpha
	if !a2.HasTool("transfer_to_alpha") {
		t.Error("beta missing transfer_to_alpha tool")
	}
}

func TestSwarm_DirectResponse(t *testing.T) {
	p := &mockSwarmProvider{responses: []*agent.ProviderResponse{
		{Text: "Hello from alpha", Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5}},
	}}
	a1, _ := agent.New(p, prompt.Text("a1"), nil)
	a2, _ := agent.New(&mockSwarmProvider{}, prompt.Text("a2"), nil)

	sw, _ := swarm.New([]swarm.Member{
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
	handoffInput, _ := json.Marshal(map[string]string{"summary": "user needs beta"})

	alphaProvider := &mockSwarmProvider{responses: []*agent.ProviderResponse{
		{
			ToolCalls: []tool.Call{
				{ToolUseID: "tc1", Name: "transfer_to_beta", Input: handoffInput},
			},
			Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5},
		},
	}}
	betaProvider := &mockSwarmProvider{responses: []*agent.ProviderResponse{
		{Text: "Hello from beta", Usage: agent.TokenUsage{InputTokens: 8, OutputTokens: 4}},
	}}

	a1, _ := agent.New(alphaProvider, prompt.Text("I am alpha"), nil)
	a2, _ := agent.New(betaProvider, prompt.Text("I am beta"), nil)

	sw, _ := swarm.New([]swarm.Member{
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
	handoffToBeta, _ := json.Marshal(map[string]string{"summary": "go to beta"})
	handoffToAlpha, _ := json.Marshal(map[string]string{"summary": "go to alpha"})

	alphaProvider := &mockSwarmProvider{responses: make([]*agent.ProviderResponse, 10)}
	betaProvider := &mockSwarmProvider{responses: make([]*agent.ProviderResponse, 10)}

	for i := range 10 {
		alphaProvider.responses[i] = &agent.ProviderResponse{
			ToolCalls: []tool.Call{{ToolUseID: "tc", Name: "transfer_to_beta", Input: handoffToBeta}},
		}
		betaProvider.responses[i] = &agent.ProviderResponse{
			ToolCalls: []tool.Call{{ToolUseID: "tc", Name: "transfer_to_alpha", Input: handoffToAlpha}},
		}
	}

	a1, _ := agent.New(alphaProvider, prompt.Text("alpha"), nil)
	a2, _ := agent.New(betaProvider, prompt.Text("beta"), nil)

	sw, _ := swarm.New([]swarm.Member{
		{Name: "alpha", Description: "first", Agent: a1},
		{Name: "beta", Description: "second", Agent: a2},
	}, swarm.WithMaxHandoffs(3))

	_, err := sw.Invoke(context.Background(), "loop")
	if err == nil {
		t.Fatal("expected max handoffs error")
	}
}

func TestSwarm_AgentWithOwnTools(t *testing.T) {
	lookupTool := tool.NewRaw("lookup", "look things up", map[string]any{
		"type": "object", "properties": map[string]any{},
	}, func(_ context.Context, _ json.RawMessage) (string, error) {
		return "found it", nil
	})

	alphaProvider := &mockSwarmProvider{responses: []*agent.ProviderResponse{
		{ToolCalls: []tool.Call{{ToolUseID: "tc1", Name: "lookup", Input: json.RawMessage("{}")}}},
		{Text: "Here's what I found"},
	}}

	a1, _ := agent.New(alphaProvider, prompt.Text("alpha"), []tool.Tool{lookupTool})
	a2, _ := agent.New(&mockSwarmProvider{}, prompt.Text("beta"), nil)

	sw, _ := swarm.New([]swarm.Member{
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
	data map[string][]agent.Message
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{data: make(map[string][]agent.Message)}
}

func (m *inMemoryStore) Load(_ context.Context, id string) ([]agent.Message, error) {
	return m.data[id], nil
}

func (m *inMemoryStore) Save(_ context.Context, id string, msgs []agent.Message) error {
	m.data[id] = msgs
	return nil
}

func TestSwarm_MemoryPersistsConversation(t *testing.T) {
	alphaProvider := &mockSwarmProvider{responses: []*agent.ProviderResponse{
		{Text: "Hello from turn 1", Usage: agent.TokenUsage{InputTokens: 10, OutputTokens: 5}},
		{Text: "Hello from turn 2", Usage: agent.TokenUsage{InputTokens: 20, OutputTokens: 8}},
	}}

	a1, _ := agent.New(alphaProvider, prompt.Text("alpha"), nil)
	a2, _ := agent.New(&mockSwarmProvider{}, prompt.Text("beta"), nil)

	mem := newInMemoryStore()
	sw, _ := swarm.New([]swarm.Member{
		{Name: "alpha", Description: "first", Agent: a1},
		{Name: "beta", Description: "second", Agent: a2},
	}, swarm.WithConversation(mem, "conv1"))

	r1, err := sw.Invoke(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if r1.Response != "Hello from turn 1" {
		t.Errorf("turn 1: unexpected response: %s", r1.Response)
	}

	saved := mem.data["conv1"]
	if len(saved) != 2 {
		t.Fatalf("expected 2 messages in memory, got %d", len(saved))
	}

	r2, err := sw.Invoke(context.Background(), "follow up")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Response != "Hello from turn 2" {
		t.Errorf("turn 2: unexpected response: %s", r2.Response)
	}

	saved = mem.data["conv1"]
	if len(saved) != 4 {
		t.Fatalf("expected 4 messages in memory, got %d", len(saved))
	}
}

func TestSwarm_MemoryPersistsActiveAgent(t *testing.T) {
	handoffInput, _ := json.Marshal(map[string]string{"summary": "go to beta"})

	alphaProvider := &mockSwarmProvider{responses: []*agent.ProviderResponse{
		{ToolCalls: []tool.Call{{ToolUseID: "tc1", Name: "transfer_to_beta", Input: handoffInput}}},
	}}
	betaProvider := &mockSwarmProvider{responses: []*agent.ProviderResponse{
		{Text: "Beta turn 1"},
		{Text: "Beta turn 2"},
	}}

	a1, _ := agent.New(alphaProvider, prompt.Text("alpha"), nil)
	a2, _ := agent.New(betaProvider, prompt.Text("beta"), nil)

	mem := newInMemoryStore()
	sw, _ := swarm.New([]swarm.Member{
		{Name: "alpha", Description: "first", Agent: a1},
		{Name: "beta", Description: "second", Agent: a2},
	}, swarm.WithConversation(mem, "conv2"))

	r1, err := sw.Invoke(context.Background(), "help")
	if err != nil {
		t.Fatal(err)
	}
	if r1.FinalAgent != "beta" {
		t.Errorf("turn 1: expected beta, got %s", r1.FinalAgent)
	}

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

func TestNew_Idempotency(t *testing.T) {
	p := &mockSwarmProvider{}
	a1, _ := agent.New(p, prompt.Text("a1"), nil)
	a2, _ := agent.New(p, prompt.Text("a2"), nil)

	members := []swarm.Member{
		{Name: "alpha", Description: "first agent", Agent: a1},
		{Name: "beta", Description: "second agent", Agent: a2},
	}

	_, err := swarm.New(members)
	if err != nil {
		t.Fatalf("first New call failed: %v", err)
	}

	_, err = swarm.New(members)
	if err != nil {
		t.Fatalf("second New call failed: %v", err)
	}

	seen := make(map[string]int)
	for _, spec := range a1.ToolSpecs() {
		seen[spec.Name]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("alpha has duplicate tool %q (%d times)", name, count)
		}
	}

	seen = make(map[string]int)
	for _, spec := range a2.ToolSpecs() {
		seen[spec.Name]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("beta has duplicate tool %q (%d times)", name, count)
		}
	}
}
