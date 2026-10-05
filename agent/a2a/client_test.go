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
			result, err := json.Marshal(a2a.StreamResponse{Event: message})
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
		task := &a2a.Task{ID: "task-1", ContextID: "ctx-1", Status: a2a.TaskStatus{State: a2a.TaskStateFailed, Message: failMsg}}
		result, err := json.Marshal(a2a.StreamResponse{Event: task})
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

func TestExtractTextFromResult_UnwrapsTaskEnvelope(t *testing.T) {
	text, err := extractTextFromResult(json.RawMessage(`{
		"task": {
			"id": "task-1",
			"status": {"state": "TASK_STATE_COMPLETED"},
			"artifacts": [{"artifactId": "artifact-1", "parts": [{"text": "hello"}, {"text": " world"}]}]
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world" {
		t.Fatalf("text = %q, want hello world", text)
	}
}

func TestExtractTextFromResult_StrictSynchronousTaskStates(t *testing.T) {
	statusMessage := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("remote status text"))
	completed := func(state a2a.TaskState) *a2a.Task {
		return &a2a.Task{
			ID:        "task-1",
			ContextID: "context-1",
			Status:    a2a.TaskStatus{State: state, Message: statusMessage},
			Artifacts: []*a2a.Artifact{{
				ID:    "artifact-1",
				Parts: a2a.ContentParts{a2a.NewTextPart("hello"), a2a.NewTextPart(" world")},
			}},
		}
	}
	message := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("direct "), a2a.NewTextPart("message"))

	cases := []struct {
		name       string
		event      a2a.Event
		want       string
		wantErrSub string
	}{
		{name: "wrapped message", event: message, want: "direct message"},
		{name: "completed task", event: completed(a2a.TaskStateCompleted), want: "hello world"},
		{name: "failed task preserves status", event: completed(a2a.TaskStateFailed), wantErrSub: "remote status text"},
		{name: "canceled task", event: completed(a2a.TaskStateCanceled), wantErrSub: "canceled"},
		{name: "auth required task", event: completed(a2a.TaskStateAuthRequired), wantErrSub: "requires authentication"},
		{name: "input required task", event: completed(a2a.TaskStateInputRequired), wantErrSub: "human-input continuation is not propagated"},
		{name: "rejected task", event: completed(a2a.TaskStateRejected), wantErrSub: "rejected"},
		{name: "submitted task", event: completed(a2a.TaskStateSubmitted), wantErrSub: "non-completed task state TASK_STATE_SUBMITTED"},
		{name: "working task", event: completed(a2a.TaskStateWorking), wantErrSub: "non-completed task state TASK_STATE_WORKING"},
		{name: "unspecified task", event: completed(a2a.TaskStateUnspecified), wantErrSub: "non-completed task state TASK_STATE_UNSPECIFIED"},
		{name: "unknown task state", event: completed(a2a.TaskState("TASK_STATE_FUTURE")), wantErrSub: "unknown non-completed task state TASK_STATE_FUTURE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(a2a.StreamResponse{Event: tc.event})
			if err != nil {
				t.Fatal(err)
			}
			got, err := extractTextFromResult(raw)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("error = %v, want substring %q", err, tc.wantErrSub)
				}
				if got != "" {
					t.Fatalf("text = %q, want no successful text", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("result = %q, %v; want %q, nil", got, err, tc.want)
			}
		})
	}
}

func TestExtractTextFromResult_CompatibilityAndInvalidShapes(t *testing.T) {
	directMessage, err := json.Marshal(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("legacy message")))
	if err != nil {
		t.Fatal(err)
	}
	if text, err := extractTextFromResult(directMessage); err != nil || text != "legacy message" {
		t.Fatalf("direct message = %q, %v", text, err)
	}

	statusUpdate, err := json.Marshal(a2a.StreamResponse{Event: &a2a.TaskStatusUpdateEvent{TaskID: "task-1", ContextID: "context-1", Status: a2a.TaskStatus{State: a2a.TaskStateWorking}}})
	if err != nil {
		t.Fatal(err)
	}
	invalid := [][]byte{
		[]byte(`{}`),
		[]byte(`null`),
		[]byte(`{"task":{},"message":{}}`),
		statusUpdate,
		[]byte(`not json`),
	}
	for _, raw := range invalid {
		if text, err := extractTextFromResult(raw); err == nil || text != "" {
			t.Fatalf("result for %s = %q, %v; want error and no text", raw, text, err)
		}
	}
}
