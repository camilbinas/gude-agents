package agentcore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
	"pgregory.net/rapid"
)

// Feature: agentcore-enhanced, Property 5: LTMStore namespace isolation

// TestProperty_LTMStoreNamespaceIsolation verifies that for any randomly generated
// namespace string, the configured namespace is always passed to the client in every
// API call (Store, Search, Delete).
//
// **Validates: Requirements 1.2, 2.2, 3.2**
func TestProperty_LTMStoreNamespaceIsolation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random non-empty namespace string.
		namespace := rapid.StringMatching(`[a-zA-Z0-9_\-]{1,64}`).Draw(rt, "namespace")

		// Create a mock client that captures the namespace passed in each API call.
		mock := &ltmNamespaceMockClient{}

		// Create the LTMStore with the generated namespace and mock client.
		store, err := NewLTMStore(
			WithLTMNamespace(namespace),
			withLTMClient(mock),
		)
		if err != nil {
			rt.Fatalf("NewLTMStore failed: %v", err)
		}

		ctx := context.Background()

		// --- Test Store: verify namespace is passed ---
		content := rapid.StringMatching(`[a-zA-Z0-9 ]{1,100}`).Draw(rt, "content")
		_, _ = store.Store(ctx, content, nil)

		if mock.batchCreateNamespace != namespace {
			rt.Fatalf("Store: expected namespace %q in BatchCreateMemoryRecords, got %q",
				namespace, mock.batchCreateNamespace)
		}

		// --- Test Search: verify namespace is passed ---
		query := rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(rt, "query")
		topK := rapid.IntRange(1, 20).Draw(rt, "topK")
		_, _ = store.Search(ctx, query, topK)

		if mock.retrieveMemoryID != namespace {
			rt.Fatalf("Search: expected MemoryId %q in RetrieveMemoryRecords, got %q",
				namespace, mock.retrieveMemoryID)
		}
		if mock.retrieveNamespace != namespace {
			rt.Fatalf("Search: expected Namespace %q in RetrieveMemoryRecords, got %q",
				namespace, mock.retrieveNamespace)
		}

		// --- Test Delete: verify namespace is passed ---
		deleteID := rapid.StringMatching(`[a-zA-Z0-9\-]{1,36}`).Draw(rt, "deleteID")
		_ = store.Delete(ctx, deleteID)

		if mock.deleteMemoryID != namespace {
			rt.Fatalf("Delete: expected MemoryId %q in DeleteMemoryRecord, got %q",
				namespace, mock.deleteMemoryID)
		}
	})
}

// ltmNamespaceMockClient captures the namespace passed in each LTM API call.
type ltmNamespaceMockClient struct {
	// Captured from BatchCreateMemoryRecords
	batchCreateNamespace string

	// Captured from RetrieveMemoryRecords
	retrieveMemoryID  string
	retrieveNamespace string

	// Captured from DeleteMemoryRecord
	deleteMemoryID string
}

func (m *ltmNamespaceMockClient) BatchCreateMemoryRecords(_ context.Context, params *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	if params.MemoryId != nil {
		m.batchCreateNamespace = *params.MemoryId
	}
	return &bedrockagentcore.BatchCreateMemoryRecordsOutput{
		SuccessfulRecords: []types.MemoryRecordOutput{
			{MemoryRecordId: strPtr("mock-id-123")},
		},
	}, nil
}

func (m *ltmNamespaceMockClient) RetrieveMemoryRecords(_ context.Context, params *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	if params.MemoryId != nil {
		m.retrieveMemoryID = *params.MemoryId
	}
	if params.Namespace != nil {
		m.retrieveNamespace = *params.Namespace
	}
	return &bedrockagentcore.RetrieveMemoryRecordsOutput{}, nil
}

func (m *ltmNamespaceMockClient) DeleteMemoryRecord(_ context.Context, params *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	if params.MemoryId != nil {
		m.deleteMemoryID = *params.MemoryId
	}
	return &bedrockagentcore.DeleteMemoryRecordOutput{}, nil
}

// --- Satisfy the rest of the agentCoreClient interface ---

func (m *ltmNamespaceMockClient) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	return nil, nil
}

func (m *ltmNamespaceMockClient) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}

func (m *ltmNamespaceMockClient) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}

func (m *ltmNamespaceMockClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}

func (m *ltmNamespaceMockClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}

func (m *ltmNamespaceMockClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}

func (m *ltmNamespaceMockClient) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}

func (m *ltmNamespaceMockClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}

func (m *ltmNamespaceMockClient) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *ltmNamespaceMockClient) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}

func (m *ltmNamespaceMockClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

// strPtr is a helper to create a string pointer.
func strPtr(s string) *string {
	return &s
}

// Feature: agentcore-enhanced, Property 14: Nil context returns error (LTMStore portion)

// TestProperty_LTMStoreNilContext verifies that calling Store, Search, and Delete
// with a nil context returns a non-nil error containing "nil context" and does not panic.
// The mock client should NOT be called because the nil context check happens before any API call.
//
// **Validates: Requirements 16.4**
func TestProperty_LTMStoreNilContext(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random valid inputs (non-empty content, non-empty query, non-empty ID, positive topK).
		content := rapid.StringMatching(`[a-zA-Z0-9 ]{1,100}`).Draw(rt, "content")
		query := rapid.StringMatching(`[a-zA-Z0-9 ]{1,100}`).Draw(rt, "query")
		id := rapid.StringMatching(`[a-zA-Z0-9\-]{1,50}`).Draw(rt, "id")
		topK := rapid.IntRange(1, 100).Draw(rt, "topK")

		// Create a mock client that tracks whether any method was called.
		mock := &ltmNilCtxMockClient{}

		// Create LTMStore with the mock client and a test namespace.
		store, err := NewLTMStore(withLTMClient(mock), WithLTMNamespace("test-ns"))
		if err != nil {
			rt.Fatalf("failed to create LTMStore: %v", err)
		}

		// Test Store with nil context — should not panic and should return error.
		storeID, storeErr := store.Store(nil, content, nil) //nolint:staticcheck
		if storeErr == nil {
			rt.Fatal("Store with nil context should return non-nil error")
		}
		if storeID != "" {
			rt.Fatalf("Store with nil context should return empty ID, got %q", storeID)
		}
		if !strings.Contains(storeErr.Error(), "nil context") {
			rt.Fatalf("Store error should contain 'nil context', got: %v", storeErr)
		}

		// Test Search with nil context — should not panic and should return error.
		results, searchErr := store.Search(nil, query, topK) //nolint:staticcheck
		if searchErr == nil {
			rt.Fatal("Search with nil context should return non-nil error")
		}
		if results != nil {
			rt.Fatalf("Search with nil context should return nil results, got %v", results)
		}
		if !strings.Contains(searchErr.Error(), "nil context") {
			rt.Fatalf("Search error should contain 'nil context', got: %v", searchErr)
		}

		// Test Delete with nil context — should not panic and should return error.
		deleteErr := store.Delete(nil, id) //nolint:staticcheck
		if deleteErr == nil {
			rt.Fatal("Delete with nil context should return non-nil error")
		}
		if !strings.Contains(deleteErr.Error(), "nil context") {
			rt.Fatalf("Delete error should contain 'nil context', got: %v", deleteErr)
		}

		// Verify the mock was never called (nil context check happens before API call).
		if mock.batchCreateCalled {
			rt.Fatal("BatchCreateMemoryRecords should not be called with nil context")
		}
		if mock.retrieveCalled {
			rt.Fatal("RetrieveMemoryRecords should not be called with nil context")
		}
		if mock.deleteCalled {
			rt.Fatal("DeleteMemoryRecord should not be called with nil context")
		}
	})
}

// ltmNilCtxMockClient implements agentCoreClient for the nil context PBT test.
// It tracks whether any LTM method was called (they should not be).
type ltmNilCtxMockClient struct {
	batchCreateCalled bool
	retrieveCalled    bool
	deleteCalled      bool
}

func (m *ltmNilCtxMockClient) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	return nil, nil
}

func (m *ltmNilCtxMockClient) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}

func (m *ltmNilCtxMockClient) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}

func (m *ltmNilCtxMockClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}

func (m *ltmNilCtxMockClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}

func (m *ltmNilCtxMockClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}

func (m *ltmNilCtxMockClient) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}

func (m *ltmNilCtxMockClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}

func (m *ltmNilCtxMockClient) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *ltmNilCtxMockClient) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}

func (m *ltmNilCtxMockClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *ltmNilCtxMockClient) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	m.batchCreateCalled = true
	return nil, nil
}

func (m *ltmNilCtxMockClient) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	m.retrieveCalled = true
	return nil, nil
}

func (m *ltmNilCtxMockClient) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	m.deleteCalled = true
	return nil, nil
}

// Feature: agentcore-enhanced, Property 3: LTMStore Delete idempotence

// TestProperty_LTMStoreDeleteIdempotence verifies that for any non-empty ID string,
// Delete returns nil regardless of whether the entry exists, and calling Delete twice
// with the same ID both return nil.
//
// **Validates: Requirements 4.1, 4.3**
func TestProperty_LTMStoreDeleteIdempotence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a non-empty ID string.
		id := rapid.StringMatching(`[a-zA-Z0-9\-_]{1,64}`).Draw(rt, "id")

		// Track how many times DeleteMemoryRecord is called.
		callCount := 0

		mock := &ltmDeleteIdempotenceMockClient{
			deleteFunc: func(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
				callCount++
				// Always return success (nil error) — simulates both existing and non-existing entries.
				return &bedrockagentcore.DeleteMemoryRecordOutput{}, nil
			},
		}

		store, err := NewLTMStore(
			withLTMClient(mock),
			WithLTMNamespace("test-ns"),
		)
		if err != nil {
			rt.Fatalf("failed to create LTMStore: %v", err)
		}

		// First Delete call — should return nil.
		err = store.Delete(context.Background(), id)
		if err != nil {
			rt.Fatalf("first Delete(%q) returned error: %v", id, err)
		}

		// Second Delete call with same ID — should also return nil (idempotent).
		err = store.Delete(context.Background(), id)
		if err != nil {
			rt.Fatalf("second Delete(%q) returned error: %v", id, err)
		}

		// Verify both calls were dispatched to the client.
		if callCount != 2 {
			rt.Fatalf("expected 2 DeleteMemoryRecord calls, got %d", callCount)
		}
	})
}

// ltmDeleteIdempotenceMockClient implements agentCoreClient for LTM Delete idempotence PBT tests.
type ltmDeleteIdempotenceMockClient struct {
	deleteFunc func(context.Context, *bedrockagentcore.DeleteMemoryRecordInput, ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error)
}

func (m *ltmDeleteIdempotenceMockClient) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	return nil, nil
}

func (m *ltmDeleteIdempotenceMockClient) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}

func (m *ltmDeleteIdempotenceMockClient) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}

func (m *ltmDeleteIdempotenceMockClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}

func (m *ltmDeleteIdempotenceMockClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}

func (m *ltmDeleteIdempotenceMockClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}

func (m *ltmDeleteIdempotenceMockClient) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}

func (m *ltmDeleteIdempotenceMockClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}

func (m *ltmDeleteIdempotenceMockClient) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *ltmDeleteIdempotenceMockClient) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}

func (m *ltmDeleteIdempotenceMockClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *ltmDeleteIdempotenceMockClient) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *ltmDeleteIdempotenceMockClient) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, nil
}

func (m *ltmDeleteIdempotenceMockClient) DeleteMemoryRecord(ctx context.Context, params *bedrockagentcore.DeleteMemoryRecordInput, optFns ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, params, optFns...)
	}
	return &bedrockagentcore.DeleteMemoryRecordOutput{}, nil
}

// Feature: agentcore-enhanced, Property 2: LTMStore Store-then-Search round-trip

// TestProperty_LTMStoreRoundTrip verifies that for any non-empty content string and
// metadata map, storing an entry and then searching for it returns results containing
// the stored entry's ID and content.
//
// The mock client stores entries on BatchCreateMemoryRecords and returns them on
// RetrieveMemoryRecords, simulating the AgentCore server-side behavior.
//
// **Validates: Requirements 2.1, 2.2, 3.1, 3.2**
func TestProperty_LTMStoreRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate non-empty content.
		content := rapid.StringMatching("[a-zA-Z0-9 ]{1,100}").Draw(rt, "content")

		// Generate a metadata map (0 to 5 entries).
		metaSize := rapid.IntRange(0, 5).Draw(rt, "metaSize")
		var metadata map[string]string
		if metaSize > 0 {
			metadata = make(map[string]string, metaSize)
			for i := 0; i < metaSize; i++ {
				key := rapid.StringMatching("[a-z]{1,10}").Draw(rt, "metaKey")
				val := rapid.StringMatching("[a-zA-Z0-9]{1,20}").Draw(rt, "metaVal")
				metadata[key] = val
			}
		}

		// Create a mock client that stores entries and returns them on search.
		mock := &ltmRoundTripMockClient{}

		store, err := NewLTMStore(
			withLTMClient(mock),
			WithLTMNamespace("test-ns"),
		)
		if err != nil {
			rt.Fatalf("failed to create LTMStore: %v", err)
		}

		// Store the entry.
		id, err := store.Store(context.Background(), content, metadata)
		if err != nil {
			rt.Fatalf("Store failed: %v", err)
		}
		if id == "" {
			rt.Fatalf("Store returned empty ID")
		}

		// Search for the entry.
		results, err := store.Search(context.Background(), content, 10)
		if err != nil {
			rt.Fatalf("Search failed: %v", err)
		}

		// Verify search results contain the stored entry's ID and content.
		found := false
		for _, r := range results {
			if r.ID == id && r.Content == content {
				found = true
				break
			}
		}
		if !found {
			rt.Fatalf("search results do not contain stored entry (id=%q, content=%q); got %d results",
				id, content, len(results))
		}
	})
}

// ltmRoundTripMockClient implements agentCoreClient for LTM round-trip PBT tests.
// It stores entries on BatchCreateMemoryRecords and returns them on RetrieveMemoryRecords.
type ltmRoundTripMockClient struct {
	mu      sync.Mutex
	entries []ltmStoredEntry
	nextID  int
}

type ltmStoredEntry struct {
	id       string
	content  string
	metadata map[string]types.MemoryRecordMetadataValue
}

func (m *ltmRoundTripMockClient) BatchCreateMemoryRecords(_ context.Context, input *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var successful []types.MemoryRecordOutput
	for _, rec := range input.Records {
		m.nextID++
		id := fmt.Sprintf("entry-%d", m.nextID)

		// Extract content text.
		var content string
		if textContent, ok := rec.Content.(*types.MemoryContentMemberText); ok {
			content = textContent.Value
		}

		m.entries = append(m.entries, ltmStoredEntry{
			id:       id,
			content:  content,
			metadata: rec.Metadata,
		})

		successful = append(successful, types.MemoryRecordOutput{
			MemoryRecordId: aws.String(id),
		})
	}

	return &bedrockagentcore.BatchCreateMemoryRecordsOutput{
		SuccessfulRecords: successful,
	}, nil
}

func (m *ltmRoundTripMockClient) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var summaries []types.MemoryRecordSummary
	for _, e := range m.entries {
		score := 0.95
		summaries = append(summaries, types.MemoryRecordSummary{
			MemoryRecordId: aws.String(e.id),
			Content:        &types.MemoryContentMemberText{Value: e.content},
			Metadata:       e.metadata,
			Score:          &score,
		})
	}

	return &bedrockagentcore.RetrieveMemoryRecordsOutput{
		MemoryRecordSummaries: summaries,
	}, nil
}

func (m *ltmRoundTripMockClient) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return &bedrockagentcore.DeleteMemoryRecordOutput{}, nil
}

// --- Satisfy the rest of the agentCoreClient interface for ltmRoundTripMockClient ---

func (m *ltmRoundTripMockClient) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	return nil, nil
}

func (m *ltmRoundTripMockClient) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}

func (m *ltmRoundTripMockClient) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}

func (m *ltmRoundTripMockClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}

func (m *ltmRoundTripMockClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}

func (m *ltmRoundTripMockClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}

func (m *ltmRoundTripMockClient) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}

func (m *ltmRoundTripMockClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}

func (m *ltmRoundTripMockClient) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *ltmRoundTripMockClient) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}

func (m *ltmRoundTripMockClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

// Feature: agentcore-enhanced, Property 1: LTMStore input validation rejects invalid inputs

// ltmInputValidationMockClient implements agentCoreClient for input validation tests.
// All LTM API methods set apiCalled to true and panic — validation should prevent any call.
type ltmInputValidationMockClient struct {
	apiCalled bool
}

func (m *ltmInputValidationMockClient) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	m.apiCalled = true
	panic("InvokeAgentRuntime should not be called during input validation")
}

func (m *ltmInputValidationMockClient) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	m.apiCalled = true
	panic("CreateEvent should not be called during input validation")
}

func (m *ltmInputValidationMockClient) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	m.apiCalled = true
	panic("ListEvents should not be called during input validation")
}

func (m *ltmInputValidationMockClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	m.apiCalled = true
	panic("ListSessions should not be called during input validation")
}

func (m *ltmInputValidationMockClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	m.apiCalled = true
	panic("StopRuntimeSession should not be called during input validation")
}

func (m *ltmInputValidationMockClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	m.apiCalled = true
	panic("StartBrowserSession should not be called during input validation")
}

func (m *ltmInputValidationMockClient) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	m.apiCalled = true
	panic("InvokeBrowser should not be called during input validation")
}

func (m *ltmInputValidationMockClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	m.apiCalled = true
	panic("StopBrowserSession should not be called during input validation")
}

func (m *ltmInputValidationMockClient) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	m.apiCalled = true
	panic("StartCodeInterpreterSession should not be called during input validation")
}

func (m *ltmInputValidationMockClient) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	m.apiCalled = true
	panic("InvokeCodeInterpreter should not be called during input validation")
}

func (m *ltmInputValidationMockClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	m.apiCalled = true
	panic("StopCodeInterpreterSession should not be called during input validation")
}

func (m *ltmInputValidationMockClient) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	m.apiCalled = true
	panic("BatchCreateMemoryRecords should not be called during input validation")
}

func (m *ltmInputValidationMockClient) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	m.apiCalled = true
	panic("RetrieveMemoryRecords should not be called during input validation")
}

func (m *ltmInputValidationMockClient) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	m.apiCalled = true
	panic("DeleteMemoryRecord should not be called during input validation")
}

// TestProperty_LTMStoreInputValidation verifies that for any arbitrary input,
// the LTMStore rejects invalid inputs with the correct sentinel errors before
// making any API calls to the underlying client.
//
// - Store with empty content returns ErrLTMContentRequired
// - Search with empty query returns ErrLTMQueryRequired
// - Search with topK < 1 returns an appropriate error
// - Delete with empty ID returns ErrLTMIDRequired
// - No API calls are made to the mock client in any of these cases
//
// **Validates: Requirements 2.3, 3.4, 3.5, 4.2, 16.2**
func TestProperty_LTMStoreInputValidation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Create a mock client that panics if any API method is called.
		mock := &ltmInputValidationMockClient{}

		// Create an LTMStore with the mock client and a random namespace.
		namespace := rapid.StringMatching(`[a-z][a-z0-9\-]{1,20}`).Draw(rt, "namespace")
		store, err := NewLTMStore(
			WithLTMNamespace(namespace),
			withLTMClient(mock),
		)
		if err != nil {
			rt.Fatalf("failed to create LTMStore: %v", err)
		}

		ctx := context.Background()

		// Generate arbitrary metadata (may be nil or populated).
		var metadata map[string]string
		if rapid.Bool().Draw(rt, "hasMetadata") {
			numKeys := rapid.IntRange(0, 5).Draw(rt, "numMetadataKeys")
			metadata = make(map[string]string, numKeys)
			for i := 0; i < numKeys; i++ {
				key := rapid.StringMatching(`[a-z]{1,10}`).Draw(rt, "metaKey")
				val := rapid.String().Draw(rt, "metaVal")
				metadata[key] = val
			}
		}

		// --- Store with empty content ---
		_, storeErr := store.Store(ctx, "", metadata)
		if storeErr == nil {
			rt.Fatal("Store with empty content should return an error, got nil")
		}
		if !errors.Is(storeErr, ErrLTMContentRequired) {
			rt.Fatalf("Store with empty content should return ErrLTMContentRequired, got: %v", storeErr)
		}

		// --- Search with empty query ---
		topK := rapid.IntRange(1, 100).Draw(rt, "validTopK")
		_, searchErr := store.Search(ctx, "", topK)
		if searchErr == nil {
			rt.Fatal("Search with empty query should return an error, got nil")
		}
		if !errors.Is(searchErr, ErrLTMQueryRequired) {
			rt.Fatalf("Search with empty query should return ErrLTMQueryRequired, got: %v", searchErr)
		}

		// --- Search with topK < 1 ---
		invalidTopK := rapid.IntRange(-1000, 0).Draw(rt, "invalidTopK")
		query := rapid.StringMatching(`[a-z ]{1,50}`).Draw(rt, "query")
		_, topKErr := store.Search(ctx, query, invalidTopK)
		if topKErr == nil {
			rt.Fatalf("Search with topK=%d should return an error, got nil", invalidTopK)
		}
		if !strings.Contains(topKErr.Error(), "topK must be at least 1") {
			rt.Fatalf("Search with topK=%d should return topK error, got: %v", invalidTopK, topKErr)
		}

		// --- Delete with empty ID ---
		deleteErr := store.Delete(ctx, "")
		if deleteErr == nil {
			rt.Fatal("Delete with empty ID should return an error, got nil")
		}
		if !errors.Is(deleteErr, ErrLTMIDRequired) {
			rt.Fatalf("Delete with empty ID should return ErrLTMIDRequired, got: %v", deleteErr)
		}

		// --- Verify no API calls were made ---
		if mock.apiCalled {
			rt.Fatal("expected no API calls to be made during input validation, but at least one was called")
		}
	})
}

// Feature: agentcore-enhanced, Property 4: LTMStore error wrapping preserves chain

// errorWrappingMockClient implements agentCoreClient and returns a configurable
// error from all LTM API methods. Used to verify error wrapping behavior.
type errorWrappingMockClient struct {
	err error
}

func (m *errorWrappingMockClient) BatchCreateMemoryRecords(_ context.Context, _ *bedrockagentcore.BatchCreateMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	return nil, m.err
}

func (m *errorWrappingMockClient) RetrieveMemoryRecords(_ context.Context, _ *bedrockagentcore.RetrieveMemoryRecordsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	return nil, m.err
}

func (m *errorWrappingMockClient) DeleteMemoryRecord(_ context.Context, _ *bedrockagentcore.DeleteMemoryRecordInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.DeleteMemoryRecordOutput, error) {
	return nil, m.err
}

func (m *errorWrappingMockClient) InvokeAgentRuntime(_ context.Context, _ *bedrockagentcore.InvokeAgentRuntimeInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeAgentRuntimeOutput, error) {
	return nil, nil
}

func (m *errorWrappingMockClient) CreateEvent(_ context.Context, _ *bedrockagentcore.CreateEventInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.CreateEventOutput, error) {
	return nil, nil
}

func (m *errorWrappingMockClient) ListEvents(_ context.Context, _ *bedrockagentcore.ListEventsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListEventsOutput, error) {
	return nil, nil
}

func (m *errorWrappingMockClient) ListSessions(_ context.Context, _ *bedrockagentcore.ListSessionsInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.ListSessionsOutput, error) {
	return nil, nil
}

func (m *errorWrappingMockClient) StopRuntimeSession(_ context.Context, _ *bedrockagentcore.StopRuntimeSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopRuntimeSessionOutput, error) {
	return nil, nil
}

func (m *errorWrappingMockClient) StartBrowserSession(_ context.Context, _ *bedrockagentcore.StartBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartBrowserSessionOutput, error) {
	return nil, nil
}

func (m *errorWrappingMockClient) InvokeBrowser(_ context.Context, _ *bedrockagentcore.InvokeBrowserInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeBrowserOutput, error) {
	return nil, nil
}

func (m *errorWrappingMockClient) StopBrowserSession(_ context.Context, _ *bedrockagentcore.StopBrowserSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopBrowserSessionOutput, error) {
	return nil, nil
}

func (m *errorWrappingMockClient) StartCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StartCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StartCodeInterpreterSessionOutput, error) {
	return nil, nil
}

func (m *errorWrappingMockClient) InvokeCodeInterpreter(_ context.Context, _ *bedrockagentcore.InvokeCodeInterpreterInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.InvokeCodeInterpreterOutput, error) {
	return nil, nil
}

func (m *errorWrappingMockClient) StopCodeInterpreterSession(_ context.Context, _ *bedrockagentcore.StopCodeInterpreterSessionInput, _ ...func(*bedrockagentcore.Options)) (*bedrockagentcore.StopCodeInterpreterSessionOutput, error) {
	return nil, nil
}

// Compile-time check that errorWrappingMockClient satisfies agentCoreClient.
var _ agentCoreClient = (*errorWrappingMockClient)(nil)

// TestProperty_LTMStoreErrorWrapping verifies that for any arbitrary error returned
// by the underlying client, the LTMStore methods (Store, Search, Delete) wrap the
// error with the "agentcore ltm:" prefix and preserve the error chain so that
// errors.Is and errors.As can still match the original error.
//
// **Validates: Requirements 2.5, 3.6, 4.4, 16.1**
func TestProperty_LTMStoreErrorWrapping(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random error message using rapid.
		errMsg := rapid.StringMatching(`[a-zA-Z0-9 _\-]{1,100}`).Draw(rt, "errMsg")
		originalErr := errors.New(errMsg)

		mock := &errorWrappingMockClient{err: originalErr}

		store, err := NewLTMStore(
			withLTMClient(mock),
			WithLTMNamespace("test-ns"),
		)
		if err != nil {
			rt.Fatalf("NewLTMStore failed: %v", err)
		}

		ctx := context.Background()

		// Test Store error wrapping.
		_, storeErr := store.Store(ctx, "some content", nil)
		if storeErr == nil {
			rt.Fatal("Store should have returned an error")
		}
		if !strings.HasPrefix(storeErr.Error(), "agentcore ltm:") {
			rt.Fatalf("Store error should have 'agentcore ltm:' prefix, got: %q", storeErr.Error())
		}
		if !errors.Is(storeErr, originalErr) {
			rt.Fatalf("errors.Is(storeErr, originalErr) should be true; storeErr=%q, originalErr=%q", storeErr, originalErr)
		}

		// Test Search error wrapping.
		_, searchErr := store.Search(ctx, "some query", 5)
		if searchErr == nil {
			rt.Fatal("Search should have returned an error")
		}
		if !strings.HasPrefix(searchErr.Error(), "agentcore ltm:") {
			rt.Fatalf("Search error should have 'agentcore ltm:' prefix, got: %q", searchErr.Error())
		}
		if !errors.Is(searchErr, originalErr) {
			rt.Fatalf("errors.Is(searchErr, originalErr) should be true; searchErr=%q, originalErr=%q", searchErr, originalErr)
		}

		// Test Delete error wrapping.
		deleteErr := store.Delete(ctx, "some-id")
		if deleteErr == nil {
			rt.Fatal("Delete should have returned an error")
		}
		if !strings.HasPrefix(deleteErr.Error(), "agentcore ltm:") {
			rt.Fatalf("Delete error should have 'agentcore ltm:' prefix, got: %q", deleteErr.Error())
		}
		if !errors.Is(deleteErr, originalErr) {
			rt.Fatalf("errors.Is(deleteErr, originalErr) should be true; deleteErr=%q, originalErr=%q", deleteErr, originalErr)
		}
	})
}
