package agent

import (
	"sync"
	"sync/atomic"
	"testing"
)

// locksLen reports the number of live entries. It takes the map mutex so it is
// safe to call concurrently with acquire/release.
func (l *conversationLocks) locksLen() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.locks)
}

func TestConversationLocksEvictWhenIdle(t *testing.T) {
	var locks conversationLocks

	h := locks.acquire("c1")
	if got := locks.locksLen(); got != 1 {
		t.Fatalf("held entries = %d, want 1", got)
	}
	h.release()
	if got := locks.locksLen(); got != 0 {
		t.Fatalf("entries after release = %d, want 0 (idle entries must be evicted)", got)
	}

	// A distinct ID reuses no prior entry and is likewise evicted.
	locks.acquire("c2").release()
	if got := locks.locksLen(); got != 0 {
		t.Fatalf("entries after second release = %d, want 0", got)
	}
}

func TestConversationLocksSerializeSameID(t *testing.T) {
	var locks conversationLocks
	var active, maxActive atomic.Int32
	var wg sync.WaitGroup
	const workers = 32
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			h := locks.acquire("same")
			n := active.Add(1)
			for {
				cur := maxActive.Load()
				if n <= cur || maxActive.CompareAndSwap(cur, n) {
					break
				}
			}
			active.Add(-1)
			h.release()
		}()
	}
	wg.Wait()
	if maxActive.Load() != 1 {
		t.Fatalf("max concurrent holders = %d, want 1", maxActive.Load())
	}
	if got := locks.locksLen(); got != 0 {
		t.Fatalf("entries after all releases = %d, want 0", got)
	}
}

func TestConversationLocksDifferentIDsDoNotBlock(t *testing.T) {
	var locks conversationLocks
	// Hold c1; acquiring c2 must not block on it.
	h1 := locks.acquire("c1")
	done := make(chan struct{})
	go func() {
		h2 := locks.acquire("c2")
		h2.release()
		close(done)
	}()
	<-done
	h1.release()
	if got := locks.locksLen(); got != 0 {
		t.Fatalf("entries after release = %d, want 0", got)
	}
}

func TestConversationLocksHighCardinalityDoesNotGrow(t *testing.T) {
	var locks conversationLocks
	var wg sync.WaitGroup
	const ids = 500
	wg.Add(ids)
	for i := range ids {
		go func(i int) {
			defer wg.Done()
			key := "conv-" + string(rune('a'+i%26)) + "-" + itoa(i)
			locks.acquire(key).release()
		}(i)
	}
	wg.Wait()
	// Every ID was acquired and released; none is held, so the map is empty
	// rather than retaining one mutex per distinct ID for the lifetime.
	if got := locks.locksLen(); got != 0 {
		t.Fatalf("entries after high-cardinality churn = %d, want 0", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
