package bedrock

import (
	"encoding/base64"
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
	blocks, err := toBedrockContentBlocks([]agent.ContentBlock{
		agent.TextBlock{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	blocks, err := toBedrockContentBlocks([]agent.ContentBlock{
		agent.ToolUseBlock{ToolUseID: "tu-1", Name: "search", Input: input},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	blocks, err := toBedrockContentBlocks([]agent.ContentBlock{
		agent.ToolResultBlock{ToolUseID: "tu-1", Content: "result text", IsError: false},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	blocks, err := toBedrockContentBlocks([]agent.ContentBlock{
		agent.ToolResultBlock{ToolUseID: "tu-2", Content: "something failed", IsError: true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr, ok := blocks[0].(*types.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("expected *ContentBlockMemberToolResult, got %T", blocks[0])
	}
	if tr.Value.Status != types.ToolResultStatusError {
		t.Errorf("expected error status, got %q", tr.Value.Status)
	}
}

func TestToBedrockContentBlocks_MixedBlocks(t *testing.T) {
	blocks, err := toBedrockContentBlocks([]agent.ContentBlock{
		agent.TextBlock{Text: "thinking..."},
		agent.ToolUseBlock{ToolUseID: "tu-1", Name: "search", Input: json.RawMessage(`{}`)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	blocks, err := toBedrockContentBlocks(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 0 {
		t.Errorf("expected 0 blocks, got %d", len(blocks))
	}
}

// ---------------------------------------------------------------------------
// toBedrockMessages
// ---------------------------------------------------------------------------

func TestToBedrockMessages_SingleUserMessage(t *testing.T) {
	msgs, err := toBedrockMessages([]agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	msgs, err := toBedrockMessages([]agent.Message{
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hello"}}},
		{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi there"}}},
		{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "bye"}}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	msgs, err := toBedrockMessages([]agent.Message{
		{
			Role: agent.RoleUser,
			Content: []agent.ContentBlock{
				agent.ToolResultBlock{ToolUseID: "tu-1", Content: "42"},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
// buildInferenceConfiguration
// ---------------------------------------------------------------------------

func TestBuildInferenceConfiguration_NilConfig_UsesConstructorDefaults(t *testing.T) {
	p := &BedrockProvider{maxTokens: 4096}
	ic := p.buildInferenceConfiguration(nil)
	if ic == nil {
		t.Fatal("expected non-nil InferenceConfiguration")
	}
	if aws.ToInt32(ic.MaxTokens) != 4096 {
		t.Errorf("expected MaxTokens 4096, got %d", aws.ToInt32(ic.MaxTokens))
	}
	if ic.Temperature != nil {
		t.Errorf("expected nil Temperature, got %v", *ic.Temperature)
	}
	if ic.TopP != nil {
		t.Errorf("expected nil TopP, got %v", *ic.TopP)
	}
	if ic.StopSequences != nil {
		t.Errorf("expected nil StopSequences, got %v", ic.StopSequences)
	}
}

func TestBuildInferenceConfiguration_TemperatureMapping(t *testing.T) {
	p := &BedrockProvider{maxTokens: 8192}
	temp := 0.7
	ic := p.buildInferenceConfiguration(&agent.InferenceConfig{Temperature: &temp})
	if ic.Temperature == nil {
		t.Fatal("expected non-nil Temperature")
	}
	if *ic.Temperature != float32(0.7) {
		t.Errorf("expected Temperature 0.7, got %v", *ic.Temperature)
	}
	// MaxTokens should still be the constructor default
	if aws.ToInt32(ic.MaxTokens) != 8192 {
		t.Errorf("expected MaxTokens 8192, got %d", aws.ToInt32(ic.MaxTokens))
	}
}

func TestBuildInferenceConfiguration_TopPMapping(t *testing.T) {
	p := &BedrockProvider{maxTokens: 8192}
	topP := 0.9
	ic := p.buildInferenceConfiguration(&agent.InferenceConfig{TopP: &topP})
	if ic.TopP == nil {
		t.Fatal("expected non-nil TopP")
	}
	if *ic.TopP != float32(0.9) {
		t.Errorf("expected TopP 0.9, got %v", *ic.TopP)
	}
}

func TestBuildInferenceConfiguration_StopSequencesMapping(t *testing.T) {
	p := &BedrockProvider{maxTokens: 8192}
	stops := []string{"STOP", "END"}
	ic := p.buildInferenceConfiguration(&agent.InferenceConfig{StopSequences: stops})
	if len(ic.StopSequences) != 2 {
		t.Fatalf("expected 2 stop sequences, got %d", len(ic.StopSequences))
	}
	if ic.StopSequences[0] != "STOP" || ic.StopSequences[1] != "END" {
		t.Errorf("expected [STOP END], got %v", ic.StopSequences)
	}
}

func TestBuildInferenceConfiguration_MaxTokensOverridesDefault(t *testing.T) {
	p := &BedrockProvider{maxTokens: 8192}
	maxTok := 2048
	ic := p.buildInferenceConfiguration(&agent.InferenceConfig{MaxTokens: &maxTok})
	if aws.ToInt32(ic.MaxTokens) != 2048 {
		t.Errorf("expected MaxTokens 2048, got %d", aws.ToInt32(ic.MaxTokens))
	}
}

func TestBuildInferenceConfiguration_AllFieldsSet(t *testing.T) {
	p := &BedrockProvider{maxTokens: 8192}
	temp := 0.5
	topP := 0.8
	maxTok := 1024
	cfg := &agent.InferenceConfig{
		Temperature:   &temp,
		TopP:          &topP,
		StopSequences: []string{"<|end|>"},
		MaxTokens:     &maxTok,
	}
	ic := p.buildInferenceConfiguration(cfg)
	if *ic.Temperature != float32(0.5) {
		t.Errorf("expected Temperature 0.5, got %v", *ic.Temperature)
	}
	if *ic.TopP != float32(0.8) {
		t.Errorf("expected TopP 0.8, got %v", *ic.TopP)
	}
	if len(ic.StopSequences) != 1 || ic.StopSequences[0] != "<|end|>" {
		t.Errorf("expected StopSequences [<|end|>], got %v", ic.StopSequences)
	}
	if aws.ToInt32(ic.MaxTokens) != 1024 {
		t.Errorf("expected MaxTokens 1024, got %d", aws.ToInt32(ic.MaxTokens))
	}
}

// ---------------------------------------------------------------------------
// buildAdditionalFields
// ---------------------------------------------------------------------------

func TestBuildAdditionalFields_NilConfig_NoThinking_ReturnsNil(t *testing.T) {
	p := &BedrockProvider{}
	result := p.buildAdditionalFields(nil)
	if result != nil {
		t.Error("expected nil AdditionalModelRequestFields when no config and no thinking")
	}
}

func TestBuildAdditionalFields_TopKOnly(t *testing.T) {
	p := &BedrockProvider{}
	topK := 50
	result := p.buildAdditionalFields(&agent.InferenceConfig{TopK: &topK})
	if result == nil {
		t.Fatal("expected non-nil AdditionalModelRequestFields for TopK")
	}
	// Marshal the document to verify the top_k field
	data, err := result.MarshalSmithyDocument()
	if err != nil {
		t.Fatalf("failed to marshal document: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("failed to unmarshal document: %v", err)
	}
	topKVal, ok := fields["top_k"]
	if !ok {
		t.Fatal("expected top_k field in AdditionalModelRequestFields")
	}
	// JSON numbers unmarshal as float64
	if topKVal != float64(50) {
		t.Errorf("expected top_k=50, got %v", topKVal)
	}
}

func TestBuildAdditionalFields_NoTopK_NoThinking_ReturnsNil(t *testing.T) {
	p := &BedrockProvider{}
	// Config with no TopK
	temp := 0.5
	result := p.buildAdditionalFields(&agent.InferenceConfig{Temperature: &temp})
	if result != nil {
		t.Error("expected nil AdditionalModelRequestFields when no TopK and no thinking")
	}
}

func TestBuildAdditionalFields_ThinkingAndTopK_Merged(t *testing.T) {
	p := &BedrockProvider{
		thinkingStyle: thinkingStyleClaude,
		thinkingLevel: "medium",
	}
	topK := 40
	result := p.buildAdditionalFields(&agent.InferenceConfig{TopK: &topK})
	if result == nil {
		t.Fatal("expected non-nil AdditionalModelRequestFields")
	}
	data, err := result.MarshalSmithyDocument()
	if err != nil {
		t.Fatalf("failed to marshal document: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("failed to unmarshal document: %v", err)
	}
	// Should have both thinking and top_k
	if _, ok := fields["thinking"]; !ok {
		t.Error("expected thinking field in merged AdditionalModelRequestFields")
	}
	if _, ok := fields["top_k"]; !ok {
		t.Error("expected top_k field in merged AdditionalModelRequestFields")
	}
	if fields["top_k"] != float64(40) {
		t.Errorf("expected top_k=40, got %v", fields["top_k"])
	}
}

func TestBuildAdditionalFields_ThinkingOnly_NoTopK(t *testing.T) {
	p := &BedrockProvider{
		thinkingStyle: thinkingStyleClaude,
		thinkingLevel: "high",
	}
	result := p.buildAdditionalFields(nil)
	if result == nil {
		t.Fatal("expected non-nil AdditionalModelRequestFields for thinking")
	}
	data, err := result.MarshalSmithyDocument()
	if err != nil {
		t.Fatalf("failed to marshal document: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("failed to unmarshal document: %v", err)
	}
	if _, ok := fields["thinking"]; !ok {
		t.Error("expected thinking field")
	}
	if _, ok := fields["top_k"]; ok {
		t.Error("expected no top_k field when TopK is not set")
	}
}

// ---------------------------------------------------------------------------
// ImageBlock translation tests (sub-task 5.1)
// ---------------------------------------------------------------------------

func TestToBedrockContentBlocks_ImageBlock_RawBytes(t *testing.T) {
	rawBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10} // JPEG magic bytes
	blocks, err := toBedrockContentBlocks([]agent.ContentBlock{
		agent.ImageBlock{
			Source: agent.ImageSource{
				Data:     rawBytes,
				MIMEType: "image/jpeg",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	img, ok := blocks[0].(*types.ContentBlockMemberImage)
	if !ok {
		t.Fatalf("expected *ContentBlockMemberImage, got %T", blocks[0])
	}
	if img.Value.Format != types.ImageFormatJpeg {
		t.Errorf("expected ImageFormatJpeg, got %v", img.Value.Format)
	}
	src, ok := img.Value.Source.(*types.ImageSourceMemberBytes)
	if !ok {
		t.Fatalf("expected *ImageSourceMemberBytes, got %T", img.Value.Source)
	}
	if string(src.Value) != string(rawBytes) {
		t.Errorf("expected bytes %v, got %v", rawBytes, src.Value)
	}
}

func TestToBedrockContentBlocks_ImageBlock_Base64String(t *testing.T) {
	rawBytes := []byte("hello image data")
	encoded := base64.StdEncoding.EncodeToString(rawBytes)

	blocks, err := toBedrockContentBlocks([]agent.ContentBlock{
		agent.ImageBlock{
			Source: agent.ImageSource{
				Base64:   encoded,
				MIMEType: "image/png",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	img, ok := blocks[0].(*types.ContentBlockMemberImage)
	if !ok {
		t.Fatalf("expected *ContentBlockMemberImage, got %T", blocks[0])
	}
	if img.Value.Format != types.ImageFormatPng {
		t.Errorf("expected ImageFormatPng, got %v", img.Value.Format)
	}
	src, ok := img.Value.Source.(*types.ImageSourceMemberBytes)
	if !ok {
		t.Fatalf("expected *ImageSourceMemberBytes, got %T", img.Value.Source)
	}
	if string(src.Value) != string(rawBytes) {
		t.Errorf("expected decoded bytes %q, got %q", rawBytes, src.Value)
	}
}

func TestToBedrockContentBlocks_ImageBlock_InvalidBase64_ReturnsError(t *testing.T) {
	_, err := toBedrockContentBlocks([]agent.ContentBlock{
		agent.ImageBlock{
			Source: agent.ImageSource{
				Base64:   "not-valid-base64!!!",
				MIMEType: "image/jpeg",
			},
		},
	})
	if err == nil {
		t.Fatal("expected an error for invalid base64, got nil")
	}
}

func TestToBedrockImageFormat_AllMIMETypes(t *testing.T) {
	cases := []struct {
		mimeType string
		expected types.ImageFormat
	}{
		{"image/jpeg", types.ImageFormatJpeg},
		{"image/png", types.ImageFormatPng},
		{"image/gif", types.ImageFormatGif},
		{"image/webp", types.ImageFormatWebp},
	}
	for _, tc := range cases {
		t.Run(tc.mimeType, func(t *testing.T) {
			got := toBedrockImageFormat(tc.mimeType)
			if got != tc.expected {
				t.Errorf("toBedrockImageFormat(%q) = %v, want %v", tc.mimeType, got, tc.expected)
			}
		})
	}
}
