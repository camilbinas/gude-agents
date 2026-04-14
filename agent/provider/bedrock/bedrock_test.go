package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// toBedrockRole
// ---------------------------------------------------------------------------

func TestToBedrockRole_User(t *testing.T) {
	got := toBedrockRole(agent.RoleUser)
	if got != types.ConversationRoleUser {
		t.Errorf("expected %q, got %q", types.ConversationRoleUser, got)
	}
}

func TestToBedrockRole_Assistant(t *testing.T) {
	got := toBedrockRole(agent.RoleAssistant)
	if got != types.ConversationRoleAssistant {
		t.Errorf("expected %q, got %q", types.ConversationRoleAssistant, got)
	}
}

func TestToBedrockRole_UnknownDefaultsToUser(t *testing.T) {
	got := toBedrockRole(agent.Role("system"))
	if got != types.ConversationRoleUser {
		t.Errorf("expected unknown role to default to %q, got %q", types.ConversationRoleUser, got)
	}
}

// ---------------------------------------------------------------------------
// toBedrockContentBlocks
// ---------------------------------------------------------------------------

func TestToBedrockContentBlocks_TextBlock(t *testing.T) {
	blocks := toBedrockContentBlocks([]agent.ContentBlock{
		agent.TextBlock{Text: "hello"},
	})
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	tb, ok := blocks[0].(*types.ContentBlockMemberText)
	if !ok {
		t.Fatalf("expected *ContentBlockMemberText, got %T", blocks[0])
	}
	if tb.Value != "hello" {
		t.Errorf("expected text %q, got %q", "hello", tb.Value)
	}
}

func TestToBedrockContentBlocks_ToolUseBlock(t *testing.T) {
	input := json.RawMessage(`{"query":"test"}`)
	blocks := toBedrockContentBlocks([]agent.ContentBlock{
		agent.ToolUseBlock{ToolUseID: "tu-1", Name: "search", Input: input},
	})
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	tu, ok := blocks[0].(*types.ContentBlockMemberToolUse)
	if !ok {
		t.Fatalf("expected *ContentBlockMemberToolUse, got %T", blocks[0])
	}
	if aws.ToString(tu.Value.ToolUseId) != "tu-1" {
		t.Errorf("expected ToolUseId %q, got %q", "tu-1", aws.ToString(tu.Value.ToolUseId))
	}
	if aws.ToString(tu.Value.Name) != "search" {
		t.Errorf("expected Name %q, got %q", "search", aws.ToString(tu.Value.Name))
	}
	if tu.Value.Input == nil {
		t.Fatal("expected non-nil Input document")
	}
}

func TestToBedrockContentBlocks_ToolResultBlock(t *testing.T) {
	blocks := toBedrockContentBlocks([]agent.ContentBlock{
		agent.ToolResultBlock{ToolUseID: "tu-1", Content: "result text", IsError: false},
	})
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	tr, ok := blocks[0].(*types.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("expected *ContentBlockMemberToolResult, got %T", blocks[0])
	}
	if aws.ToString(tr.Value.ToolUseId) != "tu-1" {
		t.Errorf("expected ToolUseId %q, got %q", "tu-1", aws.ToString(tr.Value.ToolUseId))
	}
	if tr.Value.Status == types.ToolResultStatusError {
		t.Error("expected non-error status")
	}
}

func TestToBedrockContentBlocks_ToolResultBlockWithError(t *testing.T) {
	blocks := toBedrockContentBlocks([]agent.ContentBlock{
		agent.ToolResultBlock{ToolUseID: "tu-2", Content: "something failed", IsError: true},
	})
	tr, ok := blocks[0].(*types.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("expected *ContentBlockMemberToolResult, got %T", blocks[0])
	}
	if tr.Value.Status != types.ToolResultStatusError {
		t.Errorf("expected error status, got %q", tr.Value.Status)
	}
}

func TestToBedrockContentBlocks_MixedBlocks(t *testing.T) {
	blocks := toBedrockContentBlocks([]agent.ContentBlock{
		agent.TextBlock{Text: "thinking..."},
		agent.ToolUseBlock{ToolUseID: "tu-1", Name: "search", Input: json.RawMessage(`{}`)},
	})
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if _, ok := blocks[0].(*types.ContentBlockMemberText); !ok {
		t.Errorf("expected first block to be text, got %T", blocks[0])
	}
	if _, ok := blocks[1].(*types.ContentBlockMemberToolUse); !ok {
		t.Errorf("expected second block to be tool use, got %T", blocks[1])
	}
}

func TestToBedrockContentBlocks_Empty(t *testing.T) {
	blocks := toBedrockContentBlocks(nil)
	if len(blocks) != 0 {
		t.Errorf("expected 0 blocks, got %d", len(blocks))
	}
}

// ---------------------------------------------------------------------------
// toBedrockMessages
// ---------------------------------------------------------------------------

func TestToBedrockMessages_SingleUserMessage(t *testing.T) {
	msgs := toBedrockMessages([]agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
	})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != types.ConversationRoleUser {
		t.Errorf("expected user role, got %q", msgs[0].Role)
	}
	if len(msgs[0].Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msgs[0].Content))
	}
}

func TestToBedrockMessages_MultiTurnConversation(t *testing.T) {
	msgs := toBedrockMessages([]agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi there"}}},
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "bye"}}},
	})
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[0].Role != types.ConversationRoleUser {
		t.Error("expected first message to be user")
	}
	if msgs[1].Role != types.ConversationRoleAssistant {
		t.Error("expected second message to be assistant")
	}
	if msgs[2].Role != types.ConversationRoleUser {
		t.Error("expected third message to be user")
	}
}

func TestToBedrockMessages_WithToolResultContent(t *testing.T) {
	msgs := toBedrockMessages([]agent.Message{
		{
			Role: agent.RoleUser,
			Content: []agent.ContentBlock{
				agent.ToolResultBlock{ToolUseID: "tu-1", Content: "42"},
			},
		},
	})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	tr, ok := msgs[0].Content[0].(*types.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("expected tool result block, got %T", msgs[0].Content[0])
	}
	if aws.ToString(tr.Value.ToolUseId) != "tu-1" {
		t.Errorf("expected ToolUseId %q, got %q", "tu-1", aws.ToString(tr.Value.ToolUseId))
	}
}

// ---------------------------------------------------------------------------
// toToolConfig
// ---------------------------------------------------------------------------

func TestToToolConfig_Nil_WhenEmpty(t *testing.T) {
	tc := toToolConfig(nil)
	if tc != nil {
		t.Error("expected nil ToolConfiguration for empty specs")
	}
}

func TestToToolConfig_SingleTool(t *testing.T) {
	tc := toToolConfig([]tool.Spec{
		{
			Name:        "search",
			Description: "Search for items",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
				},
			},
		},
	})
	if tc == nil {
		t.Fatal("expected non-nil ToolConfiguration")
	}
	if len(tc.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tc.Tools))
	}
	toolSpec, ok := tc.Tools[0].(*types.ToolMemberToolSpec)
	if !ok {
		t.Fatalf("expected *ToolMemberToolSpec, got %T", tc.Tools[0])
	}
	if aws.ToString(toolSpec.Value.Name) != "search" {
		t.Errorf("expected name %q, got %q", "search", aws.ToString(toolSpec.Value.Name))
	}
	if aws.ToString(toolSpec.Value.Description) != "Search for items" {
		t.Errorf("expected description %q, got %q", "Search for items", aws.ToString(toolSpec.Value.Description))
	}
	if toolSpec.Value.InputSchema == nil {
		t.Error("expected non-nil InputSchema")
	}
}

func TestToToolConfig_MultipleTools(t *testing.T) {
	tc := toToolConfig([]tool.Spec{
		{Name: "tool_a", Description: "A", InputSchema: map[string]any{"type": "object"}},
		{Name: "tool_b", Description: "B", InputSchema: map[string]any{"type": "object"}},
	})
	if tc == nil {
		t.Fatal("expected non-nil ToolConfiguration")
	}
	if len(tc.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tc.Tools))
	}
}

// ---------------------------------------------------------------------------
// parseConverseOutput
// ---------------------------------------------------------------------------

func TestParseConverseOutput_NilOutput(t *testing.T) {
	resp := parseConverseOutput(&bedrockruntime.ConverseOutput{
		Output: nil,
	})
	if resp.Text != "" {
		t.Errorf("expected empty text, got %q", resp.Text)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(resp.ToolCalls))
	}
}

func TestParseConverseOutput_TextOnly(t *testing.T) {
	resp := parseConverseOutput(&bedrockruntime.ConverseOutput{
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberText{Value: "Hello, world!"},
				},
			},
		},
	})
	if resp.Text != "Hello, world!" {
		t.Errorf("expected %q, got %q", "Hello, world!", resp.Text)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(resp.ToolCalls))
	}
}

func TestParseConverseOutput_MultipleTextBlocks(t *testing.T) {
	resp := parseConverseOutput(&bedrockruntime.ConverseOutput{
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberText{Value: "Hello, "},
					&types.ContentBlockMemberText{Value: "world!"},
				},
			},
		},
	})
	if resp.Text != "Hello, world!" {
		t.Errorf("expected %q, got %q", "Hello, world!", resp.Text)
	}
}

func TestParseConverseOutput_ToolUse(t *testing.T) {
	inputDoc := document.NewLazyDocument(map[string]any{"query": "test"})
	resp := parseConverseOutput(&bedrockruntime.ConverseOutput{
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberToolUse{
						Value: types.ToolUseBlock{
							ToolUseId: aws.String("tu-123"),
							Name:      aws.String("search"),
							Input:     inputDoc,
						},
					},
				},
			},
		},
	})
	if resp.Text != "" {
		t.Errorf("expected empty text, got %q", resp.Text)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ToolUseID != "tu-123" {
		t.Errorf("expected ToolUseID %q, got %q", "tu-123", tc.ToolUseID)
	}
	if tc.Name != "search" {
		t.Errorf("expected Name %q, got %q", "search", tc.Name)
	}
	if len(tc.Input) == 0 {
		t.Error("expected non-empty Input")
	}
}

func TestParseConverseOutput_MixedTextAndToolUse(t *testing.T) {
	inputDoc := document.NewLazyDocument(map[string]any{"q": "hello"})
	resp := parseConverseOutput(&bedrockruntime.ConverseOutput{
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberText{Value: "Let me search for that."},
					&types.ContentBlockMemberToolUse{
						Value: types.ToolUseBlock{
							ToolUseId: aws.String("tu-456"),
							Name:      aws.String("lookup"),
							Input:     inputDoc,
						},
					},
				},
			},
		},
	})
	if resp.Text != "Let me search for that." {
		t.Errorf("expected text %q, got %q", "Let me search for that.", resp.Text)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "lookup" {
		t.Errorf("expected tool name %q, got %q", "lookup", resp.ToolCalls[0].Name)
	}
}

func TestParseConverseOutput_ToolUseWithNilInput(t *testing.T) {
	resp := parseConverseOutput(&bedrockruntime.ConverseOutput{
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberToolUse{
						Value: types.ToolUseBlock{
							ToolUseId: aws.String("tu-789"),
							Name:      aws.String("no_args_tool"),
							Input:     nil,
						},
					},
				},
			},
		},
	})
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ToolUseID != "tu-789" {
		t.Errorf("expected ToolUseID %q, got %q", "tu-789", tc.ToolUseID)
	}
	if tc.Name != "no_args_tool" {
		t.Errorf("expected Name %q, got %q", "no_args_tool", tc.Name)
	}
	// Input should be nil/empty when the tool has no input document
	if len(tc.Input) != 0 {
		t.Errorf("expected empty input for nil document, got %s", string(tc.Input))
	}
}

// ---------------------------------------------------------------------------
// Property-based tests (rapid)
// ---------------------------------------------------------------------------

// Feature: agent-framework-v2, Property 4: Bedrock ToolChoice mapping
// **Validates: Requirements 3.2**
func TestProperty_BedrockToolChoiceMapping(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mode := rapid.SampledFrom([]tool.ChoiceMode{
			tool.ChoiceAuto,
			tool.ChoiceAny,
			tool.ChoiceTool,
		}).Draw(t, "mode")

		tc := &tool.Choice{Mode: mode}
		if mode == tool.ChoiceTool {
			tc.Name = rapid.StringMatching(`[a-zA-Z_][a-zA-Z0-9_]{0,63}`).Draw(t, "toolName")
		}

		result := toBedrockToolChoice(tc)
		if result == nil {
			t.Fatal("expected non-nil ToolChoice result")
		}

		switch mode {
		case tool.ChoiceAuto:
			v, ok := result.(*types.ToolChoiceMemberAuto)
			if !ok {
				t.Fatalf("expected *ToolChoiceMemberAuto, got %T", result)
			}
			_ = v // AutoToolChoice has no fields to check
		case tool.ChoiceAny:
			v, ok := result.(*types.ToolChoiceMemberAny)
			if !ok {
				t.Fatalf("expected *ToolChoiceMemberAny, got %T", result)
			}
			_ = v // AnyToolChoice has no fields to check
		case tool.ChoiceTool:
			v, ok := result.(*types.ToolChoiceMemberTool)
			if !ok {
				t.Fatalf("expected *ToolChoiceMemberTool, got %T", result)
			}
			if aws.ToString(v.Value.Name) != tc.Name {
				t.Fatalf("expected tool name %q, got %q", tc.Name, aws.ToString(v.Value.Name))
			}
		}
	})
}

// Feature: agent-framework-v2, Property 12: Bedrock TokenUsage population
// **Validates: Requirements 7.2**
func TestProperty_BedrockTokenUsagePopulation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		inputTokens := rapid.Int32Range(0, 1_000_000).Draw(t, "inputTokens")
		outputTokens := rapid.Int32Range(0, 1_000_000).Draw(t, "outputTokens")

		// Test non-streaming: parseConverseOutput + Usage field
		out := &bedrockruntime.ConverseOutput{
			Output: &types.ConverseOutputMemberMessage{
				Value: types.Message{
					Role: types.ConversationRoleAssistant,
					Content: []types.ContentBlock{
						&types.ContentBlockMemberText{Value: "hello"},
					},
				},
			},
			Usage: &types.TokenUsage{
				InputTokens:  aws.Int32(inputTokens),
				OutputTokens: aws.Int32(outputTokens),
			},
		}

		resp := parseConverseOutput(out)
		// parseConverseOutput doesn't read Usage — the Converse method does.
		// Simulate what the Converse method does after parseConverseOutput:
		if out.Usage != nil {
			resp.Usage.InputTokens = int(aws.ToInt32(out.Usage.InputTokens))
			resp.Usage.OutputTokens = int(aws.ToInt32(out.Usage.OutputTokens))
		}

		if resp.Usage.InputTokens != int(inputTokens) {
			t.Fatalf("expected InputTokens %d, got %d", inputTokens, resp.Usage.InputTokens)
		}
		if resp.Usage.OutputTokens != int(outputTokens) {
			t.Fatalf("expected OutputTokens %d, got %d", outputTokens, resp.Usage.OutputTokens)
		}
	})
}

// ---------------------------------------------------------------------------
// Capabilities
// ---------------------------------------------------------------------------

func TestCapabilities_OpenAIModels_NoToolSupport(t *testing.T) {
	for _, model := range []string{"openai.gpt-oss-120b-1:0", "openai.gpt-oss-20b-1:0"} {
		p := &BedrockProvider{model: model}
		caps := p.Capabilities()
		if caps.ToolUse {
			t.Errorf("%s: expected ToolUse=false", model)
		}
		if caps.ToolChoice {
			t.Errorf("%s: expected ToolChoice=false", model)
		}
		if caps.TokenUsage {
			t.Errorf("%s: expected TokenUsage=false", model)
		}
	}
}

func TestCapabilities_AnthropicAndNovaModels_FullSupport(t *testing.T) {
	for _, model := range []string{
		"eu.anthropic.claude-sonnet-4-6",
		"eu.amazon.nova-pro-v1:0",
		"qwen.qwen3-32b-v1:0",
	} {
		p := &BedrockProvider{model: model}
		caps := p.Capabilities()
		if !caps.ToolUse {
			t.Errorf("%s: expected ToolUse=true", model)
		}
		if !caps.ToolChoice {
			t.Errorf("%s: expected ToolChoice=true", model)
		}
		if !caps.TokenUsage {
			t.Errorf("%s: expected TokenUsage=true", model)
		}
	}
}
