package agent

import "sync"

// conversationLocks serializes invocations and execution recovery that target
// the same conversation within one Agent. It is an in-process guard only: the
// ConversationStore revision check and the ExecutionStore compare-and-swap
// remain the cross-process correctness mechanisms.
type conversationLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// lockFor returns the mutex for conversationID, allocating it on first use.
// Entries are never deleted; memory growth is bounded by the number of
// distinct conversation IDs the Agent serves over its lifetime.
func (l *conversationLocks) lockFor(conversationID string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.locks == nil {
		l.locks = make(map[string]*sync.Mutex)
	}
	m, ok := l.locks[conversationID]
	if !ok {
		m = &sync.Mutex{}
		l.locks[conversationID] = m
	}
	return m
}
