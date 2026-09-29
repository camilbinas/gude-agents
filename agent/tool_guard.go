package agent

import (
	"encoding/json"
	"errors"
)

// ErrToolCallDenied identifies a tool call blocked by a guard.
var ErrToolCallDenied = errors.New("tool_call_denied")

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
