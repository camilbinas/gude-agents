package memory

import (
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"
	"testing"
)

var errTestRNG = errors.New("rng unavailable")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errTestRNG }

func TestRandomID_FormatAndUniqueness(t *testing.T) {
	hex128 := regexp.MustCompile(`^[0-9a-f]{32}$`)
	seen := make(map[string]struct{})
	for i := 0; i < 1000; i++ {
		id, err := randomID(nil) // nil selects crypto/rand.Reader
		if err != nil {
			t.Fatal(err)
		}
		if !hex128.MatchString(id) {
			t.Fatalf("id %q is not 32 lowercase hex characters", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestRandomID_ReaderFailures(t *testing.T) {
	cases := map[string]io.Reader{
		"error":      failingReader{},
		"short read": bytes.NewReader(make([]byte, 15)),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			id, err := randomID(r)
			if err == nil || id != "" {
				t.Fatalf("randomID = %q, %v; want empty ID and error", id, err)
			}
		})
	}
}

// Remember must surface an RNG failure instead of storing an entry under a
// predictable all-zero ID.
func TestStore_RememberPropagatesRNGFailure(t *testing.T) {
	store, err := NewStore[testEntry](newMockEmbedder(4))
	if err != nil {
		t.Fatal(err)
	}
	store.random = failingReader{}

	err = store.Remember(context.Background(), "user-1", testEntry{Content: "I like Go"})
	if !errors.Is(err, errTestRNG) {
		t.Fatalf("Remember error = %v, want RNG failure", err)
	}
	if n := len(store.entries["user-1"]); n != 0 {
		t.Fatalf("stored %d entries, want none", n)
	}

	// An explicit primary key needs no randomness.
	if err := store.Remember(context.Background(), "user-1", testEntry{ID: "fixed", Content: "I like Go"}); err != nil {
		t.Fatalf("Remember with explicit ID: %v", err)
	}
}
