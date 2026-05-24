package agentcore

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
	"pgregory.net/rapid"
)

// mockCodeInterpreterStreamReader implements bedrockagentcore.CodeInterpreterStreamOutputReader
// for testing. It sends pre-configured events through a channel.
type mockCodeInterpreterStreamReader struct {
	events chan types.CodeInterpreterStreamOutput
}

func (m *mockCodeInterpreterStreamReader) Events() <-chan types.CodeInterpreterStreamOutput {
	return m.events
}

func (m *mockCodeInterpreterStreamReader) Close() error {
	return nil
}

func (m *mockCodeInterpreterStreamReader) Err() error {
	return nil
}

// newMockCodeInterpreterOutputWithStream creates an InvokeCodeInterpreterOutput with a working
// mock event stream that returns the given text as a successful result.
// It uses reflect/unsafe to set the unexported eventStream field.
func newMockCodeInterpreterOutputWithStream(text string) *bedrockagentcore.InvokeCodeInterpreterOutput {
	reader := &mockCodeInterpreterStreamReader{
		events: make(chan types.CodeInterpreterStreamOutput, 1),
	}

	// Send a result event with the text content.
	isErr := false
	reader.events <- &types.CodeInterpreterStreamOutputMemberResult{
		Value: types.CodeInterpreterResult{
			Content: []types.ContentBlock{
				{Text: aws.String(text)},
			},
			IsError: &isErr,
		},
	}
	close(reader.events)

	es := bedrockagentcore.NewInvokeCodeInterpreterEventStream(func(s *bedrockagentcore.InvokeCodeInterpreterEventStream) {
		s.Reader = reader
	})

	out := &bedrockagentcore.InvokeCodeInterpreterOutput{
		SessionId: aws.String("pbt-code-session"),
	}

	// Use reflect + unsafe to set the unexported eventStream field.
	v := reflect.ValueOf(out).Elem()
	f := v.FieldByName("eventStream")
	ptr := unsafe.Pointer(f.UnsafeAddr())
	*(**bedrockagentcore.InvokeCodeInterpreterEventStream)(ptr) = es

	return out
}

// codeInterpreterPBTMockClient implements agentCoreClient for code interpreter PBT tests.
// It returns a successful code execution with configurable output text via the event stream.
type codeInterpreterPBTMockClient struct {
	outputText string
}

func (m *codeInterpreterPBTMockClient) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	return nil, nil
}

func (m *codeInterpreterPBTMockClient) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}

func (m *codeInterpreterPBTMockClient) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}

func (m *codeInterpreterPBTMockClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}

func (m *codeInterpreterPBTMockClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}

func (m *codeInterpreterPBTMockClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}

func (m *codeInterpreterPBTMockClient) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}

func (m *codeInterpreterPBTMockClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}

func (m *codeInterpreterPBTMockClient) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return &bedrockagentcore.StartCodeInterpreterSessionOutput{
		SessionId: aws.String("pbt-code-session"),
	}, nil
}

func (m *codeInterpreterPBTMockClient) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return newMockCodeInterpreterOutputWithStream(m.outputText), nil
}

func (m *codeInterpreterPBTMockClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *codeInterpreterPBTMockClient) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *codeInterpreterPBTMockClient) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *codeInterpreterPBTMockClient) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, nil
}

// Feature: agentcore-runtime, Property 10: Output truncation (code interpreter)

// TestProperty_CodeInterpreterOutputTruncation verifies that for any string of length L
// returned by AgentCore's code interpreter, the tool result (excluding the execution time
// suffix) has length min(L, 50000).
//
// **Validates: Requirements 8.3**
func TestProperty_CodeInterpreterOutputTruncation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random content length between 0 and 100000.
		contentLen := rapid.IntRange(0, 100000).Draw(rt, "contentLen")

		// Build a string of exactly that length using a repeating character.
		content := strings.Repeat("a", contentLen)

		// Create a mock client that returns the generated content via the event stream.
		mock := &codeInterpreterPBTMockClient{
			outputText: content,
		}

		ct := NewCodeInterpreterTool(mock)

		// Invoke the tool with valid Python code.
		input, err := json.Marshal(codeInterpreterInput{Code: "print('hello')", Language: "python"})
		if err != nil {
			rt.Fatalf("failed to marshal input: %v", err)
		}

		result, handlerErr := ct.Handler(context.Background(), input)
		if handlerErr != nil {
			rt.Fatalf("unexpected error from code interpreter tool: %v", handlerErr)
		}

		// The result should contain the output followed by an execution time suffix.
		// The suffix matches the pattern "\nExecution time: X.XXs".
		// Strip the execution time suffix to measure the output portion.
		suffixIdx := strings.LastIndex(result, "\nExecution time: ")
		if suffixIdx == -1 {
			rt.Fatalf("result missing execution time suffix: %q", result)
		}

		outputPortion := result[:suffixIdx]

		// Expected output length is min(contentLen, maxCodeOutputLength).
		expectedLen := contentLen
		if expectedLen > maxCodeOutputLength {
			expectedLen = maxCodeOutputLength
		}

		if len(outputPortion) != expectedLen {
			rt.Fatalf("output truncation failed: input length=%d, expected output length=%d, got=%d",
				contentLen, expectedLen, len(outputPortion))
		}
	})
}
