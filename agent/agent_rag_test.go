package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
	"pgregory.net/rapid"
)

// --- Unit tests for DefaultContextFormatter ---
// Requirements: 8.1, 8.2, 8.3

func TestDefaultContextFormatter_EmptySlice(t *testing.T) {
	// Requirement 8.3: empty slice returns ""
	result := DefaultContextFormatter([]Document{})
	if result != "" {
		t.Fatalf("expected empty string for empty slice, got %q", result)
	}
}

func TestDefaultContextFormatter_NilSlice(t *testing.T) {
	// Requirement 8.3: nil slice returns ""
	result := DefaultContextFormatter(nil)
	if result != "" {
		t.Fatalf("expected empty string for nil slice, got %q", result)
	}
}

func TestDefaultContextFormatter_SingleDoc(t *testing.T) {
	// Requirement 8.2: formats with 1-based index
	docs := []Document{{Content: "hello world"}}
	result := DefaultContextFormatter(docs)
	expected := "<retrieved_context>\n[1] hello world\n</retrieved_context>"
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}

func TestDefaultContextFormatter_MultipleDocs(t *testing.T) {
	// Requirement 8.2: formats multiple docs with 1-based indices
	docs := []Document{
		{Content: "first doc"},
		{Content: "second doc"},
		{Content: "third doc"},
	}
	result := DefaultContextFormatter(docs)
	expected := "<retrieved_context>\n[1] first doc\n[2] second doc\n[3] third doc\n</retrieved_context>"
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}

// randomDocument returns a rapid generator that produces Document values
// with random non-empty Content and a small random Metadata map.
func randomDocument() *rapid.Generator[Document] {
	return rapid.Custom[Document](func(t *rapid.T) Document {
		content := rapid.StringMatching(`[a-z0-9 ]{1,50}`).Draw(t, "content")
		numMeta := rapid.IntRange(0, 3).Draw(t, "numMeta")
		meta := make(map[string]string, numMeta)
		for i := 0; i < numMeta; i++ {
			key := rapid.StringMatching(`[a-z]{1,8}`).Draw(t, fmt.Sprintf("metaKey[%d]", i))
			val := rapid.StringMatching(`[a-z0-9]{1,10}`).Draw(t, fmt.Sprintf("metaVal[%d]", i))
			meta[key] = val
		}
		return Document{Content: content, Metadata: meta}
	})
}

// Feature: rag, Property 11: DefaultContextFormatter includes all document contents with index numbers
// **Validates: Requirements 8.2**
func TestDefaultContextFormatter_ContainsAllDocs(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		docs := rapid.SliceOfN(randomDocument(), 1, 20).Draw(t, "docs")

		result := DefaultContextFormatter(docs)

		for i, doc := range docs {
			// Assert output contains the 1-based index marker [i]
			indexMarker := fmt.Sprintf("[%d]", i+1)
			if !strings.Contains(result, indexMarker) {
				t.Fatalf("output missing index marker %s for doc %d\noutput: %q", indexMarker, i, result)
			}
			// Assert output contains the document's Content
			if !strings.Contains(result, doc.Content) {
				t.Fatalf("output missing content for doc %d\ncontent: %q\noutput: %q", i, doc.Content, result)
			}
		}
	})
}

// --- Mock helpers for agent-level RAG tests ---

// countingRetriever counts how many times Retrieve is called.
type countingRetriever struct {
	mu    sync.Mutex
	count int
	docs  []Document
}

func (r *countingRetriever) Retrieve(_ context.Context, _ string) ([]Document, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count++
	return r.docs, nil
}

func (r *countingRetriever) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// iteratingProvider returns tool calls for N iterations, then a final text response.
// It requires a tool named "noop" to be registered on the agent.
type iteratingProvider struct {
	mu             sync.Mutex
	toolIterations int
	callIndex      int
}

func (p *iteratingProvider) Converse(ctx context.Context, params ConverseParams) (*ProviderResponse, error) {
	return p.ConverseStream(ctx, params, nil)
}

func (p *iteratingProvider) ConverseStream(_ context.Context, _ ConverseParams, cb StreamCallback) (*ProviderResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callIndex++

	if p.callIndex <= p.toolIterations {
		return &ProviderResponse{
			ToolCalls: []tool.Call{
				{ToolUseID: fmt.Sprintf("tc-%d", p.callIndex), Name: "noop", Input: json.RawMessage(`{}`)},
			},
		}, nil
	}

	text := "final answer"
	if cb != nil {
		cb(text)
	}
	return &ProviderResponse{Text: text}, nil
}

// Feature: rag, Property 9: Retriever is called exactly once per invocation regardless of tool iterations
// **Validates: Requirements 12.1, 12.2**
func TestAgent_RetrieverCalledOnce(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		toolIterations := rapid.IntRange(1, 5).Draw(t, "toolIterations")

		cr := &countingRetriever{
			docs: []Document{{Content: "some context", Metadata: map[string]string{}}},
		}

		provider := &iteratingProvider{toolIterations: toolIterations}

		noopTool := tool.NewRaw("noop", "does nothing", map[string]any{"type": "object"},
			func(_ context.Context, _ json.RawMessage) (string, error) {
				return "ok", nil
			})

		a, err := New(provider, prompt.Text("system prompt"), []tool.Tool{noopTool},
			WithRetriever(cr),
			WithMaxIterations(toolIterations+1),
		)
		if err != nil {
			t.Fatalf("failed to create agent: %v", err)
		}

		result, _, err := a.Invoke(context.Background(), "hello")
		if err != nil {
			t.Fatalf("Invoke failed: %v", err)
		}
		if result != "final answer" {
			t.Fatalf("expected %q, got %q", "final answer", result)
		}

		if count := cr.callCount(); count != 1 {
			t.Fatalf("expected retriever to be called exactly 1 time, got %d (with %d tool iterations)",
				count, toolIterations)
		}
	})
}

// Feature: rag, Property 10: Retrieved documents appear in the messages sent to the provider
// **Validates: Requirements 7.4**
func TestAgent_RetrievedDocsInSystemPrompt(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		docs := rapid.SliceOfN(randomDocument(), 1, 10).Draw(t, "docs")

		retriever := &countingRetriever{docs: docs}
		provider := newCapturingProvider(&ProviderResponse{Text: "ok"})

		a, err := New(provider, prompt.Text("base system prompt"), nil,
			WithRetriever(retriever),
		)
		if err != nil {
			t.Fatalf("failed to create agent: %v", err)
		}

		_, _, err = a.Invoke(context.Background(), "hello")
		if err != nil {
			t.Fatalf("Invoke failed: %v", err)
		}

		if len(provider.captured) == 0 {
			t.Fatal("expected at least one provider call")
		}

		// RAG context is injected as a message turn, not into the system prompt.
		// Collect all message text to check for doc content.
		var allMessageText strings.Builder
		for _, msg := range provider.captured[0].Messages {
			for _, block := range msg.Content {
				if tb, ok := block.(TextBlock); ok {
					allMessageText.WriteString(tb.Text)
				}
			}
		}
		messageText := allMessageText.String()

		for i, doc := range docs {
			if !strings.Contains(messageText, doc.Content) {
				t.Fatalf("messages missing content of doc[%d]\ncontent: %q\nmessages: %q",
					i, doc.Content, messageText)
			}
		}
	})
}

// --- Unit tests for agent RAG integration ---
// Requirements: 7.3, 7.5, 8.4, 12.3, 13.2, 13.3

// errorRetriever is a mock retriever that always returns an error.
type errorRetriever struct {
	err error
}

func (r *errorRetriever) Retrieve(_ context.Context, _ string) ([]Document, error) {
	return nil, r.err
}

// emptyRetriever is a mock retriever that always returns an empty slice.
type emptyRetriever struct{}

func (r *emptyRetriever) Retrieve(_ context.Context, _ string) ([]Document, error) {
	return []Document{}, nil
}

// recordingMemory records what messages are saved and returns stored history on Load.
type recordingMemory struct {
	mu        sync.Mutex
	saved     []Message
	saveCount int
}

func (m *recordingMemory) Load(_ context.Context, _ string) ([]Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saved == nil {
		return []Message{}, nil
	}
	// Return a copy.
	cp := make([]Message, len(m.saved))
	copy(cp, m.saved)
	return cp, nil
}

func (m *recordingMemory) Save(_ context.Context, _ string, msgs []Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveCount++
	m.saved = make([]Message, len(msgs))
	copy(m.saved, msgs)
	return nil
}

// TestAgent_RetrieverErrorWrapping verifies that when the retriever returns an error,
// the agent wraps it with the "retriever: " prefix.
// Requirement 7.3
func TestAgent_RetrieverErrorWrapping(t *testing.T) {
	innerErr := fmt.Errorf("connection timeout")
	retriever := &errorRetriever{err: innerErr}

	provider := newCapturingProvider(&ProviderResponse{Text: "should not reach"})

	a, err := New(provider, prompt.Text("system prompt"), nil,
		WithRetriever(retriever),
	)
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	_, _, err = a.Invoke(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error from retriever, got nil")
	}
	expected := "retriever: connection timeout"
	if err.Error() != expected {
		t.Fatalf("expected error %q, got %q", expected, err.Error())
	}
}

// TestAgent_EmptyRetrieval verifies that when the retriever returns an empty slice,
// the system prompt is passed to the provider unchanged.
// Requirement 7.5
func TestAgent_EmptyRetrieval(t *testing.T) {
	retriever := &emptyRetriever{}
	provider := newCapturingProvider(&ProviderResponse{Text: "ok"})

	originalSystem := "You are a helpful assistant."
	a, err := New(provider, prompt.Text(originalSystem), nil,
		WithRetriever(retriever),
	)
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	_, _, err = a.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}

	if len(provider.captured) == 0 {
		t.Fatal("expected at least one provider call")
	}
	if provider.captured[0].System != originalSystem {
		t.Fatalf("expected system prompt %q, got %q", originalSystem, provider.captured[0].System)
	}
}

// TestAgent_CustomContextFormatter verifies that when WithContextFormatter is configured,
// the custom formatter is used instead of DefaultContextFormatter.
// Requirement 8.4
func TestAgent_CustomContextFormatter(t *testing.T) {
	docs := []Document{
		{Content: "doc one", Metadata: map[string]string{}},
		{Content: "doc two", Metadata: map[string]string{}},
	}
	retriever := &countingRetriever{docs: docs}
	provider := newCapturingProvider(&ProviderResponse{Text: "ok"})

	customFormatter := func(docs []Document) string {
		var parts []string
		for _, d := range docs {
			parts = append(parts, "CUSTOM:"+d.Content)
		}
		return strings.Join(parts, "|")
	}

	a, err := New(provider, prompt.Text("base system"), nil,
		WithRetriever(retriever),
		WithContextFormatter(customFormatter),
	)
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	_, _, err = a.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}

	if len(provider.captured) == 0 {
		t.Fatal("expected at least one provider call")
	}

	// RAG context is injected as a message turn, not into the system prompt.
	var allMessageText strings.Builder
	for _, msg := range provider.captured[0].Messages {
		for _, block := range msg.Content {
			if tb, ok := block.(TextBlock); ok {
				allMessageText.WriteString(tb.Text)
			}
		}
	}
	messageText := allMessageText.String()

	// Verify custom formatter output is present.
	if !strings.Contains(messageText, "CUSTOM:doc one") {
		t.Fatalf("expected messages to contain custom-formatted doc one, got %q", messageText)
	}
	if !strings.Contains(messageText, "CUSTOM:doc two") {
		t.Fatalf("expected messages to contain custom-formatted doc two, got %q", messageText)
	}
	// Verify default formatter output is NOT present.
	if strings.Contains(messageText, "Relevant context:") {
		t.Fatalf("expected custom formatter to replace default, but found 'Relevant context:' in %q", messageText)
	}
}

// TestAgent_RAGContextNotPersistedToMemory verifies that when both a retriever and memory
// are configured, the RAG-augmented system prompt is NOT saved to memory — only the
// original messages are persisted.
// Requirements: 13.2, 13.3
func TestAgent_RAGContextNotPersistedToMemory(t *testing.T) {
	docs := []Document{
		{Content: "secret RAG context", Metadata: map[string]string{}},
	}
	retriever := &countingRetriever{docs: docs}
	provider := newCapturingProvider(&ProviderResponse{Text: "assistant reply"})
	mem := &recordingMemory{}

	originalSystem := "base system prompt"
	a, err := New(provider, prompt.Text(originalSystem), nil,
		WithRetriever(retriever),
		WithMemory(mem, "conv-1"),
	)
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	_, _, err = a.Invoke(context.Background(), "user question")
	if err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}

	// Verify the provider DID receive the RAG context in the messages.
	if len(provider.captured) == 0 {
		t.Fatal("expected at least one provider call")
	}
	var allMessageText strings.Builder
	for _, msg := range provider.captured[0].Messages {
		for _, block := range msg.Content {
			if tb, ok := block.(TextBlock); ok {
				allMessageText.WriteString(tb.Text)
			}
		}
	}
	if !strings.Contains(allMessageText.String(), "secret RAG context") {
		t.Fatalf("expected provider to receive RAG context in messages, got messages: %q", allMessageText.String())
	}

	// Verify memory does NOT contain the RAG context.
	mem.mu.Lock()
	savedMsgs := mem.saved
	mem.mu.Unlock()

	for i, msg := range savedMsgs {
		for _, block := range msg.Content {
			if tb, ok := block.(TextBlock); ok {
				if strings.Contains(tb.Text, "secret RAG context") {
					t.Fatalf("message[%d] in memory contains RAG context %q — should not be persisted", i, tb.Text)
				}
			}
		}
	}

	// Verify the saved messages are the user message + assistant reply.
	if len(savedMsgs) != 2 {
		t.Fatalf("expected 2 messages saved to memory, got %d", len(savedMsgs))
	}
	userTB, ok := savedMsgs[0].Content[0].(TextBlock)
	if !ok || userTB.Text != "user question" {
		t.Fatalf("expected first saved message to be user question, got %v", savedMsgs[0])
	}
	assistTB, ok := savedMsgs[1].Content[0].(TextBlock)
	if !ok || assistTB.Text != "assistant reply" {
		t.Fatalf("expected second saved message to be assistant reply, got %v", savedMsgs[1])
	}
}

// TestAgent_RAGAndToolsCoexistence verifies that when both a retriever and tools are
// configured, the retriever is called exactly once and tools work across iterations.
// Requirements: 12.3, 13.2, 13.3
func TestAgent_RAGAndToolsCoexistence(t *testing.T) {
	docs := []Document{
		{Content: "retrieved context", Metadata: map[string]string{}},
	}
	retriever := &countingRetriever{docs: docs}

	// Provider: first call returns a tool call, second call returns final text.
	provider := newCapturingProvider(
		&ProviderResponse{
			ToolCalls: []tool.Call{toolCall("tc1", "search")},
		},
		&ProviderResponse{Text: "final answer with context"},
	)

	var toolCalled bool
	searchTool := tool.NewRaw("search", "search tool", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			toolCalled = true
			return "search result", nil
		})

	a, err := New(provider, prompt.Text("system prompt"), []tool.Tool{searchTool},
		WithRetriever(retriever),
		WithMaxIterations(5),
	)
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	result, _, err := a.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}
	if result != "final answer with context" {
		t.Fatalf("expected %q, got %q", "final answer with context", result)
	}

	// Verify retriever was called exactly once.
	if count := retriever.callCount(); count != 1 {
		t.Fatalf("expected retriever to be called exactly 1 time, got %d", count)
	}

	// Verify the tool was actually called.
	if !toolCalled {
		t.Fatal("expected search tool to be called, but it was not")
	}

	// Verify both provider calls received the RAG context in messages.
	for i, params := range provider.captured {
		var allText strings.Builder
		for _, msg := range params.Messages {
			for _, block := range msg.Content {
				if tb, ok := block.(TextBlock); ok {
					allText.WriteString(tb.Text)
				}
			}
		}
		if !strings.Contains(allText.String(), "retrieved context") {
			t.Fatalf("provider call[%d] missing RAG context in messages: %q", i, allText.String())
		}
	}

	// Verify both provider calls had tool specs available.
	for i, params := range provider.captured {
		if len(params.ToolConfig) == 0 {
			t.Fatalf("provider call[%d] missing tool config", i)
		}
	}
}

// Feature: rag, Property 12: NewRetrieverTool handler returns formatted document content for any query
// **Validates: Requirements 14.2**
func TestNewRetrieverTool_HandlerFormatsResult(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		query := rapid.String().Draw(t, "query")
		if query == "" {
			t.Skip("empty query triggers retriever error")
		}

		docs := rapid.SliceOfN(randomDocument(), 1, 10).Draw(t, "docs")

		retriever := &countingRetriever{docs: docs}
		tool := NewRetrieverTool("search", "search docs", retriever)

		input, err := json.Marshal(map[string]string{"query": query})
		if err != nil {
			t.Fatalf("failed to marshal input: %v", err)
		}

		result, err := tool.Handler(context.Background(), json.RawMessage(input))
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}

		// The handler should return the DefaultContextFormatter output.
		expected := DefaultContextFormatter(docs)
		if result != expected {
			t.Fatalf("handler output does not match formatted content\nexpected: %q\ngot:      %q", expected, result)
		}

		// Also verify each document's content appears in the result.
		for i, doc := range docs {
			if !strings.Contains(result, doc.Content) {
				t.Fatalf("handler output missing content of doc[%d]\ncontent: %q\nresult: %q", i, doc.Content, result)
			}
		}
	})
}

// --- Unit tests for NewRetrieverTool ---
// Requirements: 14.3, 14.4, 14.5, 14.6

func TestNewRetrieverTool_SchemaShape(t *testing.T) {
	// Requirement 14.3: tool input schema has a single required "query" string field
	retriever := &countingRetriever{docs: nil}
	tool := NewRetrieverTool("search", "search docs", retriever)

	// Verify tool name and description.
	if tool.Spec.Name != "search" {
		t.Fatalf("expected tool name %q, got %q", "search", tool.Spec.Name)
	}
	if tool.Spec.Description != "search docs" {
		t.Fatalf("expected tool description %q, got %q", "search docs", tool.Spec.Description)
	}

	schema := tool.Spec.InputSchema
	if schema == nil {
		t.Fatal("expected non-nil InputSchema")
	}

	// Verify top-level type is "object".
	if schema["type"] != "object" {
		t.Fatalf("expected schema type %q, got %v", "object", schema["type"])
	}

	// Verify properties contains "query" with type "string".
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties to be map[string]any, got %T", schema["properties"])
	}
	queryProp, ok := props["query"].(map[string]any)
	if !ok {
		t.Fatalf("expected query property to be map[string]any, got %T", props["query"])
	}
	if queryProp["type"] != "string" {
		t.Fatalf("expected query type %q, got %v", "string", queryProp["type"])
	}

	// Verify "query" is in the required array.
	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("expected required to be []any, got %T", schema["required"])
	}
	found := false
	for _, r := range required {
		if r == "query" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'query' in required array, got %v", required)
	}
}

func TestNewRetrieverTool_EmptyRetrieval(t *testing.T) {
	// Requirement 14.5: empty retrieval returns "No relevant documents found."
	retriever := &emptyRetriever{}
	tool := NewRetrieverTool("search", "search docs", retriever)

	input, err := json.Marshal(map[string]string{"query": "anything"})
	if err != nil {
		t.Fatalf("failed to marshal input: %v", err)
	}

	result, err := tool.Handler(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("handler returned unexpected error: %v", err)
	}

	expected := "No relevant documents found."
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}

func TestNewRetrieverTool_ErrorPropagation(t *testing.T) {
	// Requirement 14.4: retriever error is propagated as tool error
	innerErr := fmt.Errorf("retrieve: query must not be empty")
	retriever := &errorRetriever{err: innerErr}
	tool := NewRetrieverTool("search", "search docs", retriever)

	input, err := json.Marshal(map[string]string{"query": "test"})
	if err != nil {
		t.Fatalf("failed to marshal input: %v", err)
	}

	_, err = tool.Handler(context.Background(), json.RawMessage(input))
	if err == nil {
		t.Fatal("expected error from handler, got nil")
	}
	if err.Error() != innerErr.Error() {
		t.Fatalf("expected error %q, got %q", innerErr.Error(), err.Error())
	}
}

func TestNewRetrieverTool_CustomFormatter(t *testing.T) {
	// Requirement 14.6: optional ContextFormatter controls how docs are formatted
	docs := []Document{
		{Content: "alpha", Metadata: map[string]string{}},
		{Content: "beta", Metadata: map[string]string{}},
	}
	retriever := &countingRetriever{docs: docs}

	customFormatter := func(docs []Document) string {
		var parts []string
		for _, d := range docs {
			parts = append(parts, ">>"+d.Content+"<<")
		}
		return strings.Join(parts, " | ")
	}

	tool := NewRetrieverTool("search", "search docs", retriever, customFormatter)

	input, err := json.Marshal(map[string]string{"query": "test"})
	if err != nil {
		t.Fatalf("failed to marshal input: %v", err)
	}

	result, err := tool.Handler(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("handler returned unexpected error: %v", err)
	}

	expected := ">>alpha<< | >>beta<<"
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}

	// Verify the default formatter was NOT used.
	if strings.Contains(result, "Relevant context:") {
		t.Fatalf("expected custom formatter to replace default, but found default format in %q", result)
	}
}
