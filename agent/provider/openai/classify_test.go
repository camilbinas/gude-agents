package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"

	openaisdk "github.com/openai/openai-go/v3"
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
			sdkErr := &openaisdk.Error{StatusCode: tt.status}
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

func TestClassifyError_RetryAfterHeader(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "12")
	sdkErr := &openaisdk.Error{StatusCode: http.StatusTooManyRequests, Response: resp}
	pe := classifyError(sdkErr)
	if pe.RetryAfter != 12*time.Second {
		t.Errorf("RetryAfter = %v, want 12s", pe.RetryAfter)
	}
}

func TestClassifyError_NetworkErrorRetryable(t *testing.T) {
	pe := classifyError(&net.OpError{Op: "dial", Err: errors.New("connection refused")})
	if pe.StatusCode != 0 {
		t.Errorf("StatusCode = %d, want 0", pe.StatusCode)
	}
	if !pe.Retryable {
		t.Error("network error should be retryable")
	}
}

func TestClassifyError_ContextCanceledNotRetryable(t *testing.T) {
	pe := classifyError(fmt.Errorf("stream: %w", context.Canceled))
	if pe.Retryable || agent.IsRetryable(pe) {
		t.Error("context.Canceled must not be retryable")
	}
}

func TestCollectToolCalls_SparseAndNonZeroBasedIndices(t *testing.T) {
	// Sparse, non-zero-based indices must all be returned in index order.
	in := map[int64]*toolCallAccum{
		5: {id: "call_a", name: "alpha", arguments: `{"x":1}`},
		2: {id: "call_b", name: "beta", arguments: ""},
		9: {id: "call_c", name: "gamma", arguments: `{"y":2}`},
	}
	got := collectToolCalls(in)
	if len(got) != 3 {
		t.Fatalf("got %d tool calls, want 3", len(got))
	}
	wantOrder := []struct {
		id, name, args string
	}{
		{"call_b", "beta", `{}`}, // empty arguments default to {}
		{"call_a", "alpha", `{"x":1}`},
		{"call_c", "gamma", `{"y":2}`},
	}
	for i, w := range wantOrder {
		if got[i].ToolUseID != w.id {
			t.Errorf("call[%d].ToolUseID = %q, want %q", i, got[i].ToolUseID, w.id)
		}
		if got[i].Name != w.name {
			t.Errorf("call[%d].Name = %q, want %q", i, got[i].Name, w.name)
		}
		if string(got[i].Input) != w.args {
			t.Errorf("call[%d].Input = %s, want %s", i, got[i].Input, w.args)
		}
	}
}

func TestCollectToolCalls_Empty(t *testing.T) {
	if got := collectToolCalls(nil); got != nil {
		t.Errorf("expected nil for empty map, got %v", got)
	}
}

func TestCollectToolCalls_ContiguousStillWorks(t *testing.T) {
	in := map[int64]*toolCallAccum{
		0: {id: "call_0", name: "a", arguments: `{"a":1}`},
		1: {id: "call_1", name: "b", arguments: `{"b":2}`},
	}
	got := collectToolCalls(in)
	if len(got) != 2 || got[0].ToolUseID != "call_0" || got[1].ToolUseID != "call_1" {
		b, _ := json.Marshal(got)
		t.Fatalf("contiguous indices mishandled: %s", b)
	}
}
