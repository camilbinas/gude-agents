package vllm

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// vLLM serves arbitrary models under names the operator chooses, so no model
// name has catalog metadata, including names that match OpenAI models.
func TestVLLMReportsOnlyTheBaselineAndCallerOptions(t *testing.T) {
	baseline := agent.ModelCapabilities{NativeStructuredOutput: agent.Unsupported}
	for _, id := range []string{"mistralai/Mistral-7B-Instruct-v0.2", "gpt-4o", "o3"} {
		p, err := New(id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Capabilities() != baseline {
			t.Fatalf("%s capabilities = %+v, want the baseline", id, p.Capabilities())
		}
	}
	p, err := New("gpt-4o", WithContextWindowTokens(32_768), WithToolUse(agent.Supported))
	if err != nil {
		t.Fatal(err)
	}
	want := baseline
	want.ContextWindowTokens = 32_768
	want.ToolUse = agent.Supported
	if p.Capabilities() != want {
		t.Fatalf("capabilities = %+v, want baseline plus overrides %+v", p.Capabilities(), want)
	}
}
