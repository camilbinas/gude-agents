package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/anthropic"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/provider/fallback"
	"github.com/camilbinas/gude-agents/agent/structured"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// These tests exercise structured output against real provider capability
// tables. They live here because the providers are separate modules that the
// root module must not depend on.

type structuredProfile struct {
	Name  string `json:"name" description:"A name" required:"true"`
	Count int    `json:"count" description:"A count"`
}

type structuredCapsProvider struct {
	caps   agent.ModelCapabilities
	params agent.ModelRequest
	raw    string
	calls  int
}

func (p *structuredCapsProvider) Name() string                          { return "structured-caps" }
func (p *structuredCapsProvider) Capabilities() agent.ModelCapabilities { return p.caps }
func (p *structuredCapsProvider) Stream(_ context.Context, req agent.ModelRequest, _ func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	p.calls++
	p.params = req
	return &agent.ModelResponse{ToolCalls: []tool.Call{{
		ToolUseID: "structured-call",
		Name:      "structured_output",
		Input:     json.RawMessage(p.raw),
	}}}, nil
}

func supportedToolChoice(specific agent.Capability) agent.ModelCapabilities {
	return agent.ModelCapabilities{
		ToolUse: agent.Supported,
		ToolChoice: agent.ToolChoiceCapabilities{
			Auto:     agent.Supported,
			Specific: specific,
		},
	}
}

func TestStructuredFallbackKnownSpecificUnsupportedUsesAutomaticChoice(t *testing.T) {
	primary := &structuredCapsProvider{raw: `{"name":"Ada","count":3}`, caps: supportedToolChoice(agent.Supported)}
	backup := &structuredCapsProvider{raw: `{"name":"Grace","count":4}`, caps: supportedToolChoice(agent.Unsupported)}
	a, err := agent.New(fallback.New(primary, backup), "sys")
	if err != nil {
		t.Fatal(err)
	}
	got, err := structured.Invoke[structuredProfile](agent.Background(), a, "input")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value.Name != "Ada" || primary.calls != 1 || backup.calls != 0 {
		t.Fatalf("result = %#v, primary calls = %d, backup calls = %d", got, primary.calls, backup.calls)
	}
	if primary.params.ToolChoice != nil {
		t.Fatalf("ToolChoice = %#v, want nil provider-default automatic choice", primary.params.ToolChoice)
	}
}

func TestStructuredDirectClaude55CapabilitiesUseAutomaticChoice(t *testing.T) {
	for _, factory := range []struct {
		name string
		new  func(...anthropic.Option) (*anthropic.AnthropicProvider, error)
	}{
		{name: "Sonnet 5.5", new: anthropic.ClaudeSonnet5_5},
		{name: "Opus 5.5", new: anthropic.ClaudeOpus5_5},
	} {
		t.Run(factory.name, func(t *testing.T) {
			model, err := factory.new()
			if err != nil {
				t.Fatal(err)
			}
			assertStructuredAutomaticChoice(t, model.Capabilities())
		})
	}
}

func TestStructuredBedrockClaude55CapabilitiesUseAutomaticChoice(t *testing.T) {
	for _, factory := range []struct {
		name string
		new  func(...bedrock.Option) (*bedrock.BedrockProvider, error)
	}{
		{name: "Sonnet 5.5", new: bedrock.GlobalClaudeSonnet5_5},
		{name: "Opus 5.5", new: bedrock.GlobalClaudeOpus5_5},
	} {
		t.Run(factory.name, func(t *testing.T) {
			model, err := factory.new(bedrock.WithAPIKey("test-key"))
			if err != nil {
				t.Fatal(err)
			}
			assertStructuredAutomaticChoice(t, model.Capabilities())
		})
	}
}

func assertStructuredAutomaticChoice(t *testing.T, caps agent.ModelCapabilities) {
	t.Helper()
	provider := &structuredCapsProvider{raw: `{"name":"Ada","count":3}`, caps: caps}
	a, err := agent.New(provider, "sys")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := structured.Invoke[structuredProfile](agent.Background(), a, "input"); err != nil {
		t.Fatal(err)
	}
	if provider.params.ToolChoice != nil {
		t.Fatalf("ToolChoice = %#v, want nil provider-default automatic choice", provider.params.ToolChoice)
	}
}
