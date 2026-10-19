package bedrock

import (
	"strings"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

func TestGlobalClaudeSonnet5_5Capabilities(t *testing.T) {
	provider, err := GlobalClaudeSonnet5_5(WithAPIKey("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	caps := provider.Capabilities()
	if caps.ToolUse != agent.Supported {
		t.Fatalf("ToolUse = %v, want Supported", caps.ToolUse)
	}
	if caps.ToolChoice.Auto != agent.Supported || caps.ToolChoice.Required != agent.Unsupported || caps.ToolChoice.Specific != agent.Unsupported {
		t.Fatalf("ToolChoice = %+v, want Auto supported and Required/Specific unsupported", caps.ToolChoice)
	}
}

func TestClaudeOpus5_5Capabilities(t *testing.T) {
	for _, factory := range []struct {
		name string
		new  func(...Option) (*BedrockProvider, error)
	}{
		{name: "Global", new: GlobalClaudeOpus5_5},
		{name: "US", new: US_ClaudeOpus5_5},
		{name: "EU", new: EU_ClaudeOpus5_5},
		{name: "AU", new: AU_ClaudeOpus5_5},
		{name: "JP", new: JP_ClaudeOpus5_5},
	} {
		t.Run(factory.name, func(t *testing.T) {
			provider, err := factory.new(WithAPIKey("test-key"))
			if err != nil {
				t.Fatal(err)
			}
			caps := provider.Capabilities()
			if caps.ToolUse != agent.Supported {
				t.Fatalf("ToolUse = %v, want Supported", caps.ToolUse)
			}
			if caps.ToolChoice.Auto != agent.Supported || caps.ToolChoice.Required != agent.Unsupported || caps.ToolChoice.Specific != agent.Unsupported {
				t.Fatalf("ToolChoice = %#v, want Auto supported and Required/Specific unsupported", caps.ToolChoice)
			}
		})
	}
}

// namedModels lists every named Bedrock model constructor with the canonical
// model ID it must use.
var namedModels = []struct {
	name string
	new  func(...Option) (*BedrockProvider, error)
	id   string
}{
	{"GlobalClaudeHaiku4_5", GlobalClaudeHaiku4_5, "global.anthropic.claude-haiku-4-5-20251001-v1:0"},
	{"GlobalClaudeSonnet4_5", GlobalClaudeSonnet4_5, "global.anthropic.claude-sonnet-4-5-20250929-v1:0"},
	{"GlobalClaudeSonnet4_6", GlobalClaudeSonnet4_6, "global.anthropic.claude-sonnet-4-6"},
	{"GlobalClaudeSonnet5", GlobalClaudeSonnet5, "global.anthropic.claude-sonnet-5"},
	{"GlobalClaudeOpus4_5", GlobalClaudeOpus4_5, "global.anthropic.claude-opus-4-5-20251101-v1:0"},
	{"GlobalClaudeOpus4_6", GlobalClaudeOpus4_6, "global.anthropic.claude-opus-4-6-v1"},
	{"GlobalClaudeOpus4_7", GlobalClaudeOpus4_7, "global.anthropic.claude-opus-4-7"},
	{"GlobalClaudeOpus4_8", GlobalClaudeOpus4_8, "global.anthropic.claude-opus-4-8"},
	{"GlobalClaudeOpus5", GlobalClaudeOpus5, "global.anthropic.claude-opus-5"},
	{"GlobalClaudeFable5", GlobalClaudeFable5, "global.anthropic.claude-fable-5"},
	{"GlobalClaudeOpus5_5", GlobalClaudeOpus5_5, "global.anthropic.claude-opus-5-5"},
	{"GlobalClaudeSonnet5_5", GlobalClaudeSonnet5_5, "global.anthropic.claude-sonnet-5-5"},
	{"GlobalNova2Lite", GlobalNova2Lite, "global.amazon.nova-2-lite-v1:0"},
	{"US_ClaudeHaiku4_5", US_ClaudeHaiku4_5, "us.anthropic.claude-haiku-4-5-20251001-v1:0"},
	{"US_ClaudeSonnet4_5", US_ClaudeSonnet4_5, "us.anthropic.claude-sonnet-4-5-20250929-v1:0"},
	{"US_ClaudeSonnet4_6", US_ClaudeSonnet4_6, "us.anthropic.claude-sonnet-4-6"},
	{"US_ClaudeSonnet5", US_ClaudeSonnet5, "us.anthropic.claude-sonnet-5"},
	{"US_ClaudeOpus4_5", US_ClaudeOpus4_5, "us.anthropic.claude-opus-4-5-20251101-v1:0"},
	{"US_ClaudeOpus4_6", US_ClaudeOpus4_6, "us.anthropic.claude-opus-4-6-v1"},
	{"US_ClaudeOpus4_7", US_ClaudeOpus4_7, "us.anthropic.claude-opus-4-7"},
	{"US_ClaudeOpus4_8", US_ClaudeOpus4_8, "us.anthropic.claude-opus-4-8"},
	{"US_ClaudeOpus5", US_ClaudeOpus5, "us.anthropic.claude-opus-5"},
	{"US_ClaudeFable5", US_ClaudeFable5, "us.anthropic.claude-fable-5"},
	{"US_ClaudeOpus5_5", US_ClaudeOpus5_5, "us.anthropic.claude-opus-5-5"},
	{"US_Nova2Lite", US_Nova2Lite, "us.amazon.nova-2-lite-v1:0"},
	{"EU_NovaMicro", EU_NovaMicro, "eu.amazon.nova-micro-v1:0"},
	{"EU_NovaLite", EU_NovaLite, "eu.amazon.nova-lite-v1:0"},
	{"EU_NovaPro", EU_NovaPro, "eu.amazon.nova-pro-v1:0"},
	{"EU_Nova2Lite", EU_Nova2Lite, "eu.amazon.nova-2-lite-v1:0"},
	{"EU_ClaudeHaiku4_5", EU_ClaudeHaiku4_5, "eu.anthropic.claude-haiku-4-5-20251001-v1:0"},
	{"EU_ClaudeSonnet4_5", EU_ClaudeSonnet4_5, "eu.anthropic.claude-sonnet-4-5-20250929-v1:0"},
	{"EU_ClaudeSonnet4_6", EU_ClaudeSonnet4_6, "eu.anthropic.claude-sonnet-4-6"},
	{"EU_ClaudeSonnet5", EU_ClaudeSonnet5, "eu.anthropic.claude-sonnet-5"},
	{"EU_ClaudeOpus4_5", EU_ClaudeOpus4_5, "eu.anthropic.claude-opus-4-5-20251101-v1:0"},
	{"EU_ClaudeOpus4_6", EU_ClaudeOpus4_6, "eu.anthropic.claude-opus-4-6-v1"},
	{"EU_ClaudeOpus4_7", EU_ClaudeOpus4_7, "eu.anthropic.claude-opus-4-7"},
	{"EU_ClaudeOpus4_8", EU_ClaudeOpus4_8, "eu.anthropic.claude-opus-4-8"},
	{"EU_ClaudeOpus5", EU_ClaudeOpus5, "eu.anthropic.claude-opus-5"},
	{"EU_ClaudeOpus5_5", EU_ClaudeOpus5_5, "eu.anthropic.claude-opus-5-5"},
	{"AU_ClaudeOpus5_5", AU_ClaudeOpus5_5, "au.anthropic.claude-opus-5-5"},
	{"JP_ClaudeOpus5_5", JP_ClaudeOpus5_5, "jp.anthropic.claude-opus-5-5"},
	{"NovaMicro", NovaMicro, "amazon.nova-micro-v1:0"},
	{"NovaLite", NovaLite, "amazon.nova-lite-v1:0"},
	{"NovaPro", NovaPro, "amazon.nova-pro-v1:0"},
	{"Qwen3_235B", Qwen3_235B, "qwen.qwen3-235b-a22b-2507-v1:0"},
	{"Qwen3_32B", Qwen3_32B, "qwen.qwen3-32b-v1:0"},
	{"Qwen3Coder30B", Qwen3Coder30B, "qwen.qwen3-coder-30b-a3b-v1:0"},
	{"Qwen3CoderNext", Qwen3CoderNext, "qwen.qwen3-coder-next"},
	{"Qwen3Next80B", Qwen3Next80B, "qwen.qwen3-next-80b-a3b"},
	{"DeepSeekV3_2", DeepSeekV3_2, "deepseek.v3.2"},
	{"KimiK2_5", KimiK2_5, "moonshotai.kimi-k2.5"},
	{"KimiK2Thinking", KimiK2Thinking, "moonshot.kimi-k2-thinking"},
	{"MistralLarge3", MistralLarge3, "mistral.mistral-large-3-675b-instruct"},
	{"Devstral2_123B", Devstral2_123B, "mistral.devstral-2-123b"},
	{"MiniMaxM2_1", MiniMaxM2_1, "minimax.minimax-m2.1"},
	{"MiniMaxM2_5", MiniMaxM2_5, "minimax.minimax-m2.5"},
	{"GPT_OSS_20B", GPT_OSS_20B, "openai.gpt-oss-20b-1:0"},
	{"GPT_OSS_120B", GPT_OSS_120B, "openai.gpt-oss-120b-1:0"},
	{"NemotronNano2_9B", NemotronNano2_9B, "nvidia.nemotron-nano-9b-v2"},
	{"NemotronNano2_12B", NemotronNano2_12B, "nvidia.nemotron-nano-12b-v2"},
	{"NemotronNano3_35B", NemotronNano3_35B, "nvidia.nemotron-nano-3-30b"},
	{"NemotronSuper3_120B", NemotronSuper3_120B, "nvidia.nemotron-super-3-120b"},
	{"GLM4_7Flash", GLM4_7Flash, "zai.glm-4.7-flash"},
	{"GLM4_7", GLM4_7, "zai.glm-4.7"},
	{"GLM5", GLM5, "zai.glm-5"},
	{"GlobalGPT6Astra", GlobalGPT6Astra, "global.openai.gpt-6-astra"},
	{"GlobalGPT6Luna", GlobalGPT6Luna, "global.openai.gpt-6-luna"},
	{"US_GPT6Astra", US_GPT6Astra, "us.openai.gpt-6-astra"},
	{"US_GPT6_1Sol", US_GPT6_1Sol, "us.openai.gpt-6.1-sol"},
	{"US_GPT6Luna", US_GPT6Luna, "us.openai.gpt-6-luna"},
	{"GlobalGrok4_6", GlobalGrok4_6, "global.xai.grok-4.6"},
	{"US_Grok4_6", US_Grok4_6, "us.xai.grok-4.6"},
}

func TestNewWithKnownIDMatchesNamedConstructor(t *testing.T) {
	if len(namedModels) != 72 {
		t.Fatalf("namedModels has %d rows; update it when adding or removing a constructor", len(namedModels))
	}
	for _, m := range namedModels {
		t.Run(m.name, func(t *testing.T) {
			named, err := m.new(WithAPIKey("test-key"))
			if err != nil {
				t.Fatal(err)
			}
			direct, err := New(m.id, WithAPIKey("test-key"))
			if err != nil {
				t.Fatal(err)
			}
			if named.ModelID() != m.id || direct.ModelID() != m.id {
				t.Fatalf("model IDs = %q and %q, want %q", named.ModelID(), direct.ModelID(), m.id)
			}
			if named.Capabilities() != direct.Capabilities() {
				t.Fatalf("named %+v != New(%q) %+v", named.Capabilities(), m.id, direct.Capabilities())
			}
			if named.thinkingStyle != direct.thinkingStyle {
				t.Fatalf("thinking style: named %v != New(%q) %v", named.thinkingStyle, m.id, direct.thinkingStyle)
			}
		})
	}
}

func TestEveryCatalogEntryIsReachableThroughANamedConstructor(t *testing.T) {
	reached := map[string]bool{}
	for _, m := range namedModels {
		if _, ok := lookupProfile(m.id); ok {
			base := m.id
			if _, ok := catalog[base]; !ok {
				_, base, _ = strings.Cut(m.id, ".")
			}
			reached[base] = true
		}
	}
	for id := range catalog {
		if !reached[id] {
			t.Errorf("catalog entry %q is not used by any named constructor", id)
		}
	}
}

func TestRequestStyleComesFromTheCatalog(t *testing.T) {
	cases := []struct {
		id   string
		want thinkingStyle
	}{
		{"global.anthropic.claude-sonnet-5-5", thinkingStyleClaude},
		{"us.anthropic.claude-opus-4-6-v1", thinkingStyleClaude},
		{"eu.anthropic.claude-haiku-4-5-20251001-v1:0", thinkingStyleClaude},
		{"anthropic.claude-sonnet-5", thinkingStyleClaude},
		{"global.amazon.nova-2-lite-v1:0", thinkingStyleNova2},
		{"us.xai.grok-4.6", thinkingStyleGrok},
		{"amazon.nova-pro-v1:0", thinkingStyleNone},
		{"qwen.qwen3-32b-v1:0", thinkingStyleNone},
		{"my-org.custom-model", thinkingStyleNone},
		{"arn:aws:bedrock:us-east-1:123456789012:inference-profile/custom", thinkingStyleNone},
	}
	for _, tc := range cases {
		p, err := New(tc.id, WithAPIKey("test-key"))
		if err != nil {
			t.Fatal(err)
		}
		if p.thinkingStyle != tc.want {
			t.Errorf("%s thinking style = %v, want %v", tc.id, p.thinkingStyle, tc.want)
		}
	}
}

func TestInferenceProfilePrefixesShareOneCatalogEntry(t *testing.T) {
	var first agent.ModelCapabilities
	for i, id := range []string{
		"anthropic.claude-opus-5-5", "global.anthropic.claude-opus-5-5", "us.anthropic.claude-opus-5-5",
		"eu.anthropic.claude-opus-5-5", "au.anthropic.claude-opus-5-5", "jp.anthropic.claude-opus-5-5",
	} {
		p, err := New(id, WithAPIKey("test-key"))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = p.Capabilities()
			continue
		}
		if p.Capabilities() != first {
			t.Errorf("%s capabilities = %+v, want %+v", id, p.Capabilities(), first)
		}
	}
	// An unknown prefix is not an inference profile.
	p, err := New("moon.anthropic.claude-opus-5-5", WithAPIKey("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Capabilities() != baselineCapabilities() {
		t.Errorf("unknown prefix must not match the catalog: %+v", p.Capabilities())
	}
}

func TestKnownModelReportsCatalogMetadata(t *testing.T) {
	p, err := New("global.anthropic.claude-sonnet-4-6", WithAPIKey("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	caps := p.Capabilities()
	if caps.ContextWindowTokens != 1_000_000 || caps.MaxOutputTokens != 64_000 || caps.ToolUse != agent.Supported ||
		caps.ToolChoice.Specific != agent.Supported || caps.NativeStructuredOutput != agent.Unsupported {
		t.Fatalf("capabilities = %+v", caps)
	}
}

func TestUnknownModelReportsOnlyTheAdapterBaseline(t *testing.T) {
	p, err := New("vendor.new-model-v1:0", WithAPIKey("test-key"))
	if err != nil {
		t.Fatalf("unknown model must stay constructible: %v", err)
	}
	if p.Capabilities() != baselineCapabilities() {
		t.Fatalf("capabilities = %+v, want the baseline", p.Capabilities())
	}
	custom, err := New("vendor.new-model-v1:0", WithAPIKey("test-key"), WithMaxOutputTokens(2048), WithToolUse(agent.Supported))
	if err != nil {
		t.Fatal(err)
	}
	want := baselineCapabilities()
	want.MaxOutputTokens = 2048
	want.ToolUse = agent.Supported
	if custom.Capabilities() != want {
		t.Fatalf("capabilities = %+v, want baseline plus overrides %+v", custom.Capabilities(), want)
	}
}

func TestCallerOptionsOverrideCatalogDefaults(t *testing.T) {
	p, err := GlobalClaudeSonnet5_5(
		WithAPIKey("test-key"),
		WithCapabilities(agent.ModelCapabilities{ContextWindowTokens: 321_000}),
		WithToolChoiceSpecific(agent.Supported),
	)
	if err != nil {
		t.Fatal(err)
	}
	caps := p.Capabilities()
	if caps.ContextWindowTokens != 321_000 || caps.ToolChoice.Specific != agent.Supported {
		t.Fatalf("caller overrides lost: %+v", caps)
	}
	if caps.MaxOutputTokens != 128_000 || caps.ToolChoice.Auto != agent.Supported {
		t.Fatalf("untouched catalog fields changed: %+v", caps)
	}
}

func TestFineGrainedOptionsCanResetCatalogFieldsToUnknown(t *testing.T) {
	p, err := New("global.anthropic.claude-sonnet-5-5", WithAPIKey("test-key"),
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
