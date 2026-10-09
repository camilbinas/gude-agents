package agent

import (
	"errors"
	"fmt"
	"testing"
)

// TestWrappedFrameworkErrorsSupportErrorsAs verifies that errors.As finds each
// framework error type through arbitrarily deep fmt.Errorf wrapping and that
// the original cause stays reachable.
func TestWrappedFrameworkErrorsSupportErrorsAs(t *testing.T) {
	cause := errors.New("root cause")

	tests := []struct {
		name string
		err  error
		as   func(error) bool
	}{
		{
			name: "ProviderError",
			err:  &ProviderError{Cause: cause},
			as:   func(err error) bool { var target *ProviderError; return errors.As(err, &target) },
		},
		{
			name: "ToolError",
			err:  &ToolError{ToolName: "my_tool", Cause: cause},
			as:   func(err error) bool { var target *ToolError; return errors.As(err, &target) },
		},
		{
			name: "GuardrailError",
			err:  &GuardrailError{Direction: "input", Cause: cause},
			as:   func(err error) bool { var target *GuardrailError; return errors.As(err, &target) },
		},
	}

	for _, tt := range tests {
		for _, depth := range []int{0, 1, 3, 10} {
			t.Run(fmt.Sprintf("%s/depth=%d", tt.name, depth), func(t *testing.T) {
				chain := tt.err
				for i := 0; i < depth; i++ {
					chain = fmt.Errorf("wrap %d: %w", i, chain)
				}
				if !tt.as(chain) {
					t.Fatalf("errors.As did not find %s through %d wrapping levels", tt.name, depth)
				}
				if !errors.Is(chain, cause) {
					t.Fatalf("errors.Is did not reach the root cause through %s", tt.name)
				}
			})
		}
	}
}
