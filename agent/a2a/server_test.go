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
)

func TestNewServer(t *testing.T) {
	a, err := agent.New(
		&fakeProvider{response: "ok"},
		"Test agent",
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
		"Test agent",
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
		"Test agent",
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

// TestNewServer_DefaultIgnoresForwardedPrincipal verifies that a Server built
// with no principal options does not trust raw X-Agent-Principal-* headers.
func TestNewServer_DefaultIgnoresForwardedPrincipal(t *testing.T) {
	provider := &principalCapturingProvider{response: "ok"}
	a := newCapturingPrincipalAgent(t, provider)

	srv, err := NewServer(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if srv.executor.verifyPrincipal != nil {
		t.Error("expected no verifier configured by default")
	}
	if srv.executor.trustForwardedPrincipal {
		t.Error("expected trustForwardedPrincipal to be false by default")
	}
}

// TestNewServer_WithPrincipalVerifier verifies the verifier is wired through
// to the executor.
func TestNewServer_WithPrincipalVerifier(t *testing.T) {
	provider := &principalCapturingProvider{response: "ok"}
	a := newCapturingPrincipalAgent(t, provider)

	called := false
	srv, err := NewServer(a, nil, WithPrincipalVerifier(func(p agent.Principal) (agent.Principal, error) {
		called = true
		return p, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if srv.executor.verifyPrincipal == nil {
		t.Fatal("expected verifier to be wired through to the executor")
	}
	if _, err := srv.executor.verifyPrincipal(agent.Principal{ID: "x"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("expected verifier to be invoked")
	}
}

// TestNewServer_WithTrustedForwardedPrincipal verifies the trust opt-in is
// wired through to the executor.
func TestNewServer_WithTrustedForwardedPrincipal(t *testing.T) {
	provider := &principalCapturingProvider{response: "ok"}
	a := newCapturingPrincipalAgent(t, provider)

	srv, err := NewServer(a, nil, WithTrustedForwardedPrincipal())
	if err != nil {
		t.Fatal(err)
	}
	if !srv.executor.trustForwardedPrincipal {
		t.Fatal("expected trustForwardedPrincipal to be true")
	}
	if srv.executor.verifyPrincipal != nil {
		t.Error("expected no verifier configured")
	}
}

// TestNewServer_VerifierTakesPrecedenceOverTrust verifies that when both
// WithPrincipalVerifier and WithTrustedForwardedPrincipal are given, the
// verifier is used.
func TestNewServer_VerifierTakesPrecedenceOverTrust(t *testing.T) {
	provider := &principalCapturingProvider{response: "ok"}
	a := newCapturingPrincipalAgent(t, provider)

	srv, err := NewServer(a, nil,
		WithTrustedForwardedPrincipal(),
		WithPrincipalVerifier(func(p agent.Principal) (agent.Principal, error) { return p, nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if srv.executor.verifyPrincipal == nil {
		t.Fatal("expected verifier to take precedence and be wired through")
	}
}
