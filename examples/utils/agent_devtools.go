package utils

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"runtime"
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
// real-time.
type AgentDevToolsConfig struct {
	// Port is the HTTP port to bind. Defaults to 4041.
	Port int

	// Agent is the agent under inspection. Required.
	Agent *agent.Agent

	// AgentName is the display name shown in the UI header. If empty, falls
	// back to the agent's configured name, then to "agent". Override only when
	// you want a friendlier label than the agent's internal name.
	AgentName string

	// NewContext, when set, is called with the user input to build the
	// *agent.Context used for each turn. Use it to wire conversation IDs,
	// inference config, attachments, or other per-turn options. The returned
	// context is copied and attached to the turn's cancellation context.
	NewContext func(input string) *agent.Context

	// OnTurnEnd, when set, is called after each user turn completes or the
	// stream consumer disconnects. Result is the canonical invocation outcome;
	// err is the stream or transport error, if any.
	OnTurnEnd func(c *agent.Context, result agent.Result, err error)

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
		_ = conn.Close()
	}()

	dt.mu.Lock()
	dt.clients[conn] = true
	dt.mu.Unlock()

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
	if err := dt.send(conn, meta); err != nil {
		log.Printf("WebSocket write error: %v", err)
		return
	}

	// One websocket connection is one chat session. Cancellation stops a
	// running stream when the client disconnects or sends "stop".
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
				runMu.Unlock()
				if err := dt.send(conn, map[string]any{
					"type":    "error",
					"message": "previous turn is still running; press Stop to cancel",
				}); err != nil {
					return
				}
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
			if err := dt.config.OnClear(context.Background()); err != nil {
				if writeErr := dt.send(conn, map[string]any{
					"type":    "error",
					"message": "clear failed: " + err.Error(),
				}); writeErr != nil {
					return
				}
				continue
			}
			if err := dt.send(conn, map[string]any{"type": "cleared"}); err != nil {
				return
			}
		}
	}
}

// runTurn drives one user turn through Agent.Stream and forwards every event
// to the websocket. Returning from the range loop stops the synchronous stream
// producer, so a disconnected consumer cancels provider and tool work cleanly.
func (dt *AgentDevTools) runTurn(ctx context.Context, conn *websocket.Conn, userText string) {
	aCtx := agent.NewContext(ctx)
	if dt.config.NewContext != nil {
		if configured := dt.config.NewContext(userText); configured != nil {
			configuredCopy := *configured
			configuredCopy.Context = ctx
			aCtx = &configuredCopy
		}
	}
	aCtx.WithDetailedEvents()

	var (
		result  agent.Result
		turnErr error
	)
	defer func() {
		if dt.config.OnTurnEnd != nil {
			dt.config.OnTurnEnd(aCtx, result, turnErr)
		}
	}()

	if err := dt.send(conn, map[string]any{
		"type": "user_message",
		"text": userText,
		"ts":   time.Now().UnixMilli(),
	}); err != nil {
		turnErr = err
		return
	}
	if err := dt.send(conn, map[string]any{
		"type": "turn_start",
		"ts":   time.Now().UnixMilli(),
	}); err != nil {
		turnErr = err
		return
	}

	for ev, streamErr := range dt.config.Agent.Stream(aCtx, userText) {
		if ev.Type == agent.EventEnd && ev.Result != nil {
			result = *ev.Result
		}
		if streamErr != nil {
			turnErr = streamErr
		}
		if err := dt.dispatchEvent(conn, ev); err != nil {
			turnErr = err
			break
		}
	}
}

// dispatchEvent forwards the complete grouped event and typed compatibility
// frames used by the UI. Returning a write error lets the Stream consumer stop
// immediately when its websocket disappears.
func (dt *AgentDevTools) dispatchEvent(conn *websocket.Conn, ev agent.Event) error {
	if err := dt.send(conn, map[string]any{
		"type":  "event",
		"event": ev,
	}); err != nil {
		return err
	}

	send := func(frame map[string]any) error { return dt.send(conn, frame) }
	switch ev.Type {
	case agent.EventStart:
		return send(map[string]any{"type": "invoke_start"})

	case agent.EventText:
		if ev.Text != nil && ev.Text.Content != "" {
			return send(map[string]any{"type": "text_chunk", "chunk": ev.Text.Content})
		}

	case agent.EventThinking:
		if ev.Thinking != nil && ev.Thinking.Content != "" {
			return send(map[string]any{"type": "thinking_chunk", "chunk": ev.Thinking.Content})
		}

	case agent.EventToolStart:
		if ev.Tool != nil {
			return send(map[string]any{
				"type":      "tool_start",
				"call_id":   ev.Tool.CallID,
				"tool_name": ev.Tool.Name,
				"input":     string(ev.Tool.Input),
			})
		}

	case agent.EventToolEnd:
		if ev.Tool != nil {
			return send(map[string]any{
				"type":        "tool_end",
				"call_id":     ev.Tool.CallID,
				"tool_name":   ev.Tool.Name,
				"output":      ev.Tool.Output,
				"duration_ms": ev.Tool.Duration.Milliseconds(),
				"error":       errorMessage(ev.Tool.Error),
				"error_info":  ev.Tool.Error,
			})
		}

	case agent.EventWidget:
		if ev.Widget != nil {
			return send(map[string]any{
				"type":        "widget",
				"call_id":     ev.Widget.CallID,
				"widget_type": ev.Widget.Type,
				"payload":     ev.Widget.Payload,
			})
		}

	case agent.EventInterrupt:
		if ev.Interrupt != nil {
			return send(map[string]any{"type": "interrupt", "interrupt": ev.Interrupt})
		}

	case agent.EventCustom:
		if ev.Custom != nil {
			return send(map[string]any{
				"type":    "custom",
				"name":    ev.Custom.Name,
				"payload": ev.Custom.Payload,
			})
		}

	case agent.EventIterationStart:
		if ev.Lifecycle != nil {
			return send(map[string]any{"type": "iteration_start", "iteration": ev.Lifecycle.Iteration})
		}

	case agent.EventIterationEnd:
		if ev.Lifecycle != nil {
			return send(map[string]any{
				"type":        "iteration_end",
				"iteration":   ev.Lifecycle.Iteration,
				"tool_count":  ev.Lifecycle.ToolCount,
				"is_final":    ev.Lifecycle.IsFinal,
				"duration_ms": ev.Lifecycle.Duration.Milliseconds(),
			})
		}

	case agent.EventModelStart:
		if ev.Lifecycle != nil {
			return send(map[string]any{"type": "model_start", "iteration": ev.Lifecycle.Iteration})
		}

	case agent.EventModelEnd:
		if ev.Lifecycle != nil {
			return send(map[string]any{
				"type":        "model_end",
				"iteration":   ev.Lifecycle.Iteration,
				"stop_reason": ev.Lifecycle.StopReason,
				"duration_ms": ev.Lifecycle.Duration.Milliseconds(),
			})
		}

	case agent.EventMaxIterations:
		if ev.Lifecycle != nil {
			return send(map[string]any{"type": "max_iterations", "limit": ev.Lifecycle.Limit})
		}

	case agent.EventEnd:
		result := agent.Result{}
		if ev.Result != nil {
			result = *ev.Result
		}
		return send(map[string]any{
			"type":               "invoke_end",
			"error":              errorMessage(ev.Error),
			"error_info":         ev.Error,
			"stop_reason":        result.StopReason,
			"interrupt":          result.Interrupt,
			"input_tokens":       result.Usage.InputTokens,
			"output_tokens":      result.Usage.OutputTokens,
			"cache_read_tokens":  result.Usage.CacheReadTokens,
			"cache_write_tokens": result.Usage.CacheWriteTokens,
		})
	}
	return nil
}

// send writes a JSON frame to one client. The caller must propagate the error
// so an active Stream consumer can stop immediately.
func (dt *AgentDevTools) send(conn *websocket.Conn, msg any) error {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	if err := conn.WriteJSON(msg); err != nil {
		_ = conn.Close()
		delete(dt.clients, conn)
		return err
	}
	return nil
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

func errorMessage(info *agent.ErrorInfo) string {
	if info == nil {
		return ""
	}
	return info.Message
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	_ = cmd.Start()
}
