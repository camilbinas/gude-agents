package dynamodb

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// genContentBlock generates a random ContentBlock: TextBlock, ToolUseBlock, or ToolResultBlock.
func genContentBlock(t *rapid.T) agent.ContentBlock {
	kind := rapid.IntRange(0, 2).Draw(t, "blockKind")
	switch kind {
	case 0:
		return agent.TextBlock{
			Text: rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, "text"),
		}
	case 1:
		jsonOptions := []json.RawMessage{
			json.RawMessage(`{}`),
			json.RawMessage(`{"key":"value"}`),
			json.RawMessage(`{"a":1,"b":true}`),
		}
		return agent.ToolUseBlock{
			ToolUseID: rapid.StringMatching(`tu_[a-zA-Z0-9]{4,12}`).Draw(t, "toolUseID"),
			Name:      rapid.StringMatching(`[a-z_]{1,20}`).Draw(t, "toolName"),
			Input:     rapid.SampledFrom(jsonOptions).Draw(t, "input"),
		}
	default:
		return agent.ToolResultBlock{
			ToolUseID: rapid.StringMatching(`tu_[a-zA-Z0-9]{4,12}`).Draw(t, "toolResultID"),
			Content:   rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, "resultContent"),
			IsError:   rapid.Bool().Draw(t, "isError"),
		}
	}
}

// genMessages generates a random slice of 0–10 agent.Message, each with 1–5 ContentBlocks.
func genMessages(t *rapid.T) []agent.Message {
	numMsgs := rapid.IntRange(0, 10).Draw(t, "numMessages")
	msgs := make([]agent.Message, numMsgs)
	roles := []agent.Role{agent.RoleUser, agent.RoleAssistant}
	for i := range msgs {
		numBlocks := rapid.IntRange(1, 5).Draw(t, fmt.Sprintf("numBlocks_%d", i))
		blocks := make([]agent.ContentBlock, numBlocks)
		for j := range blocks {
			blocks[j] = genContentBlock(t)
		}
		msgs[i] = agent.Message{
			Role:    rapid.SampledFrom(roles).Draw(t, fmt.Sprintf("role_%d", i)),
			Content: blocks,
		}
	}
	return msgs
}

// newTestMemory creates a DynamoDBMemory backed by the given mock with default test settings.
func newTestMemory(mock *mockDynamoDBClient, keyPrefix string) *DynamoDBMemory {
	return &DynamoDBMemory{
		client:       mock,
		table:        "test-table",
		keyPrefix:    keyPrefix,
		ttlAttribute: "ttl",
		pkAttribute:  "conversation_id",
	}
}

// Feature: memory-drivers, Property 7: DynamoDB Save/Load round-trip
// **Validates: Requirements 9.1, 10.1, 10.4**
func TestProperty_DynamoDBSaveLoadRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mock := newMockDynamoDBClient()
		m := newTestMemory(mock, "gude:")

		messages := genMessages(t)
		convID := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, "conversationID")

		ctx := context.Background()

		if err := m.Save(ctx, convID, messages); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		loaded, err := m.Load(ctx, convID)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		if !reflect.DeepEqual(messages, loaded) {
			t.Fatalf("round-trip mismatch:\n  saved:  %+v\n  loaded: %+v", messages, loaded)
		}
	})
}

// Feature: memory-drivers, Property 8: DynamoDB key formation
// **Validates: Requirements 8.2, 8.3**
func TestProperty_DynamoDBKeyFormation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mock := newMockDynamoDBClient()

		prefix := rapid.StringMatching(`[a-zA-Z0-9:_-]{1,20}`).Draw(t, "prefix")
		convID := rapid.StringMatching(`[a-zA-Z0-9]{4,16}`).Draw(t, "convID")

		m := newTestMemory(mock, prefix)

		ctx := context.Background()

		messages := []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		}

		if err := m.Save(ctx, convID, messages); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		expectedPK := prefix + convID
		if _, ok := mock.items[expectedPK]; !ok {
			t.Fatalf("expected item with pk %q, but found keys: %v", expectedPK, pkKeysOf(mock.items))
		}
	})
}

// Feature: memory-drivers, Property 9: DynamoDB overwrite
// **Validates: Requirement 9.4**
func TestProperty_DynamoDBOverwrite(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mock := newMockDynamoDBClient()
		m := newTestMemory(mock, "gude:")

		convID := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, "conversationID")
		messagesA := genMessages(t)
		messagesB := genMessages(t)

		ctx := context.Background()

		if err := m.Save(ctx, convID, messagesA); err != nil {
			t.Fatalf("Save(A) failed: %v", err)
		}
		if err := m.Save(ctx, convID, messagesB); err != nil {
			t.Fatalf("Save(B) failed: %v", err)
		}

		loaded, err := m.Load(ctx, convID)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		if !reflect.DeepEqual(messagesB, loaded) {
			t.Fatalf("overwrite mismatch:\n  expected B: %+v\n  got:        %+v", messagesB, loaded)
		}
	})
}

// Feature: memory-drivers, Property 10: DynamoDB List completeness
// **Validates: Requirement 11.1**
func TestProperty_DynamoDBListCompleteness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mock := newMockDynamoDBClient()

		// Use a unique isolated prefix per iteration to avoid cross-contamination.
		prefix := rapid.StringMatching(`pbt-[a-zA-Z0-9]{6,12}:`).Draw(t, "prefix")
		m := newTestMemory(mock, prefix)

		// Generate 1–5 distinct conversation IDs.
		n := rapid.IntRange(1, 5).Draw(t, "numConvs")
		ids := make([]string, n)
		seen := make(map[string]bool)
		for i := range ids {
			var id string
			for {
				id = rapid.StringMatching(`[a-zA-Z0-9]{4,12}`).Draw(t, fmt.Sprintf("convID_%d", i))
				if !seen[id] {
					break
				}
			}
			seen[id] = true
			ids[i] = id
		}

		ctx := context.Background()
		msgs := []agent.Message{
			{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
		}

		for _, id := range ids {
			if err := m.Save(ctx, id, msgs); err != nil {
				t.Fatalf("Save(%q) failed: %v", id, err)
			}
		}

		listed, err := m.List(ctx)
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}

		if len(listed) != len(ids) {
			t.Fatalf("List returned %d IDs, expected %d: listed=%v, saved=%v", len(listed), len(ids), listed, ids)
		}

		sort.Strings(listed)
		sort.Strings(ids)

		if !reflect.DeepEqual(ids, listed) {
			t.Fatalf("List mismatch:\n  expected: %v\n  got:      %v", ids, listed)
		}
	})
}

// Feature: memory-drivers, Property 11: DynamoDB Delete then Load returns empty
// **Validates: Requirement 11.3**
func TestProperty_DynamoDBDeleteThenLoad(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mock := newMockDynamoDBClient()
		m := newTestMemory(mock, "gude:")

		convID := rapid.StringMatching(`conv-[a-zA-Z0-9]{4,16}`).Draw(t, "conversationID")
		messages := genMessages(t)

		ctx := context.Background()

		if err := m.Save(ctx, convID, messages); err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		if err := m.Delete(ctx, convID); err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		loaded, err := m.Load(ctx, convID)
		if err != nil {
			t.Fatalf("Load after Delete failed: %v", err)
		}

		if loaded == nil {
			t.Fatal("expected non-nil empty slice after Delete, got nil")
		}
		if len(loaded) != 0 {
			t.Fatalf("expected empty slice after Delete, got %d messages", len(loaded))
		}
	})
}

// pkKeysOf returns the keys of the items map as a slice (for diagnostic messages).
func pkKeysOf(m map[string]map[string]dbtypes.AttributeValue) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
