package anthropic

import (
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

// namedModels lists every named constructor with the canonical ID it must use.
var namedModels = []struct {
	name string
	new  func(...Option) (*AnthropicProvider, error)
	id   string
}{
	{"ClaudeHaiku4_5", ClaudeHaiku4_5, "claude-haiku-4-5"},
	{"ClaudeSonnet4_5", ClaudeSonnet4_5, "claude-sonnet-4-5"},
	{"ClaudeSonnet4_6", ClaudeSonnet4_6, "claude-sonnet-4-6"},
	{"ClaudeSonnet5", ClaudeSonnet5, "claude-sonnet-5"},
	{"ClaudeOpus4_5", ClaudeOpus4_5, "claude-opus-4-5"},
	{"ClaudeOpus4_6", ClaudeOpus4_6, "claude-opus-4-6"},
	{"ClaudeOpus4_7", ClaudeOpus4_7, "claude-opus-4-7"},
	{"ClaudeOpus4_8", ClaudeOpus4_8, "claude-opus-4-8"},
	{"ClaudeOpus5", ClaudeOpus5, "claude-opus-5"},
	{"ClaudeFable5", ClaudeFable5, "claude-fable-5"},
	{"ClaudeOpus5_5", ClaudeOpus5_5, "claude-opus-5-5"},
	{"ClaudeSonnet5_5", ClaudeSonnet5_5, "claude-sonnet-5-5"},
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

func TestKnownModelReportsCatalogMetadata(t *testing.T) {
	p, err := New("claude-sonnet-5-5")
	if err != nil {
		t.Fatal(err)
	}
	caps := p.Capabilities()
	if caps.ContextWindowTokens != 1_000_000 || caps.MaxOutputTokens != 128_000 || caps.ToolUse != agent.Supported ||
		caps.ToolChoice.Auto != agent.Supported || caps.ToolChoice.Required != agent.Unsupported || caps.ToolChoice.Specific != agent.Unsupported {
		t.Fatalf("capabilities = %+v", caps)
	}
	if caps.NativeStructuredOutput != agent.Unsupported {
		t.Fatalf("adapter baseline lost: NativeStructuredOutput = %v", caps.NativeStructuredOutput)
	}
}

func TestUnknownModelReportsOnlyTheAdapterBaseline(t *testing.T) {
	p, err := New("my-custom-claude")
	if err != nil {
		t.Fatalf("unknown model must stay constructible: %v", err)
	}
	if p.Capabilities() != baselineCapabilities() {
		t.Fatalf("capabilities = %+v, want the baseline %+v", p.Capabilities(), baselineCapabilities())
	}
	custom, err := New("my-custom-claude", WithContextWindowTokens(50_000), WithToolUse(agent.Supported))
	if err != nil {
		t.Fatal(err)
	}
	want := baselineCapabilities()
	want.ContextWindowTokens = 50_000
	want.ToolUse = agent.Supported
	if custom.Capabilities() != want {
		t.Fatalf("capabilities = %+v, want baseline plus overrides %+v", custom.Capabilities(), want)
	}
}

func TestCallerOptionsOverrideCatalogDefaults(t *testing.T) {
	p, err := ClaudeSonnet5_5(
		WithCapabilities(agent.ModelCapabilities{ContextWindowTokens: 123_000}),
		WithToolChoiceSpecific(agent.Supported),
	)
	if err != nil {
		t.Fatal(err)
	}
	caps := p.Capabilities()
	if caps.ContextWindowTokens != 123_000 {
		t.Fatalf("ContextWindowTokens = %d, want the caller override", caps.ContextWindowTokens)
	}
	if caps.ToolChoice.Specific != agent.Supported {
		t.Fatalf("Specific = %v, want the caller override", caps.ToolChoice.Specific)
	}
	if caps.MaxOutputTokens != 128_000 || caps.ToolChoice.Auto != agent.Supported {
		t.Fatalf("untouched catalog fields changed: %+v", caps)
	}
}

func TestFineGrainedOptionsCanResetCatalogFieldsToUnknown(t *testing.T) {
	p, err := New("claude-sonnet-5-5",
		WithToolUse(agent.Unknown),
		WithToolChoiceAuto(agent.Unknown),
		WithToolChoiceRequired(agent.Unknown),
		WithToolChoiceSpecific(agent.Unknown),
		WithContextWindowTokens(0),
		WithMaxOutputTokens(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if p.Capabilities() != baselineCapabilities() {
		t.Fatalf("capabilities = %+v, want everything reset to the baseline", p.Capabilities())
	}
}
