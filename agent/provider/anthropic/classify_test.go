package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
)

func TestClassifyError_HTTPStatus(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		wantRetryable bool
	}{
		{"429 rate limited", http.StatusTooManyRequests, true},
		{"500 server error", http.StatusInternalServerError, true},
		{"503 unavailable", http.StatusServiceUnavailable, true},
		{"400 bad request", http.StatusBadRequest, false},
		{"401 unauthorized", http.StatusUnauthorized, false},
		{"403 forbidden", http.StatusForbidden, false},
		{"404 not found", http.StatusNotFound, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdkErr := &anthropicsdk.Error{StatusCode: tt.status}
			pe := classifyError(sdkErr)
			if pe.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d", pe.StatusCode, tt.status)
			}
			if pe.Retryable != tt.wantRetryable {
				t.Errorf("Retryable = %v, want %v", pe.Retryable, tt.wantRetryable)
			}
			if agent.IsRetryable(pe) != tt.wantRetryable {
				t.Errorf("agent.IsRetryable = %v, want %v", agent.IsRetryable(pe), tt.wantRetryable)
			}
		})
	}
}

func TestClassifyError_OverloadedIsRetryable(t *testing.T) {
	// A 529 overloaded_error body; the type is retryable even though 529 is not
	// caught by the generic 5xx rule until >= 500 (it is, but assert type path).
	sdkErr := &anthropicsdk.Error{}
	if err := sdkErr.UnmarshalJSON([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`)); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sdkErr.StatusCode = 400 // force non-retryable status to prove the type path wins
	pe := classifyError(sdkErr)
	if !pe.Retryable {
		t.Errorf("overloaded_error should be retryable even with status %d", sdkErr.StatusCode)
	}
}

func TestClassifyError_RetryAfterHeader(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "30")
	sdkErr := &anthropicsdk.Error{StatusCode: http.StatusTooManyRequests, Response: resp}
	pe := classifyError(sdkErr)
	if pe.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", pe.RetryAfter)
	}
}

func TestClassifyError_NetworkErrorRetryable(t *testing.T) {
	netErr := &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	pe := classifyError(netErr)
	if pe.StatusCode != 0 {
		t.Errorf("StatusCode = %d, want 0 for transport error", pe.StatusCode)
	}
	if !pe.Retryable {
		t.Error("network error should be retryable")
	}
}

func TestClassifyError_ContextCanceledNotRetryable(t *testing.T) {
	pe := classifyError(fmt.Errorf("stream: %w", context.Canceled))
	if pe.Retryable {
		t.Error("context.Canceled must not be retryable")
	}
	if agent.IsRetryable(pe) {
		t.Error("agent.IsRetryable must be false for context.Canceled")
	}
}
