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
	"github.com/camilbinas/gude-agents/agent/tool"
)

func TestIntegration_BackgroundTool_FullFlow(t *testing.T) {
	t.Parallel()
	p := newTestProvider(t)
	store := conversation.NewInMemory()

	const (
		convID    = "bg-int-conv-1"
		identity  = "user-bg-int"
		ack       = "Deployment kicked off — I'll let you know when it finishes."
		jobOutput = "deployment-completed: build #4711, version 2.3.0 live in 142s"
	)

	type DeployInput struct {
		Service string `json:"service" description:"Service to deploy" required:"true"`
		Version string `json:"version" description:"Target version" required:"true"`
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

	type notif struct {
		convID string
		msg    string
	}
	var notifyMu sync.Mutex
	var notifs []notif
	notifyDone := make(chan struct{})
	notifyOnce := sync.Once{}

	a, err := agent.New(
		p,
		"You are a deployment assistant. When the user asks to deploy a service, call deploy_service with the requested service and version. "+
			"After the tool returns, give a one-sentence acknowledgement to the user. Be very brief.",
		agent.WithTools(deployTool),
		agent.WithConversationStore(store),
		agent.WithBackgroundNotify(func(conversationID, msg string) {
			notifyMu.Lock()
			notifs = append(notifs, notif{convID: conversationID, msg: msg})
			notifyMu.Unlock()
			notifyOnce.Do(func() { close(notifyDone) })
		}),
		agent.WithMaxIterations(5),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	})

	origCtx, origCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer origCancel()
	origCtx = context.WithValue(origCtx, ctxKey{}, "should-not-leak")

	c := agent.NewContext(origCtx).
		WithConversationID(convID).
		WithIdentity(identity)

	result, err := a.Invoke(c, "Please deploy the checkout service to version 2.3.0.")
	if err != nil {
		t.Fatalf("originating Invoke error: %v", err)
	}
	t.Logf("Originating response: %s", result.Text)

	deadline := time.Now().Add(10 * time.Second)
	for !handlerStarted.Load() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !handlerStarted.Load() {
		t.Fatal("background handler never started")
	}

	origCancel()
	time.Sleep(100 * time.Millisecond)
	if handlerCtxCancelled.Load() {
		t.Error("background handler ctx was cancelled by originating context")
	}
	if handlerCtxValueLeaked.Load() {
		t.Error("background handler ctx leaked a value from the originating context")
	}

	close(releaseCh)
	select {
	case <-notifyDone:
	case <-time.After(120 * time.Second):
		t.Fatal("Notify_Callback did not fire within 120s after handler released")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer shutdownCancel()
	if err := a.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

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

	final, err := store.Load(context.Background(), convID)
	if err != nil {
		t.Fatalf("store.Load error: %v", err)
	}
	if len(final.Messages) == 0 {
		t.Fatal("conversation store has no messages")
	}
	t.Logf("Final messages in store: %d", len(final.Messages))

	ackFound := false
	completionFound := false
	for _, msg := range final.Messages {
		if msg.Role != agent.RoleUser {
			continue
		}
		for _, block := range msg.Content {
			switch block := block.(type) {
			case agent.ToolResultBlock:
				if block.Content == ack && !block.IsError {
					ackFound = true
				}
			case agent.TextBlock:
				if strings.HasPrefix(block.Text, "[Background tool ") && strings.Contains(block.Text, jobOutput) {
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

	last := final.Messages[len(final.Messages)-1]
	if last.Role != agent.RoleAssistant {
		t.Errorf("last persisted message role = %q, want %q", last.Role, agent.RoleAssistant)
	}
}

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

	a, err := agent.New(
		p,
		"You are a note-taking assistant. When the user asks to record a note, call record_note. Be very brief.",
		agent.WithTools(bgTool),
		agent.WithConversationStore(store),
		agent.WithMaxIterations(5),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err = a.Invoke(agent.NewContext(ctx), "Record this note: integration test in progress."); err != nil {
		t.Logf("Invoke returned error (acceptable): %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if handlerCalled.Load() {
		t.Error("background handler was dispatched despite missing conversation id")
	}
}
