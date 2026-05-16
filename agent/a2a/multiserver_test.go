package a2a

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
)

func newNamedAgent(t *testing.T, name string) *agent.Agent {
	t.Helper()
	a, err := agent.New(
		&fakeProvider{response: "ok from " + name},
		prompt.Text("You are "+name),
		nil,
		agent.WithName(name),
	)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestMultiServer_Handler_CardEndpoint(t *testing.T) {
	agentA := newNamedAgent(t, "agent-a")
	agentB := newNamedAgent(t, "agent-b")

	ms, err := NewMultiServer([]AgentRegistration{
		{Prefix: "/agents/a", Agent: agentA},
		{Prefix: "/agents/b", Agent: agentB},
	})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(ms.Handler())
	defer ts.Close()

	// Test agent-a card endpoint.
	resp, err := http.Get(ts.URL + "/agents/a/.well-known/agent-card.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("agent-a card: status = %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	var cardA a2a.AgentCard
	if err := json.Unmarshal(body, &cardA); err != nil {
		t.Fatalf("failed to unmarshal agent-a card: %v", err)
	}
	if cardA.Name != "agent-a" {
		t.Errorf("agent-a card name = %q, want %q", cardA.Name, "agent-a")
	}

	// Test agent-b card endpoint.
	resp2, err := http.Get(ts.URL + "/agents/b/.well-known/agent-card.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("agent-b card: status = %d, want 200", resp2.StatusCode)
	}

	body2, err := io.ReadAll(resp2.Body)
	if err != nil {
		t.Fatal(err)
	}

	var cardB a2a.AgentCard
	if err := json.Unmarshal(body2, &cardB); err != nil {
		t.Fatalf("failed to unmarshal agent-b card: %v", err)
	}
	if cardB.Name != "agent-b" {
		t.Errorf("agent-b card name = %q, want %q", cardB.Name, "agent-b")
	}
}

func TestMultiServer_Handler_404ForUnregisteredPrefix(t *testing.T) {
	agentA := newNamedAgent(t, "agent-a")

	ms, err := NewMultiServer([]AgentRegistration{
		{Prefix: "/agents/a", Agent: agentA},
	})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(ms.Handler())
	defer ts.Close()

	// Request to an unregistered prefix should return 404.
	resp, err := http.Get(ts.URL + "/agents/unknown/.well-known/agent-card.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unregistered prefix: status = %d, want 404", resp.StatusCode)
	}
}

func TestMultiServer_RESTHandler_CardEndpoint(t *testing.T) {
	agentA := newNamedAgent(t, "rest-agent")

	ms, err := NewMultiServer([]AgentRegistration{
		{Prefix: "/rest", Agent: agentA},
	})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(ms.RESTHandler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/rest/.well-known/agent-card.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("REST card: status = %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	var card a2a.AgentCard
	if err := json.Unmarshal(body, &card); err != nil {
		t.Fatalf("failed to unmarshal card: %v", err)
	}
	if card.Name != "rest-agent" {
		t.Errorf("card name = %q, want %q", card.Name, "rest-agent")
	}
}

func TestMultiServer_ListenAndServe_Shutdown(t *testing.T) {
	agentA := newNamedAgent(t, "shutdown-agent")

	ms, err := NewMultiServer([]AgentRegistration{
		{Prefix: "/agents/a", Agent: agentA},
	}, WithMultiServerGracefulTimeout(1*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- ms.ListenAndServe(ctx, "127.0.0.1:0")
	}()

	// Cancel immediately to trigger graceful shutdown.
	cancel()

	if err := <-errCh; err != nil {
		t.Fatalf("ListenAndServe returned error: %v", err)
	}
}

func TestMultiServer_CardURL_ContainsPrefix(t *testing.T) {
	agentA := newNamedAgent(t, "prefixed-agent")

	ms, err := NewMultiServer([]AgentRegistration{
		{Prefix: "/my/prefix", Agent: agentA},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The card should have a SupportedInterface with the prefix URL.
	entry := ms.entries[0]
	card := entry.server.Card()

	if len(card.SupportedInterfaces) == 0 {
		t.Fatal("expected at least one supported interface")
	}

	iface := card.SupportedInterfaces[0]
	if iface.URL != "/my/prefix" {
		t.Errorf("card interface URL = %q, want %q", iface.URL, "/my/prefix")
	}
}
