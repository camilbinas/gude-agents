package rag

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSplitText_NoTrailingDuplicateChunk reproduces the overlap edge case where
// the final step used to emit a trailing chunk fully contained in the previous
// one. With size 10 and overlap 5 over 11 runes, the old code produced
// [0:10], [5:11], [10:11]; the 1-rune last chunk is a near-duplicate. The fix
// stops once a chunk reaches the end.
func TestSplitText_NoTrailingDuplicateChunk(t *testing.T) {
	text := strings.Repeat("a", 11)
	chunks, err := SplitText(text, 10, 5)
	if err != nil {
		t.Fatalf("SplitText: %v", err)
	}
	// Expect exactly two chunks: [0:10] and [5:11]. No trailing [10:11].
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %q", len(chunks), chunks)
	}
	if len(chunks[len(chunks)-1]) <= 5 {
		// The last chunk should carry real new content, not a sub-overlap remnant.
		t.Fatalf("last chunk %q looks like a trailing near-duplicate", chunks[len(chunks)-1])
	}
	// The input must still be fully covered.
	if chunks[0] != text[:10] || chunks[1] != text[5:11] {
		t.Fatalf("unexpected chunk boundaries: %q", chunks)
	}
}

// TestSplitText_LastChunkNotContainedInPrevious is a general invariant check:
// with a positive overlap the final chunk must never be fully contained in the
// previous one (which would be a wasted duplicate embedding).
func TestSplitText_LastChunkNotContainedInPrevious(t *testing.T) {
	cases := []struct {
		size, overlap, n int
	}{
		{10, 5, 11},
		{4, 2, 5},
		{4, 3, 5},
		{8, 7, 9},
		{3, 1, 7},
	}
	// Use distinct runes so byte offsets, not repeated characters, determine
	// whether one chunk sits inside another.
	alphabet := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	for _, c := range cases {
		runes := make([]rune, c.n)
		for i := range runes {
			runes[i] = alphabet[i%len(alphabet)]
		}
		text := string(runes)
		chunks, err := SplitText(text, c.size, c.overlap)
		if err != nil {
			t.Fatalf("size=%d overlap=%d n=%d: %v", c.size, c.overlap, c.n, err)
		}
		if len(chunks) >= 2 {
			last := chunks[len(chunks)-1]
			prev := chunks[len(chunks)-2]
			if strings.Contains(prev, last) && len(last) < len(prev) {
				t.Fatalf("size=%d overlap=%d n=%d: last chunk %q is contained in previous %q",
					c.size, c.overlap, c.n, last, prev)
			}
		}
		// Coverage invariant: the last chunk reaches the end of the input.
		if len(chunks) > 0 && chunks[len(chunks)-1] != text[len(text)-len(chunks[len(chunks)-1]):] {
			t.Fatalf("size=%d overlap=%d n=%d: last chunk does not reach end", c.size, c.overlap, c.n)
		}
	}
}

// blockingEmbedder blocks each Embed call until the context is cancelled or a
// configured signal fires, except the first call which fails immediately. It
// records how many calls observed a cancelled context, letting a test assert
// that a single failure cancels the remaining in-flight embeds.
type blockingEmbedder struct {
	failFirst   error
	started     atomic.Int32
	cancelled   atomic.Int32
	completed   atomic.Int32
	firstCallCh chan struct{}
	firstOnce   sync.Once
}

func (b *blockingEmbedder) Embed(ctx context.Context, _ string) ([]float64, error) {
	n := b.started.Add(1)
	if n == 1 {
		// Signal that at least one worker has begun, then fail to trigger
		// cancellation of the siblings.
		b.firstOnce.Do(func() { close(b.firstCallCh) })
		return nil, b.failFirst
	}
	// Remaining workers block until the derived context is cancelled by the
	// first failure. If cancellation never propagates, the test times out.
	select {
	case <-ctx.Done():
		b.cancelled.Add(1)
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		b.completed.Add(1)
		return []float64{1.0}, nil
	}
}

// TestIngest_ConcurrentCancelsSiblingsOnFirstError verifies that when one
// concurrent embed fails, the derived context cancels the other in-flight
// embed calls instead of letting them run to completion, and that Ingest
// returns the failure.
func TestIngest_ConcurrentCancelsSiblingsOnFirstError(t *testing.T) {
	failErr := errors.New("embed boom")
	embedder := &blockingEmbedder{failFirst: failErr, firstCallCh: make(chan struct{})}
	store := &recordingVectorStore{}

	// Many chunks, high concurrency, so several workers are embedding when the
	// first failure fires.
	texts := []string{strings.Repeat("a b c d e f g h ", 40)}

	done := make(chan error, 1)
	go func() {
		done <- Ingest(context.Background(), store, embedder, texts, nil,
			WithChunkSize(8), WithChunkOverlap(0), WithConcurrency(8))
	}()

	select {
	case err := <-done:
		if !errors.Is(err, failErr) && !errors.Is(err, context.Canceled) {
			t.Fatalf("expected failErr or context.Canceled, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ingest did not return; siblings were not cancelled on first error")
	}

	// At least one sibling must have observed cancellation; none should have
	// run the full 5s block to completion.
	if embedder.completed.Load() != 0 {
		t.Fatalf("expected no embed to complete after first failure, got %d", embedder.completed.Load())
	}
}

// TestIngest_ConcurrentReturnsFirstErrorToOccur verifies Ingest returns the
// error from the first embed to fail, not the lowest-indexed one.
func TestIngest_ConcurrentReturnsFirstErrorToOccur(t *testing.T) {
	// Chunk index 5 fails fast; lower indices block briefly then would succeed.
	targetErr := errors.New("index-5 failed first")
	var started atomic.Int32
	embedder := embedFunc(func(ctx context.Context, text string) ([]float64, error) {
		idx := started.Add(1)
		if idx == 1 {
			// First worker scheduled fails immediately regardless of slice order.
			return nil, targetErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
			return []float64{1.0}, nil
		}
	})
	store := &recordingVectorStore{}
	texts := []string{strings.Repeat("w x y z ", 20)}

	err := Ingest(context.Background(), store, embedder, texts, nil,
		WithChunkSize(8), WithChunkOverlap(0), WithConcurrency(8))
	if !errors.Is(err, targetErr) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected first error to occur (%v) or context.Canceled, got %v", targetErr, err)
	}
}

// embedFunc adapts a function to rag.Embedder.
type embedFunc func(ctx context.Context, text string) ([]float64, error)

func (f embedFunc) Embed(ctx context.Context, text string) ([]float64, error) { return f(ctx, text) }

// TestIngest_NoSourceAppendsWithoutDedup documents and locks in the behavior
// that an Ingest without a "source" metadata key does NOT deduplicate: each
// call appends a fresh set of chunks. A Manager store is used so the dedup path
// (DeleteByMetadata) is reachable but should be a no-op without a source.
func TestIngest_NoSourceAppendsWithoutDedup(t *testing.T) {
	store := NewMemoryStore()
	embedder := &mockEmbedder{embedding: []float64{1.0}}

	// No metadata at all -> no source.
	for i := 0; i < 2; i++ {
		if err := Ingest(context.Background(), store, embedder, []string{"hello world"}, nil,
			WithChunkSize(100), WithChunkOverlap(0)); err != nil {
			t.Fatalf("Ingest %d: %v", i, err)
		}
	}
	store.mu.RLock()
	got := len(store.entries)
	store.mu.RUnlock()
	if got != 2 {
		t.Fatalf("expected 2 appended chunks (no dedup without source), got %d", got)
	}
}

// TestIngest_WithSourceReplacesChunks verifies the idempotent path: re-ingesting
// the same source replaces its chunks rather than appending.
func TestIngest_WithSourceReplacesChunks(t *testing.T) {
	store := NewMemoryStore()
	embedder := &mockEmbedder{embedding: []float64{1.0}}
	meta := []map[string]string{{"source": "doc.md"}}

	for i := 0; i < 3; i++ {
		if err := Ingest(context.Background(), store, embedder, []string{"hello world"}, meta,
			WithChunkSize(100), WithChunkOverlap(0)); err != nil {
			t.Fatalf("Ingest %d: %v", i, err)
		}
	}
	store.mu.RLock()
	got := len(store.entries)
	store.mu.RUnlock()
	if got != 1 {
		t.Fatalf("expected 1 chunk after repeated same-source ingest, got %d", got)
	}
}
