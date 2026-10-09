package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// validBackgroundTool returns a well-formed Background_Tool for use in validation tests.
func validBackgroundTool(name string) tool.Tool {
	return newTestBackgroundRaw(name, "does background work", "acknowledged",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "done", nil
		})
}

// ---------------------------------------------------------------------------
// Background_Tool empty Ack rejected at agent.New
// ---------------------------------------------------------------------------

func TestNewAgent_BackgroundTool_EmptyAck(t *testing.T) {
	bt := newTestBackgroundRaw("bg-tool", "desc", "", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "done", nil
		})

	_, err := New(mockProvider{}, "sys", WithTools(bt))
	if err == nil {
		t.Fatal("expected error for background tool with empty ack, got nil")
	}
	if !strings.Contains(err.Error(), "ack") {
		t.Errorf("expected error to mention 'ack', got: %v", err)
	}
	if !strings.Contains(err.Error(), "bg-tool") {
		t.Errorf("expected error to mention tool name 'bg-tool', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Background_Tool nil handler rejected at agent.New
// ---------------------------------------------------------------------------

func TestNewAgent_BackgroundTool_NilHandler(t *testing.T) {
	bt := newTestBackgroundRaw("bg-nil", "desc", "ack-string", map[string]any{"type": "object"}, nil)

	_, err := New(mockProvider{}, "sys", WithTools(bt))
	if err == nil {
		t.Fatal("expected error for background tool with nil handler, got nil")
	}
	if !strings.Contains(err.Error(), "handler") {
		t.Errorf("expected error to mention 'handler', got: %v", err)
	}
	if !strings.Contains(err.Error(), "bg-nil") {
		t.Errorf("expected error to mention tool name 'bg-nil', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Background_Tool requires a Conversation_Store at agent.New
// ---------------------------------------------------------------------------

func TestNewAgent_BackgroundTool_NoConversationStore(t *testing.T) {
	bt := validBackgroundTool("bg-noconv")

	_, err := New(mockProvider{}, "sys", WithTools(bt))
	if err == nil {
		t.Fatal("expected error for background tool without conversation store, got nil")
	}
	if !strings.Contains(err.Error(), "WithConversationStore") {
		t.Errorf("expected error to mention WithConversationStore, got: %v", err)
	}
	if !strings.Contains(err.Error(), "bg-noconv") {
		t.Errorf("expected error to mention tool name 'bg-noconv', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Background_Tool: agent.New succeeds with a conversation store
// ---------------------------------------------------------------------------

func TestNewAgent_BackgroundTool_WithConversationStore_Succeeds(t *testing.T) {
	bt := validBackgroundTool("bg-ok")
	store := newTestMemoryStore()

	a, err := New(mockProvider{}, "sys", WithTools(bt),
		WithConversationStore(store),
	)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if !a.HasTool("bg-ok") {
		t.Error("expected bg-ok tool to be registered")
	}
}

// ---------------------------------------------------------------------------
// WithBackgroundNotify public behavior
// ---------------------------------------------------------------------------

type backgroundNotification struct {
	conversationID string
	text           string
}

// runBackgroundTurn drives a conversation through one background dispatch and
// its completion re-entry turn, returning the agent after the handler ran.
func runBackgroundTurn(t *testing.T, convID string, opts ...Option) *Agent {
	t.Helper()
	bt := validBackgroundTool("bg-notify")
	provider := &approvalBatchProvider{responses: []*ModelResponse{
		{ToolCalls: []tool.Call{{ToolUseID: "tuid-1", Name: "bg-notify", Input: json.RawMessage(`{}`)}}},
		{Text: "started"},
		{Text: "background finished"},
	}}
	opts = append([]Option{WithTools(bt), WithConversationStore(newTestMemoryStore())}, opts...)
	a, err := New(provider, "sys", opts...)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if _, err := a.Invoke(Background().WithConversationID(convID), "run it"); err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}
	return a
}

func TestWithBackgroundNotify_CallbackFiresOnceWithConversationAndFinalText(t *testing.T) {
	notified := make(chan backgroundNotification, 4)
	a := runBackgroundTurn(t, "conv-notify", WithBackgroundNotify(func(conversationID, agentMessage string) {
		notified <- backgroundNotification{conversationID: conversationID, text: agentMessage}
	}))

	select {
	case got := <-notified:
		if got.conversationID != "conv-notify" || got.text != "background finished" {
			t.Fatalf("notification = %+v, want conversation conv-notify and text %q", got, "background finished")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for background notification")
	}

	if err := a.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}
	select {
	case extra := <-notified:
		t.Fatalf("callback fired more than once: %+v", extra)
	default:
	}
}

func TestBackgroundCompletion_WithoutNotifyCallbackCompletesQuietly(t *testing.T) {
	a := runBackgroundTurn(t, "conv-no-notify")

	if err := a.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}
	snapshot, err := a.conversation.Load(context.Background(), "conv-no-notify")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	last := snapshot.Messages[len(snapshot.Messages)-1]
	if got := last.Content[0].(TextBlock).Text; got != "background finished" {
		t.Fatalf("final persisted message = %q, want re-entry result", got)
	}
}

// ---------------------------------------------------------------------------
// Background_Tool: Principal isolation across the goroutine boundary
// ---------------------------------------------------------------------------

// principalCapturingToolLogObserver records the Principal seen on every
// ToolLogRecord emitted for background dispatch/completion log entries.
type principalCapturingToolLogObserver struct {
	mu      sync.Mutex
	records []ToolLogRecord
}

func (o *principalCapturingToolLogObserver) ObserveToolLog(ctx context.Context, record ToolLogRecord) context.Context {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.records = append(o.records, record)
	return ctx
}

// TestBackgroundDispatch_PrincipalNotAliasedWithOriginatingContext verifies
// that the Principal captured for a Background_Tool dispatch (propagated via
// invocationConfig into backgroundDispatch, and surfaced on ToolLogRecord by
// observeBackgroundLog) is independent of the originating context's
// Principal: mutating the value returned by the originating context's
// Principal() after dispatch must not affect the value already captured for
// the background dispatch/completion log records.
func TestBackgroundDispatch_PrincipalNotAliasedWithOriginatingContext(t *testing.T) {
	handlerDone := make(chan struct{})
	bt := newTestBackgroundRaw("bg-principal", "background tool", "started",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			close(handlerDone)
			return "done", nil
		},
	)

	observer := &principalCapturingToolLogObserver{}
	store := newTestMemoryStore()
	sp := &approvalBatchProvider{responses: []*ModelResponse{
		{ToolCalls: []tool.Call{{ToolUseID: "tuid-1", Name: "bg-principal", Input: json.RawMessage(`{}`)}}},
		{Text: "final"},
	}}
	a, err := New(sp, "sys", WithTools(bt), WithConversationStore(store), WithObserver(observer))
	if err != nil {
		t.Fatal(err)
	}

	ctx := Background().WithConversationID("conv-principal-clone").WithPrincipal(Principal{
		ID:    "u1",
		Roles: []string{"user"},
		Attrs: map[string]string{"org": "a"},
	})

	if _, err := a.Invoke(ctx, "run it"); err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}

	// dispatch() logs the "background dispatch" ToolLogRecord synchronously
	// before Invoke returns, so at least one record with a Principal should
	// already be captured here.
	observer.mu.Lock()
	if len(observer.records) == 0 {
		observer.mu.Unlock()
		t.Fatal("expected at least one ToolLogRecord for the background dispatch")
	}
	dispatchPrincipal := observer.records[0].Principal
	observer.mu.Unlock()

	if dispatchPrincipal.ID != "u1" || len(dispatchPrincipal.Roles) != 1 || dispatchPrincipal.Roles[0] != "user" {
		t.Fatalf("dispatch record principal = %+v, want ID=u1 Roles=[user]", dispatchPrincipal)
	}

	// Mutate a Principal obtained from the originating context after
	// dispatch. If invocationConfig.principal (and hence backgroundDispatch's
	// copy) aliased the same backing slice/map, this would corrupt the
	// already-captured dispatch record.
	if cp, ok := ctx.Principal(); ok {
		if len(cp.Roles) > 0 {
			cp.Roles[0] = "admin"
		}
		cp.Attrs["org"] = "mutated"
	}

	<-handlerDone
	a.backgroundRegistry.wg.Wait()

	observer.mu.Lock()
	defer observer.mu.Unlock()
	for _, rec := range observer.records {
		if rec.Principal.ID != "u1" {
			continue
		}
		if len(rec.Principal.Roles) != 1 || rec.Principal.Roles[0] != "user" {
			t.Errorf("record principal Roles = %v, want [user] (post-dispatch mutation leaked in)", rec.Principal.Roles)
		}
		if rec.Principal.Attrs["org"] != "a" {
			t.Errorf("record principal Attrs[org] = %q, want %q (post-dispatch mutation leaked in)", rec.Principal.Attrs["org"], "a")
		}
	}
}
