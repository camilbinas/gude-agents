package agent

// NormStrategy defines how the normalizer repairs message sequence violations.
type NormStrategy int

const (
	NormMerge  NormStrategy = iota // Merge consecutive same-role messages (default)
	NormFill                       // Insert synthetic opposite-role messages
	NormRemove                     // Keep only the last message in a same-role run
)

// NormalizeMessages repairs a message sequence so that it starts with a user
// message and strictly alternates between user and assistant roles.
// It returns a new slice; the input is never modified.
// If msgs is nil, it returns nil. If msgs is empty, it returns an empty slice.
func NormalizeMessages(msgs []Message, strategy NormStrategy) []Message {
	// Nil input returns nil.
	if msgs == nil {
		return nil
	}

	// Empty input returns empty slice.
	if len(msgs) == 0 {
		return []Message{}
	}

	// Build a working copy starting with opening violation repair.
	var working []Message

	// Opening violation: if the first message is not RoleUser, prepend a synthetic user message.
	if msgs[0].Role != RoleUser {
		working = append(working, Message{
			Role:    RoleUser,
			Content: []ContentBlock{TextBlock{Text: "Continue."}},
		})
	}

	// Copy all input messages into the working slice.
	for _, m := range msgs {
		working = append(working, Message{
			Role:    m.Role,
			Content: append([]ContentBlock(nil), m.Content...),
		})
	}

	// Strategy-specific alternation repair.
	switch strategy {
	case NormMerge:
		working = normMerge(working)
	case NormFill:
		working = normFill(working)
	case NormRemove:
		working = normRemove(working)
	}

	return working
}

// normFill inserts a synthetic opposite-role message between each pair of
// consecutive same-role messages. Synthetic assistant messages use "Understood."
// and synthetic user messages use "Continue.".
func normFill(msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}

	result := []Message{msgs[0]}
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == result[len(result)-1].Role {
			// Same role as previous — insert a synthetic opposite-role message.
			var synthetic Message
			if msgs[i].Role == RoleUser {
				synthetic = Message{
					Role:    RoleAssistant,
					Content: []ContentBlock{TextBlock{Text: "Understood."}},
				}
			} else {
				synthetic = Message{
					Role:    RoleUser,
					Content: []ContentBlock{TextBlock{Text: "Continue."}},
				}
			}
			result = append(result, synthetic)
		}
		result = append(result, msgs[i])
	}
	return result
}

// normMerge merges consecutive same-role messages into a single message,
// combining their Content slices in order. It processes the entire slice
// in a single pass.
func normMerge(msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}

	result := []Message{msgs[0]}
	for i := 1; i < len(msgs); i++ {
		last := &result[len(result)-1]
		if msgs[i].Role == last.Role {
			// Same role as previous — merge content blocks into the existing message.
			last.Content = append(last.Content, msgs[i].Content...)
		} else {
			// Different role — start a new message.
			result = append(result, msgs[i])
		}
	}
	return result
}

// normRemove keeps only the last message in each consecutive run of same-role
// messages, dropping all preceding messages in the run.
func normRemove(msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}

	result := []Message{msgs[0]}
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == result[len(result)-1].Role {
			// Same role as previous — replace the last result message with this one.
			result[len(result)-1] = msgs[i]
		} else {
			// Different role — append as a new message.
			result = append(result, msgs[i])
		}
	}
	return result
}
