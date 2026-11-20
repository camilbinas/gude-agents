package agent

import (
	"context"
	"sync"
)

// conversationLocks serializes invocations and execution recovery that target
// the same conversation within one Runtime. It is an in-process guard only: the
// ConversationStore revision check and the ExecutionStore compare-and-swap
// remain the cross-process correctness mechanisms. Entries are reference-counted
// and evicted when idle, so the map tracks concurrent conversations, not the
// lifetime total.
type conversationLocks struct {
	mu    sync.Mutex
	locks map[string]*conversationLock
}

// conversationLock is one conversation's mutex plus a ref count guarded by
// conversationLocks.mu.
type conversationLock struct {
	held chan struct{} // capacity 1: a send acquires, a receive releases
	refs int
}

// convLockHandle is an acquired conversation lock; release it exactly once.
type convLockHandle struct {
	locks *conversationLocks
	key   string
	lock  *conversationLock
}

// acquire locks conversationID without a cancellation bound.
func (l *conversationLocks) acquire(conversationID string) convLockHandle {
	h, _ := l.acquireCtx(context.Background(), conversationID)
	return h
}

// acquireCtx locks conversationID, giving up with ctx's error when ctx ends
// while waiting. It records a reference so the entry is not evicted while held
// or awaited; a successful handle must be released exactly once.
func (l *conversationLocks) acquireCtx(ctx context.Context, conversationID string) (convLockHandle, error) {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]*conversationLock)
	}
	entry, ok := l.locks[conversationID]
	if !ok {
		entry = &conversationLock{held: make(chan struct{}, 1)}
		l.locks[conversationID] = entry
	}
	// Count this waiter under the map lock so a concurrent release cannot evict
	// the entry before we acquire it below.
	entry.refs++
	l.mu.Unlock()

	h := convLockHandle{locks: l, key: conversationID, lock: entry}
	select {
	case entry.held <- struct{}{}:
		return h, nil
	default:
	}
	select {
	case entry.held <- struct{}{}:
		return h, nil
	case <-ctx.Done():
		h.drop()
		return convLockHandle{}, ctx.Err()
	}
}

// release unlocks the conversation mutex and drops the reference, evicting the
// entry when idle.
func (h convLockHandle) release() {
	if h.lock == nil {
		return
	}
	<-h.lock.held
	h.drop()
}

// drop releases the reference taken by acquireCtx, evicting an idle entry.
func (h convLockHandle) drop() {
	h.locks.mu.Lock()
	h.lock.refs--
	if h.lock.refs == 0 {
		// Delete only the entry we counted; a racing acquire may have replaced it.
		if current, ok := h.locks.locks[h.key]; ok && current == h.lock {
			delete(h.locks.locks, h.key)
		}
	}
	h.locks.mu.Unlock()
}
