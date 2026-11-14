package openai

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// namedModels lists every named constructor backed by a catalog entry with the
// canonical ID it must use. Constructors for shut-down IDs (no catalog entry)
// are covered separately by shutDownModels.
var namedModels = []struct {
	name string
	new  func(...Option) (*OpenAIProvider, error)
	id   string
}{
	{"GPT4o", GPT4o, "gpt-4o"},
	{"GPT4oMini", GPT4oMini, "gpt-4o-mini"},
	{"GPT4_1", GPT4_1, "gpt-4.1"},
	{"GPT4_1Mini", GPT4_1Mini, "gpt-4.1-mini"},
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
}

// shutDownModels lists constructors kept only for source compatibility. OpenAI
// shut these IDs down, so the catalog drops them and the constructor falls back
// to the adapter baseline.
var shutDownModels = []struct {
	name string
	new  func(...Option) (*OpenAIProvider, error)
	id   string
}{
	{"GPT4_1Nano", GPT4_1Nano, "gpt-4.1-nano"},
	{"O3Mini", O3Mini, "o3-mini"},
	{"O4Mini", O4Mini, "o4-mini"},
}

// catalogOnlyModels lists IDs that are current but have no named constructor;
// New(id) supplies catalog metadata.
var catalogOnlyModels = []string{"gpt-6.1-terra"}

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

func TestShutDownConstructorsReportTheBaseline(t *testing.T) {
	for _, m := range shutDownModels {
		t.Run(m.name, func(t *testing.T) {
			p, err := m.new()
			if err != nil {
				t.Fatal(err)
			}
			if p.ModelID() != m.id {
				t.Fatalf("model ID = %q, want %q", p.ModelID(), m.id)
			}
			if _, ok := catalog[m.id]; ok {
				t.Fatalf("%q is shut down and must not have a catalog entry", m.id)
			}
			if p.Capabilities() != baselineCapabilities() {
				t.Fatalf("capabilities = %+v, want the baseline %+v", p.Capabilities(), baselineCapabilities())
			}
		})
	}
}

func TestEveryCatalogEntryIsReachable(t *testing.T) {
	reachable := map[string]bool{}
	for _, m := range namedModels {
		reachable[m.id] = true
	}
	for _, id := range catalogOnlyModels {
		reachable[id] = true
	}
	for id := range catalog {
		if !reachable[id] {
			t.Errorf("catalog entry %q has no named constructor and is not listed as catalog-only", id)
		}
	}
	// catalogOnlyModels must be present in the catalog.
	for _, id := range catalogOnlyModels {
		if _, ok := catalog[id]; !ok {
			t.Errorf("catalog-only model %q is missing from the catalog", id)
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
	// gpt-4.1 documents native structured output on Chat Completions.
	if caps.NativeStructuredOutput != agent.Supported {
		t.Fatalf("NativeStructuredOutput = %v, want Supported", caps.NativeStructuredOutput)
	}
}

func TestCatalogNativeStructuredOutputFollowsToolUse(t *testing.T) {
	for id, profile := range catalog {
		p, err := New(id)
		if err != nil {
			t.Fatalf("New(%q): %v", id, err)
		}
		caps := p.Capabilities()
		switch profile.capabilities.ToolUse {
		case agent.Supported:
			if caps.NativeStructuredOutput != agent.Supported {
				t.Errorf("%q: ToolUse Supported but NativeStructuredOutput = %v", id, caps.NativeStructuredOutput)
			}
		default:
			// Unknown or Responses-API-only models must keep the tool path.
			if caps.NativeStructuredOutput == agent.Supported {
				t.Errorf("%q: ToolUse %v must not report NativeStructuredOutput Supported", id, profile.capabilities.ToolUse)
			}
		}
	}
}

func TestGPT61SolDocumentsNoToolCallingOnChatCompletions(t *testing.T) {
	p, _ := New("gpt-6.1-sol")
	caps := p.Capabilities()
	if caps.ToolUse != agent.Unsupported {
		t.Fatalf("ToolUse = %v, want Unsupported through Chat Completions", caps.ToolUse)
	}
	// Context and output limits are published on the model page.
	if caps.ContextWindowTokens != 1_050_000 || caps.MaxOutputTokens != 128_000 {
		t.Fatalf("limits = %d/%d, want 1050000/128000", caps.ContextWindowTokens, caps.MaxOutputTokens)
	}
	// Callers can still override the documented limitation.
	q, _ := New("gpt-6.1-sol", WithToolUse(agent.Unknown))
	if q.Capabilities().ToolUse != agent.Unknown {
		t.Fatal("WithToolUse(Unknown) must reset the catalog value")
	}
}

func TestGPT61TerraDocumentsNoToolCallingOnChatCompletions(t *testing.T) {
	p, _ := New("gpt-6.1-terra")
	caps := p.Capabilities()
	if caps.ToolUse != agent.Unsupported {
		t.Fatalf("ToolUse = %v, want Unsupported through Chat Completions", caps.ToolUse)
	}
	if caps.ContextWindowTokens != 1_050_000 || caps.MaxOutputTokens != 128_000 {
		t.Fatalf("limits = %d/%d, want 1050000/128000", caps.ContextWindowTokens, caps.MaxOutputTokens)
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
	// gpt-5.6 documents native structured output, so reaching the baseline also
	// takes resetting that flag back to the adapter default (Unsupported).
	p, err := New("gpt-5.6",
		WithToolUse(agent.Unknown),
		WithContextWindowTokens(0),
		WithMaxOutputTokens(0),
		WithNativeStructuredOutput(agent.Unsupported),
	)
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
