package agent

import (
	"encoding/json"
	"errors"
)

// ErrToolCallDenied is reported to MetricsHook when a tool guard denies a call.
var ErrToolCallDenied = errors.New("tool_call_denied")

// guardDenialKey is the *Context KV key for per-call denial state.
type guardDenialKey struct{}

// guardDenialState is stored under guardDenialKey when a guard denies a call.
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
