// Run: go run ./http-server
//
// A standard-library HTTP service with one long-lived Agent, one invocation
// Context per request, SSE streaming, durable execution pauses, and graceful
// shutdown. The in-memory stores are replaceable with durable backends.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/checkpoint"
	"github.com/camilbinas/gude-agents/agent/checkpoint/executionstore"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

type chatRequest struct {
	ConversationID string `json:"conversation_id"`
	Message        string `json:"message"`
}
type resumeRequest struct {
	ExecutionVersion uint64 `json:"execution_version"`
	Answer           string `json:"answer"`
}

func main() {
	conversations := conversation.NewInMemory()
	executions := executionstore.New(checkpoint.NewMemory())
	a, err := agent.New(
		bedrock.Must(bedrock.Standard()),
		"You are a support agent. Use request_human_input when an essential detail is missing.",
		agent.WithConversationStore(conversations),
		agent.WithExecutionStore(executions),
		agent.WithTools(agent.NewHumanInputTool("request_human_input", "Ask for missing information.")),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Shutdown(context.Background())

	mux := http.NewServeMux()
	mux.HandleFunc("POST /chat", chat(a))
	mux.HandleFunc("POST /interrupt/{id}/resume", resume(a))
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	log.Println("HTTP server listening on :8080")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func chat(a *agent.Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if request.ConversationID == "" || request.Message == "" {
			http.Error(w, "conversation_id and message are required", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		ctx := agent.NewContext(r.Context()).WithConversationID(request.ConversationID)
		for event, err := range a.Stream(ctx, request.Message) {
			if err != nil {
				writeSSE(w, "error", map[string]string{"message": err.Error()})
				flusher.Flush()
				return
			}
			writeSSE(w, string(event.Type), event)
			flusher.Flush()
		}
	}
}

func resume(a *agent.Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var request resumeRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if request.ExecutionVersion == 0 {
			http.Error(w, "execution_version is required", http.StatusBadRequest)
			return
		}
		interrupt, err := a.LoadInterrupt(r.Context(), id)
		if err != nil {
			if errors.Is(err, agent.ErrInterruptNotFound) || errors.Is(err, agent.ErrExecutionNotFound) {
				http.Error(w, err.Error(), http.StatusNotFound)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		if err := validateResumeVersion(interrupt, request.ExecutionVersion); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		ctx := agent.NewContext(r.Context()).WithConversationID(interrupt.ConversationID)
		result, err := a.Resume(ctx, interrupt, agent.Respond(request.Answer))
		if err != nil {
			if errors.Is(err, agent.ErrExecutionConflict) || errors.Is(err, agent.ErrConversationConflict) {
				http.Error(w, err.Error(), http.StatusConflict)
			} else {
				http.Error(w, err.Error(), http.StatusBadGateway)
			}
			return
		}
		writeJSON(w, result)
	}
}
func writeSSE(w http.ResponseWriter, event string, value any) {
	data, _ := json.Marshal(value)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func validateResumeVersion(interrupt *agent.Interrupt, observed uint64) error {
	if observed == 0 {
		return fmt.Errorf("execution_version is required")
	}
	if interrupt.ExecutionVersion != observed {
		return fmt.Errorf("stale execution version: observed %d, current %d", observed, interrupt.ExecutionVersion)
	}
	return nil
}
