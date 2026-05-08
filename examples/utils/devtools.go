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

	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/gorilla/websocket"
)

//go:embed devtools.html
var devtoolsFS embed.FS

// DevToolsConfig configures the DevTools server.
type DevToolsConfig struct {
	Port         int
	Structure    graph.GraphStructure
	RunFunc      func(ctx context.Context, dt *DevToolsHook) error
	Checkpointer graph.GraphCheckpointer // optional: enables checkpoint history in the UI
	ThreadID     string                  // optional: default thread ID for checkpoint history queries
}

// DevTools serves a web UI for visualizing graph execution in real-time.
type DevTools struct {
	config  DevToolsConfig
	clients map[*websocket.Conn]bool
	mu      sync.Mutex

	// Run state: cancel function for the current run.
	cancelMu     sync.Mutex
	cancelFn     context.CancelFunc
	paused       bool
	activeThread string // thread ID for the current run (set by frontend)
}

// NewDevTools creates a new DevTools instance.
func NewDevTools(config DevToolsConfig) *DevTools {
	if config.Port == 0 {
		config.Port = 4040
	}
	return &DevTools{
		config:  config,
		clients: make(map[*websocket.Conn]bool),
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ListenAndServe starts the devtools HTTP server and blocks.
func (dt *DevTools) ListenAndServe() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := devtoolsFS.ReadFile("devtools.html")
		if err != nil {
			http.Error(w, "failed to read devtools.html", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	mux.HandleFunc("/ws", dt.handleWS)

	addr := fmt.Sprintf(":%d", dt.config.Port)
	url := fmt.Sprintf("http://localhost:%d", dt.config.Port)
	log.Printf("DevTools running at %s", url)

	go openBrowser(url)

	return http.ListenAndServe(addr, mux)
}

func (dt *DevTools) handleWS(w http.ResponseWriter, r *http.Request) {
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

	// Send graph structure on connect.
	structureMsg := map[string]any{
		"type":      "structure",
		"structure": dt.config.Structure,
	}
	if err := conn.WriteJSON(structureMsg); err != nil {
		log.Printf("WebSocket write error: %v", err)
		return
	}

	// Read messages from client.
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var action struct {
			Action   string `json:"action"`
			ThreadID string `json:"thread_id"`
		}
		if err := json.Unmarshal(msg, &action); err != nil {
			continue
		}

		switch action.Action {
		case "start":
			if action.ThreadID != "" {
				dt.setActiveThread(action.ThreadID)
			}
			go dt.runGraph()
		case "pause":
			dt.pause()
		case "resume":
			if action.ThreadID != "" {
				dt.setActiveThread(action.ThreadID)
			}
			go dt.runGraph() // RunFunc handles resume via checkpoint
		case "checkpoints":
			dt.sendCheckpointHistory(conn, msg)
		case "checkpoint_detail":
			dt.sendCheckpointDetail(conn, msg)
		}
	}
}

func (dt *DevTools) pause() {
	dt.cancelMu.Lock()
	defer dt.cancelMu.Unlock()
	if dt.cancelFn != nil {
		dt.cancelFn()
		dt.cancelFn = nil
		dt.paused = true
	}
	dt.broadcast(map[string]any{"type": "paused"})
}

func (dt *DevTools) setActiveThread(id string) {
	dt.cancelMu.Lock()
	defer dt.cancelMu.Unlock()
	dt.activeThread = id
}

func (dt *DevTools) runGraph() {
	ctx, cancel := context.WithCancel(context.Background())
	dt.cancelMu.Lock()
	dt.cancelFn = cancel
	dt.paused = false
	threadID := dt.activeThread
	dt.cancelMu.Unlock()

	hook := &DevToolsHook{dt: dt, ThreadID: threadID}

	err := dt.config.RunFunc(ctx, hook)

	dt.cancelMu.Lock()
	dt.cancelFn = nil
	wasPaused := dt.paused
	dt.cancelMu.Unlock()

	if wasPaused || (err != nil && err == context.Canceled) {
		dt.broadcast(map[string]any{"type": "paused"})
		return
	}

	var errStr *string
	if err != nil {
		s := err.Error()
		errStr = &s
	}
	dt.broadcast(map[string]any{"type": "done", "error": errStr})
}

func (dt *DevTools) broadcast(msg any) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	for conn := range dt.clients {
		if err := conn.WriteJSON(msg); err != nil {
			conn.Close()
			delete(dt.clients, conn)
		}
	}
}

// DevToolsHook implements graph.GraphEventHook and provides streaming output.
type DevToolsHook struct {
	dt       *DevTools
	ThreadID string // thread ID for this run, set by the frontend
}

// OnEvent implements graph.GraphEventHook.
func (h *DevToolsHook) OnEvent(event graph.GraphEvent) {
	h.dt.broadcast(map[string]any{
		"type":  "event",
		"event": event,
	})

	// Emit additional typed messages for the frontend to render richer UI.
	switch event.Type {
	case graph.EventCheckpointSaved:
		// Notify frontend that a new checkpoint version is available.
		h.dt.broadcast(map[string]any{
			"type":      "checkpoint_saved",
			"thread_id": event.ThreadID,
			"version":   event.Version,
			"node_name": event.NodeName,
			"timestamp": event.Timestamp,
		})
	case graph.EventAgentStreaming:
		if event.Chunk != "" {
			h.dt.broadcast(map[string]any{
				"type":  "stream",
				"node":  event.NodeName,
				"chunk": event.Chunk,
			})
		}
	case graph.EventAgentToolCallStart:
		h.dt.broadcast(map[string]any{
			"type":      "tool_start",
			"node":      event.NodeName,
			"tool_name": event.ToolName,
			"input":     string(event.ToolInput),
		})
	case graph.EventAgentToolCallEnd:
		h.dt.broadcast(map[string]any{
			"type":      "tool_end",
			"node":      event.NodeName,
			"tool_name": event.ToolName,
			"output":    event.ToolOutput,
			"duration":  event.ToolDuration.Milliseconds(),
			"error":     errString(event.Error),
		})
	case graph.EventAgentThinking:
		if event.Chunk != "" {
			h.dt.broadcast(map[string]any{
				"type":  "thinking",
				"node":  event.NodeName,
				"chunk": event.Chunk,
			})
		}
	}
}

// EventHook returns the hook as a graph.GraphEventHook for use with WithEventHook.
func (h *DevToolsHook) EventHook() graph.GraphEventHook {
	return h
}

// StreamCallback returns a callback that sends chunks to the frontend
// tagged with the given node name. Use this as the callback in InvokeStream.
func (h *DevToolsHook) StreamCallback(nodeName string) func(chunk string) {
	return func(chunk string) {
		h.dt.broadcast(map[string]any{
			"type":  "stream",
			"node":  nodeName,
			"chunk": chunk,
		})
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// sendCheckpointHistory sends the checkpoint history for the configured thread.
func (dt *DevTools) sendCheckpointHistory(conn *websocket.Conn, msg []byte) {
	if dt.config.Checkpointer == nil {
		conn.WriteJSON(map[string]any{"type": "checkpoint_history", "error": "no checkpointer configured"})
		return
	}

	// Allow client to specify a thread_id, fall back to active thread, then config default.
	var req struct {
		ThreadID string `json:"thread_id"`
	}
	json.Unmarshal(msg, &req)
	threadID := req.ThreadID
	if threadID == "" {
		dt.cancelMu.Lock()
		threadID = dt.activeThread
		dt.cancelMu.Unlock()
	}
	if threadID == "" {
		threadID = dt.config.ThreadID
	}
	if threadID == "" {
		// If no thread specified, list all threads and return history for each.
		threads, err := dt.config.Checkpointer.List(context.Background())
		if err != nil {
			conn.WriteJSON(map[string]any{"type": "checkpoint_history", "error": err.Error()})
			return
		}
		if len(threads) == 0 {
			conn.WriteJSON(map[string]any{"type": "checkpoint_history", "threads": []string{}, "history": []any{}})
			return
		}
		threadID = threads[0] // default to first thread
	}

	history, err := dt.config.Checkpointer.History(context.Background(), threadID)
	if err != nil {
		conn.WriteJSON(map[string]any{"type": "checkpoint_history", "error": err.Error()})
		return
	}

	// Also list all available threads.
	threads, _ := dt.config.Checkpointer.List(context.Background())

	conn.WriteJSON(map[string]any{
		"type":      "checkpoint_history",
		"thread_id": threadID,
		"threads":   threads,
		"history":   history,
	})
}

// sendCheckpointDetail sends the full checkpoint at a specific version.
func (dt *DevTools) sendCheckpointDetail(conn *websocket.Conn, msg []byte) {
	if dt.config.Checkpointer == nil {
		conn.WriteJSON(map[string]any{"type": "checkpoint_detail", "error": "no checkpointer configured"})
		return
	}

	var req struct {
		ThreadID string `json:"thread_id"`
		Version  int    `json:"version"`
	}
	if err := json.Unmarshal(msg, &req); err != nil {
		conn.WriteJSON(map[string]any{"type": "checkpoint_detail", "error": "invalid request"})
		return
	}
	threadID := req.ThreadID
	if threadID == "" {
		dt.cancelMu.Lock()
		threadID = dt.activeThread
		dt.cancelMu.Unlock()
	}
	if threadID == "" {
		threadID = dt.config.ThreadID
	}
	if threadID == "" {
		conn.WriteJSON(map[string]any{"type": "checkpoint_detail", "error": "no thread_id specified"})
		return
	}

	cp, err := dt.config.Checkpointer.LoadAt(context.Background(), threadID, req.Version)
	if err != nil {
		conn.WriteJSON(map[string]any{"type": "checkpoint_detail", "error": err.Error()})
		return
	}

	conn.WriteJSON(map[string]any{
		"type":       "checkpoint_detail",
		"checkpoint": cp,
	})
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
