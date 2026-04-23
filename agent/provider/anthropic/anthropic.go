// Package anthropic implements the agent.Provider interface using the
// Anthropic Messages API via the official anthropic-sdk-go.
// Documented in docs/providers.md — update when changing constructor, options, or capabilities.
package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	pvdr "github.com/camilbinas/gude-agents/agent/provider"
	"github.com/camilbinas/gude-agents/agent/tool"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// AnthropicProvider implements agent.Provider using the Anthropic Messages API.
type AnthropicProvider struct {
	client        anthropicsdk.Client
	model         anthropicsdk.Model
	maxTokens     int64
	thinkingLevel string // "low", "medium", "high" — empty = disabled
}

// Option configures the AnthropicProvider.
type Option func(*options)

type options struct {
	apiKey        string
	maxTokens     int64
	thinkingLevel string // "low", "medium", "high" — empty = disabled
}

// WithAPIKey sets the Anthropic API key. Defaults to ANTHROPIC_API_KEY env var.
func WithAPIKey(key string) Option {
	return func(o *options) { o.apiKey = key }
}

// WithMaxTokens sets the max tokens for responses.
func WithMaxTokens(n int64) Option {
	return func(o *options) { o.maxTokens = n }
}

// WithThinking enables extended thinking at the given effort level.
// Use the shared constants: provider.ThinkingLow, provider.ThinkingMedium, provider.ThinkingHigh.
func WithThinking(effort string) Option {
	return func(o *options) { o.thinkingLevel = effort }
}

// Must is a helper that wraps a (*AnthropicProvider, error) call and panics on error.
// Use it to collapse provider creation and agent creation into a single error check
// in examples, scripts, and CLI tools where a provider failure is fatal.
//
//	a, err := agent.Default(anthropic.Must(anthropic.Standard()), instructions, tools)
func Must(p *AnthropicProvider, err error) *AnthropicProvider {
	if err != nil {
		panic("anthropic: " + err.Error())
	}
	return p
}

// New creates a new AnthropicProvider.
func New(model string, opts ...Option) (*AnthropicProvider, error) {
	o := &options{maxTokens: pvdr.DefaultMaxTokens}
	for _, fn := range opts {
		fn(o)
	}

	var clientOpts []option.RequestOption
	if o.apiKey != "" {
		clientOpts = append(clientOpts, option.WithAPIKey(o.apiKey))
	}

	return &AnthropicProvider{
		client:        anthropicsdk.NewClient(clientOpts...),
		model:         anthropicsdk.Model(model),
		maxTokens:     o.maxTokens,
		thinkingLevel: o.thinkingLevel,
	}, nil
}
func (p *AnthropicProvider) ModelID() string { return string(p.model) }

// Client returns the underlying Anthropic SDK client.
// Use this for direct SDK access when you need provider-specific features
// not exposed through the agent.Provider interface.
func (p *AnthropicProvider) Client() *anthropicsdk.Client { return &p.client }

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
	var currentThinking string
	var inThinkingBlock bool

	for stream.Next() {
		event := stream.Current()

		switch event.Type {
		case "content_block_start":
			ev := event.AsContentBlockStart()
			switch ev.ContentBlock.Type {
			case "tool_use":
				currentToolID = ev.ContentBlock.ID
				currentToolName = ev.ContentBlock.Name
				currentToolInput = ""
			case "thinking":
				inThinkingBlock = true
				currentThinking = ""
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
			case "thinking_delta":
				currentThinking += ev.Delta.Thinking
				if cb != nil && params.ThinkingCallback != nil {
					params.ThinkingCallback(ev.Delta.Thinking)
				}
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
			if inThinkingBlock {
				// Stash thinking text so callers can inspect it if needed.
				if resp.Metadata == nil {
					resp.Metadata = map[string]any{}
				}
				existing, _ := resp.Metadata["thinking"].(string)
				resp.Metadata["thinking"] = existing + currentThinking
				inThinkingBlock = false
				currentThinking = ""
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
	if p.thinkingLevel != "" {
		input.Thinking = anthropicsdk.ThinkingConfigParamOfEnabled(pvdr.ThinkingBudgets[p.thinkingLevel])
	}
	// Apply inference config overrides.
	if cfg := params.InferenceConfig; cfg != nil {
		if cfg.Temperature != nil {
			input.Temperature = param.NewOpt(*cfg.Temperature)
		}
		if cfg.TopP != nil {
			input.TopP = param.NewOpt(*cfg.TopP)
		}
		if cfg.TopK != nil {
			input.TopK = param.NewOpt(int64(*cfg.TopK))
		}
		if cfg.StopSequences != nil {
			input.StopSequences = cfg.StopSequences
		}
		if cfg.MaxTokens != nil {
			input.MaxTokens = int64(*cfg.MaxTokens)
		}
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
			Content: toAnthropicContentBlocks(m.Content, m.Role),
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

// imageBytes returns the raw bytes from an ImageSource.
// If Source.Data is set, it is returned directly.
// If Source.Base64 is set, it is decoded from standard base64.
func imageBytes(src agent.ImageSource) ([]byte, error) {
	if len(src.Data) > 0 {
		return src.Data, nil
	}
	b, err := base64.StdEncoding.DecodeString(src.Base64)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	return b, nil
}

func toAnthropicContentBlocks(blocks []agent.ContentBlock, role agent.Role) []anthropicsdk.ContentBlockParamUnion {
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
		case agent.ImageBlock:
			if role == agent.RoleAssistant {
				log.Printf("anthropic: ImageBlock in assistant-role message is not supported and will be skipped")
				continue
			}
			if v.Source.URL != "" {
				// URL source: pass directly to the provider.
				out = append(out, anthropicsdk.ContentBlockParamUnion{
					OfImage: &anthropicsdk.ImageBlockParam{
						Source: anthropicsdk.ImageBlockParamSourceUnion{
							OfURL: &anthropicsdk.URLImageSourceParam{
								URL: v.Source.URL,
							},
						},
					},
				})
			} else {
				var encoded string
				if v.Source.Base64 != "" {
					encoded = v.Source.Base64
				} else {
					bytes, err := imageBytes(v.Source)
					if err != nil {
						log.Printf("anthropic: failed to get image bytes: %v (skipping block)", err)
						continue
					}
					encoded = base64.StdEncoding.EncodeToString(bytes)
				}
				out = append(out, anthropicsdk.NewImageBlockBase64(
					string(v.Source.MIMEType),
					encoded,
				))
			}
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
