package conversation

import (
	"encoding/json"
	"fmt"

	"github.com/camilbinas/gude-agents/agent"
)

// jsonInterrupt is the durable JSON envelope for an agent.Interrupt.
// Unlike the event encoding of agent.Interrupt (which omits Messages), it can
// encode a stateless resumable message snapshot. Stateful interruptions leave
// Messages empty and instead resume through ConversationID, Revision, and
// LastSequence against the canonical ConversationStore.
type jsonInterrupt struct {
	ID             string                   `json:"id"`
	Type           agent.InterruptType      `json:"type"`
	ConversationID string                   `json:"conversation_id,omitempty"`
	Revision       uint64                   `json:"revision"`
	LastSequence   uint64                   `json:"last_sequence"`
	Approval       *agent.ApprovalInterrupt `json:"approval,omitempty"`
	Input          *agent.InputInterrupt    `json:"input,omitempty"`
	Messages       []jsonMessage            `json:"messages"`
}

// MarshalInterrupt serialises an Interrupt to JSON. Stateless interruptions
// include their Messages snapshot; stateful interruptions normally carry an
// empty Messages array because canonical history is reloaded by cursor.
func MarshalInterrupt(in *agent.Interrupt) ([]byte, error) {
	if in == nil {
		return nil, fmt.Errorf("conversation: marshal interrupt: nil interrupt")
	}
	jmsgs := make([]jsonMessage, len(in.Messages))
	for i, msg := range in.Messages {
		blocks := make([]jsonContentBlock, len(msg.Content))
		for j, cb := range msg.Content {
			blocks[j] = contentBlockToJSON(cb)
		}
		jmsgs[i] = jsonMessage{
			Role:    string(msg.Role),
			Content: blocks,
		}
	}
	return json.Marshal(jsonInterrupt{
		ID:             in.ID,
		Type:           in.Type,
		ConversationID: in.ConversationID,
		Revision:       in.Revision,
		LastSequence:   in.LastSequence,
		Approval:       in.Approval,
		Input:          in.Input,
		Messages:       jmsgs,
	})
}

// UnmarshalInterrupt deserialises JSON produced by MarshalInterrupt back into
// an Interrupt.
func UnmarshalInterrupt(data []byte) (*agent.Interrupt, error) {
	var j jsonInterrupt
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, err
	}
	var messages []agent.Message
	if len(j.Messages) > 0 {
		messages = make([]agent.Message, len(j.Messages))
		for i, jm := range j.Messages {
			blocks := make([]agent.ContentBlock, len(jm.Content))
			for k, jcb := range jm.Content {
				blocks[k] = jsonToContentBlock(jcb)
			}
			messages[i] = agent.Message{
				Role:    agent.Role(jm.Role),
				Content: blocks,
			}
		}
	}
	return &agent.Interrupt{
		ID:             j.ID,
		Type:           j.Type,
		ConversationID: j.ConversationID,
		Revision:       j.Revision,
		LastSequence:   j.LastSequence,
		Approval:       j.Approval,
		Input:          j.Input,
		Messages:       messages,
	}, nil
}
