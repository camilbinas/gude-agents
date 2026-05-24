package agentcore

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"pgregory.net/rapid"
)

// Feature: agentcore-runtime, Property 7: Streaming chunk order preservation

// TestProperty7_StreamingChunkOrderPreservation verifies that for any sequence of
// N text chunks produced by InvokeStream's StreamCallback, the chunks are submitted
// to AgentCore in the same order they were received — chunk at callback index i is
// submitted before chunk at callback index j whenever i < j.
//
// **Validates: Requirements 4.6**
func TestProperty7_StreamingChunkOrderPreservation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random number of chunks (1-20).
		numChunks := rapid.IntRange(1, 20).Draw(t, "numChunks")

		// Generate random chunk content strings.
		chunks := make([]string, numChunks)
		for i := 0; i < numChunks; i++ {
			chunks[i] = rapid.StringMatching(`.+`).Draw(t, "chunk")
		}

		// Generate a random event ID for the stream.
		eventID := rapid.StringMatching(`[a-zA-Z0-9\-]{5,20}`).Draw(t, "eventID")

		// Create a mock client that captures all submitted streamChunk payloads.
		mock := &streamingChunkCaptureMock{}

		// Create a minimal Runtime with the mock client.
		rt := &Runtime{
			client:   mock,
			cfg:      defaultRuntimeConfig(),
			sessions: make(map[string]*sync.Mutex),
		}

		// Simulate the streaming callback by calling submitStreamChunk for each chunk
		// in order, then submit the final chunk marker.
		for i, chunk := range chunks {
			sc := streamChunk{
				EventID: eventID,
				Chunk:   chunk,
				Index:   i,
				Final:   false,
			}
			err := rt.submitStreamChunk(context.Background(), sc)
			if err != nil {
				t.Fatalf("submitStreamChunk failed at index %d: %v", i, err)
			}
		}

		// Submit the final chunk marker.
		finalSC := streamChunk{
			EventID: eventID,
			Chunk:   "",
			Index:   numChunks,
			Final:   true,
		}
		err := rt.submitStreamChunk(context.Background(), finalSC)
		if err != nil {
			t.Fatalf("submitStreamChunk failed for final chunk: %v", err)
		}

		// Retrieve captured payloads from the mock.
		captured := mock.getCapturedChunks()

		// We expect numChunks + 1 submissions (N data chunks + 1 final marker).
		expectedCount := numChunks + 1
		if len(captured) != expectedCount {
			t.Fatalf("expected %d captured chunks, got %d", expectedCount, len(captured))
		}

		// Verify Index values are 0, 1, 2, ..., N-1, N in order.
		for i, sc := range captured {
			if sc.Index != i {
				t.Fatalf("expected chunk at position %d to have Index=%d, got Index=%d", i, i, sc.Index)
			}
		}

		// Verify chunk content matches the original sequence order for data chunks.
		for i := 0; i < numChunks; i++ {
			if captured[i].Chunk != chunks[i] {
				t.Fatalf("chunk content mismatch at index %d: expected %q, got %q",
					i, chunks[i], captured[i].Chunk)
			}
			// Data chunks should not be marked as final.
			if captured[i].Final {
				t.Fatalf("data chunk at index %d should not have Final=true", i)
			}
		}

		// Verify the last captured chunk is the final marker.
		finalCaptured := captured[numChunks]
		if !finalCaptured.Final {
			t.Fatalf("expected final chunk to have Final=true, got Final=false")
		}
		if finalCaptured.Chunk != "" {
			t.Fatalf("expected final chunk to have empty Chunk, got %q", finalCaptured.Chunk)
		}

		// Verify all chunks have the correct EventID.
		for i, sc := range captured {
			if sc.EventID != eventID {
				t.Fatalf("chunk at index %d has EventID=%q, expected %q", i, sc.EventID, eventID)
			}
		}
	})
}

// streamingChunkCaptureMock captures all streamChunk payloads submitted via
// InvokeAgentRuntime. It satisfies the agentCoreClient interface.
type streamingChunkCaptureMock struct {
	mu     sync.Mutex
	chunks []streamChunk
}

func (m *streamingChunkCaptureMock) getCapturedChunks() []streamChunk {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]streamChunk, len(m.chunks))
	copy(result, m.chunks)
	return result
}

func (m *streamingChunkCaptureMock) InvokeAgentRuntime(_ context.Context, input *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	// Parse the payload to capture the streamChunk.
	if input != nil && input.Payload != nil {
		var sc streamChunk
		if err := json.Unmarshal(input.Payload, &sc); err == nil {
			m.mu.Lock()
			m.chunks = append(m.chunks, sc)
			m.mu.Unlock()
		}
	}
	return &bedrockagentcore.InvokeAgentRuntimeOutput{}, nil
}

func (m *streamingChunkCaptureMock) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}

func (m *streamingChunkCaptureMock) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}

func (m *streamingChunkCaptureMock) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}

func (m *streamingChunkCaptureMock) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}

func (m *streamingChunkCaptureMock) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}

func (m *streamingChunkCaptureMock) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}

func (m *streamingChunkCaptureMock) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}

func (m *streamingChunkCaptureMock) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *streamingChunkCaptureMock) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}

func (m *streamingChunkCaptureMock) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *streamingChunkCaptureMock) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *streamingChunkCaptureMock) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *streamingChunkCaptureMock) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, nil
}

