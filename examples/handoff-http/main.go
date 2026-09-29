// Example: Human input in a multi-tenant HTTP environment.
//
// A single Agent instance serves multiple concurrent conversations. Each
// request supplies a conversation ID through its invocation Context.
//
// Flow:
//
//	POST /chat          → 200 (normal) or 202 (human input pending)
//	POST /chat/resume   → 200 (agent continues with human input)
//
// Run:
//
//	go run ./handoff-http

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// pendingInterrupts stores resumable interrupts keyed by conversation ID.
// In production, configure an InterruptStore backed by Redis or a database.
var (
	pendingInterrupts = map[string]*agent.Interrupt{}
	interruptMu       sync.Mutex
)

func main() {
	provider := bedrock.Must(bedrock.Standard())
	store := conversation.NewInMemory()

	lookup := tool.NewRaw(
		"lookup",
		"Look up data",
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return `{"found":true}`, nil
		},
		tool.WithSchema(map[string]any{"type": "object"}),
	)

	a, err := agent.New(
		provider,
		"You are a support agent. Use request_human_input when you need approval.",
		agent.WithTools(
			agent.NewHumanInputTool("request_human_input", ""),
			lookup,
		),
		agent.WithConversationStore(store),
		agent.WithMaxIterations(10),
	)
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
		if req.Message == "" {
			http.Error(w, "message is required", http.StatusBadRequest)
			return
		}

		ctx := agent.NewContext(r.Context()).WithConversationID(req.ConversationID)
		result, err := a.Invoke(ctx, req.Message)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if result.StopReason == agent.StopInterrupt {
			if !isHumanInput(result.Interrupt) {
				http.Error(w, "agent returned an unexpected interrupt", http.StatusInternalServerError)
				return
			}
			storePending(req.ConversationID, result.Interrupt)
			writeInterrupt(w, req.ConversationID, result.Interrupt)
			return
		}

		writeJSON(w, http.StatusOK, chatResponse{
			ConversationID: req.ConversationID,
			Response:       result.Text,
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
		if req.ConversationID == "" {
			http.Error(w, "conversation_id is required", http.StatusBadRequest)
			return
		}
		if req.HumanResponse == "" {
			http.Error(w, "human_response is required", http.StatusBadRequest)
			return
		}

		interrupt, ok := loadPending(req.ConversationID)
		if !ok {
			http.Error(w, "no pending human input for this conversation", http.StatusNotFound)
			return
		}

		ctx := agent.NewContext(r.Context()).WithConversationID(req.ConversationID)
		result, err := a.Resume(ctx, interrupt, agent.Respond(req.HumanResponse))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		deletePending(req.ConversationID, interrupt)
		if result.StopReason == agent.StopInterrupt {
			if !isHumanInput(result.Interrupt) {
				http.Error(w, "agent returned an unexpected interrupt", http.StatusInternalServerError)
				return
			}
			storePending(req.ConversationID, result.Interrupt)
			writeInterrupt(w, req.ConversationID, result.Interrupt)
			return
		}

		writeJSON(w, http.StatusOK, chatResponse{
			ConversationID: req.ConversationID,
			Response:       result.Text,
		})
	}
}

func isHumanInput(in *agent.Interrupt) bool {
	return in != nil && in.Type == agent.InterruptHumanInput && in.Input != nil
}

func storePending(conversationID string, in *agent.Interrupt) {
	interruptMu.Lock()
	defer interruptMu.Unlock()
	pendingInterrupts[conversationID] = in
}

func loadPending(conversationID string) (*agent.Interrupt, bool) {
	interruptMu.Lock()
	defer interruptMu.Unlock()
	in, ok := pendingInterrupts[conversationID]
	return in, ok
}

func deletePending(conversationID string, expected *agent.Interrupt) {
	interruptMu.Lock()
	defer interruptMu.Unlock()
	if pendingInterrupts[conversationID] == expected {
		delete(pendingInterrupts, conversationID)
	}
}

func writeInterrupt(w http.ResponseWriter, conversationID string, in *agent.Interrupt) {
	writeJSON(w, http.StatusAccepted, chatResponse{
		ConversationID: conversationID,
		Handoff: &handoffResponse{
			Reason:   in.Input.Reason,
			Question: in.Input.Question,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode response: %v", err)
	}
}
