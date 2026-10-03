package structured

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/anthropic"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/provider/fallback"
	"github.com/camilbinas/gude-agents/agent/tool"
)

const outputToolName = "structured_output"

type profile struct {
	Name  string `json:"name" description:"A name" required:"true"`
	Count int    `json:"count" description:"A count"`
}

type testProvider struct {
	params   agent.ModelRequest
	response *agent.ModelResponse
	err      error
	calls    int
}

func (p *testProvider) Name() string { return "structured-test" }

func (p *testProvider) Stream(_ context.Context, req agent.ModelRequest, _ func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	p.calls++
	p.params = req
	if p.err != nil {
		return nil, p.err
	}
	return p.response, nil
}

func structuredResponse(raw string) *agent.ModelResponse {
	return &agent.ModelResponse{ToolCalls: []tool.Call{{
		ToolUseID: "structured-call",
		Name:      outputToolName,
		Input:     json.RawMessage(raw),
	}}}
}

func TestInvokeReturnsValueAndRunResult(t *testing.T) {
	provider := &testProvider{response: structuredResponse(`{"name":"Ada","count":3}`)}
	provider.response.Usage = agent.TokenUsage{InputTokens: 11, OutputTokens: 7}
	provider.response.Metadata = map[string]any{"request_id": "req-1"}
	a, err := agent.New(provider, "system instructions")
	if err != nil {
		t.Fatal(err)
	}

	got, err := Invoke[profile](agent.Background(), a, "make a profile")
	if err != nil {
		t.Fatal(err)
	}
	if want := (profile{Name: "Ada", Count: 3}); got.Value != want {
		t.Fatalf("Value = %#v, want %#v", got.Value, want)
	}
	if got.Run.Text != `{"name":"Ada","count":3}` || got.Run.StopReason != agent.StopEndTurn {
		t.Fatalf("Run = %#v", got.Run)
	}
	if got.Run.Usage != (agent.TokenUsage{InputTokens: 11, OutputTokens: 7}) {
		t.Fatalf("Usage = %#v", got.Run.Usage)
	}
	if got.Run.Metadata["request_id"] != "req-1" {
		t.Fatalf("Metadata = %#v", got.Run.Metadata)
	}

	params := provider.params
	if params.System != "system instructions" || len(params.Messages) != 1 {
		t.Fatalf("provider params = %#v", params)
	}
	if len(params.Tools) != 1 || params.Tools[0].Name != outputToolName {
		t.Fatalf("Tools = %#v", params.Tools)
	}
	if !reflect.DeepEqual(params.Tools[0].InputSchema, tool.GenerateSchema[profile]()) {
		t.Fatalf("schema = %#v", params.Tools[0].InputSchema)
	}
	if params.ToolChoice == nil || params.ToolChoice.Mode != tool.ChoiceTool || params.ToolChoice.Name != outputToolName {
		t.Fatalf("ToolChoice = %#v", params.ToolChoice)
	}
}

func TestInvokeErrors(t *testing.T) {
	providerFailure := errors.New("provider failed")
	tests := []struct {
		name     string
		agent    *agent.Agent
		ctx      *agent.Context
		reason   string
		provider *testProvider
	}{
		{name: "nil agent", ctx: agent.Background(), reason: "nil_agent"},
		{name: "nil context", reason: "nil_context"},
		{name: "no tool call", ctx: agent.Background(), reason: "no_tool_call", provider: &testProvider{response: &agent.ModelResponse{Text: "plain"}}},
		{name: "wrong tool", ctx: agent.Background(), reason: "wrong_tool", provider: &testProvider{response: &agent.ModelResponse{ToolCalls: []tool.Call{{Name: "other", Input: json.RawMessage(`{}`)}}}}},
		{name: "malformed JSON", ctx: agent.Background(), reason: "deserialize", provider: &testProvider{response: structuredResponse(`{broken}`)}},
		{name: "provider", ctx: agent.Background(), provider: &testProvider{err: providerFailure}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := tt.agent
			if tt.provider != nil {
				var err error
				a, err = agent.New(tt.provider, "sys")
				if err != nil {
					t.Fatal(err)
				}
			} else if tt.name == "nil context" {
				var err error
				a, err = agent.New(&testProvider{response: structuredResponse(`{}`)}, "sys")
				if err != nil {
					t.Fatal(err)
				}
			}

			_, err := Invoke[profile](tt.ctx, a, "input")
			if err == nil {
				t.Fatal("expected error")
			}
			if tt.name == "nil context" {
				if !errors.Is(err, agent.ErrNilContext) {
					t.Fatalf("error = %v, want ErrNilContext", err)
				}
				return
			}
			if tt.name == "provider" {
				var providerErr *agent.ProviderError
				if !errors.As(err, &providerErr) || !errors.Is(providerErr.Cause, providerFailure) {
					t.Fatalf("error = %T %v", err, err)
				}
				return
			}
			var structuredErr *agent.StructuredOutputError
			if !errors.As(err, &structuredErr) || structuredErr.Reason != tt.reason {
				t.Fatalf("error = %T %#v, want reason %q", err, err, tt.reason)
			}
		})
	}
}

type conversation struct {
	mu               sync.Mutex
	messages         []agent.Message
	revision         uint64
	loads            int
	saves            int
	conflictMessages []agent.Message
}

func (c *conversation) Load(context.Context, string) (agent.ConversationSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loads++
	return agent.ConversationSnapshot{
		Messages: append([]agent.Message(nil), c.messages...),
		Revision: c.revision,
	}, nil
}

func (c *conversation) Save(_ context.Context, _ string, messages []agent.Message, expectedRevision uint64) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conflictMessages != nil {
		c.messages = append([]agent.Message(nil), c.conflictMessages...)
		c.conflictMessages = nil
		c.revision++
		return c.revision, agent.ErrConversationConflict
	}
	if expectedRevision != c.revision {
		return c.revision, agent.ErrConversationConflict
	}
	c.messages = append([]agent.Message(nil), messages...)
	c.revision++
	c.saves++
	return c.revision, nil
}

func (c *conversation) List(context.Context) ([]string, error) { return nil, nil }
func (c *conversation) Delete(context.Context, string) error   { return nil }

func TestInvokeUsesGuardrailsAndPersistsDecodedOutput(t *testing.T) {
	store := &conversation{}
	provider := &testProvider{response: structuredResponse(`{"name":"before","count":1}`)}
	a, err := agent.New(provider, "sys",
		agent.WithConversationStore(store),
		agent.WithInputGuardrail(func(_ *agent.Context, input string) (string, error) {
			return input + " filtered", nil
		}),
		agent.WithOutputGuardrail(func(_ *agent.Context, _ string) (string, error) {
			return `{"name":"after","count":2}`, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Invoke[profile](agent.Background().WithConversationID("conv-1"), a, "input")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != (profile{Name: "after", Count: 2}) {
		t.Fatalf("Value = %#v", got.Value)
	}
	lastProviderMessage := provider.params.Messages[len(provider.params.Messages)-1]
	if text := lastProviderMessage.Content[0].(agent.TextBlock).Text; text != "input filtered" {
		t.Fatalf("provider input = %q", text)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.saves != 1 || len(store.messages) != 2 {
		t.Fatalf("saves = %d, messages = %#v", store.saves, store.messages)
	}
	if text := store.messages[1].Content[0].(agent.TextBlock).Text; text != got.Run.Text {
		t.Fatalf("saved output = %q, run output = %q", text, got.Run.Text)
	}
}

func TestInvokeMissingConversationIDWithStoreFails(t *testing.T) {
	store := &conversation{revision: 1}
	provider := &testProvider{response: structuredResponse(`{"name":"Ada","count":3}`)}
	a, err := agent.New(provider, "sys", agent.WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Invoke[profile](agent.Background(), a, "input")
	if !errors.Is(err, agent.ErrConversationIDRequired) {
		t.Fatalf("err = %v, want ErrConversationIDRequired", err)
	}
	if provider.params.Messages != nil || provider.params.System != "" {
		t.Fatalf("provider was called: %#v", provider.params)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.loads != 0 || store.saves != 0 || store.revision != 1 {
		t.Fatalf("store touched: loads=%d saves=%d revision=%d", store.loads, store.saves, store.revision)
	}
}

func TestInvokeDoesNotPersistInvalidTypedOutput(t *testing.T) {
	store := &conversation{}
	a, err := agent.New(&testProvider{response: structuredResponse(`{"name":42}`)}, "sys", agent.WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Invoke[profile](agent.Background().WithConversationID("conv-1"), a, "input")
	if err == nil {
		t.Fatal("expected decode error")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.saves != 0 {
		t.Fatalf("invalid output was persisted %d times", store.saves)
	}
}

func TestInvokeTokenBudgetPreservesRunOnError(t *testing.T) {
	store := &conversation{}
	provider := &testProvider{response: structuredResponse(`{"name":"Ada","count":3}`)}
	provider.response.Usage = agent.TokenUsage{InputTokens: 6, OutputTokens: 5}
	guardCalled := false
	a, err := agent.New(provider, "sys",
		agent.WithConversationStore(store),
		agent.WithTokenBudget(10),
		agent.WithOutputGuardrail(func(_ *agent.Context, output string) (string, error) {
			guardCalled = true
			return output, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Invoke[profile](agent.Background().WithConversationID("conv-1"), a, "input")
	if !errors.Is(err, agent.ErrTokenBudgetExceeded) {
		t.Fatalf("Invoke error = %v, want ErrTokenBudgetExceeded", err)
	}
	if got.Run.Usage != provider.response.Usage {
		t.Fatalf("Run.Usage = %+v, want %+v", got.Run.Usage, provider.response.Usage)
	}
	if got.Value != (profile{}) {
		t.Fatalf("Value = %+v, want zero value before decode", got.Value)
	}
	if guardCalled {
		t.Fatal("output guardrail ran after token budget was exceeded")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.saves != 0 {
		t.Fatalf("over-budget output was persisted %d times", store.saves)
	}
}

func TestInvokeTokenBudgetAllowsExactLimit(t *testing.T) {
	provider := &testProvider{response: structuredResponse(`{"name":"Ada","count":3}`)}
	provider.response.Usage = agent.TokenUsage{InputTokens: 6, OutputTokens: 5}
	a, err := agent.New(provider, "sys", agent.WithTokenBudget(11))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Invoke[profile](agent.Background(), a, "input")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value.Name != "Ada" || got.Run.Usage.Total() != 11 {
		t.Fatalf("result = %+v", got)
	}
}

type blockingProvider struct {
	firstStarted  chan struct{}
	allowFirst    chan struct{}
	secondEntered chan struct{}
	once          sync.Once
	mu            sync.Mutex
	calls         int
}

func (p *blockingProvider) Name() string { return "blocking-structured" }
func (p *blockingProvider) Stream(_ context.Context, _ agent.ModelRequest, _ func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == 1 {
		p.firstStarted <- struct{}{}
		<-p.allowFirst
	} else {
		p.once.Do(func() { p.secondEntered <- struct{}{} })
	}
	return structuredResponse(`{"name":"ok","count":1}`), nil
}

func TestInvokeSerializesSameConversation(t *testing.T) {
	provider := &blockingProvider{
		firstStarted:  make(chan struct{}, 1),
		allowFirst:    make(chan struct{}),
		secondEntered: make(chan struct{}, 1),
	}
	store := &conversation{}
	a, err := agent.New(provider, "sys", agent.WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}
	ctx := func() *agent.Context { return agent.Background().WithConversationID("shared") }
	firstDone := make(chan error, 1)
	go func() { _, err := Invoke[profile](ctx(), a, "first"); firstDone <- err }()
	select {
	case <-provider.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first call did not reach provider")
	}
	secondDone := make(chan error, 1)
	go func() { _, err := Invoke[profile](ctx(), a, "second"); secondDone <- err }()
	select {
	case <-provider.secondEntered:
		t.Fatal("second call entered before first completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(provider.allowFirst)
	for _, done := range []<-chan error{firstDone, secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("structured invocation did not finish")
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.messages) != 4 {
		t.Fatalf("saved message count = %d, want 4", len(store.messages))
	}
}

func TestInvokeReturnsConversationConflictWithoutOverwrite(t *testing.T) {
	winner := []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "concurrent winner"}}}}
	store := &conversation{conflictMessages: winner}
	a, err := agent.New(&testProvider{response: structuredResponse(`{"name":"loser","count":1}`)}, "sys", agent.WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Invoke[profile](agent.Background().WithConversationID("conv-1"), a, "input")
	if !errors.Is(err, agent.ErrConversationConflict) {
		t.Fatalf("Invoke error = %v, want ErrConversationConflict", err)
	}
	snapshot, err := store.Load(context.Background(), "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Messages, winner) || snapshot.Revision != 1 {
		t.Fatalf("winner overwritten: %+v", snapshot)
	}
}

func (c *conversation) LoadAfter(ctx context.Context, _ string, after uint64) (agent.ConversationSnapshot, error) {
	snapshot, err := c.Load(ctx, "")
	if after > uint64(len(snapshot.Messages)) {
		after = uint64(len(snapshot.Messages))
	}
	snapshot.Messages = snapshot.Messages[after:]
	snapshot.LastSequence = uint64(len(c.messages))
	return snapshot, err
}
func (c *conversation) Append(ctx context.Context, id string, messages []agent.Message, expectedRevision uint64) (agent.ConversationCursor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conflictMessages != nil {
		c.messages = append([]agent.Message(nil), c.conflictMessages...)
		c.conflictMessages = nil
		c.revision++
		return agent.ConversationCursor{}, agent.ErrConversationConflict
	}
	if expectedRevision != c.revision {
		return agent.ConversationCursor{}, agent.ErrConversationConflict
	}
	if len(messages) == 0 {
		return agent.ConversationCursor{Revision: c.revision, LastSequence: uint64(len(c.messages))}, nil
	}
	c.messages = append(c.messages, messages...)
	c.revision++
	c.saves++
	return agent.ConversationCursor{Revision: c.revision, LastSequence: uint64(len(c.messages))}, nil
}

type capabilityProvider struct {
	*testProvider
	caps agent.ModelCapabilities
}

func (p *capabilityProvider) Capabilities() agent.ModelCapabilities { return p.caps }

func TestInvokeUnknownCapabilitiesStillUsesForcedToolPath(t *testing.T) {
	provider := &capabilityProvider{
		testProvider: &testProvider{response: structuredResponse(`{"name":"Ada","count":3}`)},
		caps:         agent.ModelCapabilities{},
	}
	a, err := agent.New(provider, "sys")
	if err != nil {
		t.Fatal(err)
	}

	got, err := Invoke[profile](agent.Background(), a, "input")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value.Name != "Ada" || provider.calls != 1 {
		t.Fatalf("result = %#v, provider calls = %d", got, provider.calls)
	}
	if provider.params.ToolChoice == nil || provider.params.ToolChoice.Mode != tool.ChoiceTool {
		t.Fatalf("ToolChoice = %#v", provider.params.ToolChoice)
	}
}

func TestInvokeAutomaticOnlyCapabilitiesUsesUnspecifiedChoice(t *testing.T) {
	provider := &capabilityProvider{
		testProvider: &testProvider{response: structuredResponse(`{"name":"Ada","count":3}`)},
		caps: agent.ModelCapabilities{
			ToolUse: agent.Supported,
			ToolChoice: agent.ToolChoiceCapabilities{
				Auto:     agent.Supported,
				Specific: agent.Unsupported,
			},
		},
	}
	a, err := agent.New(provider, "sys")
	if err != nil {
		t.Fatal(err)
	}

	got, err := Invoke[profile](agent.Background(), a, "input")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value.Name != "Ada" || provider.calls != 1 {
		t.Fatalf("result = %#v, provider calls = %d", got, provider.calls)
	}
	if provider.params.ToolChoice != nil {
		t.Fatalf("ToolChoice = %#v, want nil provider-default automatic choice", provider.params.ToolChoice)
	}
	if len(provider.params.Tools) != 1 || provider.params.Tools[0].Name != outputToolName {
		t.Fatalf("Tools = %#v, want sole structured schema tool", provider.params.Tools)
	}
}

func TestInvokeKnownUnsupportedCapabilityFailsBeforeProviderCall(t *testing.T) {
	for _, caps := range []agent.ModelCapabilities{
		{ToolUse: agent.Unsupported},
		{ToolChoice: agent.ToolChoiceCapabilities{Specific: agent.Unsupported, Auto: agent.Unsupported}},
	} {
		provider := &capabilityProvider{
			testProvider: &testProvider{response: structuredResponse(`{"name":"Ada","count":3}`)},
			caps:         caps,
		}
		a, err := agent.New(provider, "sys")
		if err != nil {
			t.Fatal(err)
		}

		_, err = Invoke[profile](agent.Background(), a, "input")
		var structuredErr *agent.StructuredOutputError
		if !errors.As(err, &structuredErr) || structuredErr.Reason != "unsupported_capability" {
			t.Fatalf("capabilities %#v: error = %T %v", caps, err, err)
		}
		if !errors.Is(err, agent.ErrStructuredOutputUnsupported) {
			t.Fatalf("capabilities %#v: error does not wrap ErrStructuredOutputUnsupported: %v", caps, err)
		}
		if provider.calls != 0 {
			t.Fatalf("capabilities %#v: provider calls = %d, want 0", caps, provider.calls)
		}
	}
}

func TestInvokeFallbackKnownSpecificUnsupportedUsesAutomaticChoice(t *testing.T) {
	primary := &capabilityProvider{
		testProvider: &testProvider{response: structuredResponse(`{"name":"Ada","count":3}`)},
		caps: agent.ModelCapabilities{
			ToolUse: agent.Supported,
			ToolChoice: agent.ToolChoiceCapabilities{
				Auto:     agent.Supported,
				Specific: agent.Supported,
			},
		},
	}
	backup := &capabilityProvider{
		testProvider: &testProvider{response: structuredResponse(`{"name":"Grace","count":4}`)},
		caps: agent.ModelCapabilities{
			ToolUse: agent.Supported,
			ToolChoice: agent.ToolChoiceCapabilities{
				Auto:     agent.Supported,
				Specific: agent.Unsupported,
			},
		},
	}
	a, err := agent.New(fallback.New(primary, backup), "sys")
	if err != nil {
		t.Fatal(err)
	}

	got, err := Invoke[profile](agent.Background(), a, "input")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value.Name != "Ada" || primary.calls != 1 || backup.calls != 0 {
		t.Fatalf("result = %#v, primary calls = %d, backup calls = %d", got, primary.calls, backup.calls)
	}
	if primary.params.ToolChoice != nil {
		t.Fatalf("ToolChoice = %#v, want nil provider-default automatic choice", primary.params.ToolChoice)
	}
}

func TestInvokeDirectClaude55CapabilitiesUseAutomaticChoice(t *testing.T) {
	for _, factory := range []struct {
		name string
		new  func(...anthropic.Option) (*anthropic.AnthropicProvider, error)
	}{
		{name: "Sonnet 5.5", new: anthropic.ClaudeSonnet5_5},
		{name: "Opus 5.5", new: anthropic.ClaudeOpus5_5},
	} {
		t.Run(factory.name, func(t *testing.T) {
			model, err := factory.new()
			if err != nil {
				t.Fatal(err)
			}
			provider := &capabilityProvider{
				testProvider: &testProvider{response: structuredResponse(`{"name":"Ada","count":3}`)},
				caps:         model.Capabilities(),
			}
			a, err := agent.New(provider, "sys")
			if err != nil {
				t.Fatal(err)
			}

			if _, err := Invoke[profile](agent.Background(), a, "input"); err != nil {
				t.Fatal(err)
			}
			if provider.params.ToolChoice != nil {
				t.Fatalf("ToolChoice = %#v, want nil provider-default automatic choice", provider.params.ToolChoice)
			}
		})
	}
}

func TestInvokeBedrockClaude55CapabilitiesUseAutomaticChoice(t *testing.T) {
	for _, factory := range []struct {
		name string
		new  func(...bedrock.Option) (*bedrock.BedrockProvider, error)
	}{
		{name: "Sonnet 5.5", new: bedrock.GlobalClaudeSonnet5_5},
		{name: "Opus 5.5", new: bedrock.GlobalClaudeOpus5_5},
	} {
		t.Run(factory.name, func(t *testing.T) {
			model, err := factory.new(bedrock.WithAPIKey("test-key"))
			if err != nil {
				t.Fatal(err)
			}
			provider := &capabilityProvider{
				testProvider: &testProvider{response: structuredResponse(`{"name":"Ada","count":3}`)},
				caps:         model.Capabilities(),
			}
			a, err := agent.New(provider, "sys")
			if err != nil {
				t.Fatal(err)
			}

			if _, err := Invoke[profile](agent.Background(), a, "input"); err != nil {
				t.Fatal(err)
			}
			if provider.params.ToolChoice != nil {
				t.Fatalf("ToolChoice = %#v, want nil provider-default automatic choice", provider.params.ToolChoice)
			}
		})
	}
}
