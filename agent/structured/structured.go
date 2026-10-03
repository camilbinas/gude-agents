// Package structured provides typed structured-output invocation.
package structured

import (
	"encoding/json"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// Result contains the decoded value and the complete Agent run outcome.
type Result[T any] struct {
	Value T
	Run   agent.Result
}

// Invoke requests structured output for T while reusing the Agent's normal
// lifecycle, locking, RAG, guardrail, retry, and observability behavior.
// It uses a named forced schema tool where supported; models that explicitly
// support only automatic tool choice receive the sole schema tool without an
// explicit selection, so callers must also handle StructuredOutputError with
// Reason "no_tool_call". Successful output still satisfies the schema subset
// and decodes into T.
func Invoke[T any](ctx *agent.Context, a *agent.Agent, input string) (Result[T], error) {
	var out Result[T]
	if a == nil {
		return out, &agent.StructuredOutputError{Reason: "nil_agent"}
	}
	run, err := a.InvokeSchema(ctx, input, tool.GenerateSchema[T](), func(raw json.RawMessage) error {
		return json.Unmarshal(raw, &out.Value)
	})
	out.Run = run
	if err != nil {
		return out, err
	}
	return out, nil
}
