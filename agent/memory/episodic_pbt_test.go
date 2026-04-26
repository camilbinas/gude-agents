package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Generators
// ---------------------------------------------------------------------------

// genNonEmptyString generates a random non-empty alphanumeric string (1–100 chars).
func genNonEmptyString(t *rapid.T, name string) string {
	return rapid.StringMatching(`[a-zA-Z0-9 ]{1,100}`).Draw(t, name)
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
			key := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "metadata_key")
			val := rapid.StringMatching(`[a-zA-Z0-9]{1,20}`).Draw(t, "metadata_val")
			_ = i
			m[key] = val
		}
		return m
	}
	return nil
}

// genTimestamp generates a random time.Time truncated to millisecond precision.
// JSON round-trips through RFC 3339 with nanosecond support, but we truncate to
// millisecond to match the design document's generator strategy.
func genTimestamp(t *rapid.T) time.Time {
	sec := rapid.Int64Range(0, 4102444800).Draw(t, "timestamp_sec") // 0 to ~2100
	return time.Unix(sec, 0).UTC().Truncate(time.Millisecond)
}

// genScore generates a random float64 in [0, 1].
func genScore(t *rapid.T) float64 {
	return rapid.Float64Range(0, 1).Draw(t, "score")
}

// genEntry generates a random Entry with non-empty fact, random/nil metadata,
// millisecond-truncated timestamp, and score in [0, 1].
func genEntry(t *rapid.T) Entry {
	return Entry{
		Fact:      genNonEmptyString(t, "fact"),
		Metadata:  genMetadata(t),
		CreatedAt: genTimestamp(t),
		Score:     genScore(t),
	}
}

// ---------------------------------------------------------------------------
// Property 1: Entry JSON serialization round-trip
// ---------------------------------------------------------------------------

// Feature: episodic-memory, Property 1: Entry JSON serialization round-trip
//
// TestProperty_EntryJSONRoundTrip verifies that for any valid Entry value
// (with arbitrary fact strings, metadata maps including nil, timestamps, and
// scores), serializing to JSON via json.Marshal and deserializing back via
// json.Unmarshal produces an Entry that is deeply equal to the original.
//
// **Validates: Requirements 7.1, 7.2, 7.3**
func TestProperty_EntryJSONRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		original := genEntry(rt)

		// Marshal to JSON.
		data, err := json.Marshal(original)
		if err != nil {
			rt.Fatalf("json.Marshal failed: %v", err)
		}

		// Unmarshal back.
		var restored Entry
		if err := json.Unmarshal(data, &restored); err != nil {
			rt.Fatalf("json.Unmarshal failed: %v", err)
		}

		// Verify deep equality.
		if !reflect.DeepEqual(original, restored) {
			rt.Fatalf("round-trip mismatch:\noriginal: %+v\nrestored: %+v\njson:     %s", original, restored, string(data))
		}
	})
}

// ---------------------------------------------------------------------------
// Deterministic mock Embedder (hash-based vectors)
// ---------------------------------------------------------------------------

// hashEmbedder is a deterministic mock Embedder that produces consistent
// embedding vectors from strings using FNV hashing. The same input always
// yields the same vector, and different inputs produce different vectors,
// making cosine similarity results predictable in tests.
type hashEmbedder struct {
	dim int // embedding dimension
}

// Embed returns a deterministic float64 vector derived from the FNV-64a hash
// of the input text. Each dimension is seeded by hashing the text concatenated
// with the dimension index, then normalized to unit length.
func (e *hashEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	vec := make([]float64, e.dim)
	for i := range vec {
		h := fnv.New64a()
		h.Write([]byte(text))
		h.Write([]byte{byte(i), byte(i >> 8)})
		// Map hash to [-1, 1] range.
		vec[i] = float64(int64(h.Sum64())) / float64(math.MaxInt64)
	}
	return vec, nil
}

// ---------------------------------------------------------------------------
// Property 2: Remember-then-Recall round-trip
// ---------------------------------------------------------------------------

// Feature: episodic-memory, Property 2: Remember-then-Recall round-trip
//
// TestProperty_RememberThenRecall verifies that for any valid user ID and
// non-empty fact string, after calling Remember(ctx, userID, fact, metadata),
// calling Recall(ctx, userID, fact, 10) returns a non-empty slice containing
// an Entry whose Fact field equals the stored fact.
//
// **Validates: Requirements 2.3, 1.4**
func TestProperty_RememberThenRecall(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random but valid inputs.
		userID := genNonEmptyString(rt, "userID")
		fact := genNonEmptyString(rt, "fact")
		metadata := genMetadata(rt)

		// Create a fresh store with a deterministic embedder per iteration.
		store := NewStore(&hashEmbedder{dim: 16})
		ctx := context.Background()

		// Remember the fact.
		err := store.Remember(ctx, userID, fact, metadata)
		if err != nil {
			rt.Fatalf("Remember failed: %v", err)
		}

		// Recall using the same fact as the query.
		results, err := store.Recall(ctx, userID, fact, 10)
		if err != nil {
			rt.Fatalf("Recall failed: %v", err)
		}

		// Results must be non-empty.
		if len(results) == 0 {
			rt.Fatal("Recall returned empty slice after Remember")
		}

		// The stored fact must appear in the results.
		found := false
		for _, entry := range results {
			if entry.Fact == fact {
				found = true
				break
			}
		}
		if !found {
			rt.Fatalf("Recall results do not contain the stored fact %q; got %+v", fact, results)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 3: Recall results ordered by descending score
// ---------------------------------------------------------------------------

// Feature: episodic-memory, Property 3: Recall results ordered by descending score
//
// TestProperty_RecallOrderedByScore verifies that for any user ID with one or
// more stored entries and any query string, the slice returned by Recall has
// its elements ordered by descending Score value, and every returned Entry
// has a populated (non-zero) Score field.
//
// **Validates: Requirements 2.4, 2.5**
func TestProperty_RecallOrderedByScore(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		userID := genNonEmptyString(rt, "userID")
		query := genNonEmptyString(rt, "query")

		// Store between 2 and 20 distinct facts so ordering is meaningful.
		n := rapid.IntRange(2, 20).Draw(rt, "num_facts")
		store := NewStore(&hashEmbedder{dim: 16})
		ctx := context.Background()

		for i := range n {
			// Use index suffix to ensure distinct facts produce distinct embeddings.
			fact := genNonEmptyString(rt, "fact") + string(rune('A'+i))
			err := store.Remember(ctx, userID, fact, nil)
			if err != nil {
				rt.Fatalf("Remember[%d] failed: %v", i, err)
			}
		}

		// Recall all stored entries.
		results, err := store.Recall(ctx, userID, query, n)
		if err != nil {
			rt.Fatalf("Recall failed: %v", err)
		}

		if len(results) == 0 {
			rt.Fatal("Recall returned empty slice after storing entries")
		}

		// Verify descending score order and non-zero scores.
		for i, entry := range results {
			if entry.Score == 0 {
				rt.Fatalf("results[%d].Score is zero; all scores must be non-zero", i)
			}
			if i > 0 && results[i-1].Score < entry.Score {
				rt.Fatalf("results not sorted by descending score: results[%d].Score=%v < results[%d].Score=%v",
					i-1, results[i-1].Score, i, entry.Score)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 4: Recall returns at most limit results, clamped to stored count
// ---------------------------------------------------------------------------

// Feature: episodic-memory, Property 4: Recall returns at most limit results, clamped to stored count
//
// TestProperty_RecallLimitClamping verifies that for any user ID with N stored
// entries and any positive limit L, Recall returns exactly min(L, N) entries.
// When the user ID has no stored entries, Recall returns an empty non-nil slice.
//
// **Validates: Requirements 2.7, 1.8**
func TestProperty_RecallLimitClamping(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		userID := genNonEmptyString(rt, "userID")
		query := genNonEmptyString(rt, "query")

		// Store between 1 and 20 facts.
		n := rapid.IntRange(1, 20).Draw(rt, "num_facts")
		store := NewStore(&hashEmbedder{dim: 16})
		ctx := context.Background()

		for i := range n {
			fact := genNonEmptyString(rt, "fact") + string(rune('A'+i))
			err := store.Remember(ctx, userID, fact, nil)
			if err != nil {
				rt.Fatalf("Remember[%d] failed: %v", i, err)
			}
		}

		// Draw a limit that may be less than, equal to, or greater than n.
		limit := rapid.IntRange(1, 30).Draw(rt, "limit")

		results, err := store.Recall(ctx, userID, query, limit)
		if err != nil {
			rt.Fatalf("Recall failed: %v", err)
		}

		// Expected count is min(limit, n).
		expected := limit
		if n < expected {
			expected = n
		}
		if len(results) != expected {
			rt.Fatalf("len(results) = %d, want min(%d, %d) = %d", len(results), limit, n, expected)
		}

		// Verify unknown user returns empty non-nil slice.
		unknownUser := userID + "_unknown"
		unknownResults, err := store.Recall(ctx, unknownUser, query, 5)
		if err != nil {
			rt.Fatalf("Recall for unknown user failed: %v", err)
		}
		if unknownResults == nil {
			rt.Fatal("Recall for unknown user returned nil, want empty non-nil slice")
		}
		if len(unknownResults) != 0 {
			rt.Fatalf("Recall for unknown user returned %d results, want 0", len(unknownResults))
		}
	})
}

// ---------------------------------------------------------------------------
// Property 5: WithIdentifier/GetIdentifier context round-trip
// ---------------------------------------------------------------------------

// Feature: episodic-memory, Property 5: WithIdentifier/GetIdentifier context round-trip
//
// TestProperty_UserIDContextRoundTrip verifies that for any non-empty string
// value, calling WithIdentifier(ctx, value) followed by GetIdentifier(ctx)
// returns the original string value.
//
// **Validates: Requirements 5.1**
func TestProperty_UserIDContextRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		id := genNonEmptyString(rt, "identifier")

		ctx := context.Background()
		ctx = agent.WithIdentifier(ctx, id)

		got := agent.GetIdentifier(ctx)
		if got != id {
			rt.Fatalf("GetIdentifier round-trip failed: set %q, got %q", id, got)
		}
	})
}

// ---------------------------------------------------------------------------
// Mock Memory for tool tests
// ---------------------------------------------------------------------------

// spyMemory records the arguments passed to Remember and Recall,
// and returns configurable results/errors. Used by Properties 6–9.
type spyMemory struct {
	// Recorded arguments from the last call.
	rememberID   string
	rememberFact string
	rememberMeta map[string]string
	recallID     string
	recallQuery  string
	recallLimit  int

	// Configurable return values.
	recallResult []Entry
	rememberErr  error
	recallErr    error
}

func (s *spyMemory) Remember(_ context.Context, identifier, fact string, metadata map[string]string) error {
	s.rememberID = identifier
	s.rememberFact = fact
	s.rememberMeta = metadata
	return s.rememberErr
}

func (s *spyMemory) Recall(_ context.Context, identifier, query string, limit int) ([]Entry, error) {
	s.recallID = identifier
	s.recallQuery = query
	s.recallLimit = limit
	return s.recallResult, s.recallErr
}

// ---------------------------------------------------------------------------
// Property 6: Tools extract user ID from context
// ---------------------------------------------------------------------------

// Feature: episodic-memory, Property 6: Tools extract identifier from context
//
// TestProperty_ToolsExtractUserID verifies that for any non-empty identifier
// set on the context via WithIdentifier, both RememberTool and RecallTool pass
// that exact identifier to the underlying Memory method call.
//
// **Validates: Requirements 3.3, 4.3, 5.2**
func TestProperty_ToolsExtractUserID(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		id := genNonEmptyString(rt, "identifier")
		fact := genNonEmptyString(rt, "fact")
		query := genNonEmptyString(rt, "query")

		ctx := agent.WithIdentifier(context.Background(), id)

		// Test RememberTool extracts identifier.
		rememberSpy := &spyMemory{}
		rememberTool := RememberTool(rememberSpy)
		rememberInput, _ := json.Marshal(map[string]any{"fact": fact})
		_, err := rememberTool.Handler(ctx, json.RawMessage(rememberInput))
		if err != nil {
			rt.Fatalf("RememberTool handler returned error: %v", err)
		}
		if rememberSpy.rememberID != id {
			rt.Fatalf("RememberTool passed identifier %q to Remember, want %q", rememberSpy.rememberID, id)
		}

		// Test RecallTool extracts identifier.
		recallSpy := &spyMemory{recallResult: []Entry{}}
		recallTool := RecallTool(recallSpy)
		recallInput, _ := json.Marshal(map[string]any{"query": query})
		_, err = recallTool.Handler(ctx, json.RawMessage(recallInput))
		if err != nil {
			rt.Fatalf("RecallTool handler returned error: %v", err)
		}
		if recallSpy.recallID != id {
			rt.Fatalf("RecallTool passed identifier %q to Recall, want %q", recallSpy.recallID, id)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 7: RecallTool default limit
// ---------------------------------------------------------------------------

// Feature: episodic-memory, Property 7: RecallTool default limit
//
// TestProperty_RecallToolDefaultLimit verifies that when the JSON input omits
// the limit field, the RecallTool calls Recall with a limit of 5. When the
// JSON input includes a positive limit value, the tool uses that value.
//
// **Validates: Requirements 4.2**
func TestProperty_RecallToolDefaultLimit(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		userID := genNonEmptyString(rt, "userID")
		query := genNonEmptyString(rt, "query")
		ctx := agent.WithIdentifier(context.Background(), userID)

		// Sub-property A: omitted limit → Recall called with 5.
		spyA := &spyMemory{recallResult: []Entry{}}
		toolA := RecallTool(spyA)
		inputA, _ := json.Marshal(map[string]any{"query": query})
		_, err := toolA.Handler(ctx, json.RawMessage(inputA))
		if err != nil {
			rt.Fatalf("RecallTool (no limit) returned error: %v", err)
		}
		if spyA.recallLimit != 5 {
			rt.Fatalf("RecallTool with omitted limit called Recall with limit=%d, want 5", spyA.recallLimit)
		}

		// Sub-property B: explicit positive limit → Recall called with that value.
		explicitLimit := rapid.IntRange(1, 100).Draw(rt, "explicit_limit")
		spyB := &spyMemory{recallResult: []Entry{}}
		toolB := RecallTool(spyB)
		inputB, _ := json.Marshal(map[string]any{"query": query, "limit": explicitLimit})
		_, err = toolB.Handler(ctx, json.RawMessage(inputB))
		if err != nil {
			rt.Fatalf("RecallTool (explicit limit) returned error: %v", err)
		}
		if spyB.recallLimit != explicitLimit {
			rt.Fatalf("RecallTool with limit=%d called Recall with limit=%d", explicitLimit, spyB.recallLimit)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 8: RecallTool output contains all entry fields
// ---------------------------------------------------------------------------

// Feature: episodic-memory, Property 8: RecallTool output contains all entry fields
//
// TestProperty_RecallToolOutputContainsFields verifies that for any non-empty
// slice of Entries returned by Recall, the formatted string output of
// RecallTool contains each entry's fact text, each metadata key-value pair,
// the creation timestamp, and the similarity score.
//
// **Validates: Requirements 4.4**
func TestProperty_RecallToolOutputContainsFields(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		userID := genNonEmptyString(rt, "userID")
		query := genNonEmptyString(rt, "query")
		ctx := agent.WithIdentifier(context.Background(), userID)

		// Generate 1–5 entries with non-nil metadata to verify all fields appear.
		numEntries := rapid.IntRange(1, 5).Draw(rt, "num_entries")
		entries := make([]Entry, numEntries)
		for i := range numEntries {
			entry := genEntry(rt)
			// Ensure metadata is non-nil so we can verify key-value pairs.
			if entry.Metadata == nil {
				entry.Metadata = map[string]string{
					rapid.StringMatching(`[a-z]{1,10}`).Draw(rt, "mk"): rapid.StringMatching(`[a-zA-Z0-9]{1,20}`).Draw(rt, "mv"),
				}
			}
			entries[i] = entry
		}

		spy := &spyMemory{recallResult: entries}
		recallTool := RecallTool(spy)
		input, _ := json.Marshal(map[string]any{"query": query})
		output, err := recallTool.Handler(ctx, json.RawMessage(input))
		if err != nil {
			rt.Fatalf("RecallTool returned error: %v", err)
		}

		// Verify each entry's fields appear in the output.
		for i, entry := range entries {
			// Fact must appear.
			if !containsSubstring(output, entry.Fact) {
				rt.Fatalf("output missing fact for entry[%d]: %q\noutput: %s", i, entry.Fact, output)
			}

			// Each metadata key-value pair must appear.
			for k, v := range entry.Metadata {
				kv := fmt.Sprintf("%s=%s", k, v)
				if !containsSubstring(output, kv) {
					rt.Fatalf("output missing metadata %q for entry[%d]\noutput: %s", kv, i, output)
				}
			}

			// Timestamp must appear (RFC 3339 format).
			ts := entry.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
			if !containsSubstring(output, ts) {
				rt.Fatalf("output missing timestamp %q for entry[%d]\noutput: %s", ts, i, output)
			}

			// Score must appear (formatted to 4 decimal places).
			score := fmt.Sprintf("%.4f", entry.Score)
			if !containsSubstring(output, score) {
				rt.Fatalf("output missing score %q for entry[%d]\noutput: %s", score, i, output)
			}
		}
	})
}

// containsSubstring checks if s contains substr.
func containsSubstring(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Property 9: Tool error propagation
// ---------------------------------------------------------------------------

// Feature: episodic-memory, Property 9: Tool error propagation
//
// TestProperty_ToolErrorPropagation verifies that errors returned by the
// underlying Memory.Remember and Memory.Recall calls are
// returned as non-nil errors from the tool handlers.
//
// **Validates: Requirements 3.5, 4.6**
func TestProperty_ToolErrorPropagation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		userID := genNonEmptyString(rt, "userID")
		fact := genNonEmptyString(rt, "fact")
		query := genNonEmptyString(rt, "query")
		errMsg := genNonEmptyString(rt, "error_message")
		ctx := agent.WithIdentifier(context.Background(), userID)

		// Test RememberTool propagates errors.
		rememberSpy := &spyMemory{
			rememberErr: fmt.Errorf("%s", errMsg),
		}
		rememberTool := RememberTool(rememberSpy)
		rememberInput, _ := json.Marshal(map[string]any{"fact": fact})
		_, err := rememberTool.Handler(ctx, json.RawMessage(rememberInput))
		if err == nil {
			rt.Fatal("RememberTool should propagate error from Remember, got nil")
		}
		if !containsSubstring(err.Error(), errMsg) {
			rt.Fatalf("RememberTool error %q does not contain expected message %q", err.Error(), errMsg)
		}

		// Test RecallTool propagates errors.
		recallSpy := &spyMemory{
			recallErr: fmt.Errorf("%s", errMsg),
		}
		recallTool := RecallTool(recallSpy)
		recallInput, _ := json.Marshal(map[string]any{"query": query})
		_, err = recallTool.Handler(ctx, json.RawMessage(recallInput))
		if err == nil {
			rt.Fatal("RecallTool should propagate error from Recall, got nil")
		}
		if !containsSubstring(err.Error(), errMsg) {
			rt.Fatalf("RecallTool error %q does not contain expected message %q", err.Error(), errMsg)
		}
	})
}
