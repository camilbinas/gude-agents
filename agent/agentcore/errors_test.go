package agentcore

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"

	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func TestSentinelErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"ErrAgentRequired", ErrAgentRequired, "agent is required"},
		{"ErrAgentNameRequired", ErrAgentNameRequired, "agent name is required"},
		{"ErrHeartbeatInterval", ErrHeartbeatInterval, "heartbeat interval must be positive"},
		{"ErrShutdownTimeout", ErrShutdownTimeout, "shutdown timeout must be positive"},
		{"ErrAlreadyRunning", ErrAlreadyRunning, "runtime is already running"},
		{"ErrLTMNamespaceRequired", ErrLTMNamespaceRequired, "agentcore ltm: namespace is required"},
		{"ErrLTMContentRequired", ErrLTMContentRequired, "agentcore ltm: content is required"},
		{"ErrLTMQueryRequired", ErrLTMQueryRequired, "agentcore ltm: query is required"},
		{"ErrLTMIDRequired", ErrLTMIDRequired, "agentcore ltm: id is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.want {
				t.Errorf("got %q, want %q", tt.err.Error(), tt.want)
			}
		})
	}
}

func TestConversationError(t *testing.T) {
	underlying := errors.New("something failed")
	err := wrapConversationError(underlying)

	// Check prefix.
	want := "agentcore conversation: something failed"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}

	// Check unwrap.
	if !errors.Is(err, underlying) {
		t.Error("expected errors.Is to find underlying error")
	}

	// Nil input returns nil.
	if wrapConversationError(nil) != nil {
		t.Error("expected nil for nil input")
	}
}

func TestNewConversationErrorf(t *testing.T) {
	underlying := errors.New("timeout")
	err := newConversationErrorf("save", underlying)

	want := "agentcore conversation: save: timeout"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}

	// Should unwrap to find the underlying error.
	var convErr *conversationError
	if !errors.As(err, &convErr) {
		t.Error("expected errors.As to find conversationError")
	}
}

func TestIsTransient(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if isTransient(nil) {
			t.Error("nil should not be transient")
		}
	})

	t.Run("5xx response error", func(t *testing.T) {
		err := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{
				Response: &http.Response{StatusCode: 503},
			},
			Err: errors.New("service unavailable"),
		}
		if !isTransient(err) {
			t.Error("503 should be transient")
		}
	})

	t.Run("4xx response error is not transient", func(t *testing.T) {
		err := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{
				Response: &http.Response{StatusCode: 400},
			},
			Err: errors.New("bad request"),
		}
		if isTransient(err) {
			t.Error("400 should not be transient")
		}
	})

	t.Run("network timeout", func(t *testing.T) {
		err := &fakeNetError{timeout: true}
		if !isTransient(err) {
			t.Error("timeout should be transient")
		}
	})

	t.Run("connection refused via OpError", func(t *testing.T) {
		err := &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: errors.New("connection refused"),
		}
		if !isTransient(err) {
			t.Error("connection refused should be transient")
		}
	})

	t.Run("DNS failure via message", func(t *testing.T) {
		err := fmt.Errorf("lookup example.com: no such host")
		if !isTransient(err) {
			t.Error("DNS failure should be transient")
		}
	})

	t.Run("wrapped 500 error", func(t *testing.T) {
		inner := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{
				Response: &http.Response{StatusCode: 500},
			},
			Err: errors.New("internal server error"),
		}
		err := fmt.Errorf("operation failed: %w", inner)
		if !isTransient(err) {
			t.Error("wrapped 500 should be transient")
		}
	})

	t.Run("generic error is not transient", func(t *testing.T) {
		err := errors.New("something else")
		if isTransient(err) {
			t.Error("generic error should not be transient")
		}
	})
}

func TestIsPermanent(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if isPermanent(nil) {
			t.Error("nil should not be permanent")
		}
	})

	t.Run("400 is permanent", func(t *testing.T) {
		err := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{
				Response: &http.Response{StatusCode: 400},
			},
			Err: errors.New("bad request"),
		}
		if !isPermanent(err) {
			t.Error("400 should be permanent")
		}
	})

	t.Run("403 is permanent", func(t *testing.T) {
		err := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{
				Response: &http.Response{StatusCode: 403},
			},
			Err: errors.New("forbidden"),
		}
		if !isPermanent(err) {
			t.Error("403 should be permanent")
		}
	})

	t.Run("404 is NOT permanent", func(t *testing.T) {
		err := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{
				Response: &http.Response{StatusCode: 404},
			},
			Err: errors.New("not found"),
		}
		if isPermanent(err) {
			t.Error("404 should not be permanent (used for re-registration)")
		}
	})

	t.Run("500 is not permanent", func(t *testing.T) {
		err := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{
				Response: &http.Response{StatusCode: 500},
			},
			Err: errors.New("internal server error"),
		}
		if isPermanent(err) {
			t.Error("500 should not be permanent")
		}
	})

	t.Run("generic error is not permanent", func(t *testing.T) {
		err := errors.New("something else")
		if isPermanent(err) {
			t.Error("generic error should not be permanent")
		}
	})

	t.Run("wrapped 429 is permanent", func(t *testing.T) {
		inner := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{
				Response: &http.Response{StatusCode: 429},
			},
			Err: errors.New("too many requests"),
		}
		err := fmt.Errorf("operation failed: %w", inner)
		if !isPermanent(err) {
			t.Error("wrapped 429 should be permanent")
		}
	})
}

// fakeNetError implements net.Error for testing.
type fakeNetError struct {
	timeout bool
}

func (e *fakeNetError) Error() string   { return "fake net error" }
func (e *fakeNetError) Timeout() bool   { return e.timeout }
func (e *fakeNetError) Temporary() bool { return false }
