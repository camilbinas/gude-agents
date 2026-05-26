package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// validBackgroundTool returns a well-formed Background_Tool for use in validation tests.
func validBackgroundTool(name string) tool.Tool {
	return tool.NewBackgroundRaw(name, "does background work", "acknowledged",
		map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "done", nil
		})
}

// ---------------------------------------------------------------------------
// Background_Tool empty Ack rejected at agent.New
// ---------------------------------------------------------------------------

func TestNewAgent_BackgroundTool_EmptyAck(t *testing.T) {
	bt := tool.NewBackgroundRaw("bg-tool", "desc", "", map[string]any{"type": "object"},
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "done", nil
		})

	_, err := New(mockProvider{}, prompt.Text("sys"), []tool.Tool{bt})
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
	bt := tool.NewBackgroundRaw("bg-nil", "desc", "ack-string", map[string]any{"type": "object"}, nil)

	_, err := New(mockProvider{}, prompt.Text("sys"), []tool.Tool{bt})
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

	_, err := New(mockProvider{}, prompt.Text("sys"), []tool.Tool{bt})
	if err == nil {
		t.Fatal("expected error for background tool without conversation store, got nil")
	}
	if !strings.Contains(err.Error(), "WithConversation") || !strings.Contains(err.Error(), "WithSharedConversation") {
		t.Errorf("expected error to mention WithConversation or WithSharedConversation, got: %v", err)
	}
	if !strings.Contains(err.Error(), "bg-noconv") {
		t.Errorf("expected error to mention tool name 'bg-noconv', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Background_Tool: agent.New succeeds when WithConversation is supplied
// ---------------------------------------------------------------------------

func TestNewAgent_BackgroundTool_WithConversation_Succeeds(t *testing.T) {
	bt := validBackgroundTool("bg-ok")
	store := newTestMemoryStore()

	a, err := New(mockProvider{}, prompt.Text("sys"), []tool.Tool{bt},
		WithConversation(store, "conv-1"),
	)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if !a.HasTool("bg-ok") {
		t.Error("expected bg-ok tool to be registered")
	}
}

func TestNewAgent_BackgroundTool_WithSharedConversation_Succeeds(t *testing.T) {
	bt := validBackgroundTool("bg-shared")
	store := newTestMemoryStore()

	a, err := New(mockProvider{}, prompt.Text("sys"), []tool.Tool{bt},
		WithSharedConversation(store),
	)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if !a.HasTool("bg-shared") {
		t.Error("expected bg-shared tool to be registered")
	}
}

// ---------------------------------------------------------------------------
// RegisterTool returns an error for Background_Tools without a Conversation_Store
// ---------------------------------------------------------------------------

func TestRegisterTool_BackgroundTool_NoConversationStore(t *testing.T) {
	// Create an agent without any conversation store.
	a, err := New(mockProvider{}, prompt.Text("sys"), nil)
	if err != nil {
		t.Fatal(err)
	}

	bt := validBackgroundTool("bg-register")
	err = a.RegisterTool(bt)
	if err == nil {
		t.Fatal("expected error when registering background tool without conversation store, got nil")
	}
	if !strings.Contains(err.Error(), "WithConversation") || !strings.Contains(err.Error(), "WithSharedConversation") {
		t.Errorf("expected error to mention WithConversation or WithSharedConversation, got: %v", err)
	}
	if !strings.Contains(err.Error(), "bg-register") {
		t.Errorf("expected error to mention tool name 'bg-register', got: %v", err)
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

	a, err := New(mockProvider{}, prompt.Text("sys"), []tool.Tool{bt},
		WithConversation(store, "conv-1"),
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

	a, err := New(mockProvider{}, prompt.Text("sys"), []tool.Tool{bt},
		WithConversation(store, "conv-1"),
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
	a.Close()
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
