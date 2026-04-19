// Package openai implements the agent.Provider interface using the
// OpenAI Chat Completions API via the official openai-go SDK.
// Documented in docs/providers.md — update when changing constructor, options, or capabilities.
package openai

import (
	"context"
	"encoding/json"

	"github.com/camilbinas/gude-agents/agent"
	pvdr "github.com/camilbinas/gude-agents/agent/provider"
	"github.com/camilbinas/gude-agents/agent/tool"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

// OpenAIProvider implements agent.Provider using the OpenAI Chat Completions API.
type OpenAIProvider struct {
	client        *openaisdk.Client
	model         string
	maxTokens     int64
	thinkingLevel string // "low", "medium", "high" — mapped to OpenAI's reasoning_effort
}

// Option configures the OpenAIProvider.
type Option func(*options)

type options struct {
	apiKey        string
	baseURL       string
	maxTokens     int64
	thinkingLevel string // "" = not set
}

// WithAPIKey sets the OpenAI API key. Defaults to OPENAI_API_KEY env var.
func WithAPIKey(key string) Option {
	return func(o *options) { o.apiKey = key }
}

// WithBaseURL sets a custom base URL for OpenAI-compatible endpoints.
func WithBaseURL(url string) Option {
	return func(o *options) { o.baseURL = url }
}

// WithMaxTokens sets the max tokens for responses.
func WithMaxTokens(n int64) Option {
	return func(o *options) { o.maxTokens = n }
}

// WithThinking sets the reasoning effort for o-series and reasoning-capable models.
// Use the shared constants: provider.ThinkingLow, provider.ThinkingMedium, provider.ThinkingHigh.
func WithThinking(effort string) Option {
	return func(o *options) { o.thinkingLevel = effort }
}

// Must is a helper that wraps a (*OpenAIProvider, error) call and panics on error.
// Use it to collapse provider creation and agent creation into a single error check
// in examples, scripts, and CLI tools where a provider failure is fatal.
//
//	a, err := agent.Default(openai.Must(openai.Standard()), instructions, tools)
func Must(p *OpenAIProvider, err error) *OpenAIProvider {
	if err != nil {
		panic("openai: " + err.Error())
	}
	return p
}

// New creates a new OpenAIProvider.
func New(model string, opts ...Option) (*OpenAIProvider, error) {
	o := &options{maxTokens: pvdr.DefaultMaxTokens}
	for _, fn := range opts {
		fn(o)
	}

	var clientOpts []option.RequestOption
	if o.apiKey != "" {
		clientOpts = append(clientOpts, option.WithAPIKey(o.apiKey))
	}
	if o.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(o.baseURL))
	}

	client := openaisdk.NewClient(clientOpts...)
	return &OpenAIProvider{
		client:        &client,
		model:         model,
		maxTokens:     o.maxTokens,
		thinkingLevel: o.thinkingLevel,
	}, nil
}

// ModelID returns the model ID this provider is configured to use.
func (p *OpenAIProvider) ModelID() string { return p.model }

// ---------------------------------------------------------------------------
// Converse (non-streaming)
// ---------------------------------------------------------------------------

func (p *OpenAIProvider) Converse(ctx context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	input := p.buildParams(params)
	completion, err := p.client.Chat.Completions.New(ctx, input)
	if err != nil {
		return nil, &agent.ProviderError{Cause: err}
	}
	resp := parseCompletion(completion)
	resp.Usage.InputTokens = int(completion.Usage.PromptTokens)
	resp.Usage.OutputTokens = int(completion.Usage.CompletionTokens)
	return resp, nil
}

// ---------------------------------------------------------------------------
// ConverseStream (streaming)
// ---------------------------------------------------------------------------

func (p *OpenAIProvider) ConverseStream(ctx context.Context, params agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	input := p.buildParams(params)
	input.StreamOptions = openaisdk.ChatCompletionStreamOptionsParam{
		IncludeUsage: openaisdk.Bool(true),
	}

	stream := p.client.Chat.Completions.NewStreaming(ctx, input)

	resp := &agent.ProviderResponse{}

	// Track in-flight tool calls being assembled from deltas.
	// OpenAI streams tool calls by index.
	type toolCallAccum struct {
		id        string
		name      string
		arguments string
	}
	toolCalls := map[int64]*toolCallAccum{}

	for stream.Next() {
		chunk := stream.Current()

		// Extract usage from the final chunk.
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			resp.Usage.InputTokens = int(chunk.Usage.PromptTokens)
			resp.Usage.OutputTokens = int(chunk.Usage.CompletionTokens)
		}

		for _, choice := range chunk.Choices {
			delta := choice.Delta

			// Text content delta.
			if delta.Content != "" {
				resp.Text += delta.Content
				if cb != nil {
					cb(delta.Content)
				}
			}

			// Tool call deltas.
			for _, tc := range delta.ToolCalls {
				acc, ok := toolCalls[tc.Index]
				if !ok {
					acc = &toolCallAccum{}
					toolCalls[tc.Index] = acc
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					acc.arguments += tc.Function.Arguments
				}
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, &agent.ProviderError{Cause: err}
	}

	// Collect accumulated tool calls in index order.
	for i := int64(0); i < int64(len(toolCalls)); i++ {
		acc := toolCalls[i]
		if acc == nil {
			continue
		}
		input := json.RawMessage(acc.arguments)
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		resp.ToolCalls = append(resp.ToolCalls, tool.Call{
			ToolUseID: acc.id,
			Name:      acc.name,
			Input:     input,
		})
	}

	return resp, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (p *OpenAIProvider) buildParams(params agent.ConverseParams) openaisdk.ChatCompletionNewParams {
	input := openaisdk.ChatCompletionNewParams{
		Model:    shared.ChatModel(p.model),
		Messages: toOpenAIMessages(params.Messages, params.System),
	}
	if p.maxTokens > 0 {
		input.MaxCompletionTokens = openaisdk.Int(p.maxTokens)
	}
	if len(params.ToolConfig) > 0 {
		input.Tools = toOpenAITools(params.ToolConfig)
	}
	if params.ToolChoice != nil {
		input.ToolChoice = toOpenAIToolChoice(params.ToolChoice)
	}
	if p.thinkingLevel != "" {
		input.ReasoningEffort = shared.ReasoningEffort(p.thinkingLevel)
	}
	// Apply inference config overrides.
	// TopK is silently ignored — OpenAI Chat Completions API does not support it.
	if cfg := params.InferenceConfig; cfg != nil {
		if cfg.Temperature != nil {
			input.Temperature = openaisdk.Float(*cfg.Temperature)
		}
		if cfg.TopP != nil {
			input.TopP = openaisdk.Float(*cfg.TopP)
		}
		if cfg.StopSequences != nil {
			input.Stop = openaisdk.ChatCompletionNewParamsStopUnion{
				OfStringArray: cfg.StopSequences,
			}
		}
		if cfg.MaxTokens != nil {
			input.MaxCompletionTokens = openaisdk.Int(int64(*cfg.MaxTokens))
		}
	}
	return input
}

func parseCompletion(completion *openaisdk.ChatCompletion) *agent.ProviderResponse {
	resp := &agent.ProviderResponse{}
	if len(completion.Choices) == 0 {
		return resp
	}
	msg := completion.Choices[0].Message
	resp.Text = msg.Content
	for _, tc := range msg.ToolCalls {
		if tc.Type == "function" {
			fn := tc.AsFunction()
			input := json.RawMessage(fn.Function.Arguments)
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			resp.ToolCalls = append(resp.ToolCalls, tool.Call{
				ToolUseID: fn.ID,
				Name:      fn.Function.Name,
				Input:     input,
			})
		}
	}
	return resp
}

// toOpenAIMessages converts framework Messages to OpenAI ChatCompletionMessageParamUnion.
// The system prompt is prepended as a system message.
func toOpenAIMessages(msgs []agent.Message, system string) []openaisdk.ChatCompletionMessageParamUnion {
	var out []openaisdk.ChatCompletionMessageParamUnion

	if system != "" {
		out = append(out, openaisdk.SystemMessage(system))
	}

	for _, m := range msgs {
		switch m.Role {
		case agent.RoleUser:
			out = append(out, toOpenAIUserMessages(m.Content)...)
		case agent.RoleAssistant:
			out = append(out, toOpenAIAssistantMessage(m.Content))
		}
	}
	return out
}

// toOpenAIUserMessages converts user content blocks to OpenAI messages.
// ToolResultBlocks become individual tool messages; TextBlocks become user messages.
func toOpenAIUserMessages(blocks []agent.ContentBlock) []openaisdk.ChatCompletionMessageParamUnion {
	var out []openaisdk.ChatCompletionMessageParamUnion
	for _, b := range blocks {
		switch v := b.(type) {
		case agent.TextBlock:
			out = append(out, openaisdk.UserMessage(v.Text))
		case agent.ToolResultBlock:
			out = append(out, openaisdk.ToolMessage(v.Content, v.ToolUseID))
		}
	}
	return out
}

// toOpenAIAssistantMessage converts assistant content blocks to an OpenAI assistant message.
func toOpenAIAssistantMessage(blocks []agent.ContentBlock) openaisdk.ChatCompletionMessageParamUnion {
	var text string
	var toolCalls []openaisdk.ChatCompletionMessageToolCallUnionParam

	for _, b := range blocks {
		switch v := b.(type) {
		case agent.TextBlock:
			text += v.Text
		case agent.ToolUseBlock:
			toolCalls = append(toolCalls, openaisdk.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openaisdk.ChatCompletionMessageFunctionToolCallParam{
					ID: v.ToolUseID,
					Function: openaisdk.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      v.Name,
						Arguments: string(v.Input),
					},
				},
			})
		}
	}

	msg := openaisdk.ChatCompletionAssistantMessageParam{
		ToolCalls: toolCalls,
	}
	if text != "" {
		msg.Content = openaisdk.ChatCompletionAssistantMessageParamContentUnion{
			OfString: openaisdk.String(text),
		}
	}
	return openaisdk.ChatCompletionMessageParamUnion{
		OfAssistant: &msg,
	}
}

// toOpenAITools converts framework ToolSpecs to OpenAI function tool params.
func toOpenAITools(specs []tool.Spec) []openaisdk.ChatCompletionToolUnionParam {
	tools := make([]openaisdk.ChatCompletionToolUnionParam, len(specs))
	for i, s := range specs {
		tools[i] = openaisdk.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        s.Name,
			Description: openaisdk.String(s.Description),
			Parameters:  shared.FunctionParameters(s.InputSchema),
		})
	}
	return tools
}

// toOpenAIToolChoice maps a framework ToolChoice to the OpenAI tool_choice parameter.
func toOpenAIToolChoice(tc *tool.Choice) openaisdk.ChatCompletionToolChoiceOptionUnionParam {
	switch tc.Mode {
	case tool.ChoiceAuto:
		return openaisdk.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: openaisdk.String(string(openaisdk.ChatCompletionToolChoiceOptionAutoAuto)),
		}
	case tool.ChoiceAny:
		return openaisdk.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: openaisdk.String(string(openaisdk.ChatCompletionToolChoiceOptionAutoRequired)),
		}
	case tool.ChoiceTool:
		return openaisdk.ToolChoiceOptionFunctionToolChoice(
			openaisdk.ChatCompletionNamedToolChoiceFunctionParam{
				Name: tc.Name,
			},
		)
	default:
		return openaisdk.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: openaisdk.String(string(openaisdk.ChatCompletionToolChoiceOptionAutoAuto)),
		}
	}
}
