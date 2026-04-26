// Feature: unified-memory-providers, Property 3: NewRememberTool / NewRecallTool round-trip
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/rag"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Property 3: NewRememberTool / NewRecallTool round-trip
// ---------------------------------------------------------------------------

// TestProperty_NewToolsRoundTrip verifies that for any non-empty identifier
// set on the agent context and any non-empty fact string, after invoking
// NewRememberTool to store the fact, invoking NewRecallTool with the same
// fact as the query returns a non-empty result containing the original fact text.
//
// **Validates: Requirements 3.2, 3.4**
func TestProperty_NewToolsRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random inputs.
		identifier := genNonEmptyString(rt, "identifier")
		fact := genNonEmptyString(rt, "fact")
		metadata := genMetadata(rt)

		// Create a fresh MemoryStore + ScopedStore + hashEmbedder per iteration.
		memStore := rag.NewMemoryStore()
		scopedStore := rag.NewScopedStore(memStore)
		embedder := &hashEmbedder{dim: 16}

		// Build composable tools.
		rememberTool := NewRememberTool(scopedStore, embedder)
		recallTool := NewRecallTool(scopedStore, embedder)

		// Set identifier on context.
		ctx := agent.WithIdentifier(context.Background(), identifier)

		// Build RememberTool JSON input.
		rememberInput := map[string]any{"fact": fact}
		if metadata != nil {
			rememberInput["metadata"] = metadata
		}
		rememberJSON, err := json.Marshal(rememberInput)
		if err != nil {
			rt.Fatalf("marshal remember input: %v", err)
		}

		// Invoke RememberTool.
		_, err = rememberTool.Handler(ctx, json.RawMessage(rememberJSON))
		if err != nil {
			rt.Fatalf("NewRememberTool handler returned error: %v", err)
		}

		// Build RecallTool JSON input using the same fact as query.
		recallJSON, err := json.Marshal(map[string]any{"query": fact, "limit": 10})
		if err != nil {
			rt.Fatalf("marshal recall input: %v", err)
		}

		// Invoke RecallTool.
		output, err := recallTool.Handler(ctx, json.RawMessage(recallJSON))
		if err != nil {
			rt.Fatalf("NewRecallTool handler returned error: %v", err)
		}

		// Results must not be the empty-results sentinel.
		if output == "No relevant memories found." {
			rt.Fatal("NewRecallTool returned no results after NewRememberTool stored a fact")
		}

		// The stored fact must appear in the output.
		if !strings.Contains(output, fact) {
			rt.Fatalf("NewRecallTool output does not contain the stored fact %q;\noutput: %s", fact, output)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 5: RememberTool stores complete metadata
// ---------------------------------------------------------------------------

// Feature: unified-memory-providers, Property 5: RememberTool stores complete metadata
//
// TestProperty_RememberToolMetadata verifies that for any non-empty fact string
// and any non-nil user-provided metadata map, after invoking NewRememberTool to
// store the fact, the document stored in the underlying VectorStore has:
// Content equal to the fact text, metadata["_scope_id"] equal to the context
// identifier, a metadata["created_at"] value parseable as RFC 3339, and all
// user-provided metadata key-value pairs present in the document metadata.
//
// **Validates: Requirements 3.8**
func TestProperty_RememberToolMetadata(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random inputs.
		identifier := genNonEmptyString(rt, "identifier")
		fact := genNonEmptyString(rt, "fact")

		// Generate non-nil user metadata with 1–5 entries.
		numMeta := rapid.IntRange(1, 5).Draw(rt, "num_meta")
		userMeta := make(map[string]string, numMeta)
		for i := range numMeta {
			_ = i
			key := rapid.StringMatching(`[a-z]{1,10}`).Draw(rt, "meta_key")
			val := rapid.StringMatching(`[a-zA-Z0-9]{1,20}`).Draw(rt, "meta_val")
			userMeta[key] = val
		}

		// Create a fresh MemoryStore + ScopedStore + hashEmbedder per iteration.
		memStore := rag.NewMemoryStore()
		scopedStore := rag.NewScopedStore(memStore)
		embedder := &hashEmbedder{dim: 16}

		// Build the composable RememberTool.
		rememberTool := NewRememberTool(scopedStore, embedder)

		// Set identifier on context.
		ctx := agent.WithIdentifier(context.Background(), identifier)

		// Build RememberTool JSON input with fact and metadata.
		rememberInput := map[string]any{
			"fact":     fact,
			"metadata": userMeta,
		}
		rememberJSON, err := json.Marshal(rememberInput)
		if err != nil {
			rt.Fatalf("marshal remember input: %v", err)
		}

		// Invoke RememberTool.
		_, err = rememberTool.Handler(ctx, json.RawMessage(rememberJSON))
		if err != nil {
			rt.Fatalf("NewRememberTool handler returned error: %v", err)
		}

		// Search the MemoryStore directly (bypassing ScopedStore) to inspect
		// the raw stored document. Use the fact's embedding as the query.
		queryEmb, err := embedder.Embed(ctx, fact)
		if err != nil {
			rt.Fatalf("Embed query failed: %v", err)
		}
		results, err := memStore.Search(ctx, queryEmb, 10)
		if err != nil {
			rt.Fatalf("MemoryStore.Search failed: %v", err)
		}

		if len(results) == 0 {
			rt.Fatal("MemoryStore has no documents after RememberTool invocation")
		}

		// Find the document whose Content matches the fact.
		var found bool
		for _, sd := range results {
			if sd.Document.Content != fact {
				continue
			}
			found = true
			doc := sd.Document

			// Verify Content == fact.
			if doc.Content != fact {
				rt.Fatalf("stored document Content = %q, want %q", doc.Content, fact)
			}

			// Verify metadata["_scope_id"] == identifier.
			scopeVal, ok := doc.Metadata[rag.ScopeMetadataKey]
			if !ok {
				rt.Fatalf("stored document missing %q metadata key", rag.ScopeMetadataKey)
			}
			if scopeVal != identifier {
				rt.Fatalf("stored document %q = %q, want %q", rag.ScopeMetadataKey, scopeVal, identifier)
			}

			// Verify metadata["created_at"] is parseable as RFC 3339.
			createdAt, ok := doc.Metadata["created_at"]
			if !ok {
				rt.Fatal("stored document missing \"created_at\" metadata key")
			}
			if _, err := time.Parse(time.RFC3339, createdAt); err != nil {
				rt.Fatalf("stored document \"created_at\" = %q is not valid RFC 3339: %v", createdAt, err)
			}

			// Verify all user-provided metadata key-value pairs are present.
			for k, v := range userMeta {
				storedVal, ok := doc.Metadata[k]
				if !ok {
					rt.Fatalf("stored document missing user metadata key %q", k)
				}
				if storedVal != v {
					rt.Fatalf("stored document metadata[%q] = %q, want %q", k, storedVal, v)
				}
			}

			break
		}

		if !found {
			rt.Fatalf("no stored document has Content == %q", fact)
		}
	})
}

// ---------------------------------------------------------------------------
// Feature: unified-memory-providers, Property 6: RecallTool output contains all result fields
// ---------------------------------------------------------------------------

// TestProperty_RecallToolOutputFormat verifies that for any non-empty slice of
// ScoredDocuments (with arbitrary fact text, non-nil metadata including user keys,
// created_at, and _scope_id, and random scores), the formatted string output of
// formatScoredDocuments contains each result's fact text, each user metadata
// key-value pair (excluding _scope_id and created_at), the created_at timestamp
// in RFC 3339 format, and the similarity score formatted to 4 decimal places.
//
// **Validates: Requirements 3.9**
func TestProperty_RecallToolOutputFormat(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate 1–5 ScoredDocuments with non-nil metadata.
		numResults := rapid.IntRange(1, 5).Draw(rt, "num_results")
		results := make([]agent.ScoredDocument, numResults)

		for i := range numResults {
			fact := genNonEmptyString(rt, "fact")
			score := rapid.Float64Range(0, 1).Draw(rt, "score")

			// Build metadata with 1–3 user keys, plus created_at and _scope_id.
			numUserKeys := rapid.IntRange(1, 3).Draw(rt, "num_user_keys")
			meta := make(map[string]string, numUserKeys+2)
			for j := range numUserKeys {
				_ = j
				key := rapid.StringMatching(`[a-z]{1,10}`).Draw(rt, "user_key")
				val := rapid.StringMatching(`[a-zA-Z0-9]{1,20}`).Draw(rt, "user_val")
				meta[key] = val
			}

			// Add created_at as a valid RFC 3339 timestamp.
			sec := rapid.Int64Range(0, 4102444800).Draw(rt, "ts_sec")
			ts := time.Unix(sec, 0).UTC().Format(time.RFC3339)
			meta["created_at"] = ts

			// Add _scope_id (should be excluded from output metadata).
			meta[rag.ScopeMetadataKey] = genNonEmptyString(rt, "scope_id")

			results[i] = agent.ScoredDocument{
				Document: agent.Document{
					Content:  fact,
					Metadata: meta,
				},
				Score: score,
			}
		}

		// Call formatScoredDocuments directly.
		output := formatScoredDocuments(results)

		// Verify each result's fields appear in the output.
		for i, sd := range results {
			// Fact text must appear.
			if !strings.Contains(output, sd.Document.Content) {
				rt.Fatalf("output missing fact for result[%d]: %q\noutput: %s",
					i, sd.Document.Content, output)
			}

			// Each user metadata key-value pair must appear (excluding _scope_id and created_at).
			for k, v := range sd.Document.Metadata {
				if k == rag.ScopeMetadataKey || k == "created_at" {
					continue
				}
				kv := fmt.Sprintf("%s=%s", k, v)
				if !strings.Contains(output, kv) {
					rt.Fatalf("output missing metadata %q for result[%d]\noutput: %s",
						kv, i, output)
				}
			}

			// created_at timestamp must appear in RFC 3339 format.
			ts := sd.Document.Metadata["created_at"]
			if !strings.Contains(output, ts) {
				rt.Fatalf("output missing timestamp %q for result[%d]\noutput: %s",
					ts, i, output)
			}

			// Score must appear formatted to 4 decimal places.
			scoreStr := fmt.Sprintf("%.4f", sd.Score)
			if !strings.Contains(output, scoreStr) {
				rt.Fatalf("output missing score %q for result[%d]\noutput: %s",
					scoreStr, i, output)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Feature: unified-memory-providers, Property 7: Tool name customization via functional options
// ---------------------------------------------------------------------------

// TestProperty_ToolNameCustomization verifies that for any non-empty tool name
// string, creating a NewRememberTool with WithToolName(name) produces a tool
// whose Spec.Name equals the provided name. The same holds for NewRecallTool.
// When no WithToolName option is provided, NewRememberTool defaults to
// "remember" and NewRecallTool defaults to "recall".
//
// **Validates: Requirements 3.10, 3.11**
func TestProperty_ToolNameCustomization(t *testing.T) {
	// Shared dependencies — tools are not invoked, so a single store/embedder suffices.
	memStore := rag.NewMemoryStore()
	scopedStore := rag.NewScopedStore(memStore)
	embedder := &hashEmbedder{dim: 16}

	t.Run("defaults", func(t *testing.T) {
		rememberTool := NewRememberTool(scopedStore, embedder)
		if rememberTool.Spec.Name != "remember" {
			t.Fatalf("NewRememberTool default Spec.Name = %q, want %q", rememberTool.Spec.Name, "remember")
		}

		recallTool := NewRecallTool(scopedStore, embedder)
		if recallTool.Spec.Name != "recall" {
			t.Fatalf("NewRecallTool default Spec.Name = %q, want %q", recallTool.Spec.Name, "recall")
		}
	})

	t.Run("custom_name", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			// Generate a random non-empty tool name (alphanumeric + underscores, 1–50 chars).
			name := rapid.StringMatching(`[a-zA-Z_][a-zA-Z0-9_]{0,49}`).Draw(rt, "tool_name")

			rememberTool := NewRememberTool(scopedStore, embedder, WithToolName(name))
			if rememberTool.Spec.Name != name {
				rt.Fatalf("NewRememberTool with WithToolName(%q): Spec.Name = %q, want %q",
					name, rememberTool.Spec.Name, name)
			}

			recallTool := NewRecallTool(scopedStore, embedder, WithToolName(name))
			if recallTool.Spec.Name != name {
				rt.Fatalf("NewRecallTool with WithToolName(%q): Spec.Name = %q, want %q",
					name, recallTool.Spec.Name, name)
			}
		})
	})
}
