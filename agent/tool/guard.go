package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// Decision is the result of a guard check.
type Decision struct {
	Allow  bool
	Reason string
}

// Allow returns a Decision that permits the tool call.
func Allow() Decision { return Decision{Allow: true} }

// Deny returns a Decision that blocks the tool call with the given reason.
func Deny(reason string) Decision { return Decision{Allow: false, Reason: reason} }

// Denyf returns a Decision that blocks the tool call with a formatted reason.
func Denyf(format string, a ...any) Decision {
	return Decision{Allow: false, Reason: fmt.Sprintf(format, a...)}
}

// WithGuard returns a tool option that attaches a typed guard to the tool.
// The guard receives the deserialized input T and decides whether the call
// should proceed. If the guard denies, the tool handler is not invoked and
// the LLM receives a structured denial result.
func WithGuard[T any](guard func(ctx context.Context, input T) (Decision, error)) func(*Tool) {
	return func(t *Tool) {
		t.Guard = func(ctx context.Context, raw json.RawMessage) (Decision, error) {
			var v T
			if err := json.Unmarshal(raw, &v); err != nil {
				return Deny(fmt.Sprintf("guard: invalid input: %v", err)), nil
			}
			return guard(ctx, v)
		}
	}
}
