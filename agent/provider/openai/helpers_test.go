package openai

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"

	openaisdk "github.com/openai/openai-go/v3"
)

// mustBuildParams calls p.buildParams and fails the test on error. buildParams
// now returns an error alongside the params so attachment decode failures
// surface instead of being dropped.
func mustBuildParams(t *testing.T, p *OpenAIProvider, req agent.ModelRequest) openaisdk.ChatCompletionNewParams {
	t.Helper()
	params, err := p.buildParams(req)
	if err != nil {
		t.Fatalf("buildParams returned error: %v", err)
	}
	return params
}

// mustUserMessages calls toOpenAIUserMessages and fails the test on error.
func mustUserMessages(t *testing.T, blocks []agent.ContentBlock) []openaisdk.ChatCompletionMessageParamUnion {
	t.Helper()
	out, err := toOpenAIUserMessages(blocks)
	if err != nil {
		t.Fatalf("toOpenAIUserMessages returned error: %v", err)
	}
	return out
}
