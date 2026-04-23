// Package gemini implements the agent.Provider interface using the
// Google GenAI SDK (google.golang.org/genai) for Gemini models.
// Documented in docs/providers.md — update when changing constructor, options, or capabilities.
package gemini

import (
	"context"
	"encoding/json"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	pvdr "github.com/camilbinas/gude-agents/agent/provider"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/google/uuid"

	"google.golang.org/genai"
)

// GeminiProvider implements agent.Provider using the Google Gemini API.
type GeminiProvider struct {
	client        *genai.Client
	model         string
	maxTokens     int32
	thinkingLevel string // "low", "medium", "high" — empty = disabled
}

// Option configures the GeminiProvider.
type Option func(*options)

type options struct {
	apiKey        string
	maxTokens     int32
	thinkingLevel string
}

// WithAPIKey sets the Gemini API key. Defaults to GEMINI_API_KEY env var,
// falling back to GOOGLE_API_KEY.
func WithAPIKey(key string) Option {
	return func(o *options) { o.apiKey = key }
}

// WithMaxTokens sets the max tokens for responses.
func WithMaxTokens(n int64) Option {
	return func(o *options) { o.maxTokens = int32(n) }
}

// WithThinking enables extended thinking at the given effort level.
// Use the shared constants: provider.ThinkingLow, provider.ThinkingMedium, provider.ThinkingHigh.
func WithThinking(effort string) Option {
	return func(o *options) { o.thinkingLevel = effort }
}

// Must is a helper that wraps a (*GeminiProvider, error) call and panics on error.
// Use it to collapse provider creation and agent creation into a single error check
// in examples, scripts, and CLI tools where a provider failure is fatal.
//
//	a, err := agent.Default(gemini.Must(gemini.Standard()), instructions, tools)
func Must(p *GeminiProvider, err error) *GeminiProvider {
	if err != nil {
		panic("gemini: " + err.Error())
	}
	return p
}

// New creates a new GeminiProvider.
func New(model string, opts ...Option) (*GeminiProvider, error) {
	o := &options{maxTokens: int32(pvdr.DefaultMaxTokens)}
	for _, fn := range opts {
		fn(o)
	}

	apiKey := o.apiKey
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, &agent.ProviderCreationError{Provider: "gemini", Cause: err}
	}

	return &GeminiProvider{
		client:        client,
		model:         model,
		maxTokens:     o.maxTokens,
		thinkingLevel: o.thinkingLevel,
	}, nil
}

// ModelID returns the model ID this provider is configured to use.
func (p *GeminiProvider) ModelID() string { return p.model }

// Client returns the underlying Google GenAI client.
// Use this for direct SDK access when you need provider-specific features
// not exposed through the agent.Provider interface.
func (p *GeminiProvider) Client() *genai.Client { return p.client }

// ---------------------------------------------------------------------------
// Converse (non-streaming)
// ---------------------------------------------------------------------------

func (p *GeminiProvider) Converse(ctx context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	config := buildConfig(p, params)
	contents := toGeminiContents(params.Messages)

	resp, err := p.client.Models.GenerateContent(ctx, p.model, contents, config)
	if err != nil {
		return nil, &agent.ProviderError{Cause: err}
	}

	return parseResponse(resp), nil
}

// ---------------------------------------------------------------------------
// ConverseStream (streaming)
// ---------------------------------------------------------------------------

func (p *GeminiProvider) ConverseStream(ctx context.Context, params agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	config := buildConfig(p, params)
	contents := toGeminiContents(params.Messages)

	iter := p.client.Models.GenerateContentStream(ctx, p.model, contents, config)

	result := &agent.ProviderResponse{}

	for resp, err := range iter {
		if err != nil {
			return nil, &agent.ProviderError{Cause: err}
		}

		if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
			// Extract usage from chunks that have no candidate content.
			if resp != nil && resp.UsageMetadata != nil {
				result.Usage.InputTokens = int(resp.UsageMetadata.PromptTokenCount)
				result.Usage.OutputTokens = int(resp.UsageMetadata.CandidatesTokenCount)
			}
			continue
		}

		for _, part := range resp.Candidates[0].Content.Parts {
			if part == nil {
				continue
			}

			// Thinking/thought parts.
			if part.Thought && part.Text != "" {
				if result.Metadata == nil {
					result.Metadata = map[string]any{}
				}
				existing, _ := result.Metadata["thinking"].(string)
				result.Metadata["thinking"] = existing + part.Text
				if params.ThinkingCallback != nil {
					params.ThinkingCallback(part.Text)
				}
				continue
			}

			// Text parts.
			if part.Text != "" {
				result.Text += part.Text
				if cb != nil {
					cb(part.Text)
				}
				continue
			}

			// Function call parts.
			if part.FunctionCall != nil {
				args := part.FunctionCall.Args
				if args == nil {
					args = map[string]any{}
				}
				argsJSON, err := json.Marshal(args)
				if err != nil {
					argsJSON = []byte(`{}`)
				}
				result.ToolCalls = append(result.ToolCalls, tool.Call{
					ToolUseID: uuid.New().String(),
					Name:      part.FunctionCall.Name,
					Input:     json.RawMessage(argsJSON),
				})
			}
		}

		// Extract usage from every chunk (last one wins).
		if resp.UsageMetadata != nil {
			result.Usage.InputTokens = int(resp.UsageMetadata.PromptTokenCount)
			result.Usage.OutputTokens = int(resp.UsageMetadata.CandidatesTokenCount)
		}
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Response parsing
// ---------------------------------------------------------------------------

// parseResponse converts a Gemini GenerateContentResponse to a framework ProviderResponse.
func parseResponse(resp *genai.GenerateContentResponse) *agent.ProviderResponse {
	result := &agent.ProviderResponse{}

	if resp == nil {
		return result
	}

	// Extract usage metadata.
	if resp.UsageMetadata != nil {
		result.Usage.InputTokens = int(resp.UsageMetadata.PromptTokenCount)
		result.Usage.OutputTokens = int(resp.UsageMetadata.CandidatesTokenCount)
	}

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return result
	}

	for _, part := range resp.Candidates[0].Content.Parts {
		if part == nil {
			continue
		}

		// Thinking/thought parts.
		if part.Thought && part.Text != "" {
			if result.Metadata == nil {
				result.Metadata = map[string]any{}
			}
			existing, _ := result.Metadata["thinking"].(string)
			result.Metadata["thinking"] = existing + part.Text
			continue
		}

		// Text parts.
		if part.Text != "" {
			result.Text += part.Text
			continue
		}

		// Function call parts.
		if part.FunctionCall != nil {
			args := part.FunctionCall.Args
			if args == nil {
				args = map[string]any{}
			}
			argsJSON, err := json.Marshal(args)
			if err != nil {
				argsJSON = []byte(`{}`)
			}
			result.ToolCalls = append(result.ToolCalls, tool.Call{
				ToolUseID: uuid.New().String(),
				Name:      part.FunctionCall.Name,
				Input:     json.RawMessage(argsJSON),
			})
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Mapping helpers (unexported)
// ---------------------------------------------------------------------------

// ptr returns a pointer to the given value.
func ptr[T any](v T) *T { return &v }

// toGeminiContents converts framework messages to Gemini content objects.
func toGeminiContents(msgs []agent.Message) []*genai.Content {
	out := make([]*genai.Content, len(msgs))
	for i, m := range msgs {
		out[i] = &genai.Content{
			Role:  toGeminiRole(m.Role),
			Parts: toGeminiParts(m.Content),
		}
	}
	return out
}

// toGeminiParts converts framework content blocks to Gemini parts.
func toGeminiParts(blocks []agent.ContentBlock) []*genai.Part {
	parts := make([]*genai.Part, 0, len(blocks))
	for _, b := range blocks {
		switch v := b.(type) {
		case agent.TextBlock:
			parts = append(parts, genai.NewPartFromText(v.Text))
		case agent.ToolUseBlock:
			args := map[string]any{}
			if len(v.Input) > 0 {
				if err := json.Unmarshal(v.Input, &args); err != nil {
					args = map[string]any{}
				}
			}
			parts = append(parts, genai.NewPartFromFunctionCall(v.Name, args))
		case agent.ToolResultBlock:
			parts = append(parts, genai.NewPartFromFunctionResponse(v.ToolUseID, map[string]any{"result": v.Content}))
		}
	}
	return parts
}

// toGeminiRole maps a framework role to the Gemini role string.
func toGeminiRole(role agent.Role) string {
	switch role {
	case agent.RoleAssistant:
		return "model"
	default:
		return "user"
	}
}

// toGeminiTools converts framework tool specs to Gemini tool objects.
func toGeminiTools(specs []tool.Spec) []*genai.Tool {
	if len(specs) == 0 {
		return nil
	}
	decls := make([]*genai.FunctionDeclaration, len(specs))
	for i, s := range specs {
		decls[i] = &genai.FunctionDeclaration{
			Name:        s.Name,
			Description: s.Description,
			Parameters:  mapToSchema(s.InputSchema),
		}
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}

// mapToSchema converts a JSON Schema map (from tool.Spec.InputSchema) to a *genai.Schema.
func mapToSchema(m map[string]any) *genai.Schema {
	if m == nil {
		return nil
	}

	s := &genai.Schema{}

	if t, ok := m["type"].(string); ok {
		switch t {
		case "object":
			s.Type = genai.TypeObject
		case "string":
			s.Type = genai.TypeString
		case "number":
			s.Type = genai.TypeNumber
		case "integer":
			s.Type = genai.TypeInteger
		case "boolean":
			s.Type = genai.TypeBoolean
		case "array":
			s.Type = genai.TypeArray
		}
	}

	if desc, ok := m["description"].(string); ok {
		s.Description = desc
	}

	if props, ok := m["properties"].(map[string]any); ok {
		s.Properties = make(map[string]*genai.Schema, len(props))
		for k, v := range props {
			if pm, ok := v.(map[string]any); ok {
				s.Properties[k] = mapToSchema(pm)
			}
		}
	}

	if items, ok := m["items"].(map[string]any); ok {
		s.Items = mapToSchema(items)
	}

	if req, ok := m["required"].([]string); ok {
		s.Required = req
	}
	// Handle []any from JSON unmarshaling
	if reqAny, ok := m["required"].([]any); ok {
		for _, r := range reqAny {
			if str, ok := r.(string); ok {
				s.Required = append(s.Required, str)
			}
		}
	}

	if enumVals, ok := m["enum"].([]any); ok {
		for _, e := range enumVals {
			if str, ok := e.(string); ok {
				s.Enum = append(s.Enum, str)
			}
		}
	}

	return s
}

// toGeminiToolConfig converts a framework tool choice to a Gemini tool config.
func toGeminiToolConfig(choice *tool.Choice) *genai.ToolConfig {
	if choice == nil {
		return nil
	}
	switch choice.Mode {
	case tool.ChoiceAuto:
		return &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigModeAuto,
			},
		}
	case tool.ChoiceAny:
		return &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigModeAny,
			},
		}
	case tool.ChoiceTool:
		return &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode:                 genai.FunctionCallingConfigModeAny,
				AllowedFunctionNames: []string{choice.Name},
			},
		}
	default:
		return nil
	}
}

// buildConfig assembles a GenerateContentConfig from provider state and converse params.
func buildConfig(p *GeminiProvider, params agent.ConverseParams) *genai.GenerateContentConfig {
	config := &genai.GenerateContentConfig{
		MaxOutputTokens: p.maxTokens,
		Tools:           toGeminiTools(params.ToolConfig),
		ToolConfig:      toGeminiToolConfig(params.ToolChoice),
	}
	if params.System != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{genai.NewPartFromText(params.System)},
		}
	}
	if p.thinkingLevel != "" {
		config.ThinkingConfig = &genai.ThinkingConfig{
			ThinkingBudget: ptr(int32(pvdr.ThinkingBudgets[p.thinkingLevel])),
		}
	}
	// Apply inference config overrides.
	if cfg := params.InferenceConfig; cfg != nil {
		if cfg.Temperature != nil {
			config.Temperature = ptr(float32(*cfg.Temperature))
		}
		if cfg.TopP != nil {
			config.TopP = ptr(float32(*cfg.TopP))
		}
		if cfg.TopK != nil {
			config.TopK = ptr(float32(*cfg.TopK))
		}
		if cfg.StopSequences != nil {
			config.StopSequences = cfg.StopSequences
		}
		if cfg.MaxTokens != nil {
			config.MaxOutputTokens = int32(*cfg.MaxTokens)
		}
	}
	return config
}
