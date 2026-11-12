package bedrock

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func TestClassifyError_ModeledRetryableExceptions(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"throttling", &types.ThrottlingException{}},
		{"service unavailable", &types.ServiceUnavailableException{}},
		{"internal server", &types.InternalServerException{}},
		{"model timeout", &types.ModelTimeoutException{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pe := classifyError(tt.err)
			if !pe.Retryable {
				t.Errorf("%s should be retryable", tt.name)
			}
			if !agent.IsRetryable(pe) {
				t.Errorf("agent.IsRetryable should be true for %s", tt.name)
			}
		})
	}
}

func TestClassifyError_ValidationNotRetryable(t *testing.T) {
	pe := classifyError(&types.ValidationException{})
	if pe.Retryable {
		t.Error("validation exception must not be retryable")
	}
}

func TestClassifyError_HTTPStatusFromResponseError(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		wantRetryable bool
	}{
		{"429", http.StatusTooManyRequests, true},
		{"500", http.StatusInternalServerError, true},
		{"503", http.StatusServiceUnavailable, true},
		{"400", http.StatusBadRequest, false},
		{"403", http.StatusForbidden, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			respErr := &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{Response: &http.Response{StatusCode: tt.status, Header: http.Header{}}},
				Err:      errors.New("boom"),
			}
			pe := classifyError(respErr)
			if pe.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d", pe.StatusCode, tt.status)
			}
			if pe.Retryable != tt.wantRetryable {
				t.Errorf("Retryable = %v, want %v", pe.Retryable, tt.wantRetryable)
			}
		})
	}
}

func TestClassifyError_RetryAfterHeader(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}}
	resp.Header.Set("Retry-After", "7")
	respErr := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: resp},
		Err:      errors.New("throttled"),
	}
	pe := classifyError(respErr)
	if pe.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %v, want 7s", pe.RetryAfter)
	}
}

func TestClassifyError_NetworkErrorRetryable(t *testing.T) {
	pe := classifyError(&net.OpError{Op: "dial", Err: errors.New("connection refused")})
	if pe.StatusCode != 0 || !pe.Retryable {
		t.Errorf("network error: StatusCode=%d Retryable=%v, want 0/true", pe.StatusCode, pe.Retryable)
	}
}

func TestClassifyError_ContextCanceledNotRetryable(t *testing.T) {
	pe := classifyError(fmt.Errorf("stream: %w", context.Canceled))
	if pe.Retryable || agent.IsRetryable(pe) {
		t.Error("context.Canceled must not be retryable")
	}
}

func TestToBedrockMessages_EmptyAssistantMessageDropped(t *testing.T) {
	// An assistant message whose only content is blank text produces no blocks.
	// It must be dropped rather than filled with a "(no content)" placeholder,
	// which would corrupt the transcript.
	msgs, err := toBedrockMessages([]agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: ""}}},
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "bye"}}},
	}, "anthropic.claude-3-5-haiku-20241022-v1:0", false)
	if err != nil {
		t.Fatalf("toBedrockMessages error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected empty assistant message to be dropped (2 remaining), got %d", len(msgs))
	}
	for _, m := range msgs {
		if m.Role == types.ConversationRoleAssistant {
			t.Error("empty assistant message should have been dropped")
		}
		for _, b := range m.Content {
			if txt, ok := b.(*types.ContentBlockMemberText); ok && txt.Value == "(no content)" {
				t.Error("no message should carry the (no content) placeholder here")
			}
		}
	}
}

func TestToBedrockMessages_EmptyUserMessageGetsPlaceholder(t *testing.T) {
	// A user message with only blank text still needs a valid non-empty block;
	// the "(no content)" placeholder is acceptable on a user turn.
	msgs, err := toBedrockMessages([]agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: ""}}},
	}, "anthropic.claude-3-5-haiku-20241022-v1:0", false)
	if err != nil {
		t.Fatalf("toBedrockMessages error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	txt, ok := msgs[0].Content[0].(*types.ContentBlockMemberText)
	if !ok || txt.Value != "(no content)" {
		t.Errorf("expected (no content) placeholder on empty user message, got %#v", msgs[0].Content)
	}
}
