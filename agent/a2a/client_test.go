package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func testCard(interfaces ...*a2a.AgentInterface) a2a.AgentCard {
	return a2a.AgentCard{
		Name:        "test-agent",
		Description: "A test agent",
		Version:     "1.0.0",
		Skills: []a2a.AgentSkill{{
			ID:          "test-skill",
			Name:        "Test Skill",
			Description: "A test skill",
		}},
		SupportedInterfaces: interfaces,
	}
}

func writeCard(t *testing.T, w http.ResponseWriter, card a2a.AgentCard) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(card); err != nil {
		t.Fatalf("encode card: %v", err)
	}
}

func TestNewClient_UsesCurrentAgentCardPath(t *testing.T) {
	var currentRequests, legacyRequests int
	card := testCard()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case wellKnownAgentCardPath:
			currentRequests++
			writeCard(t, w, card)
		case wellKnownLegacyAgentPath:
			legacyRequests++
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client, err := NewClient(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()
	if currentRequests != 1 || legacyRequests != 0 {
		t.Fatalf("discovery requests current=%d legacy=%d, want 1 and 0", currentRequests, legacyRequests)
	}
}

func TestNewClient_FallsBackToLegacyCardPathOnNotFound(t *testing.T) {
	var currentRequests, legacyRequests int
	card := testCard()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case wellKnownAgentCardPath:
			currentRequests++
			http.NotFound(w, r)
		case wellKnownLegacyAgentPath:
			legacyRequests++
			writeCard(t, w, card)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client, err := NewClient(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()
	if currentRequests != 1 || legacyRequests != 1 {
		t.Fatalf("discovery requests current=%d legacy=%d, want 1 and 1", currentRequests, legacyRequests)
	}
}

func TestNewClient_DoesNotFallbackForCurrentPathErrors(t *testing.T) {
	var legacyRequests int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == wellKnownLegacyAgentPath {
			legacyRequests++
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := NewClient(context.Background(), ts.URL)
	if err == nil {
		t.Fatal("expected error for current card path failure")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error = %q, expected it to contain 500", err)
	}
	if legacyRequests != 0 {
		t.Fatalf("legacy discovery requested %d times after current 500", legacyRequests)
	}
}

func TestNewClient_InvalidJSONCard(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wellKnownAgentCardPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("this is not valid json{{{"))
	}))
	defer ts.Close()

	_, err := NewClient(context.Background(), ts.URL)
	if err == nil || !strings.Contains(err.Error(), "parsing") {
		t.Fatalf("NewClient error = %v, want parsing error", err)
	}
}

func TestToolHandler_UsesJSONRPCInterfaceAndResolvesRelativeURL(t *testing.T) {
	var postedPath string
	card := testCard(
		a2a.NewAgentInterface("https://example.invalid/rest", a2a.TransportProtocolHTTPJSON),
		a2a.NewAgentInterface("rpc", a2a.TransportProtocolJSONRPC),
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/agents/worker"+wellKnownAgentCardPath {
			writeCard(t, w, card)
			return
		}
		if r.Method == http.MethodPost {
			postedPath = r.URL.Path
			message := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("direct "), a2a.NewTextPart("message"))
			result, err := json.Marshal(message)
			if err != nil {
				t.Fatalf("marshal message result: %v", err)
			}
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{JSONRPC: "2.0", ID: "1", Result: result})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client, err := NewClient(context.Background(), ts.URL+"/agents/worker")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()
	tools, err := client.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools failed: %v", err)
	}
	got, err := tools[0].Handler(context.Background(), []byte(`{"message":"hello"}`))
	if err != nil {
		t.Fatalf("tool handler failed: %v", err)
	}
	if got != "direct message" {
		t.Fatalf("tool result = %q, want direct message", got)
	}
	if postedPath != "/agents/worker/rpc" {
		t.Fatalf("JSON-RPC post path = %q, want /agents/worker/rpc", postedPath)
	}
}

func TestToolHandler_RemoteTaskFailed(t *testing.T) {
	card := testCard()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == wellKnownAgentCardPath {
			writeCard(t, w, card)
			return
		}
		failMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("something went wrong"))
		task := a2a.Task{ID: "task-1", ContextID: "ctx-1", Status: a2a.TaskStatus{State: a2a.TaskStateFailed, Message: failMsg}}
		result, err := json.Marshal(task)
		if err != nil {
			t.Fatalf("marshal task: %v", err)
		}
		_ = json.NewEncoder(w).Encode(jsonRPCResponse{JSONRPC: "2.0", ID: "1", Result: result})
	}))
	defer ts.Close()

	client, err := NewClient(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()
	tools, err := client.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools failed: %v", err)
	}
	_, err = tools[0].Handler(context.Background(), []byte(`{"message":"do something"}`))
	if err == nil || !strings.Contains(err.Error(), "something went wrong") {
		t.Fatalf("tool handler error = %v, want remote failure", err)
	}
}
