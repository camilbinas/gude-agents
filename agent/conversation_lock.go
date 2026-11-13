package agent

import "sync"

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
	mu   sync.Mutex
	refs int
}

// convLockHandle is an acquired conversation lock; release it exactly once.
type convLockHandle struct {
	locks *conversationLocks
	key   string
	lock  *conversationLock
}

// acquire locks the mutex for conversationID and records a reference so it is
// not evicted while held; the returned handle must be released exactly once.
func (l *conversationLocks) acquire(conversationID string) convLockHandle {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]*conversationLock)
	}
	entry, ok := l.locks[conversationID]
	if !ok {
		entry = &conversationLock{}
		l.locks[conversationID] = entry
	}
	// Count this waiter under the map lock so a concurrent release cannot evict
	// the entry before we Lock it below.
	entry.refs++
	l.mu.Unlock()

	entry.mu.Lock()
	return convLockHandle{locks: l, key: conversationID, lock: entry}
}

// release unlocks the conversation mutex and drops the reference, evicting the
// entry when idle.
func (h convLockHandle) release() {
	if h.lock == nil {
		return
	}
	h.lock.mu.Unlock()
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
