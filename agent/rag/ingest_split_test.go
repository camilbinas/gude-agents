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

// TestSplitText_NoTrailingDuplicateChunk: overlap must not emit a trailing
// chunk fully contained in the previous one.
func TestSplitText_NoTrailingDuplicateChunk(t *testing.T) {
	text := strings.Repeat("a", 11)
	chunks, err := SplitText(text, 10, 5)
	if err != nil {
		t.Fatalf("SplitText: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %q", len(chunks), chunks)
	}
	if len(chunks[len(chunks)-1]) <= 5 {
		t.Fatalf("last chunk %q looks like a trailing near-duplicate", chunks[len(chunks)-1])
	}
	if chunks[0] != text[:10] || chunks[1] != text[5:11] {
		t.Fatalf("unexpected chunk boundaries: %q", chunks)
	}
}

// TestSplitText_LastChunkNotContainedInPrevious: with positive overlap the last
// chunk is never fully contained in the previous one.
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
	// Distinct runes so containment is by position, not repeated characters.
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
		if len(chunks) > 0 && chunks[len(chunks)-1] != text[len(text)-len(chunks[len(chunks)-1]):] {
			t.Fatalf("size=%d overlap=%d n=%d: last chunk does not reach end", c.size, c.overlap, c.n)
		}
	}
}

// blockingEmbedder fails the first Embed call and blocks the rest until the
// context is cancelled, recording how many observed cancellation.
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
		b.firstOnce.Do(func() { close(b.firstCallCh) })
		return nil, b.failFirst
	}
	select {
	case <-ctx.Done():
		b.cancelled.Add(1)
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		b.completed.Add(1)
		return []float64{1.0}, nil
	}
}

// TestIngest_ConcurrentCancelsSiblingsOnFirstError: one failing embed cancels
// the other in-flight embeds and Ingest returns the failure.
func TestIngest_ConcurrentCancelsSiblingsOnFirstError(t *testing.T) {
	failErr := errors.New("embed boom")
	embedder := &blockingEmbedder{failFirst: failErr, firstCallCh: make(chan struct{})}
	store := &recordingVectorStore{}

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

	if embedder.completed.Load() != 0 {
		t.Fatalf("expected no embed to complete after first failure, got %d", embedder.completed.Load())
	}
}

// TestIngest_ConcurrentReturnsFirstErrorToOccur: Ingest returns the first embed
// error to occur, not the lowest-indexed one.
func TestIngest_ConcurrentReturnsFirstErrorToOccur(t *testing.T) {
	targetErr := errors.New("index-5 failed first")
	var started atomic.Int32
	embedder := embedFunc(func(ctx context.Context, text string) ([]float64, error) {
		idx := started.Add(1)
		if idx == 1 {
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

type embedFunc func(ctx context.Context, text string) ([]float64, error)

func (f embedFunc) Embed(ctx context.Context, text string) ([]float64, error) { return f(ctx, text) }

// TestIngest_NoSourceAppendsWithoutDedup: an Ingest without a "source" key
// appends each time rather than deduplicating.
func TestIngest_NoSourceAppendsWithoutDedup(t *testing.T) {
	store := NewMemoryStore()
	embedder := &mockEmbedder{embedding: []float64{1.0}}

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

// TestIngest_WithSourceReplacesChunks: re-ingesting the same source replaces
// its chunks rather than appending.
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
