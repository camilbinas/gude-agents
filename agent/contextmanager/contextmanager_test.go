package contextmanager

import (
	"context"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/rag"
)

func message(text string) agent.Message {
	return agent.Message{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: text}}}
}

func TestRollingSummaryPreservesCanonicalHistoryAndUsesRangeBoundary(t *testing.T) {
	store := conversation.NewInMemory()
	ctx := context.Background()
	messages := []agent.Message{message(strings.Repeat("a", 100)), message(strings.Repeat("b", 100)), message(strings.Repeat("c", 100)), message(strings.Repeat("d", 100)), message(strings.Repeat("e", 100)), message(strings.Repeat("f", 100))}
	if _, err := store.Append(ctx, "c", messages, 0); err != nil {
		t.Fatal(err)
	}
	calls := 0
	manager, err := NewRollingSummary(store, func(_ context.Context, existing string, covered []agent.Message) (string, error) {
		calls++
		return existing + " summary-" + string(rune('0'+len(covered))), nil
	}, WithMaxInputTokens(20), WithPreserveRecentTurns(1))
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := manager.HistoryBoundary(ctx, "c")
	if err != nil || boundary != 0 {
		t.Fatalf("boundary=%d,%v", boundary, err)
	}
	tail, err := store.LoadAfter(ctx, "c", boundary)
	if err != nil {
		t.Fatal(err)
	}
	out, err := manager.Prepare(ctx, agent.ContextManagerInput{ConversationID: "c", Recent: tail.Messages, Revision: tail.Revision, LastSequence: tail.LastSequence})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(out.Messages) < 3 {
		t.Fatalf("calls=%d output=%#v", calls, out.Messages)
	}
	// Summary is derived; every original event stays in the canonical log.
	all, _ := store.Load(ctx, "c")
	if len(all.Messages) != 6 || all.Revision != 1 || all.LastSequence != 6 {
		t.Fatalf("canonical=%+v", all)
	}
	state, _ := store.LoadContextState(ctx, "c", "rolling_summary")
	if len(state.Data) == 0 {
		t.Fatal("summary state was not persisted")
	}
	boundary, err = manager.HistoryBoundary(ctx, "c")
	if err != nil || boundary == 0 {
		t.Fatalf("updated boundary=%d,%v", boundary, err)
	}
	recent, _ := store.LoadAfter(ctx, "c", boundary)
	if len(recent.Messages) >= len(all.Messages) {
		t.Fatalf("range read did not reduce history: %d", len(recent.Messages))
	}
}

func TestWindowUsesTailAndKeepsToolPairs(t *testing.T) {
	store := conversation.NewInMemory()
	ctx := context.Background()
	events := []agent.Message{
		message("old"),
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.ToolUseBlock{ToolUseID: "x", Name: "t", Input: []byte(`{}`)}}},
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.ToolResultBlock{ToolUseID: "x", Content: "ok"}}},
		message("new"),
	}
	if _, err := store.Append(ctx, "c", events, 0); err != nil {
		t.Fatal(err)
	}
	w, err := NewWindow(store, 2)
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := w.HistoryBoundary(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	tail, _ := store.LoadAfter(ctx, "c", boundary)
	if hasOrphanedToolResult(tail.Messages) {
		t.Fatalf("window orphaned tool result: %#v", tail.Messages)
	}
}

// captureProvider records the last ModelRequest it received and returns a
// fixed summary. It implements agent.Provider.
type captureProvider struct {
	last agent.ModelRequest
}

func (p *captureProvider) Name() string { return "capture" }

func (p *captureProvider) Stream(_ context.Context, req agent.ModelRequest, _ func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	p.last = req
	return &agent.ModelResponse{Text: "SUMMARY"}, nil
}

// TestProviderSummarizerIncludesToolBlocksAndEndsWithUser verifies that the
// summarizer renders tool-use and tool-result content into its input and that
// the request is a single user message (provider-portable: never ends with an
// assistant turn, even when the covered window does).
func TestProviderSummarizerIncludesToolBlocksAndEndsWithUser(t *testing.T) {
	p := &captureProvider{}
	summarize := ProviderSummarizer(p, "sys-prompt")

	covered := []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "what's the weather in Berlin?"}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{
			agent.ToolUseBlock{ToolUseID: "t1", Name: "get_weather", Input: []byte(`{"city":"Berlin"}`)},
		}},
		{Role: agent.RoleUser, Content: []agent.ContentBlock{
			agent.ToolResultBlock{ToolUseID: "t1", Content: "22C sunny"},
		}},
		// Covered window deliberately ends on an assistant turn.
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "It's 22C and sunny."}}},
	}

	out, err := summarize(context.Background(), "", covered)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if out != "SUMMARY" {
		t.Fatalf("summary text = %q, want SUMMARY", out)
	}

	req := p.last
	if req.System != "sys-prompt" {
		t.Errorf("system = %q, want sys-prompt", req.System)
	}
	// Provider-portability guard: exactly one message, and it must be a user turn.
	if len(req.Messages) != 1 {
		t.Fatalf("message count = %d, want 1 (flattened)", len(req.Messages))
	}
	if req.Messages[0].Role != agent.RoleUser {
		t.Fatalf("final message role = %q, want user", req.Messages[0].Role)
	}
	tb, ok := req.Messages[0].Content[0].(agent.TextBlock)
	if !ok {
		t.Fatalf("flattened content is not a TextBlock: %#v", req.Messages[0].Content[0])
	}
	text := tb.Text
	for _, want := range []string{
		"what's the weather in Berlin?",
		`get_weather`,
		`"city":"Berlin"`,
		"tool result: 22C sunny",
		"It's 22C and sunny.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("flattened summarizer input missing %q\n--- got ---\n%s", want, text)
		}
	}
}

// TestProviderSummarizerRendersToolError verifies tool errors are rendered.
func TestProviderSummarizerRendersToolError(t *testing.T) {
	p := &captureProvider{}
	summarize := ProviderSummarizer(p, "sys")
	_, err := summarize(context.Background(), "prior summary", []agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{
			agent.ToolResultBlock{ToolUseID: "t1", Content: "boom", IsError: true},
		}},
	})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	text := p.last.Messages[0].Content[0].(agent.TextBlock).Text
	if !strings.Contains(text, "tool error: boom") {
		t.Errorf("missing rendered tool error\n--- got ---\n%s", text)
	}
	if !strings.Contains(text, "Summary so far:\nprior summary") {
		t.Errorf("missing existing-summary preamble\n--- got ---\n%s", text)
	}
}

type staticRetriever struct{ docs []rag.Document }

func (r staticRetriever) Retrieve(context.Context, string) ([]rag.Document, error) {
	return r.docs, nil
}

func TestWindowProjectsTransientRAGContextWithoutPersistence(t *testing.T) {
	store := conversation.NewInMemory()
	ctx := context.Background()
	if _, err := store.Append(ctx, "c", []agent.Message{message("older canonical")}, 0); err != nil {
		t.Fatal(err)
	}
	window, err := NewWindow(store, 1)
	if err != nil {
		t.Fatal(err)
	}
	provider := &captureProvider{}
	a, err := agent.New(provider, "sys", agent.WithConversationStore(store), agent.WithContextManager(window), agent.WithRetriever(staticRetriever{docs: []rag.Document{{Content: "retrieved transient"}}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Invoke(agent.Background().WithConversationID("c"), "current user"); err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, msg := range provider.last.Messages {
		for _, block := range msg.Content {
			if text, ok := block.(agent.TextBlock); ok {
				texts = append(texts, text.Text)
			}
		}
	}
	projected := strings.Join(texts, "\n")
	if !strings.Contains(projected, "retrieved transient") || !strings.Contains(projected, "current user") {
		t.Fatalf("Window provider projection = %q", projected)
	}
	if strings.Index(projected, "retrieved transient") > strings.Index(projected, "current user") {
		t.Fatalf("transient context must precede current message: %q", projected)
	}
	snapshot, err := store.Load(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range snapshot.Messages {
		for _, block := range msg.Content {
			if text, ok := block.(agent.TextBlock); ok && strings.Contains(text.Text, "retrieved transient") {
				t.Fatalf("transient RAG context was persisted: %#v", snapshot.Messages)
			}
		}
	}
}
