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

// vLLM's OpenAI-compatible server documents response_format json_schema, but
// support is a property of the running server and model, not of a name vLLM
// can recognize, so there is no catalog to mark Supported. The adapter default
// keeps every model on the tool path until the operator opts in.
func TestNativeStructuredOutputIsOptIn(t *testing.T) {
	p, err := New("meta-llama/Llama-3.1-8B-Instruct")
	if err != nil {
		t.Fatal(err)
	}
	if p.Capabilities().NativeStructuredOutput != agent.Unsupported {
		t.Fatalf("default NativeStructuredOutput = %v, want Unsupported", p.Capabilities().NativeStructuredOutput)
	}
	opted, err := New("meta-llama/Llama-3.1-8B-Instruct", WithNativeStructuredOutput(agent.Supported))
	if err != nil {
		t.Fatal(err)
	}
	if opted.Capabilities().NativeStructuredOutput != agent.Supported {
		t.Fatal("WithNativeStructuredOutput(Supported) must opt the server in")
	}
}
