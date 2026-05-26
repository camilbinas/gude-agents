package agentcore

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/camilbinas/gude-agents/agent"
)

// conversationMockClient implements agentCoreClient for conversation tests.
type conversationMockClient struct {
	listEventsOutput      *bedrockagentcore.ListEventsOutput
	listEventsErr         error
	stopRuntimeSessionErr error
	createEventErr        error
	listSessionsOutput    *bedrockagentcore.ListSessionsOutput
	listSessionsErr       error
}

func (m *conversationMockClient) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	return nil, nil
}

func (m *conversationMockClient) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return &bedrockagentcore.CreateEventOutput{}, m.createEventErr
}

func (m *conversationMockClient) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	if m.listEventsErr != nil {
		return nil, m.listEventsErr
	}
	if m.listEventsOutput != nil {
		return m.listEventsOutput, nil
	}
	return &bedrockagentcore.ListEventsOutput{Events: []types.Event{}}, nil
}

func (m *conversationMockClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	if m.listSessionsErr != nil {
		return nil, m.listSessionsErr
	}
	if m.listSessionsOutput != nil {
		return m.listSessionsOutput, nil
	}
	return &bedrockagentcore.ListSessionsOutput{}, nil
}

func (m *conversationMockClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	if m.stopRuntimeSessionErr != nil {
		return nil, m.stopRuntimeSessionErr
	}
	return &bedrockagentcore.StopRuntimeSessionOutput{}, nil
}

func (m *conversationMockClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}

func (m *conversationMockClient) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}

func (m *conversationMockClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}

func (m *conversationMockClient) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *conversationMockClient) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}

func (m *conversationMockClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *conversationMockClient) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *conversationMockClient) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *conversationMockClient) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, nil
}

func TestConversation_Load_UnknownConversationID(t *testing.T) {
	// Mock ListEvents returns empty Events slice for an unknown conversation ID.
	mock := &conversationMockClient{
		listEventsOutput: &bedrockagentcore.ListEventsOutput{
			Events: []types.Event{},
		},
	}

	conv := &Conversation{
		client:   mock,
		memoryID: "mem-test",
		actorID:  "actor-test",
	}

	messages, err := conv.Load(context.Background(), "unknown-conversation-id")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	// Must return non-nil empty slice — callers rely on len()/range without
	// nil-check.
	if messages == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(messages) != 0 {
		t.Errorf("expected empty slice, got %d messages", len(messages))
	}

	// Verify it's specifically []agent.Message{} (non-nil).
	var expected []agent.Message = []agent.Message{}
	if messages == nil && expected != nil {
		t.Error("messages should be non-nil like expected")
	}
}

func TestConversation_Delete_NonExistentSession(t *testing.T) {
	// Mock StopRuntimeSession returns a 404 error (session not found).
	mock := &conversationMockClient{
		stopRuntimeSessionErr: &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{
				Response: &http.Response{StatusCode: 404},
			},
			Err: errors.New("session not found"),
		},
	}

	conv := &Conversation{
		client:   mock,
		memoryID: "mem-test",
		actorID:  "actor-test",
	}

	err := conv.Delete(context.Background(), "non-existent-session")
	if err != nil {
		t.Fatalf("expected nil error for non-existent session, got: %v", err)
	}
}

func TestConversation_Constructor_WithAWSConfig(t *testing.T) {
	// Test that providing WithConversationAWSConfig works correctly
	// and creates a valid Conversation instance.
	awsCfg := aws.Config{Region: "us-west-2"}

	conv, err := NewConversation(
		WithConversationAWSConfig(awsCfg),
		WithMemoryID("mem-123"),
		WithActorID("actor-456"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conv == nil {
		t.Fatal("expected non-nil Conversation")
	}
	if conv.memoryID != "mem-123" {
		t.Errorf("expected memoryID 'mem-123', got %q", conv.memoryID)
	}
	if conv.actorID != "actor-456" {
		t.Errorf("expected actorID 'actor-456', got %q", conv.actorID)
	}
	if conv.client == nil {
		t.Error("expected non-nil client")
	}
}

func TestConversation_Constructor_MissingAWSConfig_AttemptsDefault(t *testing.T) {
	// When no AWS config is provided, NewConversation attempts to load the
	// default config. In a test environment without AWS credentials, this may
	// succeed (with empty/default config) or fail depending on the environment.
	// We verify that the constructor at least attempts the operation without panicking.
	conv, err := NewConversation(
		WithMemoryID("mem-test"),
		WithActorID("actor-test"),
	)

	// In most test environments, LoadDefaultConfig succeeds even without
	// credentials (it just loads an empty config). If it fails, that's also
	// acceptable — we just verify no panic occurred.
	if err != nil {
		// This is expected in environments without AWS config.
		// Verify the error message indicates config loading failure.
		if !containsStr(err.Error(), "agentcore conversation") {
			t.Errorf("expected error to contain 'agentcore conversation', got: %v", err)
		}
		return
	}

	// If it succeeded, verify the conversation was created properly.
	if conv == nil {
		t.Fatal("expected non-nil Conversation when no error")
	}
	if conv.client == nil {
		t.Error("expected non-nil client when constructor succeeds")
	}
}
