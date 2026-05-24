package agent

import (
	"encoding/json"
	"errors"
)

// ErrToolCallDenied is reported to MetricsHook when a tool guard denies a call.
// It is NEVER returned to the agent loop or to the LLM; it is observability-only.
var ErrToolCallDenied = errors.New("tool_call_denied")

// guardDenialKey is the *Context KV key carrying per-call denial state from
// the guard check to executeToolsWithMiddleware. The guard stashes a state
// value on the per-call *Context just before returning the denial result
// string and a nil error to the chain; the loop reads the value to translate
// the denial into a ToolResultBlock{IsError: true} and to feed the
// ErrToolCallDenied sentinel to the metrics hook.
type guardDenialKey struct{}

// guardDenialState is the value stored under guardDenialKey when a guard
// denies a call.
type guardDenialState struct {
	Tool   string
	Reason string
	Result string
}

// denialResultJSON returns the canonical denial result JSON object for a
// denied tool call.
func denialResultJSON(toolName, reason string) string {
	type body struct {
		Error  string `json:"error"`
		Tool   string `json:"tool"`
		Reason string `json:"reason"`
	}
	b, _ := json.Marshal(body{Error: "tool_call_denied", Tool: toolName, Reason: reason})
	return string(b)
}
