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

// TestNewClient_Non2xxHTTPResponse verifies that NewClient returns an error
// when the remote server responds with a non-2xx status code.
// Validates: Requirements 5.3
func TestNewClient_Non2xxHTTPResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := NewClient(context.Background(), ts.URL)
	if err == nil {
		t.Fatal("expected error for non-2xx response, got nil")
	}

	// Verify the error mentions the HTTP status code.
	if got := err.Error(); !strings.Contains(got, "500") {
		t.Errorf("error = %q, expected it to contain '500'", got)
	}
}

// TestNewClient_InvalidJSONCard verifies that NewClient returns an error
// when the remote server responds with 200 but invalid JSON.
// Validates: Requirements 5.3
func TestNewClient_InvalidJSONCard(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("this is not valid json{{{"))
	}))
	defer ts.Close()

	_, err := NewClient(context.Background(), ts.URL)
	if err == nil {
		t.Fatal("expected error for invalid JSON card, got nil")
	}

	// Verify the error mentions parsing.
	if got := err.Error(); !strings.Contains(got, "parsing") {
		t.Errorf("error = %q, expected it to contain 'parsing'", got)
	}
}

// TestToolHandler_RemoteTaskFailed verifies that the tool handler returns an error
// when the remote agent responds with a failed task status.
// Validates: Requirements 6.6
func TestToolHandler_RemoteTaskFailed(t *testing.T) {
	// First server: serves a valid agent card on GET /.well-known/agent.json
	// and returns a failed task on POST (JSON-RPC SendMessage).
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/.well-known/agent.json" {
			card := a2a.AgentCard{
				Name:        "failing-agent",
				Description: "An agent that always fails",
				Version:     "1.0.0",
				Skills: []a2a.AgentSkill{
					{
						ID:          "fail-skill",
						Name:        "Fail Skill",
						Description: "Always fails",
						Tags:        []string{"test"},
					},
				},
				SupportedInterfaces: []*a2a.AgentInterface{},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(card)
			return
		}

		// Handle JSON-RPC SendMessage — return a failed task.
		failMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("something went wrong"))
		task := a2a.Task{
			ID:        "task-1",
			ContextID: "ctx-1",
			Status: a2a.TaskStatus{
				State:   a2a.TaskStateFailed,
				Message: failMsg,
			},
		}

		rpcResp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      "1",
		}
		resultBytes, _ := json.Marshal(task)
		rpcResp.Result = resultBytes

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rpcResp)
	}))
	defer ts.Close()

	client, err := NewClient(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// Get the tools and invoke the handler.
	tools, err := client.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools() failed: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	// Invoke the tool handler with a message.
	input := []byte(`{"message": "do something"}`)
	_, err = tools[0].Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected error from tool handler when remote task fails, got nil")
	}

	// Verify the error contains the failure message from the remote agent.
	if got := err.Error(); !strings.Contains(got, "something went wrong") {
		t.Errorf("error = %q, expected it to contain 'something went wrong'", got)
	}
}
