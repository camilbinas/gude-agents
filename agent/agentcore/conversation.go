package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
)

// Compile-time interface check.
var _ agent.Conversation = (*Conversation)(nil)

// conversationPayload is the event payload stored in AgentCore Memory.
type conversationPayload struct {
	Version  int    `json:"v"`        // schema version for forward compat
	Messages string `json:"messages"` // JSON-encoded []agent.Message
}

// Conversation implements agent.Conversation using AgentCore's Memory service.
type Conversation struct {
	client   agentCoreClient
	memoryID string
	actorID  string
}

// NewConversation creates an AgentCore-backed conversation store.
func NewConversation(opts ...ConversationOption) (*Conversation, error) {
	var cfg conversationConfig

	// Apply all options, returning the first error encountered.
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}

	// Load default AWS config if none was provided via options.
	if cfg.awsCfg == nil {
		awsCfg, err := config.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("agentcore conversation: loading AWS config: %w", err)
		}
		cfg.awsCfg = &awsCfg
	}

	// Construct the AgentCore client from the AWS config.
	client := bedrockagentcore.NewFromConfig(*cfg.awsCfg)

	return &Conversation{
		client:   client,
		memoryID: cfg.memoryID,
		actorID:  cfg.actorID,
	}, nil
}

// Save persists messages for the given conversation ID.
// It serializes messages into a conversationPayload (version 1) and creates
// an event in AgentCore Memory with the session ID set to the conversation ID.
func (c *Conversation) Save(ctx context.Context, conversationID string, messages []agent.Message) error {
	// Marshal messages using the shared conversation serialization helpers.
	msgData, err := conversation.MarshalMessages(messages)
	if err != nil {
		return newConversationErrorf("save", err)
	}

	// Build the versioned payload envelope.
	payload := conversationPayload{
		Version:  1,
		Messages: string(msgData),
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return newConversationErrorf("save", err)
	}

	// Create the event in AgentCore Memory.
	now := time.Now()
	_, err = c.client.CreateEvent(ctx, &bedrockagentcore.CreateEventInput{
		MemoryId:       aws.String(c.memoryID),
		ActorId:        aws.String(c.actorID),
		SessionId:      aws.String(conversationID),
		EventTimestamp: &now,
		Payload: []types.PayloadType{
			&types.PayloadTypeMemberBlob{
				Value: document.NewLazyDocument(string(payloadJSON)),
			},
		},
	})
	if err != nil {
		return newConversationErrorf("save", err)
	}

	return nil
}

// Load retrieves messages for the given conversation ID.
// It lists events for the session, deserializes the latest conversationPayload,
// and returns the messages. Returns an empty non-nil slice for unknown IDs.
func (c *Conversation) Load(ctx context.Context, conversationID string) ([]agent.Message, error) {
	includePayloads := true
	out, err := c.client.ListEvents(ctx, &bedrockagentcore.ListEventsInput{
		MemoryId:        aws.String(c.memoryID),
		ActorId:         aws.String(c.actorID),
		SessionId:       aws.String(conversationID),
		IncludePayloads: &includePayloads,
	})
	if err != nil {
		return nil, newConversationErrorf("load", err)
	}

	// No events found — return empty non-nil slice.
	if len(out.Events) == 0 {
		return []agent.Message{}, nil
	}

	// Find the latest event (last in the list).
	latest := out.Events[len(out.Events)-1]

	// Extract the blob payload from the event.
	if len(latest.Payload) == 0 {
		return []agent.Message{}, nil
	}

	blob, ok := latest.Payload[0].(*types.PayloadTypeMemberBlob)
	if !ok {
		return []agent.Message{}, nil
	}

	// Marshal the document to JSON bytes (works for both documentMarshaler and
	// documentUnmarshaler implementations).
	docBytes, err := blob.Value.MarshalSmithyDocument()
	if err != nil {
		return nil, newConversationErrorf("load", err)
	}

	// The document contains a JSON-encoded string. Strip the outer quotes
	// to get the raw payload JSON string.
	var payloadStr string
	if err := json.Unmarshal(docBytes, &payloadStr); err != nil {
		return nil, newConversationErrorf("load", err)
	}

	// Unmarshal the conversationPayload envelope.
	var payload conversationPayload
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		return nil, newConversationErrorf("load", err)
	}

	// Unmarshal the messages from the payload.
	messages, err := conversation.UnmarshalMessages([]byte(payload.Messages))
	if err != nil {
		return nil, newConversationErrorf("load", err)
	}

	return messages, nil
}

// List returns all conversation (session) IDs in the store.
func (c *Conversation) List(ctx context.Context) ([]string, error) {
	var ids []string
	var nextToken *string

	for {
		out, err := c.client.ListSessions(ctx, &bedrockagentcore.ListSessionsInput{
			MemoryId:  aws.String(c.memoryID),
			ActorId:   aws.String(c.actorID),
			NextToken: nextToken,
		})
		if err != nil {
			return nil, newConversationErrorf("list", err)
		}

		for _, s := range out.SessionSummaries {
			if s.SessionId != nil {
				ids = append(ids, *s.SessionId)
			}
		}

		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	return ids, nil
}

// Delete removes a conversation by ID. Returns nil if not found.
func (c *Conversation) Delete(ctx context.Context, conversationID string) error {
	_, err := c.client.StopRuntimeSession(ctx, &bedrockagentcore.StopRuntimeSessionInput{
		RuntimeSessionId: aws.String(conversationID),
	})
	if err != nil {
		// If the session is not found (404), return nil per the contract.
		if is404(err) {
			return nil
		}
		return newConversationErrorf("delete", err)
	}

	return nil
}
