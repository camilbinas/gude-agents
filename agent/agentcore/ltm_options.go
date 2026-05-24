package agentcore

import "github.com/aws/aws-sdk-go-v2/aws"

// LTMOption configures the LTMStore.
type LTMOption func(*ltmConfig) error

// ltmConfig holds all configuration for the LTMStore.
type ltmConfig struct {
	awsCfg    *aws.Config
	namespace string
	client    agentCoreClient
}

// WithLTMNamespace configures the namespace that scopes all entries in the
// LTMStore. A namespace provides logical isolation between different agents
// or contexts.
func WithLTMNamespace(ns string) LTMOption {
	return func(c *ltmConfig) error {
		c.namespace = ns
		return nil
	}
}

// WithLTMAWSConfig supplies an explicit AWS configuration to the LTMStore,
// bypassing the default environment-based config loading.
func WithLTMAWSConfig(cfg aws.Config) LTMOption {
	return func(c *ltmConfig) error {
		c.awsCfg = &cfg
		return nil
	}
}

// withLTMClient is an internal option for injecting a mock client in tests.
func withLTMClient(client agentCoreClient) LTMOption {
	return func(c *ltmConfig) error {
		c.client = client
		return nil
	}
}
