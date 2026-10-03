package bedrock

import (
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

func TestStandardUsesGlobalClaudeSonnet5_5Capabilities(t *testing.T) {
	provider, err := Standard(WithAPIKey("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	if provider.ModelID() != "global.anthropic.claude-sonnet-5-5" {
		t.Fatalf("ModelID = %q", provider.ModelID())
	}
	caps := provider.Capabilities()
	if caps.ToolChoice.Auto != agent.Supported || caps.ToolChoice.Specific != agent.Unsupported {
		t.Fatalf("ToolChoice = %+v", caps.ToolChoice)
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
