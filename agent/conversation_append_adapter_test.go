package agent

// Append adapters keep retained test doubles focused on their behavior.
// Production ConversationStore has no snapshot Save fallback.
import "context"

func rangeForTest(load func(context.Context, string) (ConversationSnapshot, error), ctx context.Context, id string, after uint64) (ConversationSnapshot, error) {
	snap, err := load(ctx, id)
	if after > uint64(len(snap.Messages)) {
		after = uint64(len(snap.Messages))
	}
	snap.Messages = snap.Messages[after:]
	snap.LastSequence = uint64(len(snap.Messages)) + after
	return snap, err
}
func appendForTest(load func(context.Context, string) (ConversationSnapshot, error), save func(context.Context, string, []Message, uint64) (uint64, error), ctx context.Context, id string, msgs []Message, rev uint64) (ConversationCursor, error) {
	snap, err := load(ctx, id)
	if err != nil {
		return ConversationCursor{}, err
	}
	if snap.Revision != rev {
		return ConversationCursor{}, ErrConversationConflict
	}
	if len(msgs) == 0 {
		return ConversationCursor{Revision: rev, LastSequence: uint64(len(snap.Messages))}, nil
	}
	next, err := save(ctx, id, append(snap.Messages, msgs...), rev)
	return ConversationCursor{Revision: next, LastSequence: uint64(len(snap.Messages) + len(msgs))}, err
}
func (s *testMemoryStore) LoadAfter(c context.Context, id string, a uint64) (ConversationSnapshot, error) {
	return rangeForTest(s.Load, c, id, a)
}
func (s *testMemoryStore) Append(c context.Context, id string, m []Message, r uint64) (ConversationCursor, error) {
	return appendForTest(s.Load, s.Save, c, id, m, r)
}
func (f failingMemory) LoadAfter(c context.Context, id string, a uint64) (ConversationSnapshot, error) {
	return f.Load(c, id)
}
func (f failingMemory) Append(c context.Context, id string, m []Message, r uint64) (ConversationCursor, error) {
	return appendForTest(f.Load, f.Save, c, id, m, r)
}
func (w *trackingFlusher) LoadAfter(c context.Context, id string, a uint64) (ConversationSnapshot, error) {
	return rangeForTest(w.Load, c, id, a)
}
func (w *trackingFlusher) Append(c context.Context, id string, m []Message, r uint64) (ConversationCursor, error) {
	return appendForTest(w.Load, w.Save, c, id, m, r)
}
func (r *recordingConversation) LoadAfter(c context.Context, id string, a uint64) (ConversationSnapshot, error) {
	return rangeForTest(r.Load, c, id, a)
}
func (r *recordingConversation) Append(c context.Context, id string, x []Message, v uint64) (ConversationCursor, error) {
	return appendForTest(r.Load, r.Save, c, id, x, v)
}
func (t *trackingConversation) LoadAfter(c context.Context, id string, a uint64) (ConversationSnapshot, error) {
	return rangeForTest(t.Load, c, id, a)
}
func (t *trackingConversation) Append(c context.Context, id string, x []Message, v uint64) (ConversationCursor, error) {
	return appendForTest(t.Load, t.Save, c, id, x, v)
}
func (f *failingSaveConversation) LoadAfter(c context.Context, id string, a uint64) (ConversationSnapshot, error) {
	return f.Load(c, id)
}
func (f *failingSaveConversation) Append(c context.Context, id string, m []Message, r uint64) (ConversationCursor, error) {
	return appendForTest(f.Load, f.Save, c, id, m, r)
}
func (r *recordingMemory) LoadAfter(c context.Context, id string, a uint64) (ConversationSnapshot, error) {
	return rangeForTest(r.Load, c, id, a)
}
func (r *recordingMemory) Append(c context.Context, id string, m []Message, v uint64) (ConversationCursor, error) {
	return appendForTest(r.Load, r.Save, c, id, m, v)
}
func (s *inMemoryStore) LoadAfter(c context.Context, id string, a uint64) (ConversationSnapshot, error) {
	return rangeForTest(s.Load, c, id, a)
}
func (s *inMemoryStore) Append(c context.Context, id string, m []Message, v uint64) (ConversationCursor, error) {
	return appendForTest(s.Load, s.Save, c, id, m, v)
}

func newMemConversation() *testMemoryStore { return newTestMemoryStore() }
