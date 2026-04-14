package agent

import "context"

// InputGuardrail validates/transforms the user message before it reaches the Provider.
// Documented in docs/guardrails.md — update when changing signature or behavior.
type InputGuardrail func(ctx context.Context, message string) (string, error)

// OutputGuardrail validates/transforms the final response before returning to the caller.
// Documented in docs/guardrails.md — update when changing signature or behavior.
type OutputGuardrail func(ctx context.Context, response string) (string, error)
