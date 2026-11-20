package redis

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/camilbinas/gude-agents/agent/conversation/internal/conformance"
)

func TestSnapshotConsistency(t *testing.T) {
	mr := miniredis.RunT(t)
	store, err := New(Options{Addr: mr.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	conformance.RunSnapshotConsistency(t, store)
}
