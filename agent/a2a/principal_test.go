package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/camilbinas/gude-agents/agent"
)

// TestClient_PropagatesPrincipalHeaders verifies that the A2A client adds
// X-Agent-Principal-ID and X-Agent-Principal-Roles headers when a principal is
// present on the context.
func TestClient_PropagatesPrincipalHeaders(t *testing.T) {
	var capturedHeaders http.Header

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Serve agent card.
			card := a2a.AgentCard{
				Name: "test-agent", Description: "test", Version: "1.0.0",
				Skills: []a2a.AgentSkill{
					{ID: "skill1", Name: "Skill 1", Description: "A skill", Tags: []string{"test"}},
				},
				SupportedInterfaces: []*a2a.AgentInterface{},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(card)
			return
		}
		// Capture headers on POST.
		capturedHeaders = r.Header.Clone()

		// Return a successful task.
		task := a2a.Task{
			ID: "task-1", ContextID: "ctx-1",
			Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
		}
		rpcResp := jsonRPCResponse{JSONRPC: "2.0", ID: "1"}
		resultBytes, _ := json.Marshal(task)
		rpcResp.Result = resultBytes
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rpcResp)
	}))
	defer ts.Close()

	client, err := NewClient(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	tools, err := client.Tools(context.Background())
	if err != nil || len(tools) == 0 {
		t.Fatalf("Tools(): %v (len=%d)", err, len(tools))
	}

	// Invoke with principal set.
	ctx := agent.NewContext(context.Background()).
		WithPrincipal(agent.Principal{ID: "user42", Roles: []string{"admin", "support"}})
	_, _ = tools[0].Handler(ctx, json.RawMessage(`{"message":"hello"}`))

	if capturedHeaders == nil {
		t.Fatal("no POST request was captured")
	}
	if got := capturedHeaders.Get("X-Agent-Principal-ID"); got != "user42" {
		t.Errorf("X-Agent-Principal-ID = %q, want user42", got)
	}
	roles := capturedHeaders.Get("X-Agent-Principal-Roles")
	if !strings.Contains(roles, "admin") || !strings.Contains(roles, "support") {
		t.Errorf("X-Agent-Principal-Roles = %q, want to contain admin and support", roles)
	}
}

// TestClient_NoPrincipalHeadersAbsent verifies no principal headers are sent when
// no principal is set on the context.
func TestClient_NoPrincipalHeadersAbsent(t *testing.T) {
	var capturedHeaders http.Header

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			card := a2a.AgentCard{
				Name: "test-agent", Description: "test", Version: "1.0.0",
				Skills: []a2a.AgentSkill{
					{ID: "skill1", Name: "Skill 1", Description: "A skill", Tags: []string{"test"}},
				},
				SupportedInterfaces: []*a2a.AgentInterface{},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(card)
			return
		}
		capturedHeaders = r.Header.Clone()
		task := a2a.Task{ID: "t", ContextID: "c", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}
		rpcResp := jsonRPCResponse{JSONRPC: "2.0", ID: "1"}
		b, _ := json.Marshal(task)
		rpcResp.Result = b
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rpcResp)
	}))
	defer ts.Close()

	client, err := NewClient(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	tools, err := client.Tools(context.Background())
	if err != nil || len(tools) == 0 {
		t.Fatalf("Tools(): %v", err)
	}

	// Invoke without principal.
	_, _ = tools[0].Handler(context.Background(), json.RawMessage(`{"message":"hi"}`))

	if capturedHeaders == nil {
		t.Fatal("no POST captured")
	}
	if got := capturedHeaders.Get("X-Agent-Principal-ID"); got != "" {
		t.Errorf("X-Agent-Principal-ID should be absent, got %q", got)
	}
	if got := capturedHeaders.Get("X-Agent-Principal-Roles"); got != "" {
		t.Errorf("X-Agent-Principal-Roles should be absent, got %q", got)
	}
}

// TestPrincipalFromRequest_Present verifies header extraction works.
func TestPrincipalFromRequest_Present(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "/", bytes.NewReader(nil))
	r.Header.Set("X-Agent-Principal-ID", "alice")
	r.Header.Set("X-Agent-Principal-Roles", "admin,support")

	p, ok := PrincipalFromRequest(r)
	if !ok {
		t.Fatal("expected principal from request")
	}
	if p.ID != "alice" {
		t.Errorf("ID = %q, want alice", p.ID)
	}
	if len(p.Roles) != 2 {
		t.Errorf("Roles = %v, want [admin support]", p.Roles)
	}
}

// TestPrincipalFromRequest_Absent verifies false is returned when header is missing.
func TestPrincipalFromRequest_Absent(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "/", bytes.NewReader(nil))
	_, ok := PrincipalFromRequest(r)
	if ok {
		t.Error("expected ok=false when header absent")
	}
}
