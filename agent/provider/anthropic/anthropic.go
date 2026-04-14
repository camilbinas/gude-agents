// Package anthropic implements the agent.Provider interface using the
// Anthropic Messages API via the official anthropic-sdk-go.
// Documented in docs/providers.md — update when changing constructor, options, or capabilities.
package anthropic

import (
	"context"
	"encoding/json"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicProvider implements agent.Provider using the Anthropic Messages API.
type AnthropicProvider struct {
	client    anthropicsdk.Client
	model     anthropicsdk.Model
	maxTokens int64
}

// Option configures the AnthropicProvider.
type Option func(*options)

type options struct {
	apiKey    string
	maxTokens int64
}

// WithAPIKey sets the Anthropic API key. Defaults to ANTHROPIC_API_KEY env var.
func WithAPIKey(key string) Option {
	return func(o *options) { o.apiKey = key }
}

// WithMaxTokens sets the max tokens for responses. Defaults to 4096.
func WithMaxTokens(n int64) Option {
	return func(o *options) { o.maxTokens = n }
}

// New creates a new AnthropicProvider.
func New(model string, opts ...Option) (*AnthropicProvider, error) {
	o := &options{maxTokens: 8192}
	for _, fn := range opts {
		fn(o)
	}

	var clientOpts []option.RequestOption
	if o.apiKey != "" {
		clientOpts = append(clientOpts, option.WithAPIKey(o.apiKey))
	}

	return &AnthropicProvider{
		client:    anthropicsdk.NewClient(clientOpts...),
		model:     anthropicsdk.Model(model),
		maxTokens: o.maxTokens,
	}, nil
}

// ModelId returns the model ID this provider is configured to use.
func (p *AnthropicProvider) ModelId() string { return string(p.model) }

// ---------------------------------------------------------------------------
// Converse (non-streaming)
// ---------------------------------------------------------------------------

func (p *AnthropicProvider) Converse(ctx context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	input := p.buildParams(params)
	msg, err := p.client.Messages.New(ctx, input)
	if err != nil {
		return nil, &agent.ProviderError{Cause: err}
	}
	resp := parseMessage(msg)
	resp.Usage.InputTokens = int(msg.Usage.InputTokens)
	resp.Usage.OutputTokens = int(msg.Usage.OutputTokens)
	return resp, nil
}

// ---------------------------------------------------------------------------
// ConverseStream (streaming)
// ---------------------------------------------------------------------------

func (p *AnthropicProvider) ConverseStream(ctx context.Context, params agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	input := p.buildParams(params)
	stream := p.client.Messages.NewStreaming(ctx, input)

	resp := &agent.ProviderResponse{}
	var currentToolID, currentToolName, currentToolInput string

	for stream.Next() {
		event := stream.Current()

		switch event.Type {
		case "content_block_start":
			ev := event.AsContentBlockStart()
			if ev.ContentBlock.Type == "tool_use" {
				currentToolID = ev.ContentBlock.ID
				currentToolName = ev.ContentBlock.Name
				currentToolInput = ""
			}

		case "content_block_delta":
			ev := event.AsContentBlockDelta()
			switch ev.Delta.Type {
			case "text_delta":
				resp.Text += ev.Delta.Text
				if cb != nil {
					cb(ev.Delta.Text)
				}
			case "input_json_delta":
				currentToolInput += ev.Delta.PartialJSON
			}

		case "content_block_stop":
			if currentToolName != "" {
				input := json.RawMessage(currentToolInput)
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				resp.ToolCalls = append(resp.ToolCalls, tool.Call{
					ToolUseID: currentToolID,
					Name:      currentToolName,
					Input:     input,
				})
				currentToolID = ""
				currentToolName = ""
				currentToolInput = ""
			}

		case "message_start":
			ev := event.AsMessageStart()
			resp.Usage.InputTokens = int(ev.Message.Usage.InputTokens)

		case "message_delta":
			ev := event.AsMessageDelta()
			resp.Usage.OutputTokens = int(ev.Usage.OutputTokens)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, &agent.ProviderError{Cause: err}
	}

	return resp, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (p *AnthropicProvider) buildParams(params agent.ConverseParams) anthropicsdk.MessageNewParams {
	input := anthropicsdk.MessageNewParams{
		Model:     p.model,
		MaxTokens: p.maxTokens,
		Messages:  toAnthropicMessages(params.Messages),
	}
	if params.System != "" {
		input.System = []anthropicsdk.TextBlockParam{
			{Text: params.System},
		}
	}
	if len(params.ToolConfig) > 0 {
		input.Tools = toAnthropicTools(params.ToolConfig)
	}
	if params.ToolChoice != nil {
		input.ToolChoice = toAnthropicToolChoice(params.ToolChoice)
	}
	return input
}

func toAnthropicToolChoice(tc *tool.Choice) anthropicsdk.ToolChoiceUnionParam {
	switch tc.Mode {
	case tool.ChoiceAuto:
		return anthropicsdk.ToolChoiceUnionParam{OfAuto: &anthropicsdk.ToolChoiceAutoParam{}}
	case tool.ChoiceAny:
		return anthropicsdk.ToolChoiceUnionParam{OfAny: &anthropicsdk.ToolChoiceAnyParam{}}
	case tool.ChoiceTool:
		return anthropicsdk.ToolChoiceUnionParam{OfTool: &anthropicsdk.ToolChoiceToolParam{Name: tc.Name}}
	default:
		return anthropicsdk.ToolChoiceUnionParam{OfAuto: &anthropicsdk.ToolChoiceAutoParam{}}
	}
}

func parseMessage(msg *anthropicsdk.Message) *agent.ProviderResponse {
	resp := &agent.ProviderResponse{}
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			resp.Text += block.Text
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, tool.Call{
				ToolUseID: block.ID,
				Name:      block.Name,
				Input:     block.Input,
			})
		}
	}
	return resp
}

func toAnthropicMessages(msgs []agent.Message) []anthropicsdk.MessageParam {
	out := make([]anthropicsdk.MessageParam, len(msgs))
	for i, m := range msgs {
		out[i] = anthropicsdk.MessageParam{
			Role:    toAnthropicRole(m.Role),
			Content: toAnthropicContentBlocks(m.Content),
		}
	}
	return out
}

func toAnthropicRole(r agent.Role) anthropicsdk.MessageParamRole {
	switch r {
	case agent.RoleAssistant:
		return anthropicsdk.MessageParamRoleAssistant
	default:
		return anthropicsdk.MessageParamRoleUser
	}
}

func toAnthropicContentBlocks(blocks []agent.ContentBlock) []anthropicsdk.ContentBlockParamUnion {
	out := make([]anthropicsdk.ContentBlockParamUnion, 0, len(blocks))
	for _, b := range blocks {
		switch v := b.(type) {
		case agent.TextBlock:
			out = append(out, anthropicsdk.NewTextBlock(v.Text))
		case agent.ToolUseBlock:
			var input any = map[string]any{}
			if len(v.Input) > 0 {
				if err := json.Unmarshal(v.Input, &input); err != nil {
					input = map[string]any{}
				}
			}
			out = append(out, anthropicsdk.NewToolUseBlock(v.ToolUseID, input, v.Name))
		case agent.ToolResultBlock:
			out = append(out, anthropicsdk.NewToolResultBlock(v.ToolUseID, v.Content, v.IsError))
		}
	}
	return out
}

func toAnthropicTools(specs []tool.Spec) []anthropicsdk.ToolUnionParam {
	tools := make([]anthropicsdk.ToolUnionParam, len(specs))
	for i, s := range specs {
		props, _ := s.InputSchema["properties"]
		required, _ := s.InputSchema["required"].([]string)
		// Handle []any from JSON unmarshaling
		if required == nil {
			if reqAny, ok := s.InputSchema["required"].([]any); ok {
				for _, r := range reqAny {
					if str, ok := r.(string); ok {
						required = append(required, str)
					}
				}
			}
		}

		tools[i] = anthropicsdk.ToolUnionParam{
			OfTool: &anthropicsdk.ToolParam{
				Name:        s.Name,
				Description: anthropicsdk.String(s.Description),
				InputSchema: anthropicsdk.ToolInputSchemaParam{
					Properties: props,
					Required:   required,
				},
			},
		}
	}
	return tools
}
