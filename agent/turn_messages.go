package agent

// turnMessages separates append-only canonical history from transient context
// supplied only to the provider (for example, retrieved RAG documents).
// Transient messages are intentionally never accepted by persistence helpers.
type turnMessages struct {
	canonical []Message
	transient []Message
}

func (m *turnMessages) appendCanonical(messages ...Message) {
	m.canonical = append(m.canonical, messages...)
}

// providerMessages keeps transient context immediately before the current
// canonical delta while leaving the persisted transcript structurally separate.
func (m turnMessages) providerMessages(persistedCount int) []Message {
	if persistedCount < 0 || persistedCount > len(m.canonical) {
		persistedCount = len(m.canonical)
	}
	out := make([]Message, 0, len(m.canonical)+len(m.transient))
	out = append(out, m.canonical[:persistedCount]...)
	out = append(out, m.transient...)
	return append(out, m.canonical[persistedCount:]...)
}
