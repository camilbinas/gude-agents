package ollama

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// namedModels lists every named constructor with the canonical name it must use.
var namedModels = []struct {
	name string
	new  func(...Option) (*Provider, error)
	id   string
}{
	{"Qwen25", Qwen25, "qwen2.5"},
	{"Qwen25_7B", Qwen25_7B, "qwen2.5:7b"},
	{"Qwen25_14B", Qwen25_14B, "qwen2.5:14b"},
	{"Qwen25_32B", Qwen25_32B, "qwen2.5:32b"},
	{"Llama32", Llama32, "llama3.2"},
	{"Llama32_1B", Llama32_1B, "llama3.2:1b"},
	{"Llama32_3B", Llama32_3B, "llama3.2:3b"},
	{"Mistral", Mistral, "mistral"},
	{"MistralNemo", MistralNemo, "mistral-nemo"},
	{"Gemma3", Gemma3, "gemma3"},
	{"Gemma3_4B", Gemma3_4B, "gemma3:4b"},
	{"Phi4", Phi4, "phi4"},
	{"Qwen36", Qwen36, "qwen3.6"},
	{"Qwen35", Qwen35, "qwen3.5"},
	{"Qwen3Coder", Qwen3Coder, "qwen3-coder"},
	{"Gemma4", Gemma4, "gemma4"},
	{"GPTOSS", GPTOSS, "gpt-oss"},
	{"GPTOSS_20B", GPTOSS_20B, "gpt-oss:20b"},
	{"GPTOSS_120B", GPTOSS_120B, "gpt-oss:120b"},
}

var baseline = agent.ModelCapabilities{NativeStructuredOutput: agent.Unsupported}

func TestNewWithKnownNameMatchesNamedConstructor(t *testing.T) {
	for _, m := range namedModels {
		t.Run(m.name, func(t *testing.T) {
			named, err := m.new()
			if err != nil {
				t.Fatal(err)
			}
			direct, err := New(m.id)
			if err != nil {
				t.Fatal(err)
			}
			if named.ModelID() != m.id || direct.ModelID() != m.id {
				t.Fatalf("model IDs = %q and %q, want %q", named.ModelID(), direct.ModelID(), m.id)
			}
			if named.Capabilities() != direct.Capabilities() {
				t.Fatalf("named %+v != New(%q) %+v", named.Capabilities(), m.id, direct.Capabilities())
			}
		})
	}
}

func TestEveryCatalogEntryHasANamedConstructor(t *testing.T) {
	named := map[string]bool{}
	for _, m := range namedModels {
		named[m.id] = true
	}
	for id := range catalog {
		if !named[id] {
			t.Errorf("catalog entry %q has no named constructor", id)
		}
	}
}

func TestKnownModelReportsCatalogMetadata(t *testing.T) {
	p, err := New("gpt-oss")
	if err != nil {
		t.Fatal(err)
	}
	caps := p.Capabilities()
	if caps.ToolUse != agent.Supported || caps.NativeStructuredOutput != agent.Unsupported {
		t.Fatalf("capabilities = %+v", caps)
	}
	if caps.ContextWindowTokens != 0 {
		t.Fatalf("context window depends on the server's num_ctx and must stay Unknown, got %d", caps.ContextWindowTokens)
	}
}

func TestUnlistedAndCustomModelsReportOnlyTheBaseline(t *testing.T) {
	for _, id := range []string{"phi4", "my-local-finetune"} {
		p, err := New(id)
		if err != nil {
			t.Fatalf("%s must stay constructible: %v", id, err)
		}
		if p.Capabilities() != baseline {
			t.Fatalf("%s capabilities = %+v, want the baseline", id, p.Capabilities())
		}
	}
	custom, err := New("my-local-finetune", WithContextWindowTokens(8192), WithToolUse(agent.Supported))
	if err != nil {
		t.Fatal(err)
	}
	want := baseline
	want.ContextWindowTokens = 8192
	want.ToolUse = agent.Supported
	if custom.Capabilities() != want {
		t.Fatalf("capabilities = %+v, want baseline plus overrides %+v", custom.Capabilities(), want)
	}
}

func TestCallerOptionsOverrideAndResetCatalogDefaults(t *testing.T) {
	reset, err := GPTOSS(WithToolUse(agent.Unknown))
	if err != nil {
		t.Fatal(err)
	}
	if reset.Capabilities() != baseline {
		t.Fatalf("WithToolUse(Unknown) did not reset the catalog value: %+v", reset.Capabilities())
	}
	over, err := Llama32(WithCapabilities(agent.ModelCapabilities{ContextWindowTokens: 4096}), WithToolUse(agent.Unsupported))
	if err != nil {
		t.Fatal(err)
	}
	if over.Capabilities().ContextWindowTokens != 4096 || over.Capabilities().ToolUse != agent.Unsupported {
		t.Fatalf("caller overrides lost: %+v", over.Capabilities())
	}
}

// A model name that happens to match an OpenAI model must not inherit OpenAI's
// metadata: Ollama serves its own models under its own names.
func TestOpenAIModelNamesDoNotLeakIntoOllama(t *testing.T) {
	p, err := New("gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if p.Capabilities() != baseline {
		t.Fatalf("capabilities = %+v, want the baseline", p.Capabilities())
	}
}
