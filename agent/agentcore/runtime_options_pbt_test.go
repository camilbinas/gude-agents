package agentcore

import (
	"testing"
	"time"

	"pgregory.net/rapid"
)

// Feature: agentcore-runtime, Property 2: Duration option validation

// TestProperty_DurationOptionValidation verifies that for any time.Duration value,
// applying WithHeartbeatInterval(d) or WithShutdownTimeout(d) SHALL succeed when d > 0
// and SHALL return an error when d <= 0.
//
// **Validates: Requirements 1.6, 1.7**
func TestProperty_DurationOptionValidation(t *testing.T) {
	t.Run("WithHeartbeatInterval", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate a random duration covering negative, zero, and positive ranges.
			// Use a wide range: -1h to +1h in nanoseconds.
			d := time.Duration(rapid.Int64Range(-int64(time.Hour), int64(time.Hour)).Draw(rt, "duration"))

			cfg := defaultRuntimeConfig()
			opt := WithHeartbeatInterval(d)
			err := opt(&cfg)

			if d > 0 {
				// Positive duration: must succeed and update the config field.
				if err != nil {
					rt.Fatalf("WithHeartbeatInterval(%v) returned unexpected error: %v", d, err)
				}
				if cfg.heartbeatInterval != d {
					rt.Fatalf("WithHeartbeatInterval(%v) did not update config: got %v", d, cfg.heartbeatInterval)
				}
			} else {
				// Zero or negative duration: must return ErrHeartbeatInterval.
				if err != ErrHeartbeatInterval {
					rt.Fatalf("WithHeartbeatInterval(%v) expected ErrHeartbeatInterval, got: %v", d, err)
				}
			}
		})
	})

	t.Run("WithShutdownTimeout", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate a random duration covering negative, zero, and positive ranges.
			d := time.Duration(rapid.Int64Range(-int64(time.Hour), int64(time.Hour)).Draw(rt, "duration"))

			cfg := defaultRuntimeConfig()
			opt := WithShutdownTimeout(d)
			err := opt(&cfg)

			if d > 0 {
				// Positive duration: must succeed and update the config field.
				if err != nil {
					rt.Fatalf("WithShutdownTimeout(%v) returned unexpected error: %v", d, err)
				}
				if cfg.shutdownTimeout != d {
					rt.Fatalf("WithShutdownTimeout(%v) did not update config: got %v", d, cfg.shutdownTimeout)
				}
			} else {
				// Zero or negative duration: must return ErrShutdownTimeout.
				if err != ErrShutdownTimeout {
					rt.Fatalf("WithShutdownTimeout(%v) expected ErrShutdownTimeout, got: %v", d, err)
				}
			}
		})
	})
}
