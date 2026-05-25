package utils

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/gorilla/websocket"
)

//go:embed agent_devtools.html
var agentDevtoolsFS embed.FS

// AgentDevToolsConfig configures the agent loop DevTools server.
//
// The DevTools serve a small web UI where users can chat with an agent and
// watch every iteration, model call, tool call, and streamed chunk in
// real-time. It mirrors the existing graph DevTools, but instead of a
// node graph it visualises the agent loop iterations and per-call details.
type AgentDevToolsConfig struct {
	// Port is the HTTP port to bind. Defaults to 4041 (one above the graph
	// devtools default 4040 so the two can coexist).
	Port int

	// Agent is the agent under inspection. Required.
	Agent *agent.Agent

	// AgentName is the display name shown in the UI header. If empty, falls
	// back to the agent's configured name, then to "agent". Override only when
	// you want a friendlier label than the agent's internal name.
	AgentName string

	// NewContext, when set, is called to build the *agent.Context used for
	// each user turn. Use this to wire conversation IDs, inference config,
	// images, or per-turn options. If nil, agent.Background() is used and a
	// stable conversation ID derived from the websocket session is set.
	NewContext func() *agent.Context

	// OnTurnEnd, when set, is called after each user turn completes (whether
	// successfully or with an error). The *agent.Context is the one that drove
	// the invocation, so callers can read its Usage(), tracing hooks, etc.
	// Useful for flushing trace exporters or logging token counts — mirrors
	// the AfterInvoke hook in utils.ChatOptions.
	OnTurnEnd func(c *agent.Context, err error)

	// OnClear, when set, is exposed as a Clear button in the UI. It's called
	// with a fresh context.Context whenever the user clicks Clear and is
	// expected to wipe the agent's conversation / memory state. Mirrors
	// ChatOptions.ClearFunc.
	OnClear func(ctx context.Context) error
}

// AgentDevTools serves a chat-style web UI that visualises an agent's
// iteration loop in real-time.
type AgentDevTools struct {
	config  AgentDevToolsConfig
	clients map[*websocket.Conn]bool
	mu      sync.Mutex
}

// NewAgentDevTools constructs a new AgentDevTools.
func NewAgentDevTools(config AgentDevToolsConfig) *AgentDevTools {
	if config.Port == 0 {
		config.Port = 4041
	}
	if config.Agent == nil {
		panic("utils.NewAgentDevTools: AgentDevToolsConfig.Agent is required")
	}
	return &AgentDevTools{
		config:  config,
		clients: make(map[*websocket.Conn]bool),
	}
}

// ListenAndServe starts the HTTP server and blocks. The agent must outlive
// the server.
func (dt *AgentDevTools) ListenAndServe() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := agentDevtoolsFS.ReadFile("agent_devtools.html")
		if err != nil {
			http.Error(w, "failed to read agent_devtools.html", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})

	mux.HandleFunc("/ws", dt.handleWS)

	addr := fmt.Sprintf(":%d", dt.config.Port)
	url := fmt.Sprintf("http://localhost:%d", dt.config.Port)
	log.Printf("Agent DevTools running at %s", url)

	go openBrowser(url)

	return http.ListenAndServe(addr, mux)
}

// agentMeta is the initial frame sent to a client describing the agent.
type agentMeta struct {
	Type           string   `json:"type"`
	Name           string   `json:"name"`
	Provider       string   `json:"provider"`
	Model          string   `json:"model"`
	Tools          []string `json:"tools"`
	ClearSupported bool     `json:"clear_supported"`
}

func (dt *AgentDevTools) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer func() {
		dt.mu.Lock()
		delete(dt.clients, conn)
		dt.mu.Unlock()
		conn.Close()
	}()

	dt.mu.Lock()
	dt.clients[conn] = true
	dt.mu.Unlock()

	// Build agent metadata for the UI. Provider and model are pulled from
	// the agent itself: Provider().Name() for the provider label and the
	// optional ModelIdentifier interface for the model id.
	a := dt.config.Agent
	meta := agentMeta{
		Type: "agent_meta",
		Name: dt.config.AgentName,
	}
	if meta.Name == "" {
		meta.Name = a.Name()
	}
	if meta.Name == "" {
		meta.Name = "agent"
	}
	if p := a.Provider(); p != nil {
		meta.Provider = p.Name()
		if mi, ok := p.(agent.ModelIdentifier); ok {
			meta.Model = mi.ModelID()
		}
	}
	for _, spec := range a.ToolSpecs() {
		meta.Tools = append(meta.Tools, spec.Name)
	}
	meta.ClearSupported = dt.config.OnClear != nil
	if err := conn.WriteJSON(meta); err != nil {
		log.Printf("WebSocket write error: %v", err)
		return
	}

	// One websocket connection == one chat session. Cancellation lets us
	// stop a running invocation when the client disconnects or sends "stop".
	sessionCtx, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()

	var (
		runMu     sync.Mutex
		runCancel context.CancelFunc
	)

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg struct {
			Action string `json:"action"`
			Text   string `json:"text"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		switch msg.Action {
		case "send":
			runMu.Lock()
			if runCancel != nil {
				// A previous turn is still running. Block new sends until it
				// finishes; this matches typical chat UI semantics.
				runMu.Unlock()
				dt.send(conn, map[string]any{
					"type":    "error",
					"message": "previous turn is still running; press Stop to cancel",
				})
				continue
			}
			turnCtx, cancel := context.WithCancel(sessionCtx)
			runCancel = cancel
			runMu.Unlock()

			go func(text string) {
				dt.runTurn(turnCtx, conn, text)
				runMu.Lock()
				runCancel = nil
				runMu.Unlock()
				cancel()
			}(msg.Text)

		case "stop":
			runMu.Lock()
			if runCancel != nil {
				runCancel()
			}
			runMu.Unlock()

		case "clear":
			if dt.config.OnClear == nil {
				continue
			}
			err := dt.config.OnClear(context.Background())
			if err != nil {
				dt.send(conn, map[string]any{
					"type":    "error",
					"message": "clear failed: " + err.Error(),
				})
				continue
			}
			dt.send(conn, map[string]any{"type": "cleared"})
		}
	}
}

// runTurn drives one user turn through Agent.InvokeEventStream and forwards
// every event to the websocket as a JSON frame the UI knows how to render.
func (dt *AgentDevTools) runTurn(ctx context.Context, conn *websocket.Conn, userText string) {
	// Echo the user message immediately so the chat panel updates without
	// waiting for the model.
	dt.send(conn, map[string]any{
		"type": "user_message",
		"text": userText,
		"ts":   time.Now().UnixMilli(),
	})

	// Build the per-turn agent context wrapping our cancellable ctx so that
	// pressing Stop in the UI actually cancels the in-flight invocation.
	// We honour the user's NewContext hook for conversation IDs, inference
	// config, etc., and copy the relevant fields onto a fresh ctx-backed
	// Context (since *agent.Context has no public WithContext setter).
	aCtx := agent.NewContext(ctx)
	if dt.config.NewContext != nil {
		if user := dt.config.NewContext(); user != nil {
			if id := user.ConversationID(); id != "" {
				aCtx = aCtx.WithConversationID(id)
			}
			if cfg := user.InferenceConfig(); cfg != nil {
				aCtx = aCtx.WithInferenceConfig(cfg)
			}
			if imgs := user.Images(); len(imgs) > 0 {
				aCtx = aCtx.WithImages(imgs)
			}
			if docs := user.Documents(); len(docs) > 0 {
				aCtx = aCtx.WithDocuments(docs)
			}
			if id := user.Identifier(); id != "" {
				aCtx = aCtx.WithIdentifier(id)
			}
		}
	}

	dt.send(conn, map[string]any{
		"type": "turn_start",
		"ts":   time.Now().UnixMilli(),
	})

	events := dt.config.Agent.InvokeEventStream(aCtx, userText)
	var lastErr error
	for ev := range events {
		if ev.Type == agent.EventInvokeEnd {
			lastErr = ev.Err
		}
		dt.dispatchEvent(conn, ev)
	}

	if dt.config.OnTurnEnd != nil {
		dt.config.OnTurnEnd(aCtx, lastErr)
	}
}

// dispatchEvent translates an agent.AgentEvent into a UI-friendly JSON frame.
// We intentionally split events into two channels: a generic "event" frame
// for the timeline, plus typed frames for fast-path UI rendering of
// streaming chunks, tool calls, and iteration boundaries.
func (dt *AgentDevTools) dispatchEvent(conn *websocket.Conn, ev agent.AgentEvent) {
	// Generic timeline frame — every event lands here.
	frame := map[string]any{
		"type":      "event",
		"event":     ev.Type,
		"timestamp": ev.Timestamp.UnixMilli(),
	}
	if ev.Iteration > 0 {
		frame["iteration"] = ev.Iteration
	}
	if ev.ToolName != "" {
		frame["tool_name"] = ev.ToolName
	}
	if ev.StopReason != "" {
		frame["stop_reason"] = ev.StopReason
	}
	if ev.Duration > 0 {
		frame["duration_ms"] = ev.Duration.Milliseconds()
	}
	dt.send(conn, frame)

	// Typed fast-path frames the UI renders with bespoke layouts.
	switch ev.Type {
	case agent.EventInvokeStart:
		dt.send(conn, map[string]any{"type": "invoke_start"})

	case agent.EventIterationStart:
		dt.send(conn, map[string]any{
			"type":      "iteration_start",
			"iteration": ev.Iteration,
		})

	case agent.EventModelStart:
		dt.send(conn, map[string]any{
			"type":      "model_start",
			"iteration": ev.Iteration,
		})

	case agent.EventTextChunk:
		if ev.TextChunk != "" {
			dt.send(conn, map[string]any{
				"type":  "text_chunk",
				"chunk": ev.TextChunk,
			})
		}

	case agent.EventThinkingChunk:
		if ev.ThinkingChunk != "" {
			dt.send(conn, map[string]any{
				"type":  "thinking_chunk",
				"chunk": ev.ThinkingChunk,
			})
		}

	case agent.EventToolCallStart:
		dt.send(conn, map[string]any{
			"type":      "tool_start",
			"tool_name": ev.ToolName,
			"input":     string(ev.ToolInput),
		})

	case agent.EventToolCallEnd:
		dt.send(conn, map[string]any{
			"type":        "tool_end",
			"tool_name":   ev.ToolName,
			"output":      ev.ToolOutput,
			"duration_ms": ev.Duration.Milliseconds(),
			"error":       errString(ev.Err),
		})

	case agent.EventModelEnd:
		dt.send(conn, map[string]any{
			"type":        "model_end",
			"stop_reason": ev.StopReason,
		})

	case agent.EventIterationEnd:
		dt.send(conn, map[string]any{
			"type":        "iteration_end",
			"iteration":   ev.Iteration,
			"tool_count":  ev.ToolCount,
			"is_final":    ev.IsFinal,
			"duration_ms": ev.Duration.Milliseconds(),
		})

	case agent.EventMaxIterations:
		dt.send(conn, map[string]any{
			"type":            "max_iterations",
			"iteration_limit": ev.IterationLimit,
		})

	case agent.EventInvokeEnd:
		dt.send(conn, map[string]any{
			"type":          "invoke_end",
			"error":         errString(ev.Err),
			"input_tokens":  ev.Usage.InputTokens,
			"output_tokens": ev.Usage.OutputTokens,
		})

	case agent.EventCustom:
		dt.send(conn, map[string]any{
			"type":    "custom",
			"name":    ev.CustomName,
			"payload": ev.CustomPayload,
		})
	}
}

// send writes a JSON frame to a single client. Errors close the connection;
// the surrounding read loop will then terminate and clean up.
func (dt *AgentDevTools) send(conn *websocket.Conn, msg any) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	if err := conn.WriteJSON(msg); err != nil {
		conn.Close()
		delete(dt.clients, conn)
	}
}
