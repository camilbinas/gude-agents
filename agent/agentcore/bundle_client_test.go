package agentcore

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol/types"
)

// stubBundleClient is a configurable fake for the control-plane bundle API.
type stubBundleClient struct {
	calls atomic.Int64
	resp  *bedrockagentcorecontrol.GetConfigurationBundleVersionOutput
	err   error
}

func (s *stubBundleClient) GetConfigurationBundleVersion(
	_ context.Context,
	_ *bedrockagentcorecontrol.GetConfigurationBundleVersionInput,
	_ ...func(*bedrockagentcorecontrol.Options),
) (*bedrockagentcorecontrol.GetConfigurationBundleVersionOutput, error) {
	s.calls.Add(1)
	return s.resp, s.err
}

func newStubResponse(t *testing.T, componentID string, cfg map[string]any) *bedrockagentcorecontrol.GetConfigurationBundleVersionOutput {
	t.Helper()
	return &bedrockagentcorecontrol.GetConfigurationBundleVersionOutput{
		Components: map[string]types.ComponentConfiguration{
			componentID: {
				Configuration: document.NewLazyDocument(cfg),
			},
		},
	}
}

func TestNewBundleClient_RequiresComponentID(t *testing.T) {
	if _, err := NewBundleClient("", withBundleClient(&stubBundleClient{})); err == nil {
		t.Fatal("expected error for empty componentID")
	}
}

func TestBundleClient_Resolve_ReturnsConfig(t *testing.T) {
	stub := &stubBundleClient{
		resp: newStubResponse(t, "comp-1", map[string]any{
			"system_prompt": "hello",
			"model_id":      "claude",
			"temperature":   0.4,
		}),
	}
	c, err := NewBundleClient("comp-1", withBundleClient(stub))
	if err != nil {
		t.Fatalf("NewBundleClient: %v", err)
	}
	cfg, err := c.Resolve(context.Background(), BundleRef{
		BundleARN: "arn:aws:bedrock-agentcore:us-east-1:1:configuration-bundle/abc",
		VersionID: "v1",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.String("system_prompt") != "hello" {
		t.Errorf("system_prompt = %q, want hello", cfg.String("system_prompt"))
	}
	if cfg.String("model_id") != "claude" {
		t.Errorf("model_id = %q, want claude", cfg.String("model_id"))
	}
	if cfg.Float("temperature") != 0.4 {
		t.Errorf("temperature = %v, want 0.4", cfg.Float("temperature"))
	}
}

func TestBundleClient_Resolve_CachesByVersion(t *testing.T) {
	stub := &stubBundleClient{
		resp: newStubResponse(t, "comp-1", map[string]any{"system_prompt": "x"}),
	}
	c, _ := NewBundleClient("comp-1", withBundleClient(stub))
	ref := BundleRef{BundleARN: "arn:::bundle/b", VersionID: "v1"}

	for i := 0; i < 5; i++ {
		if _, err := c.Resolve(context.Background(), ref); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	}
	if got := stub.calls.Load(); got != 1 {
		t.Errorf("backend called %d times, want 1 (cache should hit)", got)
	}
}

func TestBundleClient_Resolve_DifferentVersionsMissCache(t *testing.T) {
	stub := &stubBundleClient{
		resp: newStubResponse(t, "comp-1", map[string]any{"system_prompt": "x"}),
	}
	c, _ := NewBundleClient("comp-1", withBundleClient(stub))

	for _, v := range []string{"v1", "v2", "v3"} {
		_, err := c.Resolve(context.Background(), BundleRef{BundleARN: "arn:::bundle/b", VersionID: v})
		if err != nil {
			t.Fatalf("Resolve %s: %v", v, err)
		}
	}
	if got := stub.calls.Load(); got != 3 {
		t.Errorf("backend called %d times, want 3 (different versions should miss)", got)
	}
}

func TestBundleClient_Resolve_ZeroRefReturnsEmpty(t *testing.T) {
	stub := &stubBundleClient{}
	c, _ := NewBundleClient("comp-1", withBundleClient(stub))
	cfg, err := c.Resolve(context.Background(), BundleRef{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(cfg) != 0 {
		t.Errorf("expected empty config, got %v", cfg)
	}
	if stub.calls.Load() != 0 {
		t.Error("backend should not be called for zero ref")
	}
}

func TestBundleClient_Resolve_MissingComponentReturnsEmpty(t *testing.T) {
	stub := &stubBundleClient{
		resp: newStubResponse(t, "OTHER-COMPONENT", map[string]any{"system_prompt": "x"}),
	}
	c, _ := NewBundleClient("MY-COMPONENT", withBundleClient(stub))
	cfg, err := c.Resolve(context.Background(), BundleRef{BundleARN: "arn:::bundle/b", VersionID: "v1"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(cfg) != 0 {
		t.Errorf("expected empty config when component missing, got %v", cfg)
	}
}

func TestBundleClient_Resolve_PropagatesError(t *testing.T) {
	stub := &stubBundleClient{err: errors.New("forbidden")}
	c, _ := NewBundleClient("comp", withBundleClient(stub))
	_, err := c.Resolve(context.Background(), BundleRef{BundleARN: "arn:::bundle/b", VersionID: "v1"})
	if err == nil {
		t.Fatal("expected error from backend")
	}
}

func TestBundleClient_Resolve_RejectsMalformedARN(t *testing.T) {
	stub := &stubBundleClient{}
	c, _ := NewBundleClient("comp", withBundleClient(stub))
	_, err := c.Resolve(context.Background(), BundleRef{BundleARN: "not-an-arn", VersionID: "v1"})
	if err == nil {
		t.Fatal("expected error for malformed ARN")
	}
	if stub.calls.Load() != 0 {
		t.Error("backend should not be called for invalid ARN")
	}
}

func TestBundleClient_LRUEvictsOldest(t *testing.T) {
	stub := &stubBundleClient{
		resp: newStubResponse(t, "comp", map[string]any{"system_prompt": "x"}),
	}
	c, _ := NewBundleClient("comp", withBundleClient(stub), WithBundleCacheSize(2))

	// Fill cache with v1, v2.
	_, _ = c.Resolve(context.Background(), BundleRef{BundleARN: "arn:::bundle/b", VersionID: "v1"})
	_, _ = c.Resolve(context.Background(), BundleRef{BundleARN: "arn:::bundle/b", VersionID: "v2"})
	// v3 evicts v1.
	_, _ = c.Resolve(context.Background(), BundleRef{BundleARN: "arn:::bundle/b", VersionID: "v3"})

	beforeRefetch := stub.calls.Load()
	// v2 still cached.
	_, _ = c.Resolve(context.Background(), BundleRef{BundleARN: "arn:::bundle/b", VersionID: "v2"})
	if stub.calls.Load() != beforeRefetch {
		t.Error("v2 should still be cached after eviction of v1")
	}
	// v1 was evicted, expect a fresh call.
	_, _ = c.Resolve(context.Background(), BundleRef{BundleARN: "arn:::bundle/b", VersionID: "v1"})
	if stub.calls.Load() != beforeRefetch+1 {
		t.Error("v1 should be re-fetched after eviction")
	}
}

func TestBundleConfig_TypedAccessors(t *testing.T) {
	cfg := BundleConfig{
		"s":   "hello",
		"i":   float64(42),
		"f":   1.5,
		"b":   true,
		"m":   map[string]any{"x": 1},
		"raw": "anything",
	}
	if cfg.String("s") != "hello" {
		t.Errorf("String = %q", cfg.String("s"))
	}
	if cfg.Int("i") != 42 {
		t.Errorf("Int = %d", cfg.Int("i"))
	}
	if cfg.Float("f") != 1.5 {
		t.Errorf("Float = %v", cfg.Float("f"))
	}
	if !cfg.Bool("b") {
		t.Error("Bool = false")
	}
	if cfg.Map("m")["x"] != 1 {
		t.Errorf("Map = %v", cfg.Map("m"))
	}
	if cfg.Raw("raw") != "anything" {
		t.Errorf("Raw = %v", cfg.Raw("raw"))
	}
	if cfg.String("missing") != "" || cfg.Int("missing") != 0 || cfg.Float("missing") != 0 || cfg.Bool("missing") {
		t.Error("missing keys should return zero values")
	}
}
