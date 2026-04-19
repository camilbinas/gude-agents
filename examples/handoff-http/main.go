// Example: Human handoff in a multi-tenant HTTP environment.
//
// A single Agent instance serves multiple concurrent conversations.
// Each request provides a conversation_id, which is passed via
// agent.WithConversationID on the context. The agent uses WithSharedMemory
// so it doesn't bind to a single conversation at construction time.
//
// Flow:
//
//	POST /chat          → 200 (normal) or 202 (handoff pending)
//	POST /chat/resume   → 200 (agent continues with human input)
//
// Run:
//
//	go run ./handoff-http

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// pendingHandoffs stores handoff requests keyed by conversation ID.
// In production, use Redis/DB instead of in-memory.
var (
	pendingHandoffs = map[string]*agent.HandoffRequest{}
	handoffMu       sync.Mutex
)

func main() {
	provider := bedrock.Must(bedrock.ClaudeSonnet4_6())

	store := memory.NewStore()

	// Single agent instance shared across all requests.
	// WithSharedMemory means no hardcoded conversationID — each request
	// provides its own via agent.WithConversationID on the context.
	a, err := agent.New(provider, prompt.Text(
		"You are a support agent. Use request_human_input when you need approval.",
	), []tool.Tool{
		agent.NewHandoffTool("request_human_input", ""),
		tool.NewRaw("lookup", "Look up data", map[string]any{"type": "object"},
			func(ctx context.Context, input json.RawMessage) (string, error) {
				return `{"found": true}`, nil
			}),
	}, agent.WithSharedMemory(store), agent.WithMaxIterations(10))
	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/chat", handleChat(a))
	http.HandleFunc("/chat/resume", handleResume(a))

	fmt.Println("Listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

type chatRequest struct {
	Message        string `json:"message"`
	ConversationID string `json:"conversation_id"`
}

type chatResponse struct {
	Response       string           `json:"response,omitempty"`
	ConversationID string           `json:"conversation_id,omitempty"`
	Handoff        *handoffResponse `json:"handoff,omitempty"`
}

type handoffResponse struct {
	Reason   string `json:"reason"`
	Question string `json:"question"`
}

type resumeRequest struct {
	ConversationID string `json:"conversation_id"`
	HumanResponse  string `json:"human_response"`
}

func handleChat(a *agent.Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.ConversationID == "" {
			http.Error(w, "conversation_id is required", http.StatusBadRequest)
			return
		}

		// Per-request conversation ID — the key to multi-tenancy.
		ic := agent.NewInvocationContext()
		ctx := agent.WithConversationID(r.Context(), req.ConversationID)
		ctx = agent.WithInvocationContext(ctx, ic)

		result, _, err := a.Invoke(ctx, req.Message)

		if errors.Is(err, agent.ErrHandoffRequested) {
			hr, _ := agent.GetHandoffRequest(ic)

			handoffMu.Lock()
			pendingHandoffs[req.ConversationID] = hr
			handoffMu.Unlock()

			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(chatResponse{
				ConversationID: req.ConversationID,
				Handoff: &handoffResponse{
					Reason:   hr.Reason,
					Question: hr.Question,
				},
			})
			return
		}

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(chatResponse{
			ConversationID: req.ConversationID,
			Response:       result,
		})
	}
}

func handleResume(a *agent.Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req resumeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		handoffMu.Lock()
		hr, ok := pendingHandoffs[req.ConversationID]
		if ok {
			delete(pendingHandoffs, req.ConversationID)
		}
		handoffMu.Unlock()

		if !ok {
			http.Error(w, "no pending handoff for this conversation", http.StatusNotFound)
			return
		}

		// Resume uses the ConversationID stored in the HandoffRequest.
		result, _, err := a.ResumeInvoke(r.Context(), hr, req.HumanResponse)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(chatResponse{
			ConversationID: req.ConversationID,
			Response:       result,
		})
	}
}
