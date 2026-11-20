// Package conformance holds the behavior every agent.ConversationManager
// implementation must share. Backends run it from their own tests.
package conformance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// Msg builds a user message.
func Msg(text string) agent.Message {
	return agent.Message{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: text}}}
}

// RunAppend verifies Load/Append/List/Delete revision and sequence semantics.
func RunAppend(t *testing.T, store agent.ConversationManager) {
	t.Helper()
	ctx := context.Background()
	id := "conversation"
	missing, err := store.Load(ctx, id)
	if err != nil || missing.Revision != 0 || missing.LastSequence != 0 || len(missing.Messages) != 0 {
		t.Fatalf("missing=%+v err=%v", missing, err)
	}
	cursor, err := store.Append(ctx, id, []agent.Message{Msg("one"), Msg("two"), Msg("three")}, 0)
	if err != nil || cursor != (agent.ConversationCursor{Revision: 1, LastSequence: 3}) {
		t.Fatalf("first Append=%+v,%v", cursor, err)
	}
	if _, err := store.Append(ctx, id, []agent.Message{Msg("stale")}, 0); !errors.Is(err, agent.ErrConversationConflict) {
		t.Fatalf("stale Append=%v", err)
	}
	all, err := store.Load(ctx, id)
	if err != nil || len(all.Messages) != 3 || all.Revision != 1 || all.LastSequence != 3 {
		t.Fatalf("Load=%+v,%v", all, err)
	}
	tail, err := store.LoadAfter(ctx, id, 2)
	if err != nil || len(tail.Messages) != 1 || tail.Messages[0].Content[0].(agent.TextBlock).Text != "three" || tail.Revision != 1 || tail.LastSequence != 3 {
		t.Fatalf("LoadAfter=%+v,%v", tail, err)
	}
	empty, err := store.LoadAfter(ctx, id, 99)
	if err != nil || len(empty.Messages) != 0 || empty.Revision != 1 || empty.LastSequence != 3 {
		t.Fatalf("LoadAfter end=%+v,%v", empty, err)
	}
	nop, err := store.Append(ctx, id, nil, 1)
	if err != nil || nop != (agent.ConversationCursor{Revision: 1, LastSequence: 3}) {
		t.Fatalf("empty Append=%+v,%v", nop, err)
	}
	stateStore, ok := store.(agent.ContextStateStore)
	if !ok {
		t.Fatalf("%T lacks ContextStateStore", store)
	}
	stateRev, err := stateStore.SaveContextState(ctx, id, "test", []byte(`{"v":1}`), 0)
	if err != nil || stateRev != 1 {
		t.Fatalf("SaveContextState=%d,%v", stateRev, err)
	}
	unchanged, _ := store.Load(ctx, id)
	if unchanged.Revision != 1 || unchanged.LastSequence != 3 {
		t.Fatalf("state changed cursor: %+v", unchanged)
	}
	if _, err := stateStore.SaveContextState(ctx, id, "test", []byte(`{"v":2}`), 0); !errors.Is(err, agent.ErrContextStateConflict) {
		t.Fatalf("stale state=%v", err)
	}
	if err := store.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	afterDelete, _ := store.Load(ctx, id)
	if afterDelete.Revision != 0 || afterDelete.LastSequence != 0 || len(afterDelete.Messages) != 0 {
		t.Fatalf("deleted=%+v", afterDelete)
	}
	if _, err := stateStore.LoadContextState(ctx, id, "test"); err != nil {
		t.Fatal(err)
	}
}

// RunContextState verifies the agent.ContextStateStore contract.
func RunContextState(t *testing.T, store agent.ConversationManager) {
	t.Helper()
	stateStore, ok := store.(agent.ContextStateStore)
	if !ok {
		t.Fatalf("%T lacks ContextStateStore", store)
	}
	ctx := context.Background()
	if _, err := stateStore.SaveContextState(ctx, "state-only", "one", []byte(`{"v":1}`), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.SaveContextState(ctx, "state-only", "two", []byte(`{"v":2}`), 0); err != nil {
		t.Fatal(err)
	}
	one, err := stateStore.LoadContextState(ctx, "state-only", "one")
	if err != nil || string(one.Data) != `{"v":1}` || one.Revision != 1 {
		t.Fatalf("first namespace=%+v err=%v", one, err)
	}
	if _, err := stateStore.SaveContextState(ctx, "state-only", "one", []byte(`not json`), 1); err == nil {
		t.Fatal("invalid context state JSON was accepted")
	}
	listed, err := store.List(ctx)
	if err != nil || len(listed) != 0 {
		t.Fatalf("state-only conversation listed=%v err=%v", listed, err)
	}
	if err := store.Delete(ctx, "state-only"); err != nil {
		t.Fatal(err)
	}
	deleted, err := stateStore.LoadContextState(ctx, "state-only", "one")
	if err != nil || deleted.Revision != 0 || len(deleted.Data) != 0 {
		t.Fatalf("deleted state=%+v err=%v", deleted, err)
	}
}

// snapshotMessage is the text of the message stored at a sequence, so a reader
// can verify exactly which sequences a snapshot returned.
func snapshotMessage(sequence uint64) string { return fmt.Sprintf("m%d", sequence) }

// RunSnapshotConsistency verifies that every LoadAfter snapshot is internally
// consistent while appends are in flight: the returned messages must be exactly
// the sequences in (afterSequence, LastSequence] of the returned cursor, never a
// newer range paired with an older cursor.
//
// Concurrent writers retry the revision CAS. The Append starting at revision r
// writes (r mod 3)+1 messages, so the cursor after r successful appends is the
// fixed pair (Revision=r, LastSequence=sum over i<r of (i mod 3)+1). That ties
// Revision to LastSequence independently of which writer won each round, and
// each message's text names its own sequence. Concurrent readers then check, for
// full reads (after 0) and suffix reads (after in the middle, at the end and
// past the end), that the cursor matches that pairing and that the messages are
// precisely the contiguous sequences after the boundary.
func RunSnapshotConsistency(t *testing.T, store agent.ConversationStore) {
	t.Helper()
	const (
		id        = "snapshot-consistency"
		writers   = 3
		readers   = 4
		revisions = 40
		pastEnd   = uint64(1) << 62
	)
	lastAt := make([]uint64, revisions+1)
	for r := 0; r < revisions; r++ {
		lastAt[r+1] = lastAt[r] + uint64(r%3) + 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// check reports the first violated invariant of a snapshot, or "".
	check := func(after uint64, snap agent.ConversationSnapshot) string {
		if snap.Revision > revisions {
			return fmt.Sprintf("revision %d exceeds the %d appends made", snap.Revision, revisions)
		}
		if want := lastAt[snap.Revision]; snap.LastSequence != want {
			return fmt.Sprintf("cursor Revision=%d LastSequence=%d, want LastSequence=%d", snap.Revision, snap.LastSequence, want)
		}
		want := uint64(0)
		if snap.LastSequence > after {
			want = snap.LastSequence - after
		}
		if uint64(len(snap.Messages)) != want {
			return fmt.Sprintf("after=%d: %d messages with cursor Revision=%d LastSequence=%d, want %d (sequences %d..%d)",
				after, len(snap.Messages), snap.Revision, snap.LastSequence, want, after+1, snap.LastSequence)
		}
		for i, msg := range snap.Messages {
			text := ""
			if len(msg.Content) == 1 {
				if block, ok := msg.Content[0].(agent.TextBlock); ok {
					text = block.Text
				}
			}
			if want := snapshotMessage(after + uint64(i) + 1); text != want {
				return fmt.Sprintf("after=%d: message %d is %q, want %q", after, i, text, want)
			}
		}
		return ""
	}

	var writerWG, readerWG sync.WaitGroup
	done := make(chan struct{})
	for w := 0; w < writers; w++ {
		writerWG.Add(1)
		go func() {
			defer writerWG.Done()
			for {
				cur, err := store.LoadAfter(ctx, id, pastEnd)
				if err != nil {
					t.Errorf("writer LoadAfter: %v", err)
					return
				}
				if problem := check(pastEnd, cur); problem != "" {
					t.Errorf("writer cursor read: %s", problem)
					return
				}
				if cur.Revision >= revisions {
					return
				}
				batch := make([]agent.Message, int(cur.Revision%3)+1)
				for i := range batch {
					batch[i] = Msg(snapshotMessage(cur.LastSequence + uint64(i) + 1))
				}
				got, err := store.Append(ctx, id, batch, cur.Revision)
				if errors.Is(err, agent.ErrConversationConflict) {
					continue
				}
				if err != nil {
					t.Errorf("writer Append at revision %d: %v", cur.Revision, err)
					return
				}
				if got.Revision != cur.Revision+1 || got.LastSequence != lastAt[got.Revision] {
					t.Errorf("Append at revision %d returned %+v, want Revision=%d LastSequence=%d",
						cur.Revision, got, cur.Revision+1, lastAt[cur.Revision+1])
					return
				}
			}
		}()
	}
	for r := 0; r < readers; r++ {
		readerWG.Add(1)
		go func(r int) {
			defer readerWG.Done()
			var prev agent.ConversationSnapshot
			for i := 0; ; i++ {
				finished := false
				select {
				case <-done:
					finished = true // one last read after the writers stop
				default:
				}
				// Rotate through a full read, mid-history and end boundaries
				// (relative to the previous cursor), and a boundary past the end.
				var after uint64
				switch (i + r) % 5 {
				case 0:
					after = 0
				case 1:
					after = 1
				case 2:
					after = prev.LastSequence / 2
				case 3:
					after = prev.LastSequence
				case 4:
					after = pastEnd
				}
				snap, err := store.LoadAfter(ctx, id, after)
				if err != nil {
					t.Errorf("reader %d LoadAfter(%d): %v", r, after, err)
					return
				}
				if problem := check(after, snap); problem != "" {
					t.Errorf("reader %d: inconsistent snapshot: %s", r, problem)
					return
				}
				if snap.Revision < prev.Revision || snap.LastSequence < prev.LastSequence {
					t.Errorf("reader %d: cursor went backwards from %d/%d to %d/%d", r,
						prev.Revision, prev.LastSequence, snap.Revision, snap.LastSequence)
					return
				}
				prev = snap
				if finished {
					return
				}
			}
		}(r)
	}
	writerWG.Wait()
	close(done)
	readerWG.Wait()

	final, err := store.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if final.Revision != revisions || final.LastSequence != lastAt[revisions] {
		t.Fatalf("final cursor %d/%d, want %d/%d", final.Revision, final.LastSequence, revisions, lastAt[revisions])
	}
	if problem := check(0, final); problem != "" {
		t.Fatalf("final full read: %s", problem)
	}
}
