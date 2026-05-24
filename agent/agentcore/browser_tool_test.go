package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
)

// browserMockClient implements agentCoreClient for browser tool tests.
type browserMockClient struct {
	startSessionOut  *bedrockagentcore.StartBrowserSessionOutput
	startSessionErr  error
	invokeBrowserOut *bedrockagentcore.InvokeBrowserOutput
	invokeBrowserErr error
	stopSessionOut   *bedrockagentcore.StopBrowserSessionOutput
	stopSessionErr   error

	// Call tracking.
	startCalled     bool
	invokeCalled    bool
	stopCalled      bool
	lastInvokeInput *bedrockagentcore.InvokeBrowserInput
}

func (m *browserMockClient) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	return nil, nil
}

func (m *browserMockClient) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}

func (m *browserMockClient) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}

func (m *browserMockClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}

func (m *browserMockClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}

func (m *browserMockClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	m.startCalled = true
	return m.startSessionOut, m.startSessionErr
}

func (m *browserMockClient) InvokeBrowser(_ context.Context, input *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	m.invokeCalled = true
	m.lastInvokeInput = input
	return m.invokeBrowserOut, m.invokeBrowserErr
}

func (m *browserMockClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	m.stopCalled = true
	return m.stopSessionOut, m.stopSessionErr
}

func (m *browserMockClient) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *browserMockClient) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}

func (m *browserMockClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *browserMockClient) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *browserMockClient) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *browserMockClient) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, nil
}

func TestNewBrowserTool_Schema(t *testing.T) {
	mock := &browserMockClient{}
	bt := NewBrowserTool(mock)

	if bt.Spec.Name != "browser" {
		t.Errorf("expected tool name %q, got %q", "browser", bt.Spec.Name)
	}

	if bt.Spec.Description == "" {
		t.Error("expected non-empty description")
	}

	// Verify schema has required properties.
	schema := bt.Spec.InputSchema
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in schema")
	}

	// Check url property.
	urlProp, ok := props["url"].(map[string]any)
	if !ok {
		t.Fatal("expected url property in schema")
	}
	if urlProp["type"] != "string" {
		t.Errorf("expected url type %q, got %v", "string", urlProp["type"])
	}
	if urlProp["maxLength"] != maxURLLength {
		t.Errorf("expected url maxLength %d, got %v", maxURLLength, urlProp["maxLength"])
	}

	// Check action property.
	actionProp, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatal("expected action property in schema")
	}
	if actionProp["type"] != "string" {
		t.Errorf("expected action type %q, got %v", "string", actionProp["type"])
	}
	enumVals, ok := actionProp["enum"].([]string)
	if !ok {
		t.Fatal("expected enum in action property")
	}
	expectedEnums := []string{"navigate", "extract_content", "screenshot"}
	if len(enumVals) != len(expectedEnums) {
		t.Fatalf("expected %d enum values, got %d", len(expectedEnums), len(enumVals))
	}
	for i, v := range expectedEnums {
		if enumVals[i] != v {
			t.Errorf("expected enum[%d] = %q, got %q", i, v, enumVals[i])
		}
	}

	// Check required fields.
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("expected required in schema")
	}
	if len(required) != 2 {
		t.Fatalf("expected 2 required fields, got %d", len(required))
	}
	requiredSet := map[string]bool{}
	for _, r := range required {
		requiredSet[r] = true
	}
	if !requiredSet["url"] || !requiredSet["action"] {
		t.Errorf("expected url and action in required, got %v", required)
	}
}

func TestBrowserTool_InvalidURLScheme(t *testing.T) {
	mock := &browserMockClient{}
	bt := NewBrowserTool(mock)

	tests := []struct {
		name string
		url  string
	}{
		{"ftp scheme", "ftp://example.com"},
		{"no scheme", "example.com"},
		{"file scheme", "file:///etc/passwd"},
		{"javascript scheme", "javascript:alert(1)"},
		{"empty url", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(browserInput{URL: tt.url, Action: "navigate"})
			_, err := bt.Handler(context.Background(), input)
			if err == nil {
				t.Error("expected error for invalid URL scheme, got nil")
			}
			if !strings.Contains(err.Error(), "HTTP and HTTPS") {
				t.Errorf("expected error about HTTP/HTTPS schemes, got: %v", err)
			}
			// Should NOT have called AgentCore.
			if mock.startCalled {
				t.Error("should not have called StartBrowserSession for invalid URL")
			}
		})
	}
}

func TestBrowserTool_ValidURLScheme(t *testing.T) {
	mock := &browserMockClient{
		startSessionOut: &bedrockagentcore.StartBrowserSessionOutput{
			SessionId: aws.String("sess-123"),
		},
		invokeBrowserOut: &bedrockagentcore.InvokeBrowserOutput{
			SessionId: aws.String("sess-123"),
			Result: &types.BrowserActionResultMemberScreenshot{
				Value: types.ScreenshotResult{
					Status: types.BrowserActionStatusSuccess,
				},
			},
		},
	}
	bt := NewBrowserTool(mock)

	tests := []struct {
		name string
		url  string
	}{
		{"http", "http://example.com"},
		{"https", "https://example.com/page"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock.startCalled = false
			mock.invokeCalled = false
			mock.stopCalled = false

			input, _ := json.Marshal(browserInput{URL: tt.url, Action: "navigate"})
			result, err := bt.Handler(context.Background(), input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !mock.startCalled {
				t.Error("expected StartBrowserSession to be called")
			}
			if !mock.invokeCalled {
				t.Error("expected InvokeBrowser to be called")
			}
			if !mock.stopCalled {
				t.Error("expected StopBrowserSession to be called")
			}
			if !strings.Contains(result, tt.url) {
				t.Errorf("expected result to contain URL %q, got: %s", tt.url, result)
			}
		})
	}
}

func TestBrowserTool_NavigateAction(t *testing.T) {
	mock := &browserMockClient{
		startSessionOut: &bedrockagentcore.StartBrowserSessionOutput{
			SessionId: aws.String("sess-nav"),
		},
		invokeBrowserOut: &bedrockagentcore.InvokeBrowserOutput{
			SessionId: aws.String("sess-nav"),
			Result: &types.BrowserActionResultMemberScreenshot{
				Value: types.ScreenshotResult{
					Status: types.BrowserActionStatusSuccess,
				},
			},
		},
	}
	bt := NewBrowserTool(mock)

	input, _ := json.Marshal(browserInput{URL: "https://example.com", Action: "navigate"})
	result, err := bt.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Navigated to: https://example.com") {
		t.Errorf("expected navigate result to contain URL, got: %s", result)
	}
	if !strings.Contains(result, "Title:") {
		t.Errorf("expected navigate result to contain Title, got: %s", result)
	}
}

func TestBrowserTool_NavigateTitleTruncation(t *testing.T) {
	// Create a long error string that will be used as the title.
	longTitle := strings.Repeat("A", 1000)
	mock := &browserMockClient{
		startSessionOut: &bedrockagentcore.StartBrowserSessionOutput{
			SessionId: aws.String("sess-trunc"),
		},
		invokeBrowserOut: &bedrockagentcore.InvokeBrowserOutput{
			SessionId: aws.String("sess-trunc"),
			Result: &types.BrowserActionResultMemberScreenshot{
				Value: types.ScreenshotResult{
					Status: types.BrowserActionStatusFailed,
					Error:  aws.String(longTitle),
				},
			},
		},
	}
	bt := NewBrowserTool(mock)

	input, _ := json.Marshal(browserInput{URL: "https://example.com", Action: "navigate"})
	result, err := bt.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The title should be truncated to maxTitleLength.
	lines := strings.Split(result, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines in result, got: %s", result)
	}
	titleLine := lines[1]
	// "Title: " is 7 chars, so the title content should be at most maxTitleLength.
	titleContent := strings.TrimPrefix(titleLine, "Title: ")
	if len(titleContent) > maxTitleLength {
		t.Errorf("expected title to be truncated to %d chars, got %d", maxTitleLength, len(titleContent))
	}
}

func TestBrowserTool_ExtractContent(t *testing.T) {
	content := "Hello, this is the page content."
	mock := &browserMockClient{
		startSessionOut: &bedrockagentcore.StartBrowserSessionOutput{
			SessionId: aws.String("sess-extract"),
		},
		invokeBrowserOut: &bedrockagentcore.InvokeBrowserOutput{
			SessionId: aws.String("sess-extract"),
			Result: &types.BrowserActionResultMemberScreenshot{
				Value: types.ScreenshotResult{
					Status: types.BrowserActionStatusSuccess,
					Data:   []byte(content),
				},
			},
		},
	}
	bt := NewBrowserTool(mock)

	input, _ := json.Marshal(browserInput{URL: "https://example.com", Action: "extract_content"})
	result, err := bt.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != content {
		t.Errorf("expected content %q, got %q", content, result)
	}
}

func TestBrowserTool_ExtractContentTruncation(t *testing.T) {
	// Create content longer than maxExtractContentLength.
	longContent := strings.Repeat("X", maxExtractContentLength+5000)
	mock := &browserMockClient{
		startSessionOut: &bedrockagentcore.StartBrowserSessionOutput{
			SessionId: aws.String("sess-trunc-content"),
		},
		invokeBrowserOut: &bedrockagentcore.InvokeBrowserOutput{
			SessionId: aws.String("sess-trunc-content"),
			Result: &types.BrowserActionResultMemberScreenshot{
				Value: types.ScreenshotResult{
					Status: types.BrowserActionStatusSuccess,
					Data:   []byte(longContent),
				},
			},
		},
	}
	bt := NewBrowserTool(mock)

	input, _ := json.Marshal(browserInput{URL: "https://example.com", Action: "extract_content"})
	result, err := bt.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != maxExtractContentLength {
		t.Errorf("expected result length %d, got %d", maxExtractContentLength, len(result))
	}
}

func TestBrowserTool_Screenshot(t *testing.T) {
	mock := &browserMockClient{
		startSessionOut: &bedrockagentcore.StartBrowserSessionOutput{
			SessionId: aws.String("sess-screenshot"),
		},
		invokeBrowserOut: &bedrockagentcore.InvokeBrowserOutput{
			SessionId: aws.String("sess-screenshot"),
			Result: &types.BrowserActionResultMemberScreenshot{
				Value: types.ScreenshotResult{
					Status: types.BrowserActionStatusSuccess,
				},
			},
		},
	}
	bt := NewBrowserTool(mock)

	input, _ := json.Marshal(browserInput{URL: "https://example.com", Action: "screenshot"})
	result, err := bt.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Screenshot captured successfully") {
		t.Errorf("expected screenshot success message, got: %s", result)
	}
}

func TestBrowserTool_ServiceError(t *testing.T) {
	mock := &browserMockClient{
		startSessionErr: errors.New("service unavailable"),
	}
	bt := NewBrowserTool(mock)

	input, _ := json.Marshal(browserInput{URL: "https://example.com", Action: "navigate"})
	_, err := bt.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for service failure, got nil")
	}
	if !strings.Contains(err.Error(), "browser service error") {
		t.Errorf("expected browser service error, got: %v", err)
	}
}

func TestBrowserTool_InvokeBrowserError(t *testing.T) {
	mock := &browserMockClient{
		startSessionOut: &bedrockagentcore.StartBrowserSessionOutput{
			SessionId: aws.String("sess-err"),
		},
		invokeBrowserErr: errors.New("timeout"),
	}
	bt := NewBrowserTool(mock)

	input, _ := json.Marshal(browserInput{URL: "https://example.com", Action: "navigate"})
	_, err := bt.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for InvokeBrowser failure, got nil")
	}
	if !strings.Contains(err.Error(), "navigation failed") {
		t.Errorf("expected navigation failed error, got: %v", err)
	}
	// Session should still be stopped.
	if !mock.stopCalled {
		t.Error("expected StopBrowserSession to be called even on error")
	}
}

func TestBrowserTool_WithBrowserTimeout(t *testing.T) {
	mock := &browserMockClient{
		startSessionOut: &bedrockagentcore.StartBrowserSessionOutput{
			SessionId: aws.String("sess-timeout"),
		},
		invokeBrowserOut: &bedrockagentcore.InvokeBrowserOutput{
			SessionId: aws.String("sess-timeout"),
			Result: &types.BrowserActionResultMemberScreenshot{
				Value: types.ScreenshotResult{
					Status: types.BrowserActionStatusSuccess,
				},
			},
		},
	}

	customTimeout := 60 * time.Second
	bt := NewBrowserTool(mock, WithBrowserTimeout(customTimeout))

	// Verify the tool works with custom timeout.
	input, _ := json.Marshal(browserInput{URL: "https://example.com", Action: "navigate"})
	_, err := bt.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBrowserTool_WithBrowserIdentifier(t *testing.T) {
	mock := &browserMockClient{
		startSessionOut: &bedrockagentcore.StartBrowserSessionOutput{
			SessionId: aws.String("sess-id"),
		},
		invokeBrowserOut: &bedrockagentcore.InvokeBrowserOutput{
			SessionId: aws.String("sess-id"),
			Result: &types.BrowserActionResultMemberScreenshot{
				Value: types.ScreenshotResult{
					Status: types.BrowserActionStatusSuccess,
				},
			},
		},
	}

	bt := NewBrowserTool(mock, WithBrowserIdentifier("my-browser"))

	input, _ := json.Marshal(browserInput{URL: "https://example.com", Action: "screenshot"})
	_, err := bt.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the browser identifier was passed to InvokeBrowser.
	if mock.lastInvokeInput == nil {
		t.Fatal("expected InvokeBrowser to be called")
	}
	if *mock.lastInvokeInput.BrowserIdentifier != "my-browser" {
		t.Errorf("expected browser identifier %q, got %q", "my-browser", *mock.lastInvokeInput.BrowserIdentifier)
	}
}

func TestBrowserTool_StopSessionCalledOnSuccess(t *testing.T) {
	mock := &browserMockClient{
		startSessionOut: &bedrockagentcore.StartBrowserSessionOutput{
			SessionId: aws.String("sess-cleanup"),
		},
		invokeBrowserOut: &bedrockagentcore.InvokeBrowserOutput{
			SessionId: aws.String("sess-cleanup"),
			Result: &types.BrowserActionResultMemberScreenshot{
				Value: types.ScreenshotResult{
					Status: types.BrowserActionStatusSuccess,
				},
			},
		},
	}
	bt := NewBrowserTool(mock)

	input, _ := json.Marshal(browserInput{URL: "https://example.com", Action: "navigate"})
	_, _ = bt.Handler(context.Background(), input)

	if !mock.stopCalled {
		t.Error("expected StopBrowserSession to be called after successful operation")
	}
}

func TestBrowserTool_ExtractContentEmpty(t *testing.T) {
	mock := &browserMockClient{
		startSessionOut: &bedrockagentcore.StartBrowserSessionOutput{
			SessionId: aws.String("sess-empty"),
		},
		invokeBrowserOut: &bedrockagentcore.InvokeBrowserOutput{
			SessionId: aws.String("sess-empty"),
			Result: &types.BrowserActionResultMemberScreenshot{
				Value: types.ScreenshotResult{
					Status: types.BrowserActionStatusSuccess,
					Data:   nil,
				},
			},
		},
	}
	bt := NewBrowserTool(mock)

	input, _ := json.Marshal(browserInput{URL: "https://example.com", Action: "extract_content"})
	result, err := bt.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "" {
		t.Errorf("expected empty result for nil data, got: %q", result)
	}
}
