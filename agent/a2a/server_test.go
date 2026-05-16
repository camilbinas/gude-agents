package a2a

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
)

func TestNewServer(t *testing.T) {
	a, err := agent.New(
		&fakeProvider{response: "ok"},
		prompt.Text("Test agent"),
		nil,
		agent.WithName("test-agent"),
	)
	if err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(a, nil)
	if err != nil {
		t.Fatal(err)
	}

	if srv.Card().Name != "test-agent" {
		t.Errorf("card name = %q, want %q", srv.Card().Name, "test-agent")
	}
}

func TestNewServer_NilAgent(t *testing.T) {
	_, err := NewServer(nil, nil)
	if err == nil {
		t.Fatal("expected error for nil agent")
	}
}

func TestServer_AgentCardEndpoint(t *testing.T) {
	a, err := agent.New(
		&fakeProvider{response: "ok"},
		prompt.Text("Test agent"),
		nil,
		agent.WithName("card-test-agent"),
	)
	if err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(a, []CardOption{
		WithCardVersion("1.2.3"),
	})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/.well-known/agent-card.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	var card a2a.AgentCard
	if err := json.Unmarshal(body, &card); err != nil {
		t.Fatalf("failed to unmarshal agent card: %v", err)
	}

	if card.Name != "card-test-agent" {
		t.Errorf("card name = %q, want %q", card.Name, "card-test-agent")
	}
	if card.Version != "1.2.3" {
		t.Errorf("card version = %q, want %q", card.Version, "1.2.3")
	}
}

func TestServer_ListenAndServe_Shutdown(t *testing.T) {
	a, err := agent.New(
		&fakeProvider{response: "ok"},
		prompt.Text("Test agent"),
		nil,
		agent.WithName("shutdown-test"),
	)
	if err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(a, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe(ctx, "127.0.0.1:0")
	}()

	// Cancel immediately to trigger shutdown.
	cancel()

	if err := <-errCh; err != nil {
		t.Fatalf("ListenAndServe returned error: %v", err)
	}
}
