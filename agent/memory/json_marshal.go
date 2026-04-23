package memory

import (
	"encoding/json"

	"github.com/camilbinas/gude-agents/agent"
)

// jsonContentBlock is the JSON envelope for an agent.ContentBlock with a type
// discriminator field. Used by the Redis, blob, and DynamoDB memory drivers.
type jsonContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	// Image fields (populated when Type == "image").
	ImageData     []byte `json:"image_data,omitempty"`      // raw image bytes; mutually exclusive with ImageBase64
	ImageBase64   string `json:"image_base64,omitempty"`    // pre-encoded base64 string; mutually exclusive with ImageData
	ImageMIMEType string `json:"image_mime_type,omitempty"` // one of image/jpeg, image/png, image/gif, image/webp
}

// jsonMessage is the JSON envelope for an agent.Message.
type jsonMessage struct {
	Role    string             `json:"role"`
	Content []jsonContentBlock `json:"content"`
}

// MarshalMessages converts a slice of agent.Message values to JSON.
func MarshalMessages(messages []agent.Message) ([]byte, error) {
	jmsgs := make([]jsonMessage, len(messages))
	for i, msg := range messages {
		blocks := make([]jsonContentBlock, len(msg.Content))
		for j, cb := range msg.Content {
			blocks[j] = ContentBlockToJSON(cb)
		}
		jmsgs[i] = jsonMessage{
			Role:    string(msg.Role),
			Content: blocks,
		}
	}
	return json.Marshal(jmsgs)
}

// UnmarshalMessages decodes JSON into a slice of agent.Message values.
func UnmarshalMessages(data []byte) ([]agent.Message, error) {
	var jmsgs []jsonMessage
	if err := json.Unmarshal(data, &jmsgs); err != nil {
		return nil, err
	}
	messages := make([]agent.Message, len(jmsgs))
	for i, jm := range jmsgs {
		blocks := make([]agent.ContentBlock, len(jm.Content))
		for j, jcb := range jm.Content {
			blocks[j] = JSONToContentBlock(jcb)
		}
		messages[i] = agent.Message{
			Role:    agent.Role(jm.Role),
			Content: blocks,
		}
	}
	return messages, nil
}

// ContentBlockToJSON converts an agent.ContentBlock to its JSON envelope
// representation.
func ContentBlockToJSON(cb agent.ContentBlock) jsonContentBlock {
	switch b := cb.(type) {
	case agent.TextBlock:
		return jsonContentBlock{Type: "text", Text: b.Text}
	case agent.ToolUseBlock:
		return jsonContentBlock{Type: "tool_use", ToolUseID: b.ToolUseID, Name: b.Name, Input: b.Input}
	case agent.ToolResultBlock:
		return jsonContentBlock{Type: "tool_result", ToolUseID: b.ToolUseID, Content: b.Content, IsError: b.IsError}
	case agent.ImageBlock:
		return jsonContentBlock{
			Type:          "image",
			ImageData:     b.Source.Data,
			ImageBase64:   b.Source.Base64,
			ImageMIMEType: b.Source.MIMEType,
		}
	default:
		return jsonContentBlock{Type: "unknown"}
	}
}

// JSONToContentBlock converts a JSON envelope back to an agent.ContentBlock.
func JSONToContentBlock(jcb jsonContentBlock) agent.ContentBlock {
	switch jcb.Type {
	case "text":
		return agent.TextBlock{Text: jcb.Text}
	case "tool_use":
		return agent.ToolUseBlock{ToolUseID: jcb.ToolUseID, Name: jcb.Name, Input: jcb.Input}
	case "tool_result":
		return agent.ToolResultBlock{ToolUseID: jcb.ToolUseID, Content: jcb.Content, IsError: jcb.IsError}
	case "image":
		return agent.ImageBlock{
			Source: agent.ImageSource{
				Data:     jcb.ImageData,
				Base64:   jcb.ImageBase64,
				MIMEType: jcb.ImageMIMEType,
			},
		}
	default:
		return agent.TextBlock{}
	}
}
