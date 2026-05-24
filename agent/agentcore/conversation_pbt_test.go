// Feature: agentcore-runtime, Property 1: Conversation message round-trip
package agentcore

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"pgregory.net/rapid"
)

// roundTripMockClient implements agentCoreClient for conversation round-trip PBT.
// It captures the CreateEvent payload, simulates the wire round-trip (marshal → unmarshal
// via the SDK's document.NewLazyDocument), and returns the result on ListEvents —
// mimicking a real AgentCore Memory service.
type roundTripMockClient struct {
	mu       sync.Mutex
	payloads map[string][]types.PayloadType // sessionID → payload from CreateEvent (wire-simulated)
}

func newRoundTripMockClient() *roundTripMockClient {
	return &roundTripMockClient{
		payloads: make(map[string][]types.PayloadType),
	}
}

func (m *roundTripMockClient) CreateEvent(_ context.Context, params *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sessionID := aws.ToString(params.SessionId)

	// Simulate the wire round-trip: marshal the document to JSON bytes,
	// then create a new LazyDocument from the string (as the SDK would on deserialization).
	var responsePayload []types.PayloadType
	for _, p := range params.Payload {
		blob, ok := p.(*types.PayloadTypeMemberBlob)
		if !ok {
			continue
		}
		// Marshal the document to JSON (simulates sending over the wire).
		jsonBytes, err := blob.Value.MarshalSmithyDocument()
		if err != nil {
			continue
		}
		// The wire format is a JSON-encoded string (e.g., "\"...\"").
		// Unmarshal it to get the raw string value, then re-wrap with NewLazyDocument.
		// This simulates what the SDK does: send JSON over the wire, receive it back,
		// and wrap the decoded value in a document.
		var strVal string
		if err := json.Unmarshal(jsonBytes, &strVal); err != nil {
			// If it's not a JSON string, use the raw bytes as-is.
			strVal = string(jsonBytes)
		}
		responsePayload = append(responsePayload, &types.PayloadTypeMemberBlob{
			Value: document.NewLazyDocument(strVal),
		})
	}
	m.payloads[sessionID] = responsePayload
	return &bedrockagentcore.CreateEventOutput{}, nil
}

func (m *roundTripMockClient) ListEvents(_ context.Context, params *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sessionID := aws.ToString(params.SessionId)
	payload, ok := m.payloads[sessionID]
	if !ok {
		return &bedrockagentcore.ListEventsOutput{Events: []types.Event{}}, nil
	}
	now := time.Now()
	return &bedrockagentcore.ListEventsOutput{
		Events: []types.Event{
			{
				ActorId:        params.ActorId,
				EventId:        aws.String("evt-1"),
				EventTimestamp: &now,
				MemoryId:       params.MemoryId,
				Payload:        payload,
				SessionId:      params.SessionId,
			},
		},
	}, nil
}

func (m *roundTripMockClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return &bedrockagentcore.ListSessionsOutput{}, nil
}

func (m *roundTripMockClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return &bedrockagentcore.StopRuntimeSessionOutput{}, nil
}

func (m *roundTripMockClient) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	return &bedrockagentcore.InvokeAgentRuntimeOutput{}, nil
}

func (m *roundTripMockClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return &bedrockagentcore.StartBrowserSessionOutput{}, nil
}

func (m *roundTripMockClient) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return &bedrockagentcore.InvokeBrowserOutput{}, nil
}

func (m *roundTripMockClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return &bedrockagentcore.StopBrowserSessionOutput{}, nil
}

func (m *roundTripMockClient) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return &bedrockagentcore.StartCodeInterpreterSessionOutput{}, nil
}

func (m *roundTripMockClient) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return &bedrockagentcore.InvokeCodeInterpreterOutput{}, nil
}

func (m *roundTripMockClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return &bedrockagentcore.StopCodeInterpreterSessionOutput{}, nil
}

func (m *roundTripMockClient) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *roundTripMockClient) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *roundTripMockClient) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, nil
}

// Compile-time check that roundTripMockClient satisfies agentCoreClient.
var _ agentCoreClient = (*roundTripMockClient)(nil)

// TestProperty_ConversationMessageRoundTrip verifies that for any valid []agent.Message
// slice containing any combination of TextBlock, ToolUseBlock, and ToolResultBlock content,
// saving to the AgentCore conversation store and then loading with the same conversation ID
// produces a slice that is deeply equal to the original — preserving message count, ordering,
// roles, content block types, content block ordering, and all typed field values.
//
// **Validates: Requirements 6.1, 6.2, 6.3, 6.4, 6.6**
func TestProperty_ConversationMessageRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mock := newRoundTripMockClient()
		conv := &Conversation{
			client:   mock,
			memoryID: "mem-test",
			actorID:  "actor-test",
		}

		messages := testutil.GenMessages(t, 10)
		convID := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, "conversationID")

		ctx := context.Background()

		if err := conv.Save(ctx, convID, messages); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		loaded, err := conv.Load(ctx, convID)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		if !reflect.DeepEqual(messages, loaded) {
			t.Fatalf("round-trip mismatch:\n  saved:  %+v\n  loaded: %+v", messages, loaded)
		}
	})
}
