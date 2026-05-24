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

// codeInterpreterMockClient implements agentCoreClient for code interpreter tool tests.
type codeInterpreterMockClient struct {
	startSessionOut *bedrockagentcore.StartCodeInterpreterSessionOutput
	startSessionErr error
	invokeOut       *bedrockagentcore.InvokeCodeInterpreterOutput
	invokeErr       error
	stopSessionOut  *bedrockagentcore.StopCodeInterpreterSessionOutput
	stopSessionErr  error

	// Call tracking.
	startCalled     bool
	invokeCalled    bool
	stopCalled      bool
	lastInvokeInput *bedrockagentcore.InvokeCodeInterpreterInput
	lastStartInput  *bedrockagentcore.StartCodeInterpreterSessionInput
}

func (m *codeInterpreterMockClient) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	return nil, nil
}

func (m *codeInterpreterMockClient) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}

func (m *codeInterpreterMockClient) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}

func (m *codeInterpreterMockClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}

func (m *codeInterpreterMockClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}

func (m *codeInterpreterMockClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}

func (m *codeInterpreterMockClient) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}

func (m *codeInterpreterMockClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}

func (m *codeInterpreterMockClient) StartCodeInterpreterSession(_ context.Context, input *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	m.startCalled = true
	m.lastStartInput = input
	return m.startSessionOut, m.startSessionErr
}

func (m *codeInterpreterMockClient) InvokeCodeInterpreter(_ context.Context, input *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	m.invokeCalled = true
	m.lastInvokeInput = input
	return m.invokeOut, m.invokeErr
}

func (m *codeInterpreterMockClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	m.stopCalled = true
	return m.stopSessionOut, m.stopSessionErr
}

func (m *codeInterpreterMockClient) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *codeInterpreterMockClient) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *codeInterpreterMockClient) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, nil
}

func TestNewCodeInterpreterTool_Schema(t *testing.T) {
	mock := &codeInterpreterMockClient{}
	ct := NewCodeInterpreterTool(mock)

	if ct.Spec.Name != "code_interpreter" {
		t.Errorf("expected tool name %q, got %q", "code_interpreter", ct.Spec.Name)
	}

	if ct.Spec.Description == "" {
		t.Error("expected non-empty description")
	}

	// Verify schema has required properties.
	schema := ct.Spec.InputSchema
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in schema")
	}

	// Check code property.
	codeProp, ok := props["code"].(map[string]any)
	if !ok {
		t.Fatal("expected code property in schema")
	}
	if codeProp["type"] != "string" {
		t.Errorf("expected code type %q, got %v", "string", codeProp["type"])
	}
	if codeProp["maxLength"] != maxCodeLength {
		t.Errorf("expected code maxLength %d, got %v", maxCodeLength, codeProp["maxLength"])
	}

	// Check language property.
	langProp, ok := props["language"].(map[string]any)
	if !ok {
		t.Fatal("expected language property in schema")
	}
	if langProp["type"] != "string" {
		t.Errorf("expected language type %q, got %v", "string", langProp["type"])
	}
	if langProp["default"] != "python" {
		t.Errorf("expected language default %q, got %v", "python", langProp["default"])
	}

	// Check required fields — only "code" should be required.
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("expected required in schema")
	}
	if len(required) != 1 {
		t.Fatalf("expected 1 required field, got %d", len(required))
	}
	if required[0] != "code" {
		t.Errorf("expected required[0] = %q, got %q", "code", required[0])
	}
}

func TestCodeInterpreterTool_UnsupportedLanguage(t *testing.T) {
	mock := &codeInterpreterMockClient{}
	ct := NewCodeInterpreterTool(mock)

	tests := []struct {
		name     string
		language string
	}{
		{"javascript", "javascript"},
		{"ruby", "ruby"},
		{"go", "go"},
		{"java", "java"},
		{"PYTHON uppercase", "PYTHON"},
		{"empty with explicit set", "rust"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(codeInterpreterInput{Code: "print('hello')", Language: tt.language})
			_, err := ct.Handler(context.Background(), input)
			if err == nil {
				t.Error("expected error for unsupported language, got nil")
			}
			if !strings.Contains(err.Error(), "unsupported language") {
				t.Errorf("expected unsupported language error, got: %v", err)
			}
			if !strings.Contains(err.Error(), "python") {
				t.Errorf("expected error to list supported values (python), got: %v", err)
			}
			// Should NOT have called AgentCore.
			if mock.startCalled {
				t.Error("should not have called StartCodeInterpreterSession for unsupported language")
			}
		})
	}
}

func TestCodeInterpreterTool_DefaultLanguage(t *testing.T) {
	// When language is empty, it should default to "python" and proceed.
	mock := &codeInterpreterMockClient{
		startSessionOut: &bedrockagentcore.StartCodeInterpreterSessionOutput{
			SessionId: aws.String("sess-default-lang"),
		},
		invokeErr: errors.New("simulated invoke error"),
	}
	ct := NewCodeInterpreterTool(mock)

	// No language field set — should default to python and attempt execution.
	input, _ := json.Marshal(map[string]string{"code": "print('hello')"})
	_, err := ct.Handler(context.Background(), input)
	// We expect an error from the invoke, but NOT an unsupported language error.
	if err == nil {
		t.Fatal("expected error from invoke, got nil")
	}
	if strings.Contains(err.Error(), "unsupported language") {
		t.Errorf("should not get unsupported language error when language is empty, got: %v", err)
	}
	// Should have called StartCodeInterpreterSession.
	if !mock.startCalled {
		t.Error("expected StartCodeInterpreterSession to be called")
	}
}

func TestCodeInterpreterTool_ServiceUnavailable_StartSession(t *testing.T) {
	mock := &codeInterpreterMockClient{
		startSessionErr: errors.New("connection refused"),
	}
	ct := NewCodeInterpreterTool(mock)

	input, _ := json.Marshal(codeInterpreterInput{Code: "print('hello')", Language: "python"})
	_, err := ct.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for service unavailability, got nil")
	}
	if !strings.Contains(err.Error(), "service unavailable") {
		t.Errorf("expected service unavailable error, got: %v", err)
	}
}

func TestCodeInterpreterTool_ServiceUnavailable_Invoke(t *testing.T) {
	mock := &codeInterpreterMockClient{
		startSessionOut: &bedrockagentcore.StartCodeInterpreterSessionOutput{
			SessionId: aws.String("sess-invoke-err"),
		},
		invokeErr: errors.New("timeout"),
	}
	ct := NewCodeInterpreterTool(mock)

	input, _ := json.Marshal(codeInterpreterInput{Code: "print('hello')", Language: "python"})
	_, err := ct.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for invoke failure, got nil")
	}
	if !strings.Contains(err.Error(), "service unavailable") {
		t.Errorf("expected service unavailable error, got: %v", err)
	}
	// Session should still be stopped.
	if !mock.stopCalled {
		t.Error("expected StopCodeInterpreterSession to be called even on error")
	}
}

func TestCodeInterpreterTool_WithCodeTimeout(t *testing.T) {
	mock := &codeInterpreterMockClient{
		startSessionOut: &bedrockagentcore.StartCodeInterpreterSessionOutput{
			SessionId: aws.String("sess-timeout"),
		},
		invokeErr: errors.New("simulated"),
	}

	customTimeout := 120 * time.Second
	ct := NewCodeInterpreterTool(mock, WithCodeTimeout(customTimeout))

	// Verify the tool works with custom timeout (we just check it doesn't panic).
	input, _ := json.Marshal(codeInterpreterInput{Code: "print('hello')", Language: "python"})
	_, _ = ct.Handler(context.Background(), input)

	if !mock.startCalled {
		t.Error("expected StartCodeInterpreterSession to be called")
	}
}

func TestCodeInterpreterTool_WithCodeInterpreterID(t *testing.T) {
	mock := &codeInterpreterMockClient{
		startSessionOut: &bedrockagentcore.StartCodeInterpreterSessionOutput{
			SessionId: aws.String("sess-custom-id"),
		},
		invokeErr: errors.New("simulated"),
	}

	ct := NewCodeInterpreterTool(mock, WithCodeInterpreterID("my-interpreter"))

	input, _ := json.Marshal(codeInterpreterInput{Code: "x = 1", Language: "python"})
	_, _ = ct.Handler(context.Background(), input)

	// Verify the code interpreter identifier was passed to StartCodeInterpreterSession.
	if mock.lastStartInput == nil {
		t.Fatal("expected StartCodeInterpreterSession to be called")
	}
	if *mock.lastStartInput.CodeInterpreterIdentifier != "my-interpreter" {
		t.Errorf("expected code interpreter identifier %q, got %q", "my-interpreter", *mock.lastStartInput.CodeInterpreterIdentifier)
	}

	// Verify it was also passed to InvokeCodeInterpreter.
	if mock.lastInvokeInput == nil {
		t.Fatal("expected InvokeCodeInterpreter to be called")
	}
	if *mock.lastInvokeInput.CodeInterpreterIdentifier != "my-interpreter" {
		t.Errorf("expected code interpreter identifier %q in invoke, got %q", "my-interpreter", *mock.lastInvokeInput.CodeInterpreterIdentifier)
	}
}

func TestCodeInterpreterTool_StopSessionCalledOnSuccess(t *testing.T) {
	// Create a mock that returns a nil output (will trigger stream error).
	mock := &codeInterpreterMockClient{
		startSessionOut: &bedrockagentcore.StartCodeInterpreterSessionOutput{
			SessionId: aws.String("sess-cleanup"),
		},
		invokeOut: nil, // nil output triggers "empty response" error in readCodeInterpreterStream
	}
	ct := NewCodeInterpreterTool(mock)

	input, _ := json.Marshal(codeInterpreterInput{Code: "print('hello')", Language: "python"})
	_, _ = ct.Handler(context.Background(), input)

	if !mock.stopCalled {
		t.Error("expected StopCodeInterpreterSession to be called after operation")
	}
}

func TestCodeInterpreterTool_InvalidJSON(t *testing.T) {
	mock := &codeInterpreterMockClient{}
	ct := NewCodeInterpreterTool(mock)

	_, err := ct.Handler(context.Background(), json.RawMessage(`{invalid json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("expected unmarshal error, got: %v", err)
	}
}

func TestCodeInterpreterTool_InvokeInput(t *testing.T) {
	mock := &codeInterpreterMockClient{
		startSessionOut: &bedrockagentcore.StartCodeInterpreterSessionOutput{
			SessionId: aws.String("sess-input-check"),
		},
		invokeErr: errors.New("simulated"),
	}
	ct := NewCodeInterpreterTool(mock)

	code := "x = 42\nprint(x)"
	input, _ := json.Marshal(codeInterpreterInput{Code: code, Language: "python"})
	_, _ = ct.Handler(context.Background(), input)

	if mock.lastInvokeInput == nil {
		t.Fatal("expected InvokeCodeInterpreter to be called")
	}

	// Verify the code was passed correctly.
	if mock.lastInvokeInput.Arguments == nil {
		t.Fatal("expected Arguments to be set")
	}
	if *mock.lastInvokeInput.Arguments.Code != code {
		t.Errorf("expected code %q, got %q", code, *mock.lastInvokeInput.Arguments.Code)
	}
	if mock.lastInvokeInput.Arguments.Language != types.ProgrammingLanguagePython {
		t.Errorf("expected language %q, got %q", types.ProgrammingLanguagePython, mock.lastInvokeInput.Arguments.Language)
	}
	if mock.lastInvokeInput.Name != types.ToolNameExecuteCode {
		t.Errorf("expected tool name %q, got %q", types.ToolNameExecuteCode, mock.lastInvokeInput.Name)
	}
	if *mock.lastInvokeInput.SessionId != "sess-input-check" {
		t.Errorf("expected session ID %q, got %q", "sess-input-check", *mock.lastInvokeInput.SessionId)
	}
}

func TestReadCodeInterpreterStream_NilOutput(t *testing.T) {
	output, isError, err := readCodeInterpreterStream(nil)
	if err == nil {
		t.Fatal("expected error for nil output")
	}
	if !strings.Contains(err.Error(), "empty response") {
		t.Errorf("expected empty response error, got: %v", err)
	}
	if output != "" {
		t.Errorf("expected empty output, got: %q", output)
	}
	if isError {
		t.Error("expected isError to be false for nil output")
	}
}

func TestCodeInterpreterTool_ExplicitPythonLanguage(t *testing.T) {
	// Explicitly setting language to "python" should work.
	mock := &codeInterpreterMockClient{
		startSessionOut: &bedrockagentcore.StartCodeInterpreterSessionOutput{
			SessionId: aws.String("sess-python"),
		},
		invokeErr: errors.New("simulated"),
	}
	ct := NewCodeInterpreterTool(mock)

	input, _ := json.Marshal(codeInterpreterInput{Code: "print('hello')", Language: "python"})
	_, err := ct.Handler(context.Background(), input)
	// Should get invoke error, not language error.
	if err == nil {
		t.Fatal("expected error from invoke, got nil")
	}
	if strings.Contains(err.Error(), "unsupported language") {
		t.Errorf("should not get unsupported language error for python, got: %v", err)
	}
	if !mock.startCalled {
		t.Error("expected StartCodeInterpreterSession to be called for python")
	}
}
