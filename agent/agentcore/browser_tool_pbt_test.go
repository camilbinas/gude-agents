package agentcore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
	"pgregory.net/rapid"
)

// Feature: agentcore-runtime, Property 9: Browser tool URL scheme validation

// TestProperty_BrowserToolURLSchemeValidation verifies that for any URL string that does
// not begin with "http://" or "https://", the Browser_Tool returns an error without
// dispatching to AgentCore. For any URL string that begins with "http://" or "https://",
// the Browser_Tool dispatches the request to AgentCore's browser service.
//
// **Validates: Requirements 7.8**
func TestProperty_BrowserToolURLSchemeValidation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random URL string.
		url := genURL(rt)

		// Determine if the URL has a valid scheme.
		hasValidScheme := strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")

		// Pick a random valid action.
		actions := []string{"navigate", "extract_content", "screenshot"}
		actionIdx := rapid.IntRange(0, len(actions)-1).Draw(rt, "actionIdx")
		action := actions[actionIdx]

		// Create a mock that tracks whether dispatch happened.
		mock := &browserPBTMockClient{
			startSessionOut: &bedrockagentcore.StartBrowserSessionOutput{
				SessionId: aws.String("pbt-session"),
			},
			invokeBrowserOut: &bedrockagentcore.InvokeBrowserOutput{
				SessionId: aws.String("pbt-session"),
				Result: &types.BrowserActionResultMemberScreenshot{
					Value: types.ScreenshotResult{
						Status: types.BrowserActionStatusSuccess,
						Data:   []byte("content"),
					},
				},
			},
		}

		bt := NewBrowserTool(mock)

		input, err := json.Marshal(browserInput{URL: url, Action: action})
		if err != nil {
			rt.Fatalf("failed to marshal input: %v", err)
		}

		_, handlerErr := bt.Handler(context.Background(), input)

		if hasValidScheme {
			// Valid scheme: should dispatch to AgentCore (StartBrowserSession called).
			if !mock.startCalled {
				rt.Fatalf("expected dispatch for valid URL %q (action=%s), but StartBrowserSession was not called",
					url, action)
			}
		} else {
			// Invalid scheme: should return error without dispatching.
			if handlerErr == nil {
				rt.Fatalf("expected error for invalid URL scheme %q, got nil", url)
			}
			if !strings.Contains(handlerErr.Error(), "HTTP and HTTPS") {
				rt.Fatalf("expected error about HTTP/HTTPS schemes for URL %q, got: %v", url, handlerErr)
			}
			// Should NOT have dispatched to AgentCore.
			if mock.startCalled {
				rt.Fatalf("should not dispatch to AgentCore for invalid URL %q, but StartBrowserSession was called", url)
			}
		}
	})
}

// genURL generates random URL strings with a mix of valid and invalid schemes.
func genURL(rt *rapid.T) string {
	// Decide whether to generate a valid or invalid scheme URL.
	// Use roughly 50/50 split to test both paths well.
	validScheme := rapid.Bool().Draw(rt, "validScheme")

	if validScheme {
		// Generate a URL with http:// or https:// prefix.
		useHTTPS := rapid.Bool().Draw(rt, "useHTTPS")
		scheme := "http://"
		if useHTTPS {
			scheme = "https://"
		}
		// Generate a random host/path suffix.
		suffix := rapid.StringMatching(`[a-z0-9][a-z0-9\.\-]{0,50}\.[a-z]{2,6}(/[a-z0-9\-/]*)?`).Draw(rt, "suffix")
		return scheme + suffix
	}

	// Generate a URL with an invalid scheme.
	// Choose from various invalid scheme patterns.
	invalidType := rapid.IntRange(0, 6).Draw(rt, "invalidType")
	switch invalidType {
	case 0:
		// ftp:// scheme
		host := rapid.StringMatching(`[a-z0-9]{1,20}\.[a-z]{2,4}`).Draw(rt, "ftpHost")
		return "ftp://" + host
	case 1:
		// file:// scheme
		path := rapid.StringMatching(`/[a-z0-9/]{1,30}`).Draw(rt, "filePath")
		return "file://" + path
	case 2:
		// javascript: scheme
		code := rapid.StringMatching(`[a-z()]{1,20}`).Draw(rt, "jsCode")
		return "javascript:" + code
	case 3:
		// No scheme at all (bare hostname)
		host := rapid.StringMatching(`[a-z0-9]{1,20}\.[a-z]{2,4}`).Draw(rt, "bareHost")
		return host
	case 4:
		// Empty string
		return ""
	case 5:
		// Random string that doesn't start with http:// or https://
		s := rapid.StringMatching(`[a-z]{1,5}://[a-z0-9]{1,20}`).Draw(rt, "randomScheme")
		// Ensure it doesn't accidentally start with http:// or https://
		if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
			return "ftp://fallback.com"
		}
		return s
	case 6:
		// data: URI
		data := rapid.StringMatching(`[a-z0-9+/=]{1,30}`).Draw(rt, "dataContent")
		return "data:text/plain;base64," + data
	default:
		return "invalid://example.com"
	}
}

// browserPBTMockClient implements agentCoreClient for browser PBT tests.
type browserPBTMockClient struct {
	startSessionOut  *bedrockagentcore.StartBrowserSessionOutput
	invokeBrowserOut *bedrockagentcore.InvokeBrowserOutput

	startCalled bool
}

func (m *browserPBTMockClient) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	return nil, nil
}

func (m *browserPBTMockClient) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}

func (m *browserPBTMockClient) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}

func (m *browserPBTMockClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}

func (m *browserPBTMockClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}

func (m *browserPBTMockClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	m.startCalled = true
	return m.startSessionOut, nil
}

func (m *browserPBTMockClient) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return m.invokeBrowserOut, nil
}

func (m *browserPBTMockClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}

func (m *browserPBTMockClient) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *browserPBTMockClient) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}

func (m *browserPBTMockClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *browserPBTMockClient) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *browserPBTMockClient) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *browserPBTMockClient) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, nil
}

// Feature: agentcore-runtime, Property 10: Output truncation (browser)

// TestProperty_BrowserOutputTruncation verifies that for any string of length L returned
// by AgentCore's browser extract_content operation, the tool result has length min(L, 32000).
//
// **Validates: Requirements 7.4**
func TestProperty_BrowserOutputTruncation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random content length between 0 and 100000.
		contentLen := rapid.IntRange(0, 100000).Draw(rt, "contentLen")

		// Build a string of exactly that length.
		content := strings.Repeat("x", contentLen)

		// Create a mock that returns the generated content as browser extract data.
		mock := &browserPBTMockClient{
			startSessionOut: &bedrockagentcore.StartBrowserSessionOutput{
				SessionId: aws.String("pbt-truncation-session"),
			},
			invokeBrowserOut: &bedrockagentcore.InvokeBrowserOutput{
				SessionId: aws.String("pbt-truncation-session"),
				Result: &types.BrowserActionResultMemberScreenshot{
					Value: types.ScreenshotResult{
						Status: types.BrowserActionStatusSuccess,
						Data:   []byte(content),
					},
				},
			},
		}

		bt := NewBrowserTool(mock)

		// Use extract_content action with a valid URL.
		input, err := json.Marshal(browserInput{URL: "https://example.com", Action: "extract_content"})
		if err != nil {
			rt.Fatalf("failed to marshal input: %v", err)
		}

		result, handlerErr := bt.Handler(context.Background(), input)
		if handlerErr != nil {
			rt.Fatalf("unexpected error from browser tool: %v", handlerErr)
		}

		// Expected length is min(contentLen, 32000).
		expectedLen := contentLen
		if expectedLen > maxExtractContentLength {
			expectedLen = maxExtractContentLength
		}

		if len(result) != expectedLen {
			rt.Fatalf("output truncation failed: input length=%d, expected output length=%d, got=%d",
				contentLen, expectedLen, len(result))
		}
	})
}
