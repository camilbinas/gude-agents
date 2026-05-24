package agentcore

import "github.com/aws/aws-sdk-go-v2/aws"

// ConversationOption configures the Conversation store.
type ConversationOption func(*conversationConfig) error

// conversationConfig holds all configuration for the AgentCore Conversation store.
type conversationConfig struct {
	awsCfg   *aws.Config
	memoryID string
	actorID  string
}

// WithConversationAWSConfig supplies an explicit AWS configuration to the
// Conversation store, bypassing the default environment-based config loading.
func WithConversationAWSConfig(cfg aws.Config) ConversationOption {
	return func(c *conversationConfig) error {
		c.awsCfg = &cfg
		return nil
	}
}

// WithMemoryID sets the memory ID used to identify the AgentCore Memory service
// instance that backs this conversation store.
func WithMemoryID(id string) ConversationOption {
	return func(c *conversationConfig) error {
		c.memoryID = id
		return nil
	}
}

// WithActorID sets the actor ID used for events created in the AgentCore Memory
// service. This identifies the entity (user or agent) that produced the event.
func WithActorID(id string) ConversationOption {
	return func(c *conversationConfig) error {
		c.actorID = id
		return nil
	}
}
