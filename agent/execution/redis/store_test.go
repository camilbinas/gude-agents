package redis

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/execution/internal/conformance"
	"github.com/camilbinas/gude-agents/agent/tool"
	goredis "github.com/redis/go-redis/v9"
)

func newClient(t *testing.T, addr string) *goredis.Client {
	t.Helper()
	client := goredis.NewClient(&goredis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func newMiniredis(t *testing.T) (*miniredis.Miniredis, *goredis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	return server, newClient(t, server.Addr())
}

func newStoreForConformance(t *testing.T) agent.ExecutionStore {
	_, client := newMiniredis(t)
	return New(client)
}

func TestConformance(t *testing.T) { conformance.Run(t, newStoreForConformance) }

func TestAgentProtocol(t *testing.T) { conformance.RunAgent(t, newStoreForConformance) }

// The paused state lives in Redis, not in the Agent or its client: a second
// Agent with a brand-new client (a restarted process) resumes it.
func TestPauseSurvivesANewClient(t *testing.T) {
	server, firstClient := newMiniredis(t)
	conversations := conformance.NewConversations()
	pause := func(client *goredis.Client, responses ...*agent.ModelResponse) *agent.Agent {
		a, err := agent.New(&staticProvider{responses: responses}, "sys",
			agent.WithTools(gatedTool()),
			agent.WithConversationStore(conversations),
			agent.WithExecutionStore(New(client)))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	first := pause(firstClient, &agent.ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "call-1", Name: "approve", Input: json.RawMessage(`{}`)}}})
	paused, err := first.Invoke(agent.Background().WithConversationID("redis-restart"), "go")
	if err != nil || paused.Interrupt == nil {
		t.Fatalf("pause = %+v, %v", paused, err)
	}
	second := pause(newClient(t, server.Addr()), &agent.ModelResponse{Text: "resumed after restart"})
	loaded, err := second.LoadInterrupt(context.Background(), paused.ExecutionID)
	if err != nil {
		t.Fatalf("LoadInterrupt: %v", err)
	}
	result, err := second.Resume(agent.Background(), loaded, agent.Approve())
	if err != nil || result.Text != "resumed after restart" {
		t.Fatalf("resume = %+v, %v", result, err)
	}
}

func gatedTool() tool.Tool {
	return tool.NewRaw("approve", "needs approval", nil, func(context.Context, json.RawMessage) (string, error) {
		return "ok", nil
	}, tool.RequiresApproval())
}

type staticProvider struct {
	mu        sync.Mutex
	responses []*agent.ModelResponse
	next      int
}

func (p *staticProvider) Name() string { return "static" }

func (p *staticProvider) Stream(context.Context, agent.ModelRequest, func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next >= len(p.responses) {
		return nil, errors.New("static provider: no more responses")
	}
	response := p.responses[p.next]
	p.next++
	return response, nil
}

func TestStoreLaysOutOneHashPerExecution(t *testing.T) {
	server, client := newMiniredis(t)
	store := New(client, WithKeyPrefix("app:"))
	ctx := context.Background()
	created, err := store.Create(ctx, conformance.Full("exec-1"))
	if err != nil {
		t.Fatal(err)
	}
	if got := server.HGet("app:exec-1", versionField); got != "1" {
		t.Fatalf("stored version = %q, want 1", got)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(server.HGet("app:exec-1", payloadField)), &payload); err != nil {
		t.Fatal(err)
	}
	if string(payload["version"]) != "0" {
		t.Fatalf("payload version = %s; the version field is authoritative", payload["version"])
	}
	saved, err := store.Save(ctx, created, created.Version)
	if err != nil || saved.Version != 2 || server.HGet("app:exec-1", versionField) != "2" {
		t.Fatalf("Save = %+v, %v; stored version %q", saved, err, server.HGet("app:exec-1", versionField))
	}
	if keys := server.Keys(); len(keys) != 1 {
		t.Fatalf("keys = %v; an execution must be exactly one key (no index, no history)", keys)
	}
}

func TestPrefixesIsolateExecutions(t *testing.T) {
	_, client := newMiniredis(t)
	one, two := New(client, WithKeyPrefix("one:")), New(client, WithKeyPrefix("two:"))
	ctx := context.Background()
	if _, err := one.Create(ctx, conformance.Full("same-id")); err != nil {
		t.Fatal(err)
	}
	if _, err := two.Load(ctx, "same-id"); !errors.Is(err, agent.ErrExecutionNotFound) {
		t.Fatalf("a different prefix observed the execution: %v", err)
	}
	if _, err := two.Create(ctx, conformance.Full("same-id")); err != nil {
		t.Fatalf("the same ID under another prefix must be creatable: %v", err)
	}
}

func TestTTLRefreshesOnSaveAndExpiresIdleExecutions(t *testing.T) {
	server, client := newMiniredis(t)
	store := New(client, WithTTL(time.Hour))
	ctx := context.Background()
	created, err := store.Create(ctx, conformance.Full("exec-ttl"))
	if err != nil {
		t.Fatal(err)
	}
	server.FastForward(40 * time.Minute)
	saved, err := store.Save(ctx, created, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	server.FastForward(40 * time.Minute) // 80 minutes after Create, 40 after Save
	if _, err := store.Load(ctx, "exec-ttl"); err != nil {
		t.Fatalf("Save did not refresh the TTL: %v", err)
	}
	server.FastForward(30 * time.Minute)
	if _, err := store.Load(ctx, "exec-ttl"); !errors.Is(err, agent.ErrExecutionNotFound) {
		t.Fatalf("Load after expiry = %v, want ErrExecutionNotFound", err)
	}
	if _, err := store.Save(ctx, saved, saved.Version); !errors.Is(err, agent.ErrExecutionConflict) {
		t.Fatalf("Save after expiry = %v, want ErrExecutionConflict", err)
	}
}

func TestNoTTLMeansExecutionsPersist(t *testing.T) {
	server, client := newMiniredis(t)
	store := New(client)
	ctx := context.Background()
	created, err := store.Create(ctx, conformance.Full("exec-forever"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, created, created.Version); err != nil {
		t.Fatal(err)
	}
	server.FastForward(24 * 365 * time.Hour)
	if _, err := store.Load(ctx, "exec-forever"); err != nil {
		t.Fatalf("an execution without a TTL expired: %v", err)
	}
}

func TestMisconfiguredStoreReturnsErrors(t *testing.T) {
	var client goredis.UniversalClient
	for name, store := range map[string]*Store{"nil client": New(client), "nil store": nil} {
		if _, err := store.Load(context.Background(), "x"); err == nil {
			t.Errorf("%s: Load returned nil error", name)
		}
	}
}
