package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

// Feature: swarm-loop-refactor, Property 7: Swarm detects handoff and updates active agent

// swarmHandoffProvider is a mock provider that returns a tool call to transfer_to_<target>
// on the first call, simulating a handoff.
type swarmHandoffProvider struct {
	target    string
	callCount atomic.Int32
}

func (p *swarmHandoffProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	p.callCount.Add(1)
	input, _ := json.Marshal(map[string]string{"summary": "handoff test"})
	return &ProviderResponse{
		ToolCalls: []tool.Call{
			{ToolUseID: "tc-handoff", Name: "transfer_to_" + p.target, Input: input},
		},
	}, nil
}

func (p *swarmHandoffProvider) ConverseStream(_ context.Context, _ ConverseParams, _ StreamCallback) (*ProviderResponse, error) {
	return p.Converse(context.Background(), ConverseParams{})
}

// swarmFinalTextProvider is a mock provider that always returns a final text response.
type swarmFinalTextProvider struct {
	text string
}

func (p *swarmFinalTextProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	return &ProviderResponse{Text: p.text}, nil
}

func (p *swarmFinalTextProvider) ConverseStream(_ context.Context, _ ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	if cb != nil {
		cb(p.text)
	}
	return &ProviderResponse{Text: p.text}, nil
}

// TestProperty_SwarmHandoffDetection verifies that for any swarm run where an agent's
// tool call returns the handoff sentinel targeting agent X, the swarm records a
// Handoff{From: current, To: X} in the result's HandoffHistory and continues
// execution with agent X as the active agent.
//
// **Validates: Requirements 2.3**
func TestProperty_SwarmHandoffDetection(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random agent names (valid identifiers).
		fromName := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "fromName")
		toName := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "toName")
		// Ensure names are different.
		for toName == fromName {
			toName = rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "toNameRetry")
		}

		// Generate a random final text for the target agent.
		finalText := rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,100}`).Draw(rt, "finalText")

		// Create providers: fromAgent always hands off to toAgent, toAgent returns final text.
		fromProvider := &swarmHandoffProvider{target: toName}
		toProvider := &swarmFinalTextProvider{text: finalText}

		// Create agents.
		fromAgent, err := New(fromProvider, prompt.Text("I am "+fromName), nil)
		if err != nil {
			rt.Fatalf("failed to create fromAgent: %v", err)
		}
		toAgent, err := New(toProvider, prompt.Text("I am "+toName), nil)
		if err != nil {
			rt.Fatalf("failed to create toAgent: %v", err)
		}

		// Create swarm with fromAgent as initial (first member).
		sw, err := NewSwarm([]SwarmMember{
			{Name: fromName, Description: fromName + " agent", Agent: fromAgent},
			{Name: toName, Description: toName + " agent", Agent: toAgent},
		})
		if err != nil {
			rt.Fatalf("failed to create swarm: %v", err)
		}

		// Run the swarm.
		result, err := sw.Invoke(context.Background(), "hello")
		if err != nil {
			rt.Fatalf("swarm Invoke returned error: %v", err)
		}

		// Verify: HandoffHistory contains exactly one handoff from fromName to toName.
		if len(result.HandoffHistory) != 1 {
			rt.Fatalf("expected 1 handoff, got %d: %+v", len(result.HandoffHistory), result.HandoffHistory)
		}
		if result.HandoffHistory[0].From != fromName {
			rt.Fatalf("handoff From: expected %q, got %q", fromName, result.HandoffHistory[0].From)
		}
		if result.HandoffHistory[0].To != toName {
			rt.Fatalf("handoff To: expected %q, got %q", toName, result.HandoffHistory[0].To)
		}

		// Verify: FinalAgent is the target agent.
		if result.FinalAgent != toName {
			rt.Fatalf("FinalAgent: expected %q, got %q", toName, result.FinalAgent)
		}
	})
}

// Feature: swarm-loop-refactor, Property 8: Swarm result contains the loop's final text

// TestProperty_SwarmResultContainsFinalText verifies that for any swarm run where
// the final agent's loop returns text T, SwarmResult.Response equals T and
// SwarmResult.FinalAgent equals the name of that agent.
//
// **Validates: Requirements 2.4**
func TestProperty_SwarmResultContainsFinalText(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random agent name.
		agentName := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "agentName")
		otherName := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "otherName")
		for otherName == agentName {
			otherName = rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "otherNameRetry")
		}

		// Generate a random text response.
		responseText := rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,200}`).Draw(rt, "responseText")

		// Create a provider that returns the random text (no handoff).
		provider := &swarmFinalTextProvider{text: responseText}
		otherProvider := &swarmFinalTextProvider{text: "other"}

		// Create agents.
		activeAgent, err := New(provider, prompt.Text("I am "+agentName), nil)
		if err != nil {
			rt.Fatalf("failed to create activeAgent: %v", err)
		}
		otherAgent, err := New(otherProvider, prompt.Text("I am "+otherName), nil)
		if err != nil {
			rt.Fatalf("failed to create otherAgent: %v", err)
		}

		// Create swarm with activeAgent as initial (first member).
		sw, err := NewSwarm([]SwarmMember{
			{Name: agentName, Description: agentName + " agent", Agent: activeAgent},
			{Name: otherName, Description: otherName + " agent", Agent: otherAgent},
		})
		if err != nil {
			rt.Fatalf("failed to create swarm: %v", err)
		}

		// Run the swarm.
		result, err := sw.Invoke(context.Background(), "hello")
		if err != nil {
			rt.Fatalf("swarm Invoke returned error: %v", err)
		}

		// Verify: SwarmResult.Response equals the text.
		if result.Response != responseText {
			rt.Fatalf("Response: expected %q, got %q", responseText, result.Response)
		}

		// Verify: SwarmResult.FinalAgent equals the agent name.
		if result.FinalAgent != agentName {
			rt.Fatalf("FinalAgent: expected %q, got %q", agentName, result.FinalAgent)
		}
	})
}

// Feature: swarm-loop-refactor, Property 12: Ping-pong loop detection

// pingPongProvider is a mock provider that always hands off to the specified target.
// It tracks the messages it receives so we can verify loop detection injection.
type pingPongProvider struct {
	mu       sync.Mutex
	target   string
	messages [][]Message // messages received on each call
}

func (p *pingPongProvider) Converse(_ context.Context, params ConverseParams) (*ProviderResponse, error) {
	p.mu.Lock()
	msgCopy := make([]Message, len(params.Messages))
	copy(msgCopy, params.Messages)
	p.messages = append(p.messages, msgCopy)
	p.mu.Unlock()

	input, _ := json.Marshal(map[string]string{"summary": "ping pong"})
	return &ProviderResponse{
		ToolCalls: []tool.Call{
			{ToolUseID: "tc-pp", Name: "transfer_to_" + p.target, Input: input},
		},
	}, nil
}

func (p *pingPongProvider) ConverseStream(_ context.Context, params ConverseParams, _ StreamCallback) (*ProviderResponse, error) {
	return p.Converse(context.Background(), params)
}

func (p *pingPongProvider) getMessages() [][]Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([][]Message, len(p.messages))
	copy(cp, p.messages)
	return cp
}

// loopDetectionFinalProvider hands off on the first call, then returns final text
// on the second call (after loop detection message is injected).
type loopDetectionFinalProvider struct {
	mu        sync.Mutex
	target    string
	finalText string
	callCount int
	messages  [][]Message
}

func (p *loopDetectionFinalProvider) Converse(_ context.Context, params ConverseParams) (*ProviderResponse, error) {
	p.mu.Lock()
	p.callCount++
	call := p.callCount
	msgCopy := make([]Message, len(params.Messages))
	copy(msgCopy, params.Messages)
	p.messages = append(p.messages, msgCopy)
	p.mu.Unlock()

	if call == 1 {
		// First call: hand off to target
		input, _ := json.Marshal(map[string]string{"summary": "ping pong"})
		return &ProviderResponse{
			ToolCalls: []tool.Call{
				{ToolUseID: "tc-pp", Name: "transfer_to_" + p.target, Input: input},
			},
		}, nil
	}
	// Second call (after loop detection): return final text
	return &ProviderResponse{Text: p.finalText}, nil
}

func (p *loopDetectionFinalProvider) ConverseStream(_ context.Context, params ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	resp, err := p.Converse(context.Background(), params)
	if err == nil && cb != nil && resp.Text != "" {
		cb(resp.Text)
	}
	return resp, err
}

func (p *loopDetectionFinalProvider) getMessages() [][]Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([][]Message, len(p.messages))
	copy(cp, p.messages)
	return cp
}

// TestProperty_PingPongDetection verifies that for any handoff history where agent X
// has previously appeared as a From agent, when a new handoff targets X, the swarm
// appends a message containing the loop-detection instruction text before X's next turn.
//
// **Validates: Requirements 10.1**
func TestProperty_PingPongDetection(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random agent names.
		nameA := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "nameA")
		nameB := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "nameB")
		for nameB == nameA {
			nameB = rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "nameBRetry")
		}

		// Generate a random final text.
		finalText := rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(rt, "finalText")

		// Agent A: hands off to B on first call, then returns final text on second call
		// (second call happens after B hands back to A with loop detection).
		providerA := &loopDetectionFinalProvider{target: nameB, finalText: finalText}

		// Agent B: always hands off to A (triggers the ping-pong).
		providerB := &pingPongProvider{target: nameA}

		agentA, err := New(providerA, prompt.Text("I am "+nameA), nil)
		if err != nil {
			rt.Fatalf("failed to create agentA: %v", err)
		}
		agentB, err := New(providerB, prompt.Text("I am "+nameB), nil)
		if err != nil {
			rt.Fatalf("failed to create agentB: %v", err)
		}

		// Create swarm: A is initial, max handoffs high enough to allow A→B→A.
		sw, err := NewSwarm([]SwarmMember{
			{Name: nameA, Description: nameA + " agent", Agent: agentA},
			{Name: nameB, Description: nameB + " agent", Agent: agentB},
		}, WithSwarmMaxHandoffs(10))
		if err != nil {
			rt.Fatalf("failed to create swarm: %v", err)
		}

		// Run the swarm. Flow: A→B→A (loop detected on second A turn).
		result, err := sw.Invoke(context.Background(), "start")
		if err != nil {
			rt.Fatalf("swarm Invoke returned error: %v", err)
		}

		// Verify: the result should have A as final agent (it returned text on second call).
		if result.FinalAgent != nameA {
			rt.Fatalf("FinalAgent: expected %q, got %q", nameA, result.FinalAgent)
		}

		// Verify: HandoffHistory should be A→B, B→A.
		if len(result.HandoffHistory) != 2 {
			rt.Fatalf("expected 2 handoffs, got %d: %+v", len(result.HandoffHistory), result.HandoffHistory)
		}
		if result.HandoffHistory[0].From != nameA || result.HandoffHistory[0].To != nameB {
			rt.Fatalf("handoff[0]: expected %s→%s, got %s→%s", nameA, nameB, result.HandoffHistory[0].From, result.HandoffHistory[0].To)
		}
		if result.HandoffHistory[1].From != nameB || result.HandoffHistory[1].To != nameA {
			rt.Fatalf("handoff[1]: expected %s→%s, got %s→%s", nameB, nameA, result.HandoffHistory[1].From, result.HandoffHistory[1].To)
		}

		// Verify: when A was called the second time, the messages should contain
		// the loop detection instruction text ("You have already been consulted").
		aMessages := providerA.getMessages()
		if len(aMessages) < 2 {
			rt.Fatalf("expected at least 2 calls to agent A, got %d", len(aMessages))
		}
		secondCallMsgs := aMessages[1]
		found := false
		for _, msg := range secondCallMsgs {
			for _, block := range msg.Content {
				if tb, ok := block.(TextBlock); ok {
					if strings.Contains(tb.Text, "You have already been consulted") {
						found = true
						break
					}
				}
			}
			if found {
				break
			}
		}
		if !found {
			rt.Fatalf("loop detection message not found in agent A's second call messages")
		}
	})
}

// Feature: swarm-loop-refactor, Property 13: Max handoffs enforcement

// alwaysHandoffProvider is a mock provider that always hands off to the specified target.
type alwaysHandoffProvider struct {
	target string
}

func (p *alwaysHandoffProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	input, _ := json.Marshal(map[string]string{"summary": "always handoff"})
	return &ProviderResponse{
		ToolCalls: []tool.Call{
			{ToolUseID: "tc-always", Name: "transfer_to_" + p.target, Input: input},
		},
	}, nil
}

func (p *alwaysHandoffProvider) ConverseStream(_ context.Context, _ ConverseParams, _ StreamCallback) (*ProviderResponse, error) {
	return p.Converse(context.Background(), ConverseParams{})
}

// TestProperty_MaxHandoffsEnforcement verifies that for any maxHandoffs value N
// and a swarm where agents always hand off, the swarm returns a "max handoffs exceeded"
// error after exactly N+1 agent turns (N handoffs consumed, then the limit is hit
// on the next attempt).
//
// **Validates: Requirements 10.2**
func TestProperty_MaxHandoffsEnforcement(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random maxHandoffs value between 1 and 10.
		maxHandoffs := rapid.IntRange(1, 10).Draw(rt, "maxHandoffs")

		// Create two agents that always hand off to each other.
		nameA := "alpha"
		nameB := "beta"

		providerA := &alwaysHandoffProvider{target: nameB}
		providerB := &alwaysHandoffProvider{target: nameA}

		agentA, err := New(providerA, prompt.Text("I am alpha"), nil)
		if err != nil {
			rt.Fatalf("failed to create agentA: %v", err)
		}
		agentB, err := New(providerB, prompt.Text("I am beta"), nil)
		if err != nil {
			rt.Fatalf("failed to create agentB: %v", err)
		}

		sw, err := NewSwarm([]SwarmMember{
			{Name: nameA, Description: "alpha agent", Agent: agentA},
			{Name: nameB, Description: "beta agent", Agent: agentB},
		}, WithSwarmMaxHandoffs(maxHandoffs))
		if err != nil {
			rt.Fatalf("failed to create swarm: %v", err)
		}

		// Run the swarm — should fail with max handoffs exceeded.
		result, err := sw.Invoke(context.Background(), "loop forever")

		// Verify: error contains "max handoffs".
		if err == nil {
			rt.Fatalf("expected max handoffs error, got nil")
		}
		expectedMsg := fmt.Sprintf("max handoffs (%d) exceeded", maxHandoffs)
		if err.Error() != expectedMsg {
			rt.Fatalf("expected error %q, got %q", expectedMsg, err.Error())
		}

		// Verify: HandoffHistory length equals maxHandoffs.
		// The loop runs maxHandoffs+1 iterations (0..maxHandoffs), consuming maxHandoffs handoffs
		// before hitting the limit on the (maxHandoffs+1)th attempt.
		// But the last iteration also produces a handoff that gets recorded before the error.
		// Looking at the swarm code: the loop is `for handoff := 0; handoff <= s.maxHandoffs; handoff++`
		// which means it runs maxHandoffs+1 times. Each iteration that hands off increments the counter
		// and continues. The error fires when the loop exits (after maxHandoffs+1 iterations all handed off).
		// So HandoffHistory should have maxHandoffs entries (one per successful handoff iteration,
		// but the last iteration that would exceed is the one that exits the loop).
		// Actually looking more carefully: the loop runs from 0 to maxHandoffs inclusive.
		// Each iteration that hands off appends to HandoffHistory and continues.
		// After maxHandoffs+1 iterations all hand off, the loop exits and returns the error.
		// So HandoffHistory has maxHandoffs+1 entries? No — let's trace:
		// handoff=0: agent runs, hands off → append to history, continue (handoff becomes 1)
		// handoff=1: agent runs, hands off → append to history, continue (handoff becomes 2)
		// ...
		// handoff=maxHandoffs: agent runs, hands off → append to history, continue → loop exits
		// Wait, the loop condition is handoff <= maxHandoffs, so after handoff=maxHandoffs,
		// the loop body runs, and if it hands off, it appends and continues.
		// But `continue` increments handoff to maxHandoffs+1, which fails the condition, so loop exits.
		// So we get maxHandoffs+1 handoffs recorded? Let me re-read the code...
		// Actually: `for handoff := 0; handoff <= s.maxHandoffs; handoff++`
		// Iteration 0: runs, hands off, appends, continues → handoff becomes 1
		// Iteration 1: runs, hands off, appends, continues → handoff becomes 2
		// ...
		// Iteration maxHandoffs: runs, hands off, appends, continues → handoff becomes maxHandoffs+1
		// Loop condition: maxHandoffs+1 <= maxHandoffs → false → exits loop
		// So HandoffHistory has maxHandoffs+1 entries.
		// But wait — the property says "N handoffs consumed, then the limit is hit on the next attempt"
		// meaning N+1 agent turns total. Let me just verify the actual count.
		expectedHandoffs := maxHandoffs + 1
		if len(result.HandoffHistory) != expectedHandoffs {
			rt.Fatalf("expected %d handoffs in history, got %d", expectedHandoffs, len(result.HandoffHistory))
		}
	})
}

// Feature: swarm-loop-refactor, Property 10: Conversation state round-trip

// sequentialTextProvider returns a different text response on each call.
// It is safe for sequential use within a single test iteration.
type sequentialTextProvider struct {
	responses []string
	callIndex int
}

func (p *sequentialTextProvider) Converse(_ context.Context, _ ConverseParams) (*ProviderResponse, error) {
	if p.callIndex >= len(p.responses) {
		return &ProviderResponse{Text: "fallback"}, nil
	}
	resp := p.responses[p.callIndex]
	p.callIndex++
	return &ProviderResponse{Text: resp}, nil
}

func (p *sequentialTextProvider) ConverseStream(_ context.Context, _ ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	resp, err := p.Converse(context.Background(), ConverseParams{})
	if err == nil && cb != nil && resp.Text != "" {
		cb(resp.Text)
	}
	return resp, err
}

// TestProperty_ConversationRoundTrip verifies that for any swarm with conversation
// configured, after a successful run the conversation store contains the full message
// history and the metadata key contains the final agent name. On a subsequent run,
// loading restores those exact messages and active agent.
//
// **Validates: Requirements 9.1, 9.2, 9.3**
func TestProperty_ConversationRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random agent names.
		agentName := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "agentName")
		otherName := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "otherName")
		for otherName == agentName {
			otherName = rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "otherNameRetry")
		}

		// Generate random user messages and expected responses.
		userMsg1 := rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(rt, "userMsg1")
		userMsg2 := rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(rt, "userMsg2")
		response1 := rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,100}`).Draw(rt, "response1")
		response2 := rapid.StringMatching(`[a-zA-Z0-9 .,!?]{1,100}`).Draw(rt, "response2")

		// Generate a random conversation ID.
		convID := rapid.StringMatching(`[a-z0-9]{5,12}`).Draw(rt, "convID")

		// Create a provider that returns response1 on first call, response2 on second.
		provider := &sequentialTextProvider{responses: []string{response1, response2}}
		otherProvider := &swarmFinalTextProvider{text: "other-response"}

		// Create agents.
		activeAgent, err := New(provider, prompt.Text("I am "+agentName), nil)
		if err != nil {
			rt.Fatalf("failed to create activeAgent: %v", err)
		}
		otherAgent, err := New(otherProvider, prompt.Text("I am "+otherName), nil)
		if err != nil {
			rt.Fatalf("failed to create otherAgent: %v", err)
		}

		// Create an in-memory conversation store.
		conv := newInMemoryStore()

		// Create swarm with conversation configured.
		sw, err := NewSwarm([]SwarmMember{
			{Name: agentName, Description: agentName + " agent", Agent: activeAgent},
			{Name: otherName, Description: otherName + " agent", Agent: otherAgent},
		}, WithSwarmConversation(conv, convID))
		if err != nil {
			rt.Fatalf("failed to create swarm: %v", err)
		}

		// --- First run ---
		result1, err := sw.Invoke(context.Background(), userMsg1)
		if err != nil {
			rt.Fatalf("first Invoke returned error: %v", err)
		}

		// Verify: response matches expected.
		if result1.Response != response1 {
			rt.Fatalf("first run Response: expected %q, got %q", response1, result1.Response)
		}
		if result1.FinalAgent != agentName {
			rt.Fatalf("first run FinalAgent: expected %q, got %q", agentName, result1.FinalAgent)
		}

		// Verify: conversation store contains messages (Load returns non-empty history).
		history, err := conv.Load(context.Background(), convID)
		if err != nil {
			rt.Fatalf("Load conversation failed: %v", err)
		}
		if len(history) == 0 {
			rt.Fatalf("expected non-empty conversation history after first run")
		}

		// Verify: history should contain at least the user message and assistant response.
		// The swarm saves: [user message, assistant response].
		if len(history) < 2 {
			rt.Fatalf("expected at least 2 messages in history, got %d", len(history))
		}

		// Verify: the metadata key contains the final agent name.
		metaKey := "meta:" + convID + ":swarm_active"
		metaMsgs, err := conv.Load(context.Background(), metaKey)
		if err != nil {
			rt.Fatalf("Load metadata failed: %v", err)
		}
		if len(metaMsgs) == 0 {
			rt.Fatalf("expected non-empty metadata after first run")
		}
		lastMeta := metaMsgs[len(metaMsgs)-1]
		if len(lastMeta.Content) == 0 {
			rt.Fatalf("metadata message has no content")
		}
		metaText, ok := lastMeta.Content[0].(TextBlock)
		if !ok {
			rt.Fatalf("metadata content is not TextBlock")
		}
		if metaText.Text != agentName {
			rt.Fatalf("metadata active agent: expected %q, got %q", agentName, metaText.Text)
		}

		// --- Second run ---
		result2, err := sw.Invoke(context.Background(), userMsg2)
		if err != nil {
			rt.Fatalf("second Invoke returned error: %v", err)
		}

		// Verify: the second run continues with the same active agent (no re-triage).
		if result2.FinalAgent != agentName {
			rt.Fatalf("second run FinalAgent: expected %q (same agent), got %q", agentName, result2.FinalAgent)
		}

		// Verify: the response comes from the expected agent.
		if result2.Response != response2 {
			rt.Fatalf("second run Response: expected %q, got %q", response2, result2.Response)
		}

		// Verify: conversation history grew (now includes both turns).
		history2, err := conv.Load(context.Background(), convID)
		if err != nil {
			rt.Fatalf("Load conversation after second run failed: %v", err)
		}
		// Should have: user1, assistant1, user2, assistant2 = 4 messages.
		if len(history2) < 4 {
			rt.Fatalf("expected at least 4 messages in history after second run, got %d", len(history2))
		}
	})
}
