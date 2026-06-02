package conversation

import (
	"encoding/json"

	"github.com/camilbinas/gude-agents/agent"
)

// jsonHandoffRequest is the JSON envelope for an agent.HandoffRequest.
// The Messages field uses the same type-discriminated encoding as all
// other durable conversation stores.
type jsonHandoffRequest struct {
	Reason         string        `json:"reason"`
	Question       string        `json:"question"`
	ConversationID string        `json:"conversation_id"`
	Messages       []jsonMessage `json:"messages"`
}

// MarshalHandoffRequest serialises a HandoffRequest to JSON.
// The Messages slice is encoded with full type-discriminated ContentBlock
// envelopes, making the result safe to store in Redis, Postgres, or DynamoDB
// and restore in a different process.
func MarshalHandoffRequest(hr *agent.HandoffRequest) ([]byte, error) {
	jmsgs := make([]jsonMessage, len(hr.Messages))
	for i, msg := range hr.Messages {
		blocks := make([]jsonContentBlock, len(msg.Content))
		for j, cb := range msg.Content {
			blocks[j] = contentBlockToJSON(cb)
		}
		jmsgs[i] = jsonMessage{
			Role:    string(msg.Role),
			Content: blocks,
		}
	}
	return json.Marshal(jsonHandoffRequest{
		Reason:         hr.Reason,
		Question:       hr.Question,
		ConversationID: hr.ConversationID,
		Messages:       jmsgs,
	})
}

// UnmarshalHandoffRequest deserialises JSON produced by MarshalHandoffRequest
// back into a HandoffRequest.
func UnmarshalHandoffRequest(data []byte) (*agent.HandoffRequest, error) {
	var j jsonHandoffRequest
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, err
	}
	messages := make([]agent.Message, len(j.Messages))
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
	return &agent.HandoffRequest{
		Reason:         j.Reason,
		Question:       j.Question,
		ConversationID: j.ConversationID,
		Messages:       messages,
	}, nil
}
