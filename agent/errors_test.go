package agent


import (
	"errors"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// TestProperty5 verifies that errors.As succeeds at every wrapping depth
// for ProviderError, ToolError, and GuardrailError.
//
func TestProperty5(t *testing.T) {
	t.Run("ProviderError", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			depth := rapid.IntRange(1, 10).Draw(t, "depth")
			base := &ProviderError{Cause: errors.New("provider failure")}
			var chain error = base
			for i := 0; i < depth; i++ {
				chain = fmt.Errorf("wrap %d: %w", i, chain)
			}
			var target *ProviderError
			if !errors.As(chain, &target) {
				t.Fatalf("errors.As failed for ProviderError at depth %d", depth)
			}
		})
	})

	t.Run("ToolError", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			depth := rapid.IntRange(1, 10).Draw(t, "depth")
			base := &ToolError{ToolName: "my_tool", Cause: errors.New("tool failure")}
			var chain error = base
			for i := 0; i < depth; i++ {
				chain = fmt.Errorf("wrap %d: %w", i, chain)
			}
			var target *ToolError
			if !errors.As(chain, &target) {
				t.Fatalf("errors.As failed for ToolError at depth %d", depth)
			}
		})
	})

	t.Run("GuardrailError", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			depth := rapid.IntRange(1, 10).Draw(t, "depth")
			base := &GuardrailError{Direction: "input", Cause: errors.New("guardrail failure")}
			var chain error = base
			for i := 0; i < depth; i++ {
				chain = fmt.Errorf("wrap %d: %w", i, chain)
			}
			var target *GuardrailError
			if !errors.As(chain, &target) {
				t.Fatalf("errors.As failed for GuardrailError at depth %d", depth)
			}
		})
	})
}
