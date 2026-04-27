package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Task 5.1: Test infrastructure
// ---------------------------------------------------------------------------

// TestMemoryStruct is a struct with diverse field types used for property-based
// testing of the typed memory codec and end-to-end round-trip.
type TestMemoryStruct struct {
	Text       string            `json:"text"`
	Category   string            `json:"category"`
	Count      int               `json:"count"`
	Score      float64           `json:"score"`
	Active     bool              `json:"active"`
	Tags       []string          `json:"tags"`
	Attributes map[string]string `json:"attributes"`
	CreatedAt  time.Time         `json:"created_at"`
	Notes      *string           `json:"notes"`
}

// genTestMemoryStruct returns a rapid generator for TestMemoryStruct that
// produces random instances with varied field values, including empty strings,
// nil pointers, empty slices, and empty maps.
func genTestMemoryStruct() *rapid.Generator[TestMemoryStruct] {
	return rapid.Custom[TestMemoryStruct](func(t *rapid.T) TestMemoryStruct {
		text := rapid.String().Draw(t, "text")
		category := rapid.String().Draw(t, "category")
		count := rapid.Int().Draw(t, "count")
		score := rapid.Float64().Draw(t, "score")
		active := rapid.Bool().Draw(t, "active")

		// Tags: randomly nil, empty, or populated.
		var tags []string
		tagsChoice := rapid.IntRange(0, 2).Draw(t, "tags_choice")
		switch tagsChoice {
		case 0:
			tags = nil
		case 1:
			tags = []string{}
		case 2:
			n := rapid.IntRange(1, 5).Draw(t, "tags_len")
			tags = make([]string, n)
			for i := range n {
				tags[i] = rapid.String().Draw(t, "tag")
			}
		}

		// Attributes: randomly nil, empty, or populated.
		var attrs map[string]string
		attrsChoice := rapid.IntRange(0, 2).Draw(t, "attrs_choice")
		switch attrsChoice {
		case 0:
			attrs = nil
		case 1:
			attrs = map[string]string{}
		case 2:
			n := rapid.IntRange(1, 5).Draw(t, "attrs_len")
			attrs = make(map[string]string, n)
			for i := range n {
				_ = i
				k := rapid.String().Draw(t, "attr_key")
				v := rapid.String().Draw(t, "attr_val")
				attrs[k] = v
			}
		}

		// CreatedAt: generate a Unix timestamp and convert to time.Time.
		// Using time.Unix(ts, 0).UTC() ensures JSON round-trip stability
		// (RFC3339 loses sub-second precision with some formats).
		ts := rapid.Int64Range(0, 4102444800).Draw(t, "created_at_ts")
		createdAt := time.Unix(ts, 0).UTC()

		// Notes: randomly nil or a non-nil string pointer.
		var notes *string
		if rapid.Bool().Draw(t, "notes_present") {
			s := rapid.String().Draw(t, "notes_val")
			notes = &s
		}

		return TestMemoryStruct{
			Text:       text,
			Category:   category,
			Count:      count,
			Score:      score,
			Active:     active,
			Tags:       tags,
			Attributes: attrs,
			CreatedAt:  createdAt,
			Notes:      notes,
		}
	})
}

// genTestMemoryStructNonEmptyText returns a rapid generator for TestMemoryStruct
// that guarantees the Text field is non-empty (for use with contentFunc).
func genTestMemoryStructNonEmptyText() *rapid.Generator[TestMemoryStruct] {
	return rapid.Custom[TestMemoryStruct](func(t *rapid.T) TestMemoryStruct {
		s := genTestMemoryStruct().Draw(t, "base")
		// Ensure Text is non-empty by generating a non-empty string.
		s.Text = rapid.StringMatching(`[a-zA-Z0-9 ]{1,100}`).Draw(t, "non_empty_text")
		return s
	})
}

// jsonNormalize marshals a value to JSON, returning the JSON bytes for
// comparison. This accounts for differences like nil vs empty slices that
// JSON round-trip normalizes.
func jsonNormalize(v any) ([]byte, error) {
	return json.Marshal(v)
}

// ---------------------------------------------------------------------------
// Task 5.2: Property 1 — Codec round-trip preserves value
// ---------------------------------------------------------------------------

// TestProperty_CodecRoundTrip verifies that for any valid TestMemoryStruct,
// encoding via encodeTypedValue then decoding via decodeTypedValue produces
// a value equivalent to the original after JSON normalization.
//
// **Validates: Requirements 3.1, 3.2, 3.3, 10.2, 10.3**
func TestProperty_CodecRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		original := genTestMemoryStruct().Draw(rt, "value")

		// Encode.
		meta, err := encodeTypedValue(original)
		if err != nil {
			rt.Fatalf("encodeTypedValue failed: %v", err)
		}

		// Decode.
		decoded, err := decodeTypedValue[TestMemoryStruct](meta)
		if err != nil {
			rt.Fatalf("decodeTypedValue failed: %v", err)
		}

		// Compare via JSON normalization: marshal both to JSON and compare bytes.
		originalJSON, err := jsonNormalize(original)
		if err != nil {
			rt.Fatalf("jsonNormalize(original) failed: %v", err)
		}
		decodedJSON, err := jsonNormalize(decoded)
		if err != nil {
			rt.Fatalf("jsonNormalize(decoded) failed: %v", err)
		}

		if string(originalJSON) != string(decodedJSON) {
			rt.Fatalf("codec round-trip mismatch:\noriginal JSON: %s\ndecoded JSON:  %s", string(originalJSON), string(decodedJSON))
		}
	})
}

// ---------------------------------------------------------------------------
// Feature: memory-rag-separation, Property 4: Typed Memory Encoding/Decoding Round-Trip
// ---------------------------------------------------------------------------

// testItem is a struct with diverse field types used for property-based testing
// of the TypedMemoryStore encoding/decoding round-trip.
type testItem struct {
	Name string   `json:"name"`
	Age  int      `json:"age"`
	Tags []string `json:"tags"`
}

// genTestItem returns a rapid generator for testItem that produces random
// instances with varied field values, including empty tags slices and nil tags.
func genTestItem() *rapid.Generator[testItem] {
	return rapid.Custom[testItem](func(t *rapid.T) testItem {
		name := rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(t, "name")
		age := rapid.IntRange(0, 150).Draw(t, "age")

		var tags []string
		tagsChoice := rapid.IntRange(0, 2).Draw(t, "tags_choice")
		switch tagsChoice {
		case 0:
			tags = nil
		case 1:
			tags = []string{}
		case 2:
			n := rapid.IntRange(1, 5).Draw(t, "tags_len")
			tags = make([]string, n)
			for i := range n {
				tags[i] = rapid.StringMatching(`[a-zA-Z0-9]{1,20}`).Draw(t, "tag")
			}
		}

		return testItem{
			Name: name,
			Age:  age,
			Tags: tags,
		}
	})
}

// genTestItemNonEmptyName returns a rapid generator for testItem that
// guarantees the Name field is non-empty (required for contentFunc).
func genTestItemNonEmptyName() *rapid.Generator[testItem] {
	return rapid.Custom[testItem](func(t *rapid.T) testItem {
		item := genTestItem().Draw(t, "base")
		item.Name = rapid.StringMatching(`[a-zA-Z0-9]{1,50}`).Draw(t, "non_empty_name")
		return item
	})
}

// TestProperty_TypedMemoryRoundTrip verifies that for any valid testItem value,
// encoding it via TypedMemoryStore[testItem].Add (which stores the JSON under
// the _typed_data metadata key) and then decoding it via
// TypedMemoryStore[testItem].Search produces a value equal to the original.
// That is, decode(encode(value)) == value.
//
// Uses InMemoryStore as the underlying store and hashEmbedder for embeddings.
//
// **Validates: Requirements 6.2**
func TestProperty_TypedMemoryRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		const dim = 16
		embedder := &hashEmbedder{dim: dim}
		memStore := NewInMemoryStore()
		typedStore := NewTypedMemoryStore[testItem](memStore)
		ctx := context.Background()

		identifier := genNonEmptyString(rt, "identifier")
		original := genTestItemNonEmptyName().Draw(rt, "value")

		contentFunc := func(item testItem) string { return item.Name }

		// Compute embedding for the content.
		emb, err := embedder.Embed(ctx, contentFunc(original))
		if err != nil {
			rt.Fatalf("Embed failed: %v", err)
		}

		// Store via TypedMemoryStore.Add.
		if err := typedStore.Add(ctx, identifier, []testItem{original}, contentFunc, [][]float64{emb}); err != nil {
			rt.Fatalf("TypedMemoryStore.Add failed: %v", err)
		}

		// Retrieve via TypedMemoryStore.Search using the same embedding.
		results, err := typedStore.Search(ctx, identifier, emb, 1)
		if err != nil {
			rt.Fatalf("TypedMemoryStore.Search failed: %v", err)
		}

		if len(results) == 0 {
			rt.Fatal("TypedMemoryStore.Search returned empty slice after Add")
		}

		decoded := results[0].Value

		// Compare via JSON normalization to handle nil vs empty slice differences.
		originalJSON, err := jsonNormalize(original)
		if err != nil {
			rt.Fatalf("jsonNormalize(original) failed: %v", err)
		}
		decodedJSON, err := jsonNormalize(decoded)
		if err != nil {
			rt.Fatalf("jsonNormalize(decoded) failed: %v", err)
		}

		if string(originalJSON) != string(decodedJSON) {
			rt.Fatalf("typed memory round-trip mismatch:\noriginal JSON: %s\ndecoded JSON:  %s", string(originalJSON), string(decodedJSON))
		}
	})
}

// ---------------------------------------------------------------------------
// Task 5.3: Property 2 — End-to-end Remember/Recall round-trip
// ---------------------------------------------------------------------------

// TestProperty_EndToEndRoundTrip verifies that for any valid TestMemoryStruct
// with non-empty contentFunc output, calling Remember then Recall with the
// same content as the query returns a value matching the original after JSON
// normalization.
//
// Uses a deterministic mock embedder (same fixed vector for all inputs) so
// that cosine similarity of identical vectors is 1.0, guaranteeing the stored
// value is the top search result.
//
// **Validates: Requirements 1.1, 1.2, 2.2, 5.2, 5.3, 10.1**
func TestProperty_EndToEndRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		original := genTestMemoryStructNonEmptyText().Draw(rt, "value")

		contentFunc := func(s TestMemoryStruct) string { return s.Text }

		// Use the fixedEmbedder from typed_test.go — same fixed vector for all
		// inputs guarantees cosine similarity = 1.0 for the stored entry.
		emb := &fixedEmbedder{vec: []float64{0.1, 0.2, 0.3}}
		adapter := NewTypedInMemory[TestMemoryStruct](emb, contentFunc)

		ctx := context.Background()
		identifier := "pbt-user"

		// Remember.
		if err := adapter.Remember(ctx, identifier, original); err != nil {
			rt.Fatalf("Remember failed: %v", err)
		}

		// Recall using the same content text as the query.
		entries, err := adapter.Recall(ctx, identifier, original.Text, 1)
		if err != nil {
			rt.Fatalf("Recall failed: %v", err)
		}

		if len(entries) == 0 {
			rt.Fatal("Recall returned empty slice after Remember")
		}

		// Compare via JSON normalization.
		originalJSON, err := jsonNormalize(original)
		if err != nil {
			rt.Fatalf("jsonNormalize(original) failed: %v", err)
		}
		recalledJSON, err := jsonNormalize(entries[0].Value)
		if err != nil {
			rt.Fatalf("jsonNormalize(recalled) failed: %v", err)
		}

		if string(originalJSON) != string(recalledJSON) {
			rt.Fatalf("end-to-end round-trip mismatch:\noriginal JSON: %s\nrecalled JSON: %s", string(originalJSON), string(recalledJSON))
		}
	})
}

// ---------------------------------------------------------------------------
// Task 5.4: Property 3 — ContentFunc output determines embedded text
// ---------------------------------------------------------------------------

// TestProperty_ContentFuncDeterminesEmbeddedText verifies that for any valid
// TestMemoryStruct, when Remember is called, the text passed to Embedder.Embed
// is exactly the string returned by contentFunc.
//
// Uses a recording mock embedder that captures the text argument.
//
// **Validates: Requirements 2.2, 5.2**
func TestProperty_ContentFuncDeterminesEmbeddedText(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		original := genTestMemoryStructNonEmptyText().Draw(rt, "value")

		contentFunc := func(s TestMemoryStruct) string { return s.Text }

		// Use the recordingEmbedder from typed_test.go to capture the text
		// passed to Embed.
		rec := &recordingEmbedder{vec: []float64{0.1, 0.2, 0.3}}
		adapter := NewTypedInMemory[TestMemoryStruct](rec, contentFunc)

		ctx := context.Background()
		identifier := "pbt-user"

		// Remember.
		if err := adapter.Remember(ctx, identifier, original); err != nil {
			rt.Fatalf("Remember failed: %v", err)
		}

		// The recording embedder should have captured exactly one call with
		// the text returned by contentFunc.
		expectedText := contentFunc(original)
		if len(rec.texts) == 0 {
			rt.Fatal("recordingEmbedder captured no calls to Embed")
		}
		if rec.texts[0] != expectedText {
			rt.Fatalf("Embed received %q, want %q (contentFunc output)", rec.texts[0], expectedText)
		}
	})
}
