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
	recent, current := m.projectedCanonical(persistedCount)
	out := make([]Message, 0, len(recent)+len(m.transient)+len(current))
	out = append(out, recent...)
	out = append(out, m.transient...)
	return append(out, current...)
}

// projectedCanonical returns disposable model-facing views of persisted and
// current canonical messages. Tool result runs are normalized independently so
// the context-manager boundary remains intact; normal execution never splits
// an active ToolUse batch across that boundary.
func (m turnMessages) projectedCanonical(persistedCount int) (recent, current []Message) {
	if persistedCount < 0 || persistedCount > len(m.canonical) {
		persistedCount = len(m.canonical)
	}
	return projectToolResultOrder(m.canonical[:persistedCount]), projectToolResultOrder(m.canonical[persistedCount:])
}

// projectToolResultOrder groups the contiguous ToolResult messages following
// a ToolUse batch into one model-only message, ordered by the original ToolUse
// blocks. It never mutates canonical messages or invents missing results.
// Results with an unrecognized ID are retained after known results in their
// original relative order so an invalid transcript is not silently changed.
func projectToolResultOrder(messages []Message) []Message {
	out := make([]Message, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		message := messages[i]
		out = append(out, Message{Role: message.Role, Content: append([]ContentBlock(nil), message.Content...)})
		if message.Role != RoleAssistant {
			continue
		}

		order := make([]string, 0, len(message.Content))
		known := make(map[string]struct{}, len(message.Content))
		for _, block := range message.Content {
			use, ok := block.(ToolUseBlock)
			if !ok {
				continue
			}
			if _, duplicate := known[use.ToolUseID]; !duplicate {
				order = append(order, use.ToolUseID)
				known[use.ToolUseID] = struct{}{}
			}
		}
		if len(order) == 0 {
			continue
		}

		j := i + 1
		byID := make(map[string][]ContentBlock, len(order))
		var unknown []ContentBlock
		for j < len(messages) && messages[j].Role == RoleUser && onlyToolResults(messages[j].Content) {
			for _, block := range messages[j].Content {
				result := block.(ToolResultBlock)
				if _, ok := known[result.ToolUseID]; ok {
					byID[result.ToolUseID] = append(byID[result.ToolUseID], result)
				} else {
					unknown = append(unknown, result)
				}
			}
			j++
		}
		if j == i+1 {
			continue
		}

		results := make([]ContentBlock, 0)
		for _, id := range order {
			results = append(results, byID[id]...)
		}
		results = append(results, unknown...)
		out = append(out, Message{Role: RoleUser, Content: results})
		i = j - 1
	}
	return out
}
