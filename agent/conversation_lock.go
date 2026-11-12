package agent

import "sync"

// conversationLocks serializes invocations and execution recovery that target
// the same conversation within one Runtime. It is an in-process guard only: the
// ConversationStore revision check and the ExecutionStore compare-and-swap
// remain the cross-process correctness mechanisms.
//
// Entries are reference-counted and evicted once no invocation holds or waits
// on them, so a long-lived Runtime serving high-cardinality conversation IDs
// does not accumulate one mutex per distinct ID for its whole lifetime. The map
// size tracks concurrent in-flight conversations, not the lifetime total.
type conversationLocks struct {
	mu    sync.Mutex
	locks map[string]*conversationLock
}

// conversationLock is one conversation's mutex plus the number of goroutines
// currently holding or waiting on it. refs is guarded by conversationLocks.mu.
type conversationLock struct {
	mu   sync.Mutex
	refs int
}

// convLockHandle is an acquired conversation lock. Release unlocks it and drops
// the reference, evicting the entry when it reaches zero. A handle is used
// exactly once.
type convLockHandle struct {
	locks *conversationLocks
	key   string
	lock  *conversationLock
}

// acquire locks the mutex for conversationID, allocating it on first use and
// recording a reference so it is not evicted while held or awaited. The
// returned handle must be released exactly once.
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
	// Count this waiter before releasing the map lock so a concurrent release
	// cannot evict the entry between here and our Lock below.
	entry.refs++
	l.mu.Unlock()

	entry.mu.Lock()
	return convLockHandle{locks: l, key: conversationID, lock: entry}
}

// release unlocks the conversation mutex and drops the reference, evicting the
// entry from the map when no goroutine holds or waits on it.
func (h convLockHandle) release() {
	if h.lock == nil {
		return
	}
	h.lock.mu.Unlock()
	h.locks.mu.Lock()
	h.lock.refs--
	if h.lock.refs == 0 {
		// Only delete the entry we counted: a racing acquire that allocated a
		// fresh entry would have a positive refs and must not be removed.
		if current, ok := h.locks.locks[h.key]; ok && current == h.lock {
			delete(h.locks.locks, h.key)
		}
	}
	h.locks.mu.Unlock()
}
