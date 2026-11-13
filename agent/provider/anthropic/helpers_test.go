package anthropic

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
)

// mustBuildParams calls p.buildParams and fails the test on error.
func mustBuildParams(t *testing.T, p *AnthropicProvider, req agent.ModelRequest) anthropicsdk.MessageNewParams {
	t.Helper()
	params, err := p.buildParams(req)
	if err != nil {
		t.Fatalf("buildParams returned error: %v", err)
	}
	return params
}

// mustContentBlocks calls toAnthropicContentBlocks and fails the test on error.
func mustContentBlocks(t *testing.T, blocks []agent.ContentBlock, role agent.Role, cachingEnabled bool) []anthropicsdk.ContentBlockParamUnion {
	t.Helper()
	out, err := toAnthropicContentBlocks(blocks, role, cachingEnabled)
	if err != nil {
		t.Fatalf("toAnthropicContentBlocks returned error: %v", err)
	}
	return out
}
