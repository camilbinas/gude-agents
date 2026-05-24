package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol"
)

// BundleConfig is the resolved key/value configuration of a single component
// from an AgentCore configuration bundle version. The keys are the component's
// configuration field names (e.g. "system_prompt", "model_id") and the values
// are JSON-decoded scalars or nested structures.
//
// Use the typed accessors (String, Int, Bool, Float, Map, Raw) for safe
// extraction.
type BundleConfig map[string]any

// String returns the string value for key, or an empty string if missing or of
// a different type.
func (b BundleConfig) String(key string) string {
	v, _ := b[key].(string)
	return v
}

// Int returns the int value for key, or 0 if missing or of a different type.
// JSON numbers decode to float64; this method coerces those to int when they
// have no fractional part.
func (b BundleConfig) Int(key string) int {
	switch v := b[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return 0
}

// Float returns the float64 value for key, or 0 if missing or of a different type.
func (b BundleConfig) Float(key string) float64 {
	switch v := b[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
}

// Bool returns the bool value for key, or false if missing or of a different type.
func (b BundleConfig) Bool(key string) bool {
	v, _ := b[key].(bool)
	return v
}

// Map returns the nested map value for key, or nil if missing or not a map.
func (b BundleConfig) Map(key string) map[string]any {
	v, _ := b[key].(map[string]any)
	return v
}

// Raw returns the raw value for key, or nil if missing.
func (b BundleConfig) Raw(key string) any {
	return b[key]
}

// BundleRef identifies a specific configuration bundle version.
// This is the resolved form of the W3C baggage propagated by the AgentCore
// gateway during A/B testing.
type BundleRef struct {
	// BundleARN is the full ARN of the bundle. Used as a logical identifier;
	// the BundleClient extracts the bundle ID from the ARN suffix.
	BundleARN string
	// VersionID is the version (typically a UUID) selected by the gateway
	// for this request.
	VersionID string
}

// IsZero reports whether ref has neither an ARN nor a version set.
func (r BundleRef) IsZero() bool { return r.BundleARN == "" && r.VersionID == "" }

// configBundleClient is the narrow interface BundleClient depends on. The
// concrete *bedrockagentcorecontrol.Client satisfies it; tests provide a
// stub implementation.
type configBundleClient interface {
	GetConfigurationBundleVersion(
		ctx context.Context,
		params *bedrockagentcorecontrol.GetConfigurationBundleVersionInput,
		optFns ...func(*bedrockagentcorecontrol.Options),
	) (*bedrockagentcorecontrol.GetConfigurationBundleVersionOutput, error)
}

var _ configBundleClient = (*bedrockagentcorecontrol.Client)(nil)

// BundleClient retrieves and caches AgentCore configuration bundle versions.
// It is safe for concurrent use; the cache is bounded by Capacity (default 64
// entries) and evicts the oldest entry when full.
//
// Each cache entry maps a (bundleARN, versionID) pair to the resolved
// BundleConfig for the component identified by ComponentID (the runtime ARN).
type BundleClient struct {
	client      configBundleClient
	componentID string
	capacity    int

	mu    sync.Mutex
	cache map[string]BundleConfig
	order []string // insertion order; oldest first
}

// BundleClientOption configures a BundleClient.
type BundleClientOption func(*BundleClient)

// WithBundleAWSConfig supplies an explicit AWS configuration. If omitted, the
// default AWS config is loaded from the environment.
func WithBundleAWSConfig(cfg aws.Config) BundleClientOption {
	return func(c *BundleClient) {
		c.client = bedrockagentcorecontrol.NewFromConfig(cfg)
	}
}

// WithBundleCacheSize sets the LRU cache size. Must be >= 1; values < 1 are
// silently ignored.
func WithBundleCacheSize(n int) BundleClientOption {
	return func(c *BundleClient) {
		if n >= 1 {
			c.capacity = n
		}
	}
}

// withBundleClient is an internal option used by tests to inject a stub
// client.
func withBundleClient(client configBundleClient) BundleClientOption {
	return func(c *BundleClient) {
		c.client = client
	}
}

// NewBundleClient constructs a BundleClient that resolves configuration for
// the given component identifier. The component ID is typically the runtime
// ARN (e.g. arn:aws:bedrock-agentcore:...:runtime/MyAgent-...) since each
// runtime is one component within a bundle.
func NewBundleClient(componentID string, opts ...BundleClientOption) (*BundleClient, error) {
	if componentID == "" {
		return nil, errors.New("agentcore bundle: componentID is required")
	}

	bc := &BundleClient{
		componentID: componentID,
		capacity:    64,
		cache:       make(map[string]BundleConfig),
	}
	for _, opt := range opts {
		opt(bc)
	}

	if bc.client == nil {
		cfg, err := awsconfig.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("agentcore bundle: loading AWS config: %w", err)
		}
		bc.client = bedrockagentcorecontrol.NewFromConfig(cfg)
	}

	return bc, nil
}

// Resolve fetches the configuration for the given bundle reference, returning
// the component's BundleConfig. Repeated calls for the same (ARN, version)
// pair hit the cache.
//
// If the ref is zero or the component is not present in the bundle, Resolve
// returns an empty BundleConfig and a nil error. This matches the AWS SDK
// semantics — agents should fall back to defaults when no override is in
// effect.
func (c *BundleClient) Resolve(ctx context.Context, ref BundleRef) (BundleConfig, error) {
	if c == nil || ref.IsZero() {
		return BundleConfig{}, nil
	}

	cacheKey := ref.BundleARN + "|" + ref.VersionID
	if cfg, ok := c.lookup(cacheKey); ok {
		return cfg, nil
	}

	bundleID, err := bundleIDFromARN(ref.BundleARN)
	if err != nil {
		return nil, fmt.Errorf("agentcore bundle: %w", err)
	}

	out, err := c.client.GetConfigurationBundleVersion(ctx, &bedrockagentcorecontrol.GetConfigurationBundleVersionInput{
		BundleId:  &bundleID,
		VersionId: &ref.VersionID,
	})
	if err != nil {
		return nil, fmt.Errorf("agentcore bundle: GetConfigurationBundleVersion: %w", err)
	}

	cfg := extractComponentConfig(out, c.componentID)
	c.store(cacheKey, cfg)
	return cfg, nil
}

func (c *BundleClient) lookup(key string) (BundleConfig, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg, ok := c.cache[key]
	return cfg, ok
}

func (c *BundleClient) store(key string, cfg BundleConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.cache[key]; ok {
		c.cache[key] = cfg
		return
	}
	if len(c.order) >= c.capacity {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.cache, oldest)
	}
	c.cache[key] = cfg
	c.order = append(c.order, key)
}

// bundleIDFromARN extracts the bundle ID from a bundle ARN. The format is:
//
//	arn:aws:bedrock-agentcore:REGION:ACCOUNT:configuration-bundle/BUNDLE_ID
func bundleIDFromARN(arn string) (string, error) {
	if arn == "" {
		return "", errors.New("empty bundle ARN")
	}
	idx := strings.LastIndex(arn, "/")
	if idx < 0 || idx == len(arn)-1 {
		return "", fmt.Errorf("malformed bundle ARN %q", arn)
	}
	return arn[idx+1:], nil
}

// extractComponentConfig pulls the configuration for the requested component
// out of the bundle response. Returns an empty BundleConfig if the component
// is not present.
func extractComponentConfig(out *bedrockagentcorecontrol.GetConfigurationBundleVersionOutput, componentID string) BundleConfig {
	if out == nil || out.Components == nil {
		return BundleConfig{}
	}
	comp, ok := out.Components[componentID]
	if !ok {
		return BundleConfig{}
	}
	if comp.Configuration == nil {
		return BundleConfig{}
	}

	raw, err := comp.Configuration.MarshalSmithyDocument()
	if err != nil {
		return BundleConfig{}
	}
	var cfg BundleConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return BundleConfig{}
	}
	if cfg == nil {
		return BundleConfig{}
	}
	return cfg
}
