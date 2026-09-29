package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
// WithBackgroundNotify wires the notify callback onto the registry
// ---------------------------------------------------------------------------

func TestWithBackgroundNotify_WiredOntoRegistry(t *testing.T) {
	bt := validBackgroundTool("bg-notify")
	store := newTestMemoryStore()

	var called bool
	notifyFn := func(convID, msg string) { called = true }
	_ = called // suppress unused warning; we only check registry wiring

	a, err := New(mockProvider{}, "sys", WithTools(bt),
		WithConversationStore(store),
		WithBackgroundNotify(notifyFn),
	)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if a.backgroundRegistry == nil {
		t.Fatal("expected backgroundRegistry to be non-nil when a Background_Tool is registered")
	}
	if a.backgroundRegistry.notify == nil {
		t.Error("expected registry.notify to be non-nil when WithBackgroundNotify is used")
	}
}

func TestWithoutBackgroundNotify_NotifyIsNil(t *testing.T) {
	bt := validBackgroundTool("bg-no-notify")
	store := newTestMemoryStore()

	a, err := New(mockProvider{}, "sys", WithTools(bt),
		WithConversationStore(store),
	)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if a.backgroundRegistry == nil {
		t.Fatal("expected backgroundRegistry to be non-nil when a Background_Tool is registered")
	}
	if a.backgroundRegistry.notify != nil {
		t.Error("expected registry.notify to be nil when WithBackgroundNotify is not used")
	}

	// Close should not error even without a notify callback.
	_ = a.Shutdown(context.Background())
}

// ---------------------------------------------------------------------------
// Requirements 11.3, 13.3, 13.4: v1 scope documentation notes
// ---------------------------------------------------------------------------

func TestDocumentation_V1ScopeNotes(t *testing.T) {
	// Locate background.go relative to this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to determine test file path via runtime.Caller")
	}
	bgFile := filepath.Join(filepath.Dir(thisFile), "background.go")

	data, err := os.ReadFile(bgFile)
	if err != nil {
		t.Fatalf("failed to read background.go: %v", err)
	}
	src := string(data)

	// Asserts the in-memory-only note.
	if !strings.Contains(src, "process memory only") {
		t.Error("background.go package doc must contain the in-memory-only note ('process memory only')")
	}

	// Asserts the abandonment-on-exit note.
	if !strings.Contains(src, "abandoned") && !strings.Contains(src, "results are lost") {
		t.Error("background.go package doc must contain the abandonment-on-exit note ('abandoned' or 'results are lost')")
	}

	// Asserts the no-streaming-notification note.
	if !strings.Contains(src, "Streaming") || !strings.Contains(src, "future extension") {
		t.Error("background.go package doc must contain the no-streaming-notification note ('Streaming' and 'future extension')")
	}
}
