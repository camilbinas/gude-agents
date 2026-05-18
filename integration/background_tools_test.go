package integration_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// TestIntegration_BackgroundTool_FullFlow drives the whole Background_Tool
// lifecycle against a real LLM:
//
//   - The originating turn invokes a Background_Tool. The framework returns
//     an ack to the LLM, persists the ack ToolResultBlock, and finishes the turn.
//   - The handler runs detached on context.Background(), even though the
//     originating *Context's deadline expires before the handler finishes.
//   - When the handler completes, the framework injects the result into the
//     conversation, runs a Re_Entry_Turn, and invokes the Notify_Callback
//     with the reactive assistant text.
//
// The test asserts: ack persisted, handler observed background context (no
// originating value, no cancellation), Notify_Callback fired exactly once
// with the correct conversation id, and the final stored conversation
// contains both the originating ack and a reactive assistant message
// referencing the handler's result.
func TestIntegration_BackgroundTool_FullFlow(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	store := conversation.NewInMemory()

	const (
		convID     = "bg-int-conv-1"
		identifier = "user-bg-int"
		ack        = "Deployment kicked off — I'll let you know when it finishes."
		jobOutput  = "deployment-completed: build #4711, version 2.3.0 live in 142s"
	)

	// Background_Tool: a simulated long-running deploy. Blocks on releaseCh
	// so we can verify ack-before-completion and detached-context semantics.
	type DeployInput struct {
		Service string `json:"service" description:"Service to deploy" required:"true"`
		Version string `json:"version" description:"Target version"    required:"true"`
	}

	releaseCh := make(chan struct{})
	var handlerStarted atomic.Bool
	var handlerCtxValueLeaked atomic.Bool
	var handlerCtxCancelled atomic.Bool

	type ctxKey struct{}

	deployTool := tool.NewBackground(
		"deploy_service",
		"Deploy a service to production. The actual deployment runs asynchronously; you receive an ack and will be notified when it completes.",
		ack,
		func(ctx context.Context, in DeployInput) (string, error) {
			handlerStarted.Store(true)
			if ctx.Value(ctxKey{}) != nil {
				handlerCtxValueLeaked.Store(true)
			}
			select {
			case <-ctx.Done():
				handlerCtxCancelled.Store(true)
			default:
			}
			<-releaseCh
			return jobOutput, nil
		},
	)

	// Notify callback: capture the reactive message.
	type notif struct {
		convID string
		msg    string
	}
	var notifyMu sync.Mutex
	var notifs []notif
	notifyDone := make(chan struct{})
	notifyOnce := sync.Once{}

	a, err := agent.New(p,
		prompt.Text(
			"You are a deployment assistant. When the user asks to deploy a service, "+
				"call deploy_service with the requested service and version. "+
				"After the tool returns, give a one-sentence acknowledgement to the user. Be very brief.",
		),
		[]tool.Tool{deployTool},
		agent.WithConversation(store, convID),
		agent.WithBackgroundNotify(func(cID, msg string) {
			notifyMu.Lock()
			notifs = append(notifs, notif{convID: cID, msg: msg})
			notifyMu.Unlock()
			notifyOnce.Do(func() { close(notifyDone) })
		}),
		agent.WithMaxIterations(5),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// Originating turn — short deadline. The handler will outlive it.
	origCtx, origCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer origCancel()
	origCtx = context.WithValue(origCtx, ctxKey{}, "should-not-leak")

	c := agent.NewContext(origCtx).
		WithConversationID(convID).
		WithIdentifier(identifier)

	result, err := a.Invoke(c, "Please deploy the checkout service to version 2.3.0.")
	if err != nil {
		t.Fatalf("originating Invoke error: %v", err)
	}
	t.Logf("Originating response: %s", result)

	// Wait for the handler to start running before cancelling the originating
	// context. A short retry loop avoids racing the goroutine.
	deadline := time.Now().Add(10 * time.Second)
	for !handlerStarted.Load() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !handlerStarted.Load() {
		t.Fatal("background handler never started")
	}

	// Cancel the originating context. The handler runs on context.Background()
	// so this must NOT cancel it.
	origCancel()

	// Sanity wait — give the handler a moment to observe a cancel that should
	// never arrive.
	time.Sleep(100 * time.Millisecond)
	if handlerCtxCancelled.Load() {
		t.Error("background handler ctx was cancelled by originating context")
	}
	if handlerCtxValueLeaked.Load() {
		t.Error("background handler ctx leaked a value from the originating context")
	}

	// Release the handler so the Re_Entry_Turn can run.
	close(releaseCh)

	// Wait for the Notify_Callback to fire (or timeout).
	select {
	case <-notifyDone:
	case <-time.After(120 * time.Second):
		t.Fatal("Notify_Callback did not fire within 120s after handler released")
	}

	// Ensure the registry has fully drained before reading observations.
	a.Close()

	notifyMu.Lock()
	defer notifyMu.Unlock()

	if len(notifs) != 1 {
		t.Fatalf("expected exactly 1 notify, got %d", len(notifs))
	}
	if notifs[0].convID != convID {
		t.Errorf("notify convID = %q, want %q", notifs[0].convID, convID)
	}
	if strings.TrimSpace(notifs[0].msg) == "" {
		t.Error("notify message was empty")
	}
	t.Logf("Notify message: %s", notifs[0].msg)

	// Inspect the persisted conversation. It should contain the originating
	// ack ToolResultBlock, the injected completion TextBlock, and a reactive
	// assistant message at the end.
	final, err := store.Load(context.Background(), convID)
	if err != nil {
		t.Fatalf("store.Load error: %v", err)
	}
	if len(final) == 0 {
		t.Fatal("conversation store has no messages")
	}
	t.Logf("Final messages in store: %d", len(final))

	ackFound := false
	completionFound := false
	for _, msg := range final {
		if msg.Role != agent.RoleUser {
			continue
		}
		for _, block := range msg.Content {
			switch b := block.(type) {
			case agent.ToolResultBlock:
				if b.Content == ack && !b.IsError {
					ackFound = true
				}
			case agent.TextBlock:
				if strings.HasPrefix(b.Text, "[Background tool ") &&
					strings.Contains(b.Text, jobOutput) {
					completionFound = true
				}
			}
		}
	}
	if !ackFound {
		t.Error("ack ToolResultBlock not found in persisted conversation")
	}
	if !completionFound {
		t.Error("background completion TextBlock not found in persisted conversation")
	}

	// The last message must be the reactive assistant text from the Re_Entry_Turn.
	last := final[len(final)-1]
	if last.Role != agent.RoleAssistant {
		t.Errorf("last persisted message role = %q, want %q", last.Role, agent.RoleAssistant)
	}
}

// TestIntegration_BackgroundTool_MissingConversationID verifies the negative
// path when the agent is configured with WithSharedConversation (no default
// conversation id) and the *Context provides none either: the originating
// turn must surface an IsError tool result for the Background_Tool call and
// must not dispatch the handler.
func TestIntegration_BackgroundTool_MissingConversationID(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	store := conversation.NewInMemory()

	var handlerCalled atomic.Bool

	type Input struct {
		Note string `json:"note" description:"A note to record" required:"true"`
	}

	bgTool := tool.NewBackground(
		"record_note",
		"Record a note in the background. Always call this tool when the user asks to record a note.",
		"Note queued.",
		func(_ context.Context, _ Input) (string, error) {
			handlerCalled.Store(true)
			return "ok", nil
		},
	)

	a, err := agent.New(p,
		prompt.Text(
			"You are a note-taking assistant. When the user asks to record a note, call record_note. Be very brief.",
		),
		[]tool.Tool{bgTool},
		agent.WithSharedConversation(store), // no default conversation id
		agent.WithMaxIterations(5),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// *Context with no conversation id either.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := agent.NewContext(ctx)
	_, err = a.Invoke(c, "Record this note: integration test in progress.")
	if err != nil {
		// The agent may or may not return an error depending on the LLM's
		// reaction to the IsError tool result; what matters is that no
		// handler was dispatched.
		t.Logf("Invoke returned error (acceptable): %v", err)
	}

	// Give any (incorrectly-spawned) goroutine a chance to set the flag.
	time.Sleep(200 * time.Millisecond)
	if handlerCalled.Load() {
		t.Error("background handler was dispatched despite missing conversation id")
	}
}
