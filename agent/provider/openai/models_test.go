package openai

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// namedModels lists every named constructor with the canonical ID it must use.
var namedModels = []struct {
	name string
	new  func(...Option) (*OpenAIProvider, error)
	id   string
}{
	{"GPT4o", GPT4o, "gpt-4o"},
	{"GPT4oMini", GPT4oMini, "gpt-4o-mini"},
	{"GPT4_1", GPT4_1, "gpt-4.1"},
	{"GPT4_1Mini", GPT4_1Mini, "gpt-4.1-mini"},
	{"GPT4_1Nano", GPT4_1Nano, "gpt-4.1-nano"},
	{"GPT5", GPT5, "gpt-5"},
	{"GPT5Mini", GPT5Mini, "gpt-5-mini"},
	{"GPT5Nano", GPT5Nano, "gpt-5-nano"},
	{"GPT5_4", GPT5_4, "gpt-5.4"},
	{"GPT5_4Mini", GPT5_4Mini, "gpt-5.4-mini"},
	{"GPT5_4Nano", GPT5_4Nano, "gpt-5.4-nano"},
	{"GPT5_5", GPT5_5, "gpt-5.5"},
	{"GPT5_6", GPT5_6, "gpt-5.6"},
	{"GPT5_6Sol", GPT5_6Sol, "gpt-5.6-sol"},
	{"GPT5_6Terra", GPT5_6Terra, "gpt-5.6-terra"},
	{"GPT5_6Luna", GPT5_6Luna, "gpt-5.6-luna"},
	{"GPT6Astra", GPT6Astra, "gpt-6-astra"},
	{"GPT6Sol", GPT6Sol, "gpt-6-sol"},
	{"GPT6_1Sol", GPT6_1Sol, "gpt-6.1-sol"},
	{"GPT6Luna", GPT6Luna, "gpt-6-luna"},
	{"O3", O3, "o3"},
	{"O3Mini", O3Mini, "o3-mini"},
	{"O4Mini", O4Mini, "o4-mini"},
}

func TestNewWithKnownIDMatchesNamedConstructor(t *testing.T) {
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
			if _, ok := catalog[m.id]; !ok {
				t.Fatalf("%q is a named model but has no catalog entry", m.id)
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

func TestGPT56AliasSharesGPT56SolMetadata(t *testing.T) {
	alias, _ := New("gpt-5.6")
	sol, _ := New("gpt-5.6-sol")
	if alias.Capabilities() != sol.Capabilities() {
		t.Fatalf("gpt-5.6 routes to GPT-5.6 Sol but reports %+v vs %+v", alias.Capabilities(), sol.Capabilities())
	}
}

func TestKnownModelReportsCatalogMetadata(t *testing.T) {
	p, err := New("gpt-4.1")
	if err != nil {
		t.Fatal(err)
	}
	caps := p.Capabilities()
	if caps.ContextWindowTokens != 1_047_576 || caps.MaxOutputTokens != 32_768 || caps.ToolUse != agent.Supported {
		t.Fatalf("capabilities = %+v", caps)
	}
	if caps.ToolChoice != (agent.ToolChoiceCapabilities{}) {
		t.Fatalf("tool-choice modes are not documented per model and must stay Unknown: %+v", caps.ToolChoice)
	}
	if caps.NativeStructuredOutput != agent.Unsupported {
		t.Fatalf("adapter baseline lost: %v", caps.NativeStructuredOutput)
	}
}

func TestGPT61SolDocumentsNoToolCallingOnChatCompletions(t *testing.T) {
	p, _ := New("gpt-6.1-sol")
	if p.Capabilities().ToolUse != agent.Unsupported {
		t.Fatalf("ToolUse = %v, want Unsupported through Chat Completions", p.Capabilities().ToolUse)
	}
	// Callers can still override the documented limitation.
	q, _ := New("gpt-6.1-sol", WithToolUse(agent.Unknown))
	if q.Capabilities().ToolUse != agent.Unknown {
		t.Fatal("WithToolUse(Unknown) must reset the catalog value")
	}
}

func TestUnknownModelReportsOnlyTheAdapterBaseline(t *testing.T) {
	p, err := New("my-fine-tune")
	if err != nil {
		t.Fatalf("unknown model must stay constructible: %v", err)
	}
	if p.Capabilities() != baselineCapabilities() {
		t.Fatalf("capabilities = %+v, want the baseline %+v", p.Capabilities(), baselineCapabilities())
	}
	custom, err := New("my-fine-tune", WithMaxOutputTokens(4096), WithToolUse(agent.Supported))
	if err != nil {
		t.Fatal(err)
	}
	want := baselineCapabilities()
	want.MaxOutputTokens = 4096
	want.ToolUse = agent.Supported
	if custom.Capabilities() != want {
		t.Fatalf("capabilities = %+v, want baseline plus overrides %+v", custom.Capabilities(), want)
	}
}

func TestCallerOptionsOverrideCatalogDefaults(t *testing.T) {
	p, err := GPT5_6(
		WithCapabilities(agent.ModelCapabilities{ContextWindowTokens: 200_000}),
		WithToolChoiceAuto(agent.Supported),
	)
	if err != nil {
		t.Fatal(err)
	}
	caps := p.Capabilities()
	if caps.ContextWindowTokens != 200_000 || caps.ToolChoice.Auto != agent.Supported {
		t.Fatalf("caller overrides lost: %+v", caps)
	}
	if caps.MaxOutputTokens != 128_000 || caps.ToolUse != agent.Supported {
		t.Fatalf("untouched catalog fields changed: %+v", caps)
	}
}

func TestFineGrainedOptionsCanResetCatalogFieldsToUnknown(t *testing.T) {
	p, err := New("gpt-5.6", WithToolUse(agent.Unknown), WithContextWindowTokens(0), WithMaxOutputTokens(0))
	if err != nil {
		t.Fatal(err)
	}
	if p.Capabilities() != baselineCapabilities() {
		t.Fatalf("capabilities = %+v, want everything reset to the baseline", p.Capabilities())
	}
}

// OpenAI-compatible servers choose their own model names, so a name that looks
// like an OpenAI model must not inherit OpenAI's metadata.
func TestNewCompatibleDoesNotConsultTheOpenAICatalog(t *testing.T) {
	p, err := NewCompatible("gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if p.Capabilities() != baselineCapabilities() {
		t.Fatalf("capabilities = %+v, want the baseline", p.Capabilities())
	}
	q, err := NewCompatible("gpt-4o", WithContextWindowTokens(32_000))
	if err != nil {
		t.Fatal(err)
	}
	if q.Capabilities().ContextWindowTokens != 32_000 {
		t.Fatal("options must still apply")
	}
}
