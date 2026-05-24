package agentcore

import (
	"errors"
	"fmt"
	"net"
	"strings"

	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Sentinel errors returned by constructors and runtime methods.
var (
	// ErrAgentRequired is returned when a nil agent is passed to NewRuntime.
	ErrAgentRequired = errors.New("agent is required")

	// ErrAgentNameRequired is returned when no agent name can be resolved.
	ErrAgentNameRequired = errors.New("agent name is required")

	// ErrHeartbeatInterval is returned when a non-positive heartbeat interval is provided.
	ErrHeartbeatInterval = errors.New("heartbeat interval must be positive")

	// ErrShutdownTimeout is returned when a non-positive shutdown timeout is provided.
	ErrShutdownTimeout = errors.New("shutdown timeout must be positive")

	// ErrAlreadyRunning is returned when Run is called on an already-running Runtime.
	ErrAlreadyRunning = errors.New("runtime is already running")

	// ErrLTMNamespaceRequired is returned when no namespace is provided to NewLTMStore.
	ErrLTMNamespaceRequired = errors.New("agentcore ltm: namespace is required")

	// ErrLTMContentRequired is returned when empty content is passed to LTMStore.Store.
	ErrLTMContentRequired = errors.New("agentcore ltm: content is required")

	// ErrLTMQueryRequired is returned when an empty query is passed to LTMStore.Search.
	ErrLTMQueryRequired = errors.New("agentcore ltm: query is required")

	// ErrLTMIDRequired is returned when an empty ID is passed to LTMStore.Delete.
	ErrLTMIDRequired = errors.New("agentcore ltm: id is required")
)

// conversationError wraps errors from AgentCore conversation (memory) API calls
// with the "agentcore conversation:" prefix.
type conversationError struct {
	err error
}

func (e *conversationError) Error() string {
	return "agentcore conversation: " + e.err.Error()
}

func (e *conversationError) Unwrap() error {
	return e.err
}

// wrapConversationError wraps an error with the conversationError type.
// Returns nil if err is nil.
func wrapConversationError(err error) error {
	if err == nil {
		return nil
	}
	return &conversationError{err: err}
}

// isTransient reports whether err represents a transient failure that should be
// retried. It classifies the following as transient:
//   - HTTP 5xx status codes (from smithy ResponseError)
//   - Network timeouts (net.Error with Timeout() == true)
//   - Connection refused
//   - DNS resolution failures
func isTransient(err error) bool {
	if err == nil {
		return false
	}

	// Check for smithy HTTP response errors with 5xx status.
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) {
		code := respErr.HTTPStatusCode()
		if code >= 500 && code < 600 {
			return true
		}
	}

	// Check for network timeout.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// Check for connection refused and DNS failures by inspecting OpError.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	// Fallback: check error message for common transient patterns.
	msg := err.Error()
	if strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout") {
		return true
	}

	return false
}

// isPermanent reports whether err represents a permanent failure that should NOT
// be retried. It classifies HTTP 4xx responses (excluding 404) as permanent.
func isPermanent(err error) bool {
	if err == nil {
		return false
	}

	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) {
		code := respErr.HTTPStatusCode()
		if code >= 400 && code < 500 && code != 404 {
			return true
		}
	}

	return false
}

// is404 reports whether err represents an HTTP 404 Not Found response.
// This is used to detect worker-deregistered conditions that require re-registration.
func is404(err error) bool {
	if err == nil {
		return false
	}
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) {
		return respErr.HTTPStatusCode() == 404
	}
	return false
}

// newConversationErrorf creates a conversationError with a formatted message
// wrapping the given error.
func newConversationErrorf(op string, err error) error {
	return &conversationError{err: fmt.Errorf("%s: %w", op, err)}
}
