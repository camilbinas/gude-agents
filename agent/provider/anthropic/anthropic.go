// Package anthropic implements the agent.Provider interface using the
// Anthropic Messages API via the official anthropic-sdk-go.
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
	client         anthropicsdk.Client
	model          anthropicsdk.Model
	maxTokens      *int64              // nil = no explicit limit (provider default)
	thinkingEffort pvdr.ThinkingEffort // empty = effort not set
	thinkingBudget int64               // 0 = budget not set; takes precedence over effort
	cachingEnabled bool
	capabilities   agent.ModelCapabilities
}

// Option configures the AnthropicProvider.
type Option func(*options)

type options struct {
	apiKey         string
	maxTokens      *int64 // nil = no explicit limit
	thinkingEffort pvdr.ThinkingEffort
	thinkingBudget int64
	cachingEnabled bool
	capabilities   agent.ModelCapabilities
}

// WithAPIKey sets the Anthropic API key. Defaults to ANTHROPIC_API_KEY env var.
func WithAPIKey(key string) Option {
	return func(o *options) { o.apiKey = key }
}

// WithCapabilities partially overrides best-known capability metadata. Only
// non-zero numeric values and non-Unknown capability values replace existing
// defaults. Use fine-grained options to explicitly reset a field to Unknown.
func WithCapabilities(c agent.ModelCapabilities) Option {
	return func(o *options) { o.capabilities = agent.MergeModelCapabilities(o.capabilities, c) }
}

// WithContextWindowTokens overrides advisory total context capacity metadata.
// Pass 0 to explicitly mark the value unknown.
func WithContextWindowTokens(tokens int) Option {
	return func(o *options) {
		if tokens > 0 {
			o.capabilities.ContextWindowTokens = tokens
		} else {
			o.capabilities.ContextWindowTokens = 0
		}
	}
}

// WithMaxOutputTokens overrides advisory output-token ceiling metadata.
// Pass 0 to explicitly mark the value unknown.
func WithMaxOutputTokens(tokens int) Option {
	return func(o *options) {
		if tokens > 0 {
			o.capabilities.MaxOutputTokens = tokens
		} else {
			o.capabilities.MaxOutputTokens = 0
		}
	}
}

// WithToolUse overrides effective tool-use support, including agent.Unknown.
func WithToolUse(capability agent.Capability) Option {
	return func(o *options) { o.capabilities.ToolUse = capability }
}

// WithToolChoice partially overrides tool-choice metadata. Use per-mode
// options for an explicit reset to agent.Unknown.
func WithToolChoice(capabilities agent.ToolChoiceCapabilities) Option {
	return func(o *options) {
		o.capabilities = agent.MergeModelCapabilities(o.capabilities, agent.ModelCapabilities{ToolChoice: capabilities})
	}
}

// WithToolChoiceAuto overrides automatic tool-choice support.
func WithToolChoiceAuto(capability agent.Capability) Option {
	return func(o *options) { o.capabilities.ToolChoice.Auto = capability }
}

// WithToolChoiceRequired overrides required-tool-choice support.
func WithToolChoiceRequired(capability agent.Capability) Option {
	return func(o *options) { o.capabilities.ToolChoice.Required = capability }
}

// WithToolChoiceSpecific overrides named-tool-choice support.
func WithToolChoiceSpecific(capability agent.Capability) Option {
	return func(o *options) { o.capabilities.ToolChoice.Specific = capability }
}

// WithNativeStructuredOutput overrides native schema/JSON structured-output
// support, including an explicit reset to agent.Unknown.
func WithNativeStructuredOutput(capability agent.Capability) Option {
	return func(o *options) { o.capabilities.NativeStructuredOutput = capability }
}

// WithMaxTokens sets the max tokens for responses.
func WithMaxTokens(n int64) Option {
	return func(o *options) { o.maxTokens = &n }
}

// WithThinking enables extended thinking at the given effort level. The effort
// is mapped to a token budget via provider.ThinkingBudgets. For direct control
// over the token budget, use WithThinkingBudget instead — it takes precedence
// when both are set.
//
// When thinking is enabled, the resolved budget is added on top of MaxTokens
// so the model has headroom to both reason and produce a final answer.
func WithThinking(effort pvdr.ThinkingEffort) Option {
	return func(o *options) { o.thinkingEffort = effort }
}

// WithThinkingBudget enables extended thinking with an explicit token budget.
// Use this when you need finer control than the predefined ThinkingEffort levels.
// Takes precedence over WithThinking when both are set.
//
// The budget is added on top of MaxTokens so the model has headroom to both
// reason and produce a final answer.
func WithThinkingBudget(tokens int64) Option {
	return func(o *options) { o.thinkingBudget = tokens }
}

// WithSystemPromptCaching enables prompt caching. When set and ModelRequest.System is
// non-empty, cache_control is attached to the last system TextBlockParam.
// DocumentBlocks in messages also get cache_control attached automatically.
func WithSystemPromptCaching() Option {
	return func(o *options) { o.cachingEnabled = true }
}

// Must is a helper that wraps a (*AnthropicProvider, error) call and panics on error.
// Use it to collapse provider creation and agent creation into a single error check
// in examples, scripts, and CLI tools where a provider failure is fatal.
//
//	provider := anthropic.Must(anthropic.Standard())
//	a, err := agent.New(provider, instructions)
func Must(p *AnthropicProvider, err error) *AnthropicProvider {
	if err != nil {
		panic("anthropic: " + err.Error())
	}
	return p
}

// New creates a new AnthropicProvider.
func New(model string, opts ...Option) (*AnthropicProvider, error) {
	o := &options{capabilities: agent.ModelCapabilities{NativeStructuredOutput: agent.Unsupported}}
	for _, fn := range opts {
		fn(o)
	}

	var clientOpts []option.RequestOption
	if o.apiKey != "" {
		clientOpts = append(clientOpts, option.WithAPIKey(o.apiKey))
	}

	return &AnthropicProvider{
		client:         anthropicsdk.NewClient(clientOpts...),
		model:          anthropicsdk.Model(model),
		maxTokens:      o.maxTokens,
		thinkingEffort: o.thinkingEffort,
		thinkingBudget: o.thinkingBudget,
		cachingEnabled: o.cachingEnabled,
		capabilities:   o.capabilities,
	}, nil
}

var _ agent.Provider = (*AnthropicProvider)(nil)
var _ agent.CapabilityProvider = (*AnthropicProvider)(nil)

// Name returns a human-readable identifier for this provider instance.
func (p *AnthropicProvider) Name() string { return "anthropic" }

func (p *AnthropicProvider) ModelID() string { return string(p.model) }

// Capabilities returns effective adapter/model capabilities captured during
// construction. The returned value cannot mutate this provider.
func (p *AnthropicProvider) Capabilities() agent.ModelCapabilities { return p.capabilities }

// Client returns the underlying Anthropic SDK client.
// Use this for direct SDK access when you need provider-specific features
// not exposed through the agent.Provider interface.
func (p *AnthropicProvider) Client() *anthropicsdk.Client { return &p.client }

// Stream sends a request to Anthropic and emits incremental text and thinking events.
func (p *AnthropicProvider) Stream(ctx context.Context, req agent.ModelRequest, emit func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	input := p.buildParams(req)
	stream := p.client.Messages.NewStreaming(ctx, input)

	resp := &agent.ModelResponse{}
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
				if emit != nil {
					emit(agent.ModelEvent{Type: agent.ModelEventText, Text: ev.Delta.Text})
				}
			case "input_json_delta":
				currentToolInput += ev.Delta.PartialJSON
			case "thinking_delta":
				currentThinking += ev.Delta.Thinking
				if emit != nil {
					emit(agent.ModelEvent{Type: agent.ModelEventThinking, Text: ev.Delta.Thinking})
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
			resp.Usage.CacheReadTokens = int(ev.Message.Usage.CacheReadInputTokens)
			resp.Usage.CacheWriteTokens = int(ev.Message.Usage.CacheCreationInputTokens)

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

// resolveThinkingBudget returns the active thinking budget in tokens.
// An explicit budget set via WithThinkingBudget takes precedence over an
// effort level set via WithThinking. Returns 0 when thinking is disabled
// or the effort level is unknown.
func (p *AnthropicProvider) resolveThinkingBudget() int64 {
	if p.thinkingBudget > 0 {
		return p.thinkingBudget
	}
	if p.thinkingEffort != "" {
		return pvdr.ThinkingBudgets[p.thinkingEffort]
	}
	return 0
}

func (p *AnthropicProvider) buildParams(req agent.ModelRequest) anthropicsdk.MessageNewParams {
	// Anthropic requires max_tokens. Start from the historical generic default,
	// then use a known model ceiling only when it is lower. This makes
	// MaxOutputTokens a safe implicit fallback rather than a request to consume
	// a model's full capacity on every call.
	var maxTokens int64 = 128_000
	ceiling := int64(p.capabilities.MaxOutputTokens)
	if ceiling > 0 && ceiling < maxTokens {
		maxTokens = ceiling
	}
	if p.maxTokens != nil {
		maxTokens = *p.maxTokens
	}
	if cfg := req.InferenceConfig; cfg != nil && cfg.MaxTokens != nil {
		maxTokens = int64(*cfg.MaxTokens)
	}
	if ceiling > 0 && maxTokens > ceiling {
		maxTokens = ceiling
	}

	cachingEnabled := req.CachingEnabled || p.cachingEnabled
	msgs := toAnthropicMessages(req.Messages, cachingEnabled)
	input := anthropicsdk.MessageNewParams{
		Model:     p.model,
		MaxTokens: maxTokens,
		Messages:  msgs,
	}
	if req.System != "" {
		blocks := []anthropicsdk.TextBlockParam{{Text: req.System}}
		if cachingEnabled {
			blocks[len(blocks)-1].CacheControl = anthropicsdk.NewCacheControlEphemeralParam()
		}
		input.System = blocks
	}
	if len(req.Tools) > 0 {
		input.Tools = toAnthropicTools(req.Tools)
	}
	if req.ToolChoice != nil {
		input.ToolChoice = toAnthropicToolChoice(req.ToolChoice)
	}
	if budget := p.resolveThinkingBudget(); budget > 0 {
		input.Thinking = anthropicsdk.ThinkingConfigParamOfEnabled(budget)
		// Anthropic requires max_tokens to include room for both thinking and
		// the visible answer. A known model ceiling remains authoritative.
		input.MaxTokens = maxTokens + budget
		if ceiling > 0 && input.MaxTokens > ceiling {
			input.MaxTokens = ceiling
		}
	}
	// Apply inference config overrides other than MaxTokens, which was applied
	// above before reasoning headroom is calculated.
	if cfg := req.InferenceConfig; cfg != nil {
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

func toAnthropicMessages(msgs []agent.Message, cachingEnabled bool) []anthropicsdk.MessageParam {
	out := make([]anthropicsdk.MessageParam, len(msgs))
	for i, m := range msgs {
		out[i] = anthropicsdk.MessageParam{
			Role:    toAnthropicRole(m.Role),
			Content: toAnthropicContentBlocks(m.Content, m.Role, cachingEnabled),
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

// buildAnthropicToolResultParam constructs a ToolResultBlockParam from a
// ToolResultBlock. The caller may set CacheControl on the returned struct.
func buildAnthropicToolResultParam(v agent.ToolResultBlock) anthropicsdk.ToolResultBlockParam {
	if len(v.Images) == 0 {
		return anthropicsdk.ToolResultBlockParam{
			ToolUseID: v.ToolUseID,
			Content: []anthropicsdk.ToolResultBlockParamContentUnion{
				{OfText: &anthropicsdk.TextBlockParam{Text: v.Content}},
			},
			IsError: anthropicsdk.Bool(v.IsError),
		}
	}
	content := []anthropicsdk.ToolResultBlockParamContentUnion{
		{OfText: &anthropicsdk.TextBlockParam{Text: v.Content}},
	}
	for _, img := range v.Images {
		if img.Source.URL != "" {
			content = append(content, anthropicsdk.ToolResultBlockParamContentUnion{
				OfImage: &anthropicsdk.ImageBlockParam{
					Source: anthropicsdk.ImageBlockParamSourceUnion{
						OfURL: &anthropicsdk.URLImageSourceParam{URL: img.Source.URL},
					},
				},
			})
		} else {
			var encoded string
			if img.Source.Base64 != "" {
				encoded = img.Source.Base64
			} else if len(img.Source.Data) > 0 {
				encoded = base64.StdEncoding.EncodeToString(img.Source.Data)
			}
			content = append(content, anthropicsdk.ToolResultBlockParamContentUnion{
				OfImage: &anthropicsdk.ImageBlockParam{
					Source: anthropicsdk.ImageBlockParamSourceUnion{
						OfBase64: &anthropicsdk.Base64ImageSourceParam{
							MediaType: anthropicsdk.Base64ImageSourceMediaType(img.Source.MIMEType),
							Data:      encoded,
						},
					},
				},
			})
		}
	}
	return anthropicsdk.ToolResultBlockParam{
		ToolUseID: v.ToolUseID,
		Content:   content,
		IsError:   anthropicsdk.Bool(v.IsError),
	}
}

// buildAnthropicImageParam constructs an ImageBlockParam from an ImageBlock.
// Returns (param, true) on success or (zero, false) if the image bytes cannot
// be decoded. The caller may set CacheControl on the returned struct.
func buildAnthropicImageParam(v agent.ImageBlock) (anthropicsdk.ImageBlockParam, bool) {
	if v.Source.URL != "" {
		return anthropicsdk.ImageBlockParam{
			Source: anthropicsdk.ImageBlockParamSourceUnion{
				OfURL: &anthropicsdk.URLImageSourceParam{URL: v.Source.URL},
			},
		}, true
	}
	var encoded string
	if v.Source.Base64 != "" {
		encoded = v.Source.Base64
	} else {
		bytes, err := imageBytes(v.Source)
		if err != nil {
			log.Printf("anthropic: failed to get image bytes: %v (skipping block)", err)
			return anthropicsdk.ImageBlockParam{}, false
		}
		encoded = base64.StdEncoding.EncodeToString(bytes)
	}
	return anthropicsdk.ImageBlockParam{
		Source: anthropicsdk.ImageBlockParamSourceUnion{
			OfBase64: &anthropicsdk.Base64ImageSourceParam{
				MediaType: anthropicsdk.Base64ImageSourceMediaType(v.Source.MIMEType),
				Data:      encoded,
			},
		},
	}, true
}

// buildAnthropicDocParam constructs a DocumentBlockParam from a DocumentBlock.
// Returns (param, true) on success or (zero, false) if the document bytes
// cannot be decoded. The caller may set CacheControl on the returned struct.
func buildAnthropicDocParam(v agent.DocumentBlock) (anthropicsdk.DocumentBlockParam, bool) {
	if v.Source.FileID != "" {
		return anthropicsdk.DocumentBlockParam{
			Source: anthropicsdk.DocumentBlockParamSourceUnion{
				OfFile: &anthropicsdk.FileDocumentSourceParam{FileID: v.Source.FileID},
			},
		}, true
	}
	if v.Source.URL != "" {
		return anthropicsdk.DocumentBlockParam{
			Source: anthropicsdk.DocumentBlockParamSourceUnion{
				OfURL: &anthropicsdk.URLPDFSourceParam{URL: v.Source.URL},
			},
		}, true
	}
	var encoded string
	if v.Source.Base64 != "" {
		encoded = v.Source.Base64
	} else {
		bytes, err := imageBytes(agent.ImageSource{Data: v.Source.Data})
		if err != nil {
			log.Printf("anthropic: failed to get document bytes: %v (skipping block)", err)
			return anthropicsdk.DocumentBlockParam{}, false
		}
		encoded = base64.StdEncoding.EncodeToString(bytes)
	}
	return anthropicsdk.DocumentBlockParam{
		Source: anthropicsdk.DocumentBlockParamSourceUnion{
			OfBase64: &anthropicsdk.Base64PDFSourceParam{Data: encoded},
		},
	}, true
}

// toAnthropicContentBlocks translates a slice of ContentBlocks to Anthropic SDK params.
// When cachingEnabled, cache_control is attached to every DocumentBlock.
func toAnthropicContentBlocks(blocks []agent.ContentBlock, role agent.Role, cachingEnabled bool) []anthropicsdk.ContentBlockParamUnion {
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
			trb := buildAnthropicToolResultParam(v)
			out = append(out, anthropicsdk.ContentBlockParamUnion{OfToolResult: &trb})
		case agent.ImageBlock:
			if role == agent.RoleAssistant {
				log.Printf("anthropic: ImageBlock in assistant-role message is not supported and will be skipped")
				continue
			}
			img, ok := buildAnthropicImageParam(v)
			if !ok {
				continue
			}
			out = append(out, anthropicsdk.ContentBlockParamUnion{OfImage: &img})
		case agent.DocumentBlock:
			if role == agent.RoleAssistant {
				log.Printf("anthropic: DocumentBlock in assistant-role message is not supported and will be skipped")
				continue
			}
			doc, ok := buildAnthropicDocParam(v)
			if !ok {
				continue
			}
			// When caching is enabled, attach cache_control to every DocumentBlock.
			if cachingEnabled {
				doc.CacheControl = anthropicsdk.NewCacheControlEphemeralParam()
			}
			out = append(out, anthropicsdk.ContentBlockParamUnion{OfDocument: &doc})
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
