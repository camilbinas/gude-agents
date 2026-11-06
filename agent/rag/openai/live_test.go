package openai

// Opt-in live smoke tests for the OpenAI embedder. They are skipped unless
// TEST_LIVE_INFERENCE is exactly "1", and then require TEST_EMBEDDING_MODEL_ID.
// No default embedder is chosen. Credentials or TEST_EMBEDDING_MODEL_ID alone never
// enable them. Run from this module's directory:
//
//	TEST_LIVE_INFERENCE=1 TEST_EMBEDDING_MODEL_ID=<embedding-model> go test -run Live -v ./...
//
// The tests assert structure (vector length, consistency, ordering), not values.

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent/rag"
)

// liveEmbedder skips unless live tests are enabled, fails if the embedding
// model ID is missing, and only then constructs the embedder.
func liveEmbedder(t *testing.T) *Embedder {
	t.Helper()
	if os.Getenv("TEST_LIVE_INFERENCE") != "1" {
		t.Skip("live test skipped: set TEST_LIVE_INFERENCE=1 (and TEST_EMBEDDING_MODEL_ID) to run")
	}
	model := os.Getenv("TEST_EMBEDDING_MODEL_ID")
	if model == "" {
		t.Fatalf("TEST_LIVE_INFERENCE=1 requires TEST_EMBEDDING_MODEL_ID to be set; no default embedder is chosen")
	}
	e, err := NewEmbedder(WithEmbedderModel(model))
	if err != nil {
		t.Fatalf("NewEmbedder(%q): %v", model, err)
	}
	return e
}

const liveTimeout = 2 * time.Minute

func liveContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	t.Cleanup(cancel)
	return ctx
}

func liveCosine(a, b []float64) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

var liveTexts = []string{
	"The cat sat on the mat.",
	"Quarterly revenue grew by twelve percent.",
	"Photosynthesis converts sunlight into chemical energy.",
}

func liveEmbedAll(t *testing.T, e rag.Embedder) [][]float64 {
	t.Helper()
	ctx := liveContext(t)
	out := make([][]float64, len(liveTexts))
	for i, text := range liveTexts {
		v, err := e.Embed(ctx, text)
		if err != nil {
			t.Fatalf("Embed(%q): %v", text, err)
		}
		out[i] = v
	}
	return out
}

func TestLive_Embed(t *testing.T) {
	e := liveEmbedder(t)
	first := liveEmbedAll(t, e)
	second := liveEmbedAll(t, e)
	for i := range first {
		if len(first[i]) == 0 {
			t.Fatalf("text %d: empty vector", i)
		}
		if len(first[i]) != len(first[0]) || len(second[i]) != len(first[0]) {
			t.Errorf("text %d: vector length %d/%d differs from %d", i, len(first[i]), len(second[i]), len(first[0]))
		}
	}
}

// TestLive_EmbedBatchOrder checks that results line up with their inputs: each
// vector from a second pass is closest to the same-position vector of the first.
func TestLive_EmbedBatchOrder(t *testing.T) {
	e := liveEmbedder(t)
	first := liveEmbedAll(t, e)
	second := liveEmbedAll(t, e)
	for i := range second {
		if len(first[i]) != len(second[i]) {
			t.Fatalf("text %d: vector length changed between calls (%d vs %d)", i, len(first[i]), len(second[i]))
		}
		best, bestSim := -1, math.Inf(-1)
		for j := range first {
			if len(first[j]) != len(second[i]) {
				t.Fatalf("text %d: vector length differs from text %d", i, j)
			}
			if sim := liveCosine(second[i], first[j]); sim > bestSim {
				best, bestSim = j, sim
			}
		}
		if best != i {
			t.Errorf("text %d matched text %d (similarity %.3f); order not preserved", i, best, bestSim)
		}
	}
}
