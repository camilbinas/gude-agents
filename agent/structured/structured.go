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

// Invoke forces a structured response for T while reusing the Agent's normal
// lifecycle, locking, RAG, guardrail, retry, and observability behavior.
// The output must satisfy the schema generated from T within the supported
// subset enforced by agent.ValidateToolInput (type, enum, required,
// properties, items) and must decode into T; otherwise Invoke returns an
// *agent.StructuredOutputError with Reason "schema_validation" or
// "deserialize".
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
