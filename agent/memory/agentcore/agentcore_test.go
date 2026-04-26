package agentcore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Mock AgentCoreAPI
// ---------------------------------------------------------------------------

// mockAgentCoreAPI implements AgentCoreAPI, recording request parameters and
// returning configurable responses/errors. Used by property and unit tests.
type mockAgentCoreAPI struct {
	// Recorded inputs from the last call to each method.
	createEventInput           *bedrockagentcore.CreateEventInput
	batchCreateMemoryRecInput  *bedrockagentcore.BatchCreateMemoryRecordsInput
	retrieveMemoryRecordsInput *bedrockagentcore.RetrieveMemoryRecordsInput

	// Configurable return values.
	createEventOutput           *bedrockagentcore.CreateEventOutput
	createEventErr              error
	batchCreateMemoryRecOutput  *bedrockagentcore.BatchCreateMemoryRecordsOutput
	batchCreateMemoryRecErr     error
	retrieveMemoryRecordsOutput *bedrockagentcore.RetrieveMemoryRecordsOutput
	retrieveMemoryRecordsErr    error
}

func (m *mockAgentCoreAPI) CreateEvent(
	_ context.Context,
	params *bedrockagentcore.CreateEventInput,
	_ ...func(*bedrockagentcore.Options),
) (*bedrockagentcore.CreateEventOutput, error) {
	m.createEventInput = params
	return m.createEventOutput, m.createEventErr
}

func (m *mockAgentCoreAPI) BatchCreateMemoryRecords(
	_ context.Context,
	params *bedrockagentcore.BatchCreateMemoryRecordsInput,
	_ ...func(*bedrockagentcore.Options),
) (*bedrockagentcore.BatchCreateMemoryRecordsOutput, error) {
	m.batchCreateMemoryRecInput = params
	return m.batchCreateMemoryRecOutput, m.batchCreateMemoryRecErr
}

func (m *mockAgentCoreAPI) RetrieveMemoryRecords(
	_ context.Context,
	params *bedrockagentcore.RetrieveMemoryRecordsInput,
	_ ...func(*bedrockagentcore.Options),
) (*bedrockagentcore.RetrieveMemoryRecordsOutput, error) {
	m.retrieveMemoryRecordsInput = params
	return m.retrieveMemoryRecordsOutput, m.retrieveMemoryRecordsErr
}

// ---------------------------------------------------------------------------
// Generators
// ---------------------------------------------------------------------------

// genIdentifier generates a random non-empty alphanumeric string (1–50 chars).
func genIdentifier(t *rapid.T) string {
	return rapid.StringMatching(`[a-zA-Z0-9]{1,50}`).Draw(t, "identifier")
}

// genFact generates a random non-empty string (1–200 chars).
func genFact(t *rapid.T) string {
	return rapid.StringMatching(`[a-zA-Z0-9 ]{1,200}`).Draw(t, "fact")
}

// genMetadata generates a randomly nil or non-nil metadata map with 0–5 entries.
func genMetadata(t *rapid.T) map[string]string {
	if rapid.Bool().Draw(t, "metadata_present") {
		n := rapid.IntRange(0, 5).Draw(t, "metadata_len")
		if n == 0 {
			return map[string]string{}
		}
		m := make(map[string]string, n)
		for i := range n {
			key := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, fmt.Sprintf("metadata_key_%d", i))
			val := rapid.StringMatching(`[a-zA-Z0-9]{1,20}`).Draw(t, fmt.Sprintf("metadata_val_%d", i))
			m[key] = val
		}
		return m
	}
	return nil
}

// genMemoryRecordSummary generates a random MemoryRecordSummary with content
// text, score in [0,1], timestamp, and optional metadata.
func genMemoryRecordSummary(t *rapid.T, label string) types.MemoryRecordSummary {
	fact := rapid.StringMatching(`[a-zA-Z0-9 ]{1,200}`).Draw(t, label+"_content")
	score := rapid.Float64Range(0, 1).Draw(t, label+"_score")
	sec := rapid.Int64Range(0, 4102444800).Draw(t, label+"_ts_sec")
	ts := time.Unix(sec, 0).UTC()

	summary := types.MemoryRecordSummary{
		Content:   &types.MemoryContentMemberText{Value: fact},
		Score:     aws.Float64(score),
		CreatedAt: aws.Time(ts),
	}

	// Optionally add metadata.
	if rapid.Bool().Draw(t, label+"_has_metadata") {
		n := rapid.IntRange(1, 5).Draw(t, label+"_md_len")
		md := make(map[string]types.MetadataValue, n)
		for i := range n {
			key := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, fmt.Sprintf("%s_md_key_%d", label, i))
			val := rapid.StringMatching(`[a-zA-Z0-9]{1,20}`).Draw(t, fmt.Sprintf("%s_md_val_%d", label, i))
			md[key] = &types.MetadataValueMemberStringValue{Value: val}
		}
		summary.Metadata = md
	}

	return summary
}

// genError generates a random error with a non-empty message.
func genError(t *rapid.T) error {
	msg := rapid.StringMatching(`[a-zA-Z0-9 ]{1,100}`).Draw(t, "error_msg")
	return fmt.Errorf("%s", msg)
}

// ---------------------------------------------------------------------------
// Property Tests
// ---------------------------------------------------------------------------

// TestProperty_RememberCreateEventMapping verifies that Remember in CreateEvent
// mode correctly maps all inputs to the CreateEvent request fields.
//
// **Validates: Requirements 3.1, 3.2, 3.4, 3.9**
func TestProperty_RememberCreateEventMapping(t *testing.T) {
	t.Helper()

	rapid.Check(t, func(rt *rapid.T) {
		identifier := genIdentifier(rt)
		fact := genFact(rt)
		metadata := genMetadata(rt)

		mock := &mockAgentCoreAPI{
			createEventOutput: &bedrockagentcore.CreateEventOutput{},
		}

		memoryID := rapid.StringMatching(`[a-zA-Z0-9]{5,20}`).Draw(rt, "memoryID")
		sessionID := rapid.StringMatching(`[a-zA-Z0-9-]{10,36}`).Draw(rt, "sessionID")

		store, err := New(mock, memoryID,
			WithStoreMode(CreateEventMode),
			WithSessionIDFunc(func() string { return sessionID }),
		)
		if err != nil {
			rt.Fatalf("New() returned unexpected error: %v", err)
		}

		err = store.Remember(context.Background(), identifier, fact, metadata)
		if err != nil {
			rt.Fatalf("Remember() returned unexpected error: %v", err)
		}

		// The mock must have recorded a CreateEvent call.
		input := mock.createEventInput
		if input == nil {
			rt.Fatal("CreateEvent was not called")
		}

		// Req 3.5: MemoryId matches the store's memory ID.
		if aws.ToString(input.MemoryId) != memoryID {
			rt.Fatalf("MemoryId: got %q, want %q", aws.ToString(input.MemoryId), memoryID)
		}

		// Req 3.2: ActorId equals the identifier.
		if aws.ToString(input.ActorId) != identifier {
			rt.Fatalf("ActorId: got %q, want %q", aws.ToString(input.ActorId), identifier)
		}

		// Req 3.3: SessionId matches the generated session ID.
		if aws.ToString(input.SessionId) != sessionID {
			rt.Fatalf("SessionId: got %q, want %q", aws.ToString(input.SessionId), sessionID)
		}

		// Req 3.4: Payload contains the fact as text content.
		if len(input.Payload) != 1 {
			rt.Fatalf("Payload length: got %d, want 1", len(input.Payload))
		}
		conv, ok := input.Payload[0].(*types.PayloadTypeMemberConversational)
		if !ok {
			rt.Fatalf("Payload[0] type: got %T, want *types.PayloadTypeMemberConversational", input.Payload[0])
		}
		textContent, ok := conv.Value.Content.(*types.ContentMemberText)
		if !ok {
			rt.Fatalf("Content type: got %T, want *types.ContentMemberText", conv.Value.Content)
		}
		if textContent.Value != fact {
			rt.Fatalf("Payload fact: got %q, want %q", textContent.Value, fact)
		}

		// Req 3.9: Metadata matches the input metadata.
		if len(metadata) == 0 {
			// When metadata is nil or empty, the request metadata should be nil or empty.
			if len(input.Metadata) != 0 {
				rt.Fatalf("Metadata: expected nil or empty, got %v", input.Metadata)
			}
		} else {
			if len(input.Metadata) != len(metadata) {
				rt.Fatalf("Metadata length: got %d, want %d", len(input.Metadata), len(metadata))
			}
			for k, v := range metadata {
				mv, exists := input.Metadata[k]
				if !exists {
					rt.Fatalf("Metadata key %q: not found in request", k)
				}
				sv, ok := mv.(*types.MetadataValueMemberStringValue)
				if !ok {
					rt.Fatalf("Metadata key %q: got type %T, want *types.MetadataValueMemberStringValue", k, mv)
				}
				if sv.Value != v {
					rt.Fatalf("Metadata key %q: got %q, want %q", k, sv.Value, v)
				}
			}
		}
	})
}

// TestProperty_RememberBatchCreateMapping verifies that Remember in BatchCreate
// mode correctly maps all inputs to the BatchCreateMemoryRecords request fields.
//
// **Validates: Requirements 4.1, 4.2, 4.3, 4.4, 4.7**
func TestProperty_RememberBatchCreateMapping(t *testing.T) {
	t.Helper()

	rapid.Check(t, func(rt *rapid.T) {
		identifier := genIdentifier(rt)
		fact := genFact(rt)
		metadata := genMetadata(rt)

		mock := &mockAgentCoreAPI{
			batchCreateMemoryRecOutput: &bedrockagentcore.BatchCreateMemoryRecordsOutput{},
		}

		memoryID := rapid.StringMatching(`[a-zA-Z0-9]{5,20}`).Draw(rt, "memoryID")

		store, err := New(mock, memoryID,
			WithStoreMode(BatchCreateMode),
		)
		if err != nil {
			rt.Fatalf("New() returned unexpected error: %v", err)
		}

		before := time.Now()
		err = store.Remember(context.Background(), identifier, fact, metadata)
		if err != nil {
			rt.Fatalf("Remember() returned unexpected error: %v", err)
		}
		after := time.Now()

		// The mock must have recorded a BatchCreateMemoryRecords call.
		input := mock.batchCreateMemoryRecInput
		if input == nil {
			rt.Fatal("BatchCreateMemoryRecords was not called")
		}

		// Req 4.5: MemoryId matches the store's memory ID.
		if aws.ToString(input.MemoryId) != memoryID {
			rt.Fatalf("MemoryId: got %q, want %q", aws.ToString(input.MemoryId), memoryID)
		}

		// Req 4.1: Exactly one record in the request.
		if len(input.Records) != 1 {
			rt.Fatalf("Records length: got %d, want 1", len(input.Records))
		}
		record := input.Records[0]

		// Req 4.4: Content is MemoryContentMemberText with Value == fact.
		textContent, ok := record.Content.(*types.MemoryContentMemberText)
		if !ok {
			rt.Fatalf("Content type: got %T, want *types.MemoryContentMemberText", record.Content)
		}
		if textContent.Value != fact {
			rt.Fatalf("Content text: got %q, want %q", textContent.Value, fact)
		}

		// Req 4.3: Namespaces contains the rendered namespace (default: /facts/{identifier}/).
		expectedNS := "/facts/" + identifier + "/"
		if len(record.Namespaces) != 1 {
			rt.Fatalf("Namespaces length: got %d, want 1", len(record.Namespaces))
		}
		if record.Namespaces[0] != expectedNS {
			rt.Fatalf("Namespace: got %q, want %q", record.Namespaces[0], expectedNS)
		}

		// Timestamp is set and within the test execution window.
		if record.Timestamp == nil {
			rt.Fatal("Timestamp: expected non-nil")
		}
		ts := *record.Timestamp
		if ts.Before(before) || ts.After(after) {
			rt.Fatalf("Timestamp %v not in expected range [%v, %v]", ts, before, after)
		}

		// Note: The SDK's MemoryRecordCreateInput does not have a Metadata field,
		// so metadata mapping for BatchCreate mode is not asserted here.
		// See task 3.2 notes about this SDK limitation.
	})
}

// TestProperty_RecallRequestMapping verifies that Recall correctly maps all
// inputs to the RetrieveMemoryRecords request fields.
//
// **Validates: Requirements 5.1, 5.2, 5.4**
func TestProperty_RecallRequestMapping(t *testing.T) {
	t.Helper()

	rapid.Check(t, func(rt *rapid.T) {
		identifier := genIdentifier(rt)
		query := genFact(rt) // reuse genFact for non-empty query strings
		limit := rapid.IntRange(1, 1000).Draw(rt, "limit")

		mock := &mockAgentCoreAPI{
			retrieveMemoryRecordsOutput: &bedrockagentcore.RetrieveMemoryRecordsOutput{
				MemoryRecordSummaries: []types.MemoryRecordSummary{},
			},
		}

		memoryID := rapid.StringMatching(`[a-zA-Z0-9]{5,20}`).Draw(rt, "memoryID")

		store, err := New(mock, memoryID)
		if err != nil {
			rt.Fatalf("New() returned unexpected error: %v", err)
		}

		entries, err := store.Recall(context.Background(), identifier, query, limit)
		if err != nil {
			rt.Fatalf("Recall() returned unexpected error: %v", err)
		}

		// Recall with empty results should return an empty non-nil slice.
		if entries == nil {
			rt.Fatal("Recall() returned nil slice, expected empty non-nil slice")
		}

		// The mock must have recorded a RetrieveMemoryRecords call.
		input := mock.retrieveMemoryRecordsInput
		if input == nil {
			rt.Fatal("RetrieveMemoryRecords was not called")
		}

		// Req 5.3: MemoryId matches the store's memory ID.
		if aws.ToString(input.MemoryId) != memoryID {
			rt.Fatalf("MemoryId: got %q, want %q", aws.ToString(input.MemoryId), memoryID)
		}

		// Req 5.2: Namespace matches the rendered namespace (default: /facts/{identifier}/).
		expectedNS := "/facts/" + identifier + "/"
		if aws.ToString(input.Namespace) != expectedNS {
			rt.Fatalf("Namespace: got %q, want %q", aws.ToString(input.Namespace), expectedNS)
		}

		// Req 5.4: MaxResults equals the limit.
		if aws.ToInt32(input.MaxResults) != int32(limit) {
			rt.Fatalf("MaxResults: got %d, want %d", aws.ToInt32(input.MaxResults), int32(limit))
		}

		// Req 5.1: SearchCriteria.SearchQuery equals the query.
		if input.SearchCriteria == nil {
			rt.Fatal("SearchCriteria: expected non-nil")
		}
		if aws.ToString(input.SearchCriteria.SearchQuery) != query {
			rt.Fatalf("SearchQuery: got %q, want %q", aws.ToString(input.SearchCriteria.SearchQuery), query)
		}
	})
}

// TestProperty_RecallResponseMapping verifies that Recall correctly maps each
// MemoryRecordSummary returned by the AgentCore API to a memory.Entry,
// preserving all fields and the order provided by the backend.
//
// **Validates: Requirements 5.5, 5.6, 5.7, 5.8, 5.9**
func TestProperty_RecallResponseMapping(t *testing.T) {
	t.Helper()

	rapid.Check(t, func(rt *rapid.T) {
		// Generate 1–10 random MemoryRecordSummary values.
		n := rapid.IntRange(1, 10).Draw(rt, "num_summaries")
		summaries := make([]types.MemoryRecordSummary, n)
		for i := range n {
			summaries[i] = genMemoryRecordSummary(rt, fmt.Sprintf("summary_%d", i))
		}

		mock := &mockAgentCoreAPI{
			retrieveMemoryRecordsOutput: &bedrockagentcore.RetrieveMemoryRecordsOutput{
				MemoryRecordSummaries: summaries,
			},
		}

		memoryID := rapid.StringMatching(`[a-zA-Z0-9]{5,20}`).Draw(rt, "memoryID")
		identifier := genIdentifier(rt)
		query := genFact(rt)

		store, err := New(mock, memoryID)
		if err != nil {
			rt.Fatalf("New() returned unexpected error: %v", err)
		}

		entries, err := store.Recall(context.Background(), identifier, query, n)
		if err != nil {
			rt.Fatalf("Recall() returned unexpected error: %v", err)
		}

		// Req 5.5: The number of returned entries matches the number of summaries.
		if len(entries) != n {
			rt.Fatalf("entries length: got %d, want %d", len(entries), n)
		}

		// Verify each entry maps correctly to its corresponding summary,
		// preserving the order returned by AgentCore (Req 5.9).
		for i, entry := range entries {
			summary := summaries[i]

			// Req 5.5: Fact equals the summary content text.
			textContent, ok := summary.Content.(*types.MemoryContentMemberText)
			if !ok {
				rt.Fatalf("summary[%d] content type: got %T, want *types.MemoryContentMemberText", i, summary.Content)
			}
			if entry.Fact != textContent.Value {
				rt.Fatalf("entries[%d].Fact: got %q, want %q", i, entry.Fact, textContent.Value)
			}

			// Req 5.6: Score equals the summary score.
			if entry.Score != aws.ToFloat64(summary.Score) {
				rt.Fatalf("entries[%d].Score: got %f, want %f", i, entry.Score, aws.ToFloat64(summary.Score))
			}

			// Req 5.7: CreatedAt equals the summary timestamp.
			expectedTime := aws.ToTime(summary.CreatedAt)
			if !entry.CreatedAt.Equal(expectedTime) {
				rt.Fatalf("entries[%d].CreatedAt: got %v, want %v", i, entry.CreatedAt, expectedTime)
			}

			// Req 5.8: Metadata matches the summary metadata (MetadataValue → map[string]string).
			expectedMD := convertMetadataForTest(summary.Metadata)
			if len(entry.Metadata) != len(expectedMD) {
				rt.Fatalf("entries[%d].Metadata length: got %d, want %d", i, len(entry.Metadata), len(expectedMD))
			}
			for k, v := range expectedMD {
				got, exists := entry.Metadata[k]
				if !exists {
					rt.Fatalf("entries[%d].Metadata key %q: not found", i, k)
				}
				if got != v {
					rt.Fatalf("entries[%d].Metadata key %q: got %q, want %q", i, k, got, v)
				}
			}
		}
	})
}

// convertMetadataForTest converts AgentCore MetadataValue map to a plain string
// map, mirroring the production convertMetadata function for test assertions.
func convertMetadataForTest(md map[string]types.MetadataValue) map[string]string {
	if len(md) == 0 {
		return nil
	}
	result := make(map[string]string, len(md))
	for k, v := range md {
		if sv, ok := v.(*types.MetadataValueMemberStringValue); ok {
			result[k] = sv.Value
		}
	}
	return result
}

// TestProperty_ErrorWrapping verifies that API errors returned by the
// underlying AgentCore client are wrapped with the correct "agentcore memory:"
// prefix, contain the operation-specific sub-prefix, and remain unwrappable
// via errors.Is.
//
// **Validates: Requirements 3.6, 4.6, 5.13, 7.1, 7.2, 7.3**
func TestProperty_ErrorWrapping(t *testing.T) {
	t.Helper()

	// Sub-test 1: CreateEvent error path.
	t.Run("CreateEvent", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			origErr := genError(rt)

			mock := &mockAgentCoreAPI{
				createEventErr: origErr,
			}

			memoryID := genIdentifier(rt)
			store, err := New(mock, memoryID, WithStoreMode(CreateEventMode))
			if err != nil {
				rt.Fatalf("New() returned unexpected error: %v", err)
			}

			identifier := genIdentifier(rt)
			fact := genFact(rt)

			err = store.Remember(context.Background(), identifier, fact, nil)
			if err == nil {
				rt.Fatal("Remember() returned nil error, expected wrapped error")
			}

			errMsg := err.Error()

			// Req 7.1: Error starts with "agentcore memory:".
			if !strings.HasPrefix(errMsg, "agentcore memory:") {
				rt.Fatalf("error prefix: got %q, want prefix \"agentcore memory:\"", errMsg)
			}

			// Req 7.3: Error contains operation sub-prefix "create event:".
			if !strings.Contains(errMsg, "create event:") {
				rt.Fatalf("error sub-prefix: got %q, want to contain \"create event:\"", errMsg)
			}

			// Req 7.2: Original error is unwrappable via errors.Is.
			if !errors.Is(err, origErr) {
				rt.Fatalf("errors.Is: wrapped error does not match original; got %v", err)
			}
		})
	})

	// Sub-test 2: BatchCreateMemoryRecords error path.
	t.Run("BatchCreateMemoryRecords", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			origErr := genError(rt)

			mock := &mockAgentCoreAPI{
				batchCreateMemoryRecErr: origErr,
			}

			memoryID := genIdentifier(rt)
			store, err := New(mock, memoryID, WithStoreMode(BatchCreateMode))
			if err != nil {
				rt.Fatalf("New() returned unexpected error: %v", err)
			}

			identifier := genIdentifier(rt)
			fact := genFact(rt)

			err = store.Remember(context.Background(), identifier, fact, nil)
			if err == nil {
				rt.Fatal("Remember() returned nil error, expected wrapped error")
			}

			errMsg := err.Error()

			// Req 7.1: Error starts with "agentcore memory:".
			if !strings.HasPrefix(errMsg, "agentcore memory:") {
				rt.Fatalf("error prefix: got %q, want prefix \"agentcore memory:\"", errMsg)
			}

			// Req 7.3: Error contains operation sub-prefix "batch create:".
			if !strings.Contains(errMsg, "batch create:") {
				rt.Fatalf("error sub-prefix: got %q, want to contain \"batch create:\"", errMsg)
			}

			// Req 7.2: Original error is unwrappable via errors.Is.
			if !errors.Is(err, origErr) {
				rt.Fatalf("errors.Is: wrapped error does not match original; got %v", err)
			}
		})
	})

	// Sub-test 3: RetrieveMemoryRecords error path.
	t.Run("RetrieveMemoryRecords", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			origErr := genError(rt)

			mock := &mockAgentCoreAPI{
				retrieveMemoryRecordsErr: origErr,
			}

			memoryID := genIdentifier(rt)
			store, err := New(mock, memoryID)
			if err != nil {
				rt.Fatalf("New() returned unexpected error: %v", err)
			}

			identifier := genIdentifier(rt)
			query := genFact(rt)
			limit := rapid.IntRange(1, 1000).Draw(rt, "limit")

			_, err = store.Recall(context.Background(), identifier, query, limit)
			if err == nil {
				rt.Fatal("Recall() returned nil error, expected wrapped error")
			}

			errMsg := err.Error()

			// Req 7.1: Error starts with "agentcore memory:".
			if !strings.HasPrefix(errMsg, "agentcore memory:") {
				rt.Fatalf("error prefix: got %q, want prefix \"agentcore memory:\"", errMsg)
			}

			// Req 7.3: Error contains operation sub-prefix "retrieve:".
			if !strings.Contains(errMsg, "retrieve:") {
				rt.Fatalf("error sub-prefix: got %q, want to contain \"retrieve:\"", errMsg)
			}

			// Req 7.2: Original error is unwrappable via errors.Is.
			if !errors.Is(err, origErr) {
				rt.Fatalf("errors.Is: wrapped error does not match original; got %v", err)
			}
		})
	})
}

// TestProperty_NamespaceTemplateRendering verifies that the namespace template
// renders correctly for any actor ID. With the default template the result
// equals "/facts/{actorID}/", and with a custom template the actor ID is
// interpolated into the correct position.
//
// **Validates: Requirements 2.6, 6.1, 6.4**
func TestProperty_NamespaceTemplateRendering(t *testing.T) {
	t.Helper()

	// Sub-test 1: Default template renders to /facts/{actorID}/.
	t.Run("DefaultTemplate", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			actorID := genIdentifier(rt)

			mock := &mockAgentCoreAPI{
				batchCreateMemoryRecOutput: &bedrockagentcore.BatchCreateMemoryRecordsOutput{},
			}

			memoryID := genIdentifier(rt)

			// Create store with default options (default namespace template).
			store, err := New(mock, memoryID)
			if err != nil {
				rt.Fatalf("New() returned unexpected error: %v", err)
			}

			got, err := store.renderNamespace(actorID)
			if err != nil {
				rt.Fatalf("renderNamespace() returned unexpected error: %v", err)
			}

			want := "/facts/" + actorID + "/"
			if got != want {
				rt.Fatalf("renderNamespace(%q): got %q, want %q", actorID, got, want)
			}

			// The result must contain the actor ID.
			if !strings.Contains(got, actorID) {
				rt.Fatalf("renderNamespace(%q): result %q does not contain actor ID", actorID, got)
			}
		})
	})

	// Sub-test 2: Custom template renders correctly.
	t.Run("CustomTemplate", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			actorID := genIdentifier(rt)

			mock := &mockAgentCoreAPI{
				batchCreateMemoryRecOutput: &bedrockagentcore.BatchCreateMemoryRecordsOutput{},
			}

			memoryID := genIdentifier(rt)

			// Create store with a custom namespace template.
			store, err := New(mock, memoryID,
				WithNamespaceTemplate("/custom/{{.ActorID}}/data/"),
			)
			if err != nil {
				rt.Fatalf("New() returned unexpected error: %v", err)
			}

			got, err := store.renderNamespace(actorID)
			if err != nil {
				rt.Fatalf("renderNamespace() returned unexpected error: %v", err)
			}

			want := "/custom/" + actorID + "/data/"
			if got != want {
				rt.Fatalf("renderNamespace(%q): got %q, want %q", actorID, got, want)
			}

			// The result must contain the actor ID.
			if !strings.Contains(got, actorID) {
				rt.Fatalf("renderNamespace(%q): result %q does not contain actor ID", actorID, got)
			}
		})
	})
}

// ---------------------------------------------------------------------------
// Unit Tests — Constructor Validation and Edge Cases
// ---------------------------------------------------------------------------

// TestNew_NilClient verifies that New returns an error when the client is nil.
//
// Validates: Requirement 2.3
func TestNew_NilClient(t *testing.T) {
	_, err := New(nil, "some-memory-id")
	if err == nil {
		t.Fatal("New(nil, ...) returned nil error, expected error")
	}
	if !strings.Contains(err.Error(), "client is required") {
		t.Fatalf("error message %q does not contain %q", err.Error(), "client is required")
	}
}

// TestNew_EmptyMemoryID verifies that New returns an error when the memory ID
// is empty.
//
// Validates: Requirement 2.4
func TestNew_EmptyMemoryID(t *testing.T) {
	mock := &mockAgentCoreAPI{}
	_, err := New(mock, "")
	if err == nil {
		t.Fatal("New(mock, \"\") returned nil error, expected error")
	}
	if !strings.Contains(err.Error(), "memory ID is required") {
		t.Fatalf("error message %q does not contain %q", err.Error(), "memory ID is required")
	}
}

// TestNew_InvalidTemplate verifies that New returns an error when the namespace
// template is invalid.
//
// Validates: Requirement 6.2
func TestNew_InvalidTemplate(t *testing.T) {
	mock := &mockAgentCoreAPI{}
	_, err := New(mock, "some-id", WithNamespaceTemplate("{{.Invalid"))
	if err == nil {
		t.Fatal("New with invalid template returned nil error, expected error")
	}
	if !strings.Contains(err.Error(), "parse namespace template") {
		t.Fatalf("error message %q does not contain %q", err.Error(), "parse namespace template")
	}
}

// TestNew_Defaults verifies that New succeeds with valid inputs and applies
// default configuration: CreateEventMode and the default namespace template.
//
// Validates: Requirements 2.6, 2.7, 2.8
func TestNew_Defaults(t *testing.T) {
	mock := &mockAgentCoreAPI{}
	store, err := New(mock, "test-memory-id")
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}

	// Default mode should be CreateEventMode.
	if store.mode != CreateEventMode {
		t.Fatalf("default mode: got %d, want %d (CreateEventMode)", store.mode, CreateEventMode)
	}

	// Default namespace template should render to /facts/{actorID}/.
	ns, err := store.renderNamespace("actor42")
	if err != nil {
		t.Fatalf("renderNamespace() returned unexpected error: %v", err)
	}
	if ns != "/facts/actor42/" {
		t.Fatalf("default namespace: got %q, want %q", ns, "/facts/actor42/")
	}
}

// TestRemember_EmptyIdentifier verifies that Remember returns an error when
// the identifier is empty.
//
// Validates: Requirement 3.7
func TestRemember_EmptyIdentifier(t *testing.T) {
	mock := &mockAgentCoreAPI{}
	store, err := New(mock, "mem-id")
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}

	err = store.Remember(context.Background(), "", "some fact", nil)
	if err == nil {
		t.Fatal("Remember with empty identifier returned nil error, expected error")
	}
	if !strings.Contains(err.Error(), "identifier must not be empty") {
		t.Fatalf("error message %q does not contain %q", err.Error(), "identifier must not be empty")
	}
}

// TestRemember_EmptyFact verifies that Remember returns an error when the fact
// is empty.
//
// Validates: Requirement 3.8
func TestRemember_EmptyFact(t *testing.T) {
	mock := &mockAgentCoreAPI{}
	store, err := New(mock, "mem-id")
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}

	err = store.Remember(context.Background(), "user-1", "", nil)
	if err == nil {
		t.Fatal("Remember with empty fact returned nil error, expected error")
	}
	if !strings.Contains(err.Error(), "fact must not be empty") {
		t.Fatalf("error message %q does not contain %q", err.Error(), "fact must not be empty")
	}
}

// TestRecall_EmptyIdentifier verifies that Recall returns an error when the
// identifier is empty.
//
// Validates: Requirement 5.10
func TestRecall_EmptyIdentifier(t *testing.T) {
	mock := &mockAgentCoreAPI{}
	store, err := New(mock, "mem-id")
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}

	_, err = store.Recall(context.Background(), "", "query", 5)
	if err == nil {
		t.Fatal("Recall with empty identifier returned nil error, expected error")
	}
	if !strings.Contains(err.Error(), "identifier must not be empty") {
		t.Fatalf("error message %q does not contain %q", err.Error(), "identifier must not be empty")
	}
}

// TestRecall_InvalidLimit verifies that Recall returns an error when the limit
// is less than 1 (testing both 0 and -1).
//
// Validates: Requirement 5.11
func TestRecall_InvalidLimit(t *testing.T) {
	mock := &mockAgentCoreAPI{}
	store, err := New(mock, "mem-id")
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}

	for _, limit := range []int{0, -1} {
		_, err = store.Recall(context.Background(), "user-1", "query", limit)
		if err == nil {
			t.Fatalf("Recall with limit=%d returned nil error, expected error", limit)
		}
		if !strings.Contains(err.Error(), "limit must be at least 1") {
			t.Fatalf("limit=%d: error message %q does not contain %q", limit, err.Error(), "limit must be at least 1")
		}
	}
}

// TestRecall_EmptyResults verifies that Recall returns an empty non-nil slice
// when the API returns no records.
//
// Validates: Requirement 5.12
func TestRecall_EmptyResults(t *testing.T) {
	mock := &mockAgentCoreAPI{
		retrieveMemoryRecordsOutput: &bedrockagentcore.RetrieveMemoryRecordsOutput{
			MemoryRecordSummaries: []types.MemoryRecordSummary{},
		},
	}
	store, err := New(mock, "mem-id")
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}

	entries, err := store.Recall(context.Background(), "user-1", "query", 5)
	if err != nil {
		t.Fatalf("Recall() returned unexpected error: %v", err)
	}
	if entries == nil {
		t.Fatal("Recall() returned nil slice, expected empty non-nil slice")
	}
	if len(entries) != 0 {
		t.Fatalf("Recall() returned %d entries, expected 0", len(entries))
	}
}

// TestRemember_CreateEventMode_IncludesMemoryID verifies that the memoryID is
// included in the CreateEvent request.
//
// Validates: Requirement 3.5
func TestRemember_CreateEventMode_IncludesMemoryID(t *testing.T) {
	mock := &mockAgentCoreAPI{
		createEventOutput: &bedrockagentcore.CreateEventOutput{},
	}
	store, err := New(mock, "my-memory-123", WithStoreMode(CreateEventMode))
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}

	err = store.Remember(context.Background(), "user-1", "likes coffee", nil)
	if err != nil {
		t.Fatalf("Remember() returned unexpected error: %v", err)
	}

	if mock.createEventInput == nil {
		t.Fatal("CreateEvent was not called")
	}
	if aws.ToString(mock.createEventInput.MemoryId) != "my-memory-123" {
		t.Fatalf("MemoryId: got %q, want %q", aws.ToString(mock.createEventInput.MemoryId), "my-memory-123")
	}
}

// TestRemember_CreateEventMode_UsesSessionIDFunc verifies that a custom
// sessionIDFunc is called and its value appears in the CreateEvent request.
//
// Validates: Requirement 3.3
func TestRemember_CreateEventMode_UsesSessionIDFunc(t *testing.T) {
	mock := &mockAgentCoreAPI{
		createEventOutput: &bedrockagentcore.CreateEventOutput{},
	}

	called := false
	customSessionID := "custom-session-abc"
	store, err := New(mock, "mem-id",
		WithStoreMode(CreateEventMode),
		WithSessionIDFunc(func() string {
			called = true
			return customSessionID
		}),
	)
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}

	err = store.Remember(context.Background(), "user-1", "some fact", nil)
	if err != nil {
		t.Fatalf("Remember() returned unexpected error: %v", err)
	}

	if !called {
		t.Fatal("custom sessionIDFunc was not called")
	}
	if mock.createEventInput == nil {
		t.Fatal("CreateEvent was not called")
	}
	if aws.ToString(mock.createEventInput.SessionId) != customSessionID {
		t.Fatalf("SessionId: got %q, want %q", aws.ToString(mock.createEventInput.SessionId), customSessionID)
	}
}

// TestRemember_BatchCreateMode_IncludesMemoryID verifies that the memoryID is
// included in the BatchCreateMemoryRecords request.
//
// Validates: Requirement 4.5
func TestRemember_BatchCreateMode_IncludesMemoryID(t *testing.T) {
	mock := &mockAgentCoreAPI{
		batchCreateMemoryRecOutput: &bedrockagentcore.BatchCreateMemoryRecordsOutput{},
	}
	store, err := New(mock, "batch-mem-456", WithStoreMode(BatchCreateMode))
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}

	err = store.Remember(context.Background(), "user-1", "likes tea", nil)
	if err != nil {
		t.Fatalf("Remember() returned unexpected error: %v", err)
	}

	if mock.batchCreateMemoryRecInput == nil {
		t.Fatal("BatchCreateMemoryRecords was not called")
	}
	if aws.ToString(mock.batchCreateMemoryRecInput.MemoryId) != "batch-mem-456" {
		t.Fatalf("MemoryId: got %q, want %q", aws.ToString(mock.batchCreateMemoryRecInput.MemoryId), "batch-mem-456")
	}
}

// TestRecall_IncludesMemoryID verifies that the memoryID is included in the
// RetrieveMemoryRecords request.
//
// Validates: Requirement 5.3
func TestRecall_IncludesMemoryID(t *testing.T) {
	mock := &mockAgentCoreAPI{
		retrieveMemoryRecordsOutput: &bedrockagentcore.RetrieveMemoryRecordsOutput{
			MemoryRecordSummaries: []types.MemoryRecordSummary{},
		},
	}
	store, err := New(mock, "recall-mem-789")
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}

	_, err = store.Recall(context.Background(), "user-1", "query", 5)
	if err != nil {
		t.Fatalf("Recall() returned unexpected error: %v", err)
	}

	if mock.retrieveMemoryRecordsInput == nil {
		t.Fatal("RetrieveMemoryRecords was not called")
	}
	if aws.ToString(mock.retrieveMemoryRecordsInput.MemoryId) != "recall-mem-789" {
		t.Fatalf("MemoryId: got %q, want %q", aws.ToString(mock.retrieveMemoryRecordsInput.MemoryId), "recall-mem-789")
	}
}

// TestStoreMode_CreateEventDefault verifies that the default store mode calls
// CreateEvent (not BatchCreateMemoryRecords).
//
// Validates: Requirement 2.8
func TestStoreMode_CreateEventDefault(t *testing.T) {
	mock := &mockAgentCoreAPI{
		createEventOutput: &bedrockagentcore.CreateEventOutput{},
	}
	// No WithStoreMode option — should default to CreateEventMode.
	store, err := New(mock, "mem-id")
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}

	err = store.Remember(context.Background(), "user-1", "a fact", nil)
	if err != nil {
		t.Fatalf("Remember() returned unexpected error: %v", err)
	}

	if mock.createEventInput == nil {
		t.Fatal("CreateEvent was not called (expected for default mode)")
	}
	if mock.batchCreateMemoryRecInput != nil {
		t.Fatal("BatchCreateMemoryRecords was called unexpectedly (default mode should use CreateEvent)")
	}
}

// TestStoreMode_BatchCreate verifies that WithStoreMode(BatchCreateMode) causes
// Remember to call BatchCreateMemoryRecords (not CreateEvent).
//
// Validates: Requirement 2.8
func TestStoreMode_BatchCreate(t *testing.T) {
	mock := &mockAgentCoreAPI{
		batchCreateMemoryRecOutput: &bedrockagentcore.BatchCreateMemoryRecordsOutput{},
	}
	store, err := New(mock, "mem-id", WithStoreMode(BatchCreateMode))
	if err != nil {
		t.Fatalf("New() returned unexpected error: %v", err)
	}

	err = store.Remember(context.Background(), "user-1", "a fact", nil)
	if err != nil {
		t.Fatalf("Remember() returned unexpected error: %v", err)
	}

	if mock.batchCreateMemoryRecInput == nil {
		t.Fatal("BatchCreateMemoryRecords was not called (expected for BatchCreateMode)")
	}
	if mock.createEventInput != nil {
		t.Fatal("CreateEvent was called unexpectedly (BatchCreateMode should use BatchCreateMemoryRecords)")
	}
}
