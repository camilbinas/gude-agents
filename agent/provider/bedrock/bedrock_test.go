package bedrock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	pvdr "github.com/camilbinas/gude-agents/agent/provider"
	"github.com/camilbinas/gude-agents/agent/tool"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// toBedrockRole
// ---------------------------------------------------------------------------

// ptr returns a pointer to the given int32 value. Used in tests to set *int32 fields inline.
func ptr(v int32) *int32 { return &v }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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
	}, "", false)
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
	}, "", false)
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
	}, "", false)
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
	}, "", false)
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
	}, "", false)
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
	blocks, err := toBedrockContentBlocks(nil, "", false)
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
	}, "", false)
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
	}, "", false)
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
	}, "", false)
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
// stream event reduction
// ---------------------------------------------------------------------------

func TestApplyStreamEvent_TextAndThinking(t *testing.T) {
	resp := &agent.ModelResponse{}
	state := &streamState{}
	var events []agent.ModelEvent
	emit := func(event agent.ModelEvent) { events = append(events, event) }

	applyStreamEvent(resp, state, &types.ConverseStreamOutputMemberContentBlockDelta{
		Value: types.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta:             &types.ContentBlockDeltaMemberText{Value: "Hello, "},
		},
	}, emit)
	applyStreamEvent(resp, state, &types.ConverseStreamOutputMemberContentBlockDelta{
		Value: types.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta:             &types.ContentBlockDeltaMemberText{Value: "world!"},
		},
	}, emit)
	applyStreamEvent(resp, state, &types.ConverseStreamOutputMemberContentBlockDelta{
		Value: types.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(1),
			Delta: &types.ContentBlockDeltaMemberReasoningContent{Value: &types.ReasoningContentBlockDeltaMemberText{
				Value: "check facts",
			}},
		},
	}, emit)
	applyStreamEvent(resp, state, &types.ConverseStreamOutputMemberContentBlockStop{
		Value: types.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(1)},
	}, emit)
	applyStreamEvent(resp, state, &types.ConverseStreamOutputMemberContentBlockDelta{
		Value: types.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(2),
			Delta: &types.ContentBlockDeltaMemberReasoningContent{Value: &types.ReasoningContentBlockDeltaMemberText{
				Value: "; cite source",
			}},
		},
	}, emit)
	applyStreamEvent(resp, state, &types.ConverseStreamOutputMemberContentBlockStop{
		Value: types.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(2)},
	}, emit)

	if resp.Text != "Hello, world!" {
		t.Fatalf("Text = %q, want %q", resp.Text, "Hello, world!")
	}
	if got := resp.Metadata["thinking"]; got != "check facts; cite source" {
		t.Fatalf("thinking metadata = %q, want %q", got, "check facts; cite source")
	}
	wantEvents := []agent.ModelEvent{
		{Type: agent.ModelEventText, Text: "Hello, "},
		{Type: agent.ModelEventText, Text: "world!"},
		{Type: agent.ModelEventThinking, Text: "check facts"},
		{Type: agent.ModelEventThinking, Text: "; cite source"},
	}
	if len(events) != len(wantEvents) {
		t.Fatalf("got %d events, want %d", len(events), len(wantEvents))
	}
	for i := range wantEvents {
		if events[i] != wantEvents[i] {
			t.Errorf("event %d = %#v, want %#v", i, events[i], wantEvents[i])
		}
	}
}

func TestApplyStreamEvent_NilEmitter(t *testing.T) {
	resp := &agent.ModelResponse{}
	applyStreamEvent(resp, &streamState{}, &types.ConverseStreamOutputMemberContentBlockDelta{
		Value: types.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0),
			Delta:             &types.ContentBlockDeltaMemberText{Value: "hello"},
		},
	}, nil)
	if resp.Text != "hello" {
		t.Fatalf("Text = %q, want hello", resp.Text)
	}
}

func TestApplyStreamEvent_ToolUse(t *testing.T) {
	tests := []struct {
		name   string
		parts  []string
		input  string
		toolID string
	}{
		{name: "split input", parts: []string{`{"query":`, `"test"}`}, input: `{"query":"test"}`, toolID: "tu-123"},
		{name: "empty input", input: `{}`, toolID: "tu-456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &agent.ModelResponse{}
			state := &streamState{}
			applyStreamEvent(resp, state, &types.ConverseStreamOutputMemberContentBlockStart{
				Value: types.ContentBlockStartEvent{
					ContentBlockIndex: aws.Int32(0),
					Start: &types.ContentBlockStartMemberToolUse{Value: types.ToolUseBlockStart{
						Name:      aws.String("search"),
						ToolUseId: aws.String(tt.toolID),
					}},
				},
			}, nil)
			for _, part := range tt.parts {
				applyStreamEvent(resp, state, &types.ConverseStreamOutputMemberContentBlockDelta{
					Value: types.ContentBlockDeltaEvent{
						ContentBlockIndex: aws.Int32(0),
						Delta: &types.ContentBlockDeltaMemberToolUse{Value: types.ToolUseBlockDelta{
							Input: aws.String(part),
						}},
					},
				}, nil)
			}
			applyStreamEvent(resp, state, &types.ConverseStreamOutputMemberContentBlockStop{
				Value: types.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)},
			}, nil)

			if len(resp.ToolCalls) != 1 {
				t.Fatalf("got %d tool calls, want 1", len(resp.ToolCalls))
			}
			call := resp.ToolCalls[0]
			if call.ToolUseID != tt.toolID || call.Name != "search" || string(call.Input) != tt.input {
				t.Fatalf("tool call = %#v, want ID %q name search input %s", call, tt.toolID, tt.input)
			}
		})
	}
}

func TestApplyStreamEvent_UsageAndCacheMetadata(t *testing.T) {
	resp := &agent.ModelResponse{}
	applyStreamEvent(resp, &streamState{}, &types.ConverseStreamOutputMemberMetadata{
		Value: types.ConverseStreamMetadataEvent{Usage: &types.TokenUsage{
			InputTokens:           aws.Int32(100),
			OutputTokens:          aws.Int32(50),
			CacheReadInputTokens:  aws.Int32(30),
			CacheWriteInputTokens: aws.Int32(20),
		}},
	}, nil)

	want := (agent.TokenUsage{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 30, CacheWriteTokens: 20})
	if resp.Usage != want {
		t.Fatalf("Usage = %#v, want %#v", resp.Usage, want)
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
		resp := &agent.ModelResponse{}
		applyTokenUsage(resp, &types.TokenUsage{
			InputTokens:  aws.Int32(inputTokens),
			OutputTokens: aws.Int32(outputTokens),
		})

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
	p := &BedrockProvider{maxTokens: ptr(4096)}
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
	p := &BedrockProvider{maxTokens: ptr(8192)}
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
	p := &BedrockProvider{maxTokens: ptr(8192)}
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
	p := &BedrockProvider{maxTokens: ptr(8192)}
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
	p := &BedrockProvider{maxTokens: ptr(8192)}
	maxTok := 2048
	ic := p.buildInferenceConfiguration(&agent.InferenceConfig{MaxTokens: &maxTok})
	if aws.ToInt32(ic.MaxTokens) != 2048 {
		t.Errorf("expected MaxTokens 2048, got %d", aws.ToInt32(ic.MaxTokens))
	}
}

func TestBuildInferenceConfiguration_AllFieldsSet(t *testing.T) {
	p := &BedrockProvider{maxTokens: ptr(8192)}
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
		thinkingStyle:  thinkingStyleClaude,
		thinkingEffort: pvdr.ThinkingMedium,
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
		thinkingStyle:  thinkingStyleClaude,
		thinkingEffort: pvdr.ThinkingHigh,
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
// ImageBlock translation tests
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
	}, "", false)
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
	}, "", false)
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
	}, "", false)
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

func TestToBedrockContentBlocks_DocumentS3Location(t *testing.T) {
	blocks, err := toBedrockContentBlocks([]agent.ContentBlock{agent.DocumentBlock{Source: agent.DocumentSource{
		S3URI:         "s3://example-bucket/reports/quarterly.pdf",
		S3BucketOwner: "123456789012",
		MIMEType:      "application/pdf",
		Name:          "quarterly.pdf",
	}}}, "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	doc, ok := blocks[0].(*types.ContentBlockMemberDocument)
	if !ok {
		t.Fatalf("expected *ContentBlockMemberDocument, got %T", blocks[0])
	}
	if aws.ToString(doc.Value.Name) != "quarterly" {
		t.Errorf("Name = %q, want %q", aws.ToString(doc.Value.Name), "quarterly")
	}
	if doc.Value.Format != types.DocumentFormatPdf {
		t.Errorf("Format = %q, want %q", doc.Value.Format, types.DocumentFormatPdf)
	}
	s3, ok := doc.Value.Source.(*types.DocumentSourceMemberS3Location)
	if !ok {
		t.Fatalf("expected *DocumentSourceMemberS3Location, got %T", doc.Value.Source)
	}
	if aws.ToString(s3.Value.Uri) != "s3://example-bucket/reports/quarterly.pdf" {
		t.Errorf("URI = %q, want %q", aws.ToString(s3.Value.Uri), "s3://example-bucket/reports/quarterly.pdf")
	}
	if aws.ToString(s3.Value.BucketOwner) != "123456789012" {
		t.Errorf("BucketOwner = %q, want %q", aws.ToString(s3.Value.BucketOwner), "123456789012")
	}
}

func TestToBedrockContentBlocks_DocumentFileIDRejected(t *testing.T) {
	_, err := toBedrockContentBlocks([]agent.ContentBlock{agent.DocumentBlock{Source: agent.DocumentSource{
		FileID: "file_abc123",
	}}}, "", false)
	if err == nil || err.Error() != "DocumentBlock: Bedrock does not support provider file IDs" {
		t.Fatalf("error = %v, want provider file ID rejection", err)
	}
}

func TestURLSourceFetchesUseConfiguredClient(t *testing.T) {
	const imageBody = "image bytes"
	const documentBody = "document bytes"

	client := newURLFetchClient(time.Second)
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", req.Method)
		}
		var body string
		switch req.URL.Path {
		case "/image":
			body = imageBody
		case "/document":
			body = documentBody
		default:
			t.Fatalf("unexpected URL path %q", req.URL.Path)
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        make(http.Header),
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
			Request:       req,
		}, nil
	})

	image, err := imageBytesWithFetcher(context.Background(), client, agent.ImageSource{URL: "https://8.8.8.8/image"})
	if err != nil {
		t.Fatalf("imageBytesWithFetcher() error = %v", err)
	}
	if string(image) != imageBody {
		t.Errorf("imageBytesWithFetcher() = %q, want %q", image, imageBody)
	}

	document, err := documentBytesWithFetcher(context.Background(), client, agent.DocumentSource{URL: "https://8.8.8.8/document"})
	if err != nil {
		t.Fatalf("documentBytesWithFetcher() error = %v", err)
	}
	if string(document) != documentBody {
		t.Errorf("documentBytesWithFetcher() = %q, want %q", document, documentBody)
	}
}

func TestFetchURLBytesRejectsUnsafeTargetsBeforeTransport(t *testing.T) {
	unsafeURLs := []struct {
		name   string
		url    string
		reason string
	}{
		{name: "HTTP", url: "http://8.8.8.8/image", reason: "only HTTPS"},
		{name: "loopback IPv4", url: "https://127.0.0.1/image", reason: "loopback"},
		{name: "private IPv4", url: "https://10.0.0.1/image", reason: "private"},
		{name: "private IPv6", url: "https://[fd00::1]/image", reason: "private"},
		{name: "link-local IPv4", url: "https://169.254.10.1/image", reason: "link-local"},
		{name: "link-local IPv6", url: "https://[fe80::1]/image", reason: "link-local"},
		{name: "metadata", url: "https://169.254.169.254/image", reason: "metadata"},
	}

	for _, tt := range unsafeURLs {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				called = true
				return nil, errors.New("transport should not be called")
			})}
			_, err := fetchURLBytes(context.Background(), client, tt.url, "image")
			if err == nil || !strings.Contains(err.Error(), tt.reason) {
				t.Fatalf("fetchURLBytes() error = %v, want %q rejection", err, tt.reason)
			}
			if called {
				t.Fatal("unsafe URL reached transport")
			}
		})
	}
}

func TestValidateURLFetchTargetRejectsPrivateDNSAnswers(t *testing.T) {
	lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "private.example.test":
			return []net.IPAddr{{IP: net.ParseIP("10.0.0.4")}}, nil
		case "mixed.example.test":
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("10.0.0.4")}}, nil
		case "public.example.test":
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		default:
			return nil, errors.New("unexpected lookup")
		}
	}
	policy := urlFetchPolicy{lookupIPAddr: lookup}

	for _, host := range []string{"private.example.test", "mixed.example.test"} {
		target, err := url.Parse("https://" + host + "/image")
		if err != nil {
			t.Fatal(err)
		}
		err = validateURLFetchTarget(context.Background(), target, policy)
		if err == nil || !strings.Contains(err.Error(), "private") {
			t.Errorf("validateURLFetchTarget(%q) error = %v, want private-address rejection", host, err)
		}
	}

	target, err := url.Parse("https://public.example.test/image")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateURLFetchTarget(context.Background(), target, policy); err != nil {
		t.Fatalf("validateURLFetchTarget(public URL) error = %v", err)
	}
}

func TestFetchURLBytesRejectsUnsafeRedirectTarget(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://127.0.0.1/private"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})}

	_, err := fetchURLBytes(context.Background(), client, "https://8.8.8.8/start", "image")
	if err == nil || !strings.Contains(err.Error(), "unsafe redirect target") {
		t.Fatalf("fetchURLBytes() error = %v, want unsafe redirect rejection", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1; unsafe redirect must not be requested", requests)
	}
}

func TestFetchURLBytesBoundsRedirects(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://8.8.8.8/again"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})}

	_, err := fetchURLBytes(context.Background(), client, "https://8.8.8.8/start", "document")
	if err == nil || !strings.Contains(err.Error(), "exceeded 3 redirects") {
		t.Fatalf("fetchURLBytes() error = %v, want redirect-limit error", err)
	}
	if requests != defaultURLFetchMaxRedirects+1 {
		t.Fatalf("requests = %d, want %d", requests, defaultURLFetchMaxRedirects+1)
	}
}

func TestFetchURLBytesRejectsNon2xxResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("unavailable")),
			Request:    req,
		}, nil
	})}

	_, err := fetchURLBytes(context.Background(), client, "https://8.8.8.8/image", "image")
	if err == nil || !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("fetchURLBytes() error = %v, want non-2xx status error", err)
	}
}

func TestFetchURLBytesRejectsOversizedResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        make(http.Header),
			Body:          io.NopCloser(strings.NewReader(strings.Repeat("x", int(defaultURLFetchMaxResponseBytes+1)))),
			ContentLength: -1,
			Request:       req,
		}, nil
	})}

	_, err := fetchURLBytes(context.Background(), client, "https://8.8.8.8/document", "document")
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("fetchURLBytes() error = %v, want size-limit error", err)
	}
}

func TestFetchURLBytesHonorsContextCancellation(t *testing.T) {
	requestStarted := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		close(requestStarted)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := fetchURLBytes(ctx, client, "https://8.8.8.8/image", "image")
		errCh <- err
	}()

	select {
	case <-requestStarted:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("URL request did not start")
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("fetchURLBytes() error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("fetchURLBytes() did not return after context cancellation")
	}
}

func TestWithURLFetchAllowPrivateNetworks(t *testing.T) {
	o := &options{}
	WithURLFetchAllowPrivateNetworks()(o)
	if !o.allowPrivateURLFetches {
		t.Fatal("trusted internal URL-fetch option did not enable private networks")
	}

	lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.4")}}, nil
	}
	policy := urlFetchPolicy{allowPrivateNetworks: o.allowPrivateURLFetches, lookupIPAddr: lookup}
	target, err := url.Parse("https://internal.example.test/image")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateURLFetchTarget(context.Background(), target, policy); err != nil {
		t.Fatalf("private HTTPS target with trusted option error = %v", err)
	}
	if err := validateURLFetchIP(netip.MustParseAddr("127.0.0.1"), policy); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("trusted option loopback validation error = %v, want loopback rejection", err)
	}
	if err := validateURLFetchIP(urlFetchMetadataIPv4, policy); err == nil || !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("trusted option metadata validation error = %v, want metadata rejection", err)
	}
}

func TestNewURLFetchClientUsesFiniteConfigurableTimeout(t *testing.T) {
	if got := newURLFetchClient(0).Timeout; got != defaultURLFetchTimeout {
		t.Errorf("default timeout = %s, want %s", got, defaultURLFetchTimeout)
	}
	if got := newURLFetchClient(125 * time.Millisecond).Timeout; got != 125*time.Millisecond {
		t.Errorf("configured timeout = %s, want 125ms", got)
	}

	o := &options{}
	WithURLFetchTimeout(250 * time.Millisecond)(o)
	if got := newURLFetchClient(o.urlFetchTimeout).Timeout; got != 250*time.Millisecond {
		t.Errorf("option timeout = %s, want 250ms", got)
	}
}
