package agentcore

import (
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/aws/aws-sdk-go-v2/aws"
)

// RuntimeOption configures the Runtime.
type RuntimeOption func(*runtimeConfig) error

// runtimeConfig holds all configuration for the Runtime.
type runtimeConfig struct {
	awsCfg            *aws.Config
	agentName         string
	heartbeatInterval time.Duration
	shutdownTimeout   time.Duration
	streaming         bool
	maxConcurrency    int
	autoConversation  bool
	a2aCard           *a2a.AgentCard // non-nil when WithA2A is configured
	a2aAddr           string         // listen address for A2A HTTP server (default ":8080")
}

// defaultRuntimeConfig returns a runtimeConfig with sensible defaults applied.
func defaultRuntimeConfig() runtimeConfig {
	return runtimeConfig{
		heartbeatInterval: 5 * time.Second,
		shutdownTimeout:   30 * time.Second,
		streaming:         true,
		maxConcurrency:    10,
		a2aAddr:           ":8080",
	}
}

// WithAWSConfig supplies an explicit AWS configuration to the Runtime,
// bypassing the default environment-based config loading.
func WithAWSConfig(cfg aws.Config) RuntimeOption {
	return func(c *runtimeConfig) error {
		c.awsCfg = &cfg
		return nil
	}
}

// WithAgentName sets the agent name used during worker registration with AgentCore.
func WithAgentName(name string) RuntimeOption {
	return func(c *runtimeConfig) error {
		c.agentName = name
		return nil
	}
}

// WithHeartbeatInterval configures the frequency at which the Runtime sends
// heartbeat signals to AgentCore. The duration must be positive.
func WithHeartbeatInterval(d time.Duration) RuntimeOption {
	return func(c *runtimeConfig) error {
		if d <= 0 {
			return ErrHeartbeatInterval
		}
		c.heartbeatInterval = d
		return nil
	}
}

// WithShutdownTimeout configures the maximum time the Runtime waits for
// in-flight events to complete during graceful shutdown. The duration must be positive.
func WithShutdownTimeout(d time.Duration) RuntimeOption {
	return func(c *runtimeConfig) error {
		if d <= 0 {
			return ErrShutdownTimeout
		}
		c.shutdownTimeout = d
		return nil
	}
}

// WithStreaming enables or disables streamed response submission to AgentCore.
// When enabled, the Runtime uses Agent.InvokeStream and submits chunks incrementally.
// When disabled, the Runtime uses Agent.Invoke and submits complete responses.
func WithStreaming(enabled bool) RuntimeOption {
	return func(c *runtimeConfig) error {
		c.streaming = enabled
		return nil
	}
}

// WithMaxConcurrency sets the maximum number of events that may be processed
// concurrently across different sessions. The default is 10.
func WithMaxConcurrency(n int) RuntimeOption {
	return func(c *runtimeConfig) error {
		c.maxConcurrency = n
		return nil
	}
}

// WithAutoConversation configures the Runtime to automatically create an
// AgentCore Conversation store and wire it onto the agent's invocation context
// for each event, mapping the AgentCore session ID to the conversation ID.
func WithAutoConversation() RuntimeOption {
	return func(c *runtimeConfig) error {
		c.autoConversation = true
		return nil
	}
}
