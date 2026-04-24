// Package bedrock implements the agent.Provider interface using the
// AWS Bedrock ConverseStream / Converse APIs.
// Documented in docs/providers.md — update when changing constructor, options, or capabilities.
package bedrock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	pvdr "github.com/camilbinas/gude-agents/agent/provider"
	"github.com/camilbinas/gude-agents/agent/tool"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// thinkingStyle describes how a model accepts thinking/reasoning configuration.
type thinkingStyle int

const (
	thinkingStyleNone   thinkingStyle = iota // model does not support thinking
	thinkingStyleClaude                      // {"thinking": {"type": "enabled", "budget_tokens": N}}
	thinkingStyleNova2                       // {"reasoningConfig": {"type": "enabled", "maxReasoningEffort": "..."}}
)

// BedrockProvider implements agent.Provider using the AWS Bedrock runtime.
type BedrockProvider struct {
	client           *bedrockruntime.Client
	model            string
	maxTokens        int32
	thinkingStyle    thinkingStyle // set by model constructors
	thinkingLevel    string        // "low", "medium", "high" — empty = disabled
	guardrailID      string        // empty = no guardrail
	guardrailVersion string
}

// Option configures the BedrockProvider.
type Option func(*options)

type options struct {
	region           string
	maxTokens        int32
	thinkingLevel    string
	thinkingStyle    thinkingStyle
	apiKey           string
	guardrailID      string
	guardrailVersion string
}

// WithRegion sets a custom AWS region for the Bedrock client.
func WithRegion(region string) Option {
	return func(o *options) { o.region = region }
}

// WithMaxTokens sets the maximum number of tokens in the response.
func WithMaxTokens(n int64) Option {
	return func(o *options) { o.maxTokens = int32(n) }
}

// WithThinking enables extended thinking at the given effort level.
// Use the shared constants: provider.ThinkingLow, provider.ThinkingMedium, provider.ThinkingHigh.
// For Claude models this sets a token budget; for Nova 2 it sets maxReasoningEffort directly.
func WithThinking(effort string) Option {
	return func(o *options) { o.thinkingLevel = effort }
}

// WithAPIKey sets an Amazon Bedrock API key (bearer token) for authentication.
// This is an alternative to IAM credentials — useful for quick setup and
// exploratory use. If not set, the provider falls back to the standard AWS
// credential chain (env vars, ~/.aws/credentials, IAM roles, etc.).
// The key is also read automatically from the AWS_BEARER_TOKEN_BEDROCK
// environment variable when no explicit key is provided.
func WithAPIKey(key string) Option {
	return func(o *options) { o.apiKey = key }
}

// WithGuardrail enables an Amazon Bedrock Guardrail on every Converse and
// ConverseStream call. The guardrail is a managed resource created in the
// AWS console or via the Bedrock API — this option references it by ID.
// Use "DRAFT" as the version to test with the latest unpublished draft.
func WithGuardrail(id, version string) Option {
	return func(o *options) {
		o.guardrailID = id
		o.guardrailVersion = version
	}
}

// withThinkingStyle sets the thinking API shape for the model. Used by model constructors only.
func withThinkingStyle(s thinkingStyle) Option {
	return func(o *options) { o.thinkingStyle = s }
}

// Must is a helper that wraps a (*BedrockProvider, error) call and panics on error.
// Use it to collapse provider creation and agent creation into a single error check
// in examples, scripts, and CLI tools where a provider failure is fatal.
//
//	a, err := agent.Default(bedrock.Must(bedrock.Standard()), instructions, tools)
func Must(p *BedrockProvider, err error) *BedrockProvider {
	if err != nil {
		panic("bedrock: " + err.Error())
	}
	return p
}

// New creates a new BedrockProvider. It loads AWS config from the default
// credential chain and accepts optional configuration.
func New(model string, opts ...Option) (*BedrockProvider, error) {
	o := &options{maxTokens: pvdr.DefaultMaxTokens}
	for _, fn := range opts {
		fn(o)
	}

	region := o.region
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	if region == "" {
		region = "us-east-1"
	}

	// Resolve API key: explicit option takes precedence over env var.
	apiKey := o.apiKey
	if apiKey == "" {
		apiKey = os.Getenv("AWS_BEARER_TOKEN_BEDROCK")
	}

	var cfgOpts []func(*awsconfig.LoadOptions) error
	cfgOpts = append(cfgOpts, awsconfig.WithRegion(region))

	// When an API key is provided, use anonymous credentials — the bearer
	// token in the Authorization header is the sole authentication mechanism.
	if apiKey != "" {
		cfgOpts = append(cfgOpts, awsconfig.WithCredentialsProvider(
			aws.AnonymousCredentials{},
		))
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), cfgOpts...)
	if err != nil {
		return nil, &agent.ProviderCreationError{Provider: "bedrock", Cause: err}
	}

	// Build client options — inject the bearer token header when present.
	var clientOpts []func(*bedrockruntime.Options)
	if apiKey != "" {
		token := apiKey // capture for closure
		clientOpts = append(clientOpts, func(o *bedrockruntime.Options) {
			o.APIOptions = append(o.APIOptions, addBearerTokenMiddleware(token))
		})
	}

	return &BedrockProvider{
		client:           bedrockruntime.NewFromConfig(cfg, clientOpts...),
		model:            model,
		maxTokens:        o.maxTokens,
		thinkingStyle:    o.thinkingStyle,
		thinkingLevel:    o.thinkingLevel,
		guardrailID:      o.guardrailID,
		guardrailVersion: o.guardrailVersion,
	}, nil
}

// Model returns the model ID this provider is configured to use.
func (p *BedrockProvider) ModelID() string { return p.model }

// Client returns the underlying AWS Bedrock runtime client.
// Use this for direct SDK access when you need provider-specific features
// not exposed through the agent.Provider interface.
func (p *BedrockProvider) Client() *bedrockruntime.Client { return p.client }

// ---------------------------------------------------------------------------
// Converse (non-streaming)
// ---------------------------------------------------------------------------

// Converse sends messages to Bedrock and returns a complete response.
func (p *BedrockProvider) Converse(ctx context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	infCfg := p.buildInferenceConfiguration(params.InferenceConfig)
	msgs, err := toBedrockMessages(params.Messages)
	if err != nil {
		return nil, &agent.ProviderError{Cause: err}
	}
	input := &bedrockruntime.ConverseInput{
		ModelId:         aws.String(p.model),
		Messages:        msgs,
		InferenceConfig: infCfg,
	}
	if params.System != "" {
		input.System = []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: params.System},
		}
	}
	if tc := toToolConfig(params.ToolConfig); tc != nil {
		if bc := toBedrockToolChoice(params.ToolChoice); bc != nil {
			tc.ToolChoice = bc
		}
		input.ToolConfig = tc
	}
	input.AdditionalModelRequestFields = p.buildAdditionalFields(params.InferenceConfig)

	if p.guardrailID != "" {
		input.GuardrailConfig = &types.GuardrailConfiguration{
			GuardrailIdentifier: aws.String(p.guardrailID),
			GuardrailVersion:    aws.String(p.guardrailVersion),
		}
	}

	out, err := p.client.Converse(ctx, input)
	if err != nil {
		return nil, &agent.ProviderError{Cause: err}
	}

	resp := parseConverseOutput(out)
	if out.Usage != nil {
		resp.Usage.InputTokens = int(aws.ToInt32(out.Usage.InputTokens))
		resp.Usage.OutputTokens = int(aws.ToInt32(out.Usage.OutputTokens))
	}
	return resp, nil
}

// parseConverseOutput maps the Bedrock Converse response to a ProviderResponse.
func parseConverseOutput(out *bedrockruntime.ConverseOutput) *agent.ProviderResponse {
	resp := &agent.ProviderResponse{}
	if out.Output == nil {
		return resp
	}
	msg, ok := out.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return resp
	}
	for _, block := range msg.Value.Content {
		switch b := block.(type) {
		case *types.ContentBlockMemberText:
			resp.Text += b.Value
		case *types.ContentBlockMemberToolUse:
			var raw json.RawMessage
			if b.Value.Input != nil {
				if data, err := b.Value.Input.MarshalSmithyDocument(); err == nil {
					raw = json.RawMessage(data)
				}
			}
			resp.ToolCalls = append(resp.ToolCalls, tool.Call{
				ToolUseID: aws.ToString(b.Value.ToolUseId),
				Name:      aws.ToString(b.Value.Name),
				Input:     raw,
			})
		case *types.ContentBlockMemberReasoningContent:
			if rt, ok := b.Value.(*types.ReasoningContentBlockMemberReasoningText); ok {
				if resp.Metadata == nil {
					resp.Metadata = map[string]any{}
				}
				existing, _ := resp.Metadata["thinking"].(string)
				resp.Metadata["thinking"] = existing + aws.ToString(rt.Value.Text)
			}
		}
	}
	return resp
}

// ---------------------------------------------------------------------------
// ConverseStream (streaming)
// ---------------------------------------------------------------------------

// ConverseStream sends messages to Bedrock and streams the response.
// Text deltas are forwarded to cb. Tool use blocks are collected and
// returned in the ProviderResponse.
func (p *BedrockProvider) ConverseStream(ctx context.Context, params agent.ConverseParams, cb agent.StreamCallback) (*agent.ProviderResponse, error) {
	infCfg := p.buildInferenceConfiguration(params.InferenceConfig)
	msgs, err := toBedrockMessages(params.Messages)
	if err != nil {
		return nil, &agent.ProviderError{Cause: err}
	}
	input := &bedrockruntime.ConverseStreamInput{
		ModelId:         aws.String(p.model),
		Messages:        msgs,
		InferenceConfig: infCfg,
	}
	if params.System != "" {
		input.System = []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: params.System},
		}
	}
	if tc := toToolConfig(params.ToolConfig); tc != nil {
		if bc := toBedrockToolChoice(params.ToolChoice); bc != nil {
			tc.ToolChoice = bc
		}
		input.ToolConfig = tc
	}
	input.AdditionalModelRequestFields = p.buildAdditionalFields(params.InferenceConfig)

	if p.guardrailID != "" {
		input.GuardrailConfig = &types.GuardrailStreamConfiguration{
			GuardrailIdentifier: aws.String(p.guardrailID),
			GuardrailVersion:    aws.String(p.guardrailVersion),
		}
	}

	out, err := p.client.ConverseStream(ctx, input)
	if err != nil {
		return nil, &agent.ProviderError{Cause: err}
	}

	resp := &agent.ProviderResponse{}

	var currentToolName, currentToolID, currentToolInput string
	var currentReasoning string

	stream := out.GetStream()
	for event := range stream.Events() {
		switch ev := event.(type) {
		case *types.ConverseStreamOutputMemberContentBlockStart:
			if tuStart, ok := ev.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
				currentToolName = aws.ToString(tuStart.Value.Name)
				currentToolID = aws.ToString(tuStart.Value.ToolUseId)
				currentToolInput = ""
			}

		case *types.ConverseStreamOutputMemberContentBlockDelta:
			switch delta := ev.Value.Delta.(type) {
			case *types.ContentBlockDeltaMemberText:
				resp.Text += delta.Value
				if cb != nil {
					cb(delta.Value)
				}
			case *types.ContentBlockDeltaMemberToolUse:
				currentToolInput += aws.ToString(delta.Value.Input)
			case *types.ContentBlockDeltaMemberReasoningContent:
				if td, ok := delta.Value.(*types.ReasoningContentBlockDeltaMemberText); ok {
					currentReasoning += td.Value
					if params.ThinkingCallback != nil {
						params.ThinkingCallback(td.Value)
					}
				}
			}

		case *types.ConverseStreamOutputMemberContentBlockStop:
			if currentToolName != "" {
				raw := json.RawMessage(currentToolInput)
				if len(raw) == 0 {
					raw = json.RawMessage(`{}`)
				}
				resp.ToolCalls = append(resp.ToolCalls, tool.Call{
					ToolUseID: currentToolID,
					Name:      currentToolName,
					Input:     raw,
				})
				currentToolName = ""
				currentToolID = ""
				currentToolInput = ""
			}
			if currentReasoning != "" {
				if resp.Metadata == nil {
					resp.Metadata = map[string]any{}
				}
				existing, _ := resp.Metadata["thinking"].(string)
				resp.Metadata["thinking"] = existing + currentReasoning
				currentReasoning = ""
			}

		case *types.ConverseStreamOutputMemberMetadata:
			if ev.Value.Usage != nil {
				resp.Usage.InputTokens = int(aws.ToInt32(ev.Value.Usage.InputTokens))
				resp.Usage.OutputTokens = int(aws.ToInt32(ev.Value.Usage.OutputTokens))
			}

		case *types.ConverseStreamOutputMemberMessageStop:
			// End of message — nothing to do.
		}
	}
	stream.Close()
	if err := stream.Err(); err != nil {
		return nil, &agent.ProviderError{Cause: err}
	}

	return resp, nil
}

// ---------------------------------------------------------------------------
// Inference config helpers
// ---------------------------------------------------------------------------

// buildInferenceConfiguration builds the Bedrock InferenceConfiguration from
// the provider's constructor defaults and the optional per-call InferenceConfig.
// Temperature, TopP, StopSequences, and MaxTokens are mapped here.
// TopK is handled separately via buildAdditionalFields.
func (p *BedrockProvider) buildInferenceConfiguration(cfg *agent.InferenceConfig) *types.InferenceConfiguration {
	ic := &types.InferenceConfiguration{
		MaxTokens: aws.Int32(p.maxTokens),
	}
	if cfg == nil {
		return ic
	}
	if cfg.Temperature != nil {
		v := float32(*cfg.Temperature)
		ic.Temperature = &v
	}
	if cfg.TopP != nil {
		v := float32(*cfg.TopP)
		ic.TopP = &v
	}
	if cfg.StopSequences != nil {
		ic.StopSequences = cfg.StopSequences
	}
	if cfg.MaxTokens != nil {
		ic.MaxTokens = aws.Int32(int32(*cfg.MaxTokens))
	}
	return ic
}

// buildAdditionalFields builds the AdditionalModelRequestFields document,
// merging thinking configuration (if enabled) with TopK (if provided).
func (p *BedrockProvider) buildAdditionalFields(cfg *agent.InferenceConfig) document.Interface {
	hasThinking := p.thinkingLevel != "" && p.thinkingStyle != thinkingStyleNone
	hasTopK := cfg != nil && cfg.TopK != nil

	if !hasThinking && !hasTopK {
		return nil
	}

	fields := map[string]any{}

	// Merge thinking fields if enabled.
	if hasThinking {
		if p.thinkingStyle == thinkingStyleClaude {
			fields["thinking"] = map[string]any{
				"type":          "enabled",
				"budget_tokens": pvdr.ThinkingBudgets[p.thinkingLevel],
			}
		} else {
			fields["reasoningConfig"] = map[string]any{
				"type":               "enabled",
				"maxReasoningEffort": p.thinkingLevel,
			}
		}
	}

	// Add TopK for Anthropic models via model-specific fields.
	if hasTopK {
		fields["top_k"] = *cfg.TopK
	}

	return document.NewLazyDocument(fields)
}

// ---------------------------------------------------------------------------
// Type mapping helpers: framework → Bedrock SDK
// ---------------------------------------------------------------------------

// toBedrockMessages converts framework Messages to Bedrock SDK Messages.
func toBedrockMessages(msgs []agent.Message) ([]types.Message, error) {
	out := make([]types.Message, len(msgs))
	for i, m := range msgs {
		blocks, err := toBedrockContentBlocks(m.Content)
		if err != nil {
			return nil, err
		}
		out[i] = types.Message{
			Role:    toBedrockRole(m.Role),
			Content: blocks,
		}
	}
	return out, nil
}

func toBedrockRole(r agent.Role) types.ConversationRole {
	switch r {
	case agent.RoleAssistant:
		return types.ConversationRoleAssistant
	default:
		return types.ConversationRoleUser
	}
}

func toBedrockContentBlocks(blocks []agent.ContentBlock) ([]types.ContentBlock, error) {
	out := make([]types.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch v := b.(type) {
		case agent.TextBlock:
			out = append(out, &types.ContentBlockMemberText{Value: v.Text})

		case agent.ToolUseBlock:
			// Bedrock requires Input to be non-nil, even for tools with no parameters.
			// Default to an empty object if the input is missing or empty.
			var parsed any = map[string]any{}
			if len(v.Input) > 0 {
				if err := json.Unmarshal(v.Input, &parsed); err != nil {
					parsed = map[string]any{}
				}
			}
			inputDoc := document.NewLazyDocument(parsed)
			out = append(out, &types.ContentBlockMemberToolUse{
				Value: types.ToolUseBlock{
					ToolUseId: aws.String(v.ToolUseID),
					Name:      aws.String(v.Name),
					Input:     inputDoc,
				},
			})

		case agent.ToolResultBlock:
			trb := types.ToolResultBlock{
				ToolUseId: aws.String(v.ToolUseID),
				Content: []types.ToolResultContentBlock{
					&types.ToolResultContentBlockMemberText{Value: v.Content},
				},
			}
			for _, img := range v.Images {
				bytes, err := imageBytes(img.Source)
				if err != nil {
					return nil, fmt.Errorf("tool result ImageBlock: %w", err)
				}
				mimeType := img.Source.MIMEType
				if mimeType == "" {
					mimeType = "image/jpeg"
				}
				trb.Content = append(trb.Content, &types.ToolResultContentBlockMemberImage{
					Value: types.ImageBlock{
						Format: toBedrockImageFormat(mimeType),
						Source: &types.ImageSourceMemberBytes{Value: bytes},
					},
				})
			}
			if v.IsError {
				trb.Status = types.ToolResultStatusError
			}
			out = append(out, &types.ContentBlockMemberToolResult{Value: trb})

		case agent.ImageBlock:
			bytes, err := imageBytes(v.Source)
			if err != nil {
				return nil, fmt.Errorf("ImageBlock: %w", err)
			}
			mimeType := v.Source.MIMEType
			if mimeType == "" {
				mimeType = "image/jpeg" // fallback for URL sources
			}
			format := toBedrockImageFormat(mimeType)
			out = append(out, &types.ContentBlockMemberImage{
				Value: types.ImageBlock{
					Format: format,
					Source: &types.ImageSourceMemberBytes{
						Value: bytes,
					},
				},
			})

		case agent.DocumentBlock:
			bytes, err := documentBytes(v.Source)
			if err != nil {
				return nil, fmt.Errorf("DocumentBlock: %w", err)
			}
			name := sanitizeDocName(v.Source.Name)
			out = append(out, &types.ContentBlockMemberDocument{
				Value: types.DocumentBlock{
					Name:   aws.String(name),
					Format: toBedrockDocFormat(v.Source.MIMEType),
					Source: &types.DocumentSourceMemberBytes{
						Value: bytes,
					},
				},
			})
		}
	}
	return out, nil
}

// imageBytes returns the raw bytes from an ImageSource.
// If Source.Data is set, it is returned directly.
// If Source.Base64 is set, it is decoded from standard base64.
// If Source.URL is set, the image is fetched via HTTP GET.
func imageBytes(src agent.ImageSource) ([]byte, error) {
	if len(src.Data) > 0 {
		return src.Data, nil
	}
	if src.Base64 != "" {
		b, err := base64.StdEncoding.DecodeString(src.Base64)
		if err != nil {
			return nil, fmt.Errorf("base64 decode: %w", err)
		}
		return b, nil
	}
	if src.URL != "" {
		resp, err := http.Get(src.URL)
		if err != nil {
			return nil, fmt.Errorf("fetch image URL: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch image URL: status %d", resp.StatusCode)
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read image URL body: %w", err)
		}
		return b, nil
	}
	return nil, fmt.Errorf("ImageSource has no data, base64, or URL")
}

// toBedrockImageFormat maps a MIME type string to the Bedrock ImageFormat enum.
func toBedrockImageFormat(mimeType string) types.ImageFormat {
	switch mimeType {
	case "image/jpeg":
		return types.ImageFormatJpeg
	case "image/png":
		return types.ImageFormatPng
	case "image/gif":
		return types.ImageFormatGif
	case "image/webp":
		return types.ImageFormatWebp
	default:
		return types.ImageFormatJpeg
	}
}

// reInvalidDocNameChars matches characters not allowed in Bedrock document names.
// Bedrock only allows: alphanumeric, whitespace, hyphens, parentheses, square brackets.
var reInvalidDocNameChars = regexp.MustCompile(`[^a-zA-Z0-9\s\-\(\)\[\]]`)

// reMultiSpaces collapses consecutive whitespace to a single space.
var reMultiSpaces = regexp.MustCompile(`\s{2,}`)

// sanitizeDocName cleans a filename for Bedrock's DocumentBlock.Name field.
// Strips the extension, replaces invalid characters, and collapses whitespace.
func sanitizeDocName(name string) string {
	if name == "" {
		return "document"
	}
	// Strip file extension (e.g. "report.pdf" → "report").
	if idx := len(name) - len(filepath.Ext(name)); idx > 0 {
		name = name[:idx]
	}
	name = reInvalidDocNameChars.ReplaceAllString(name, " ")
	name = reMultiSpaces.ReplaceAllString(name, " ")
	name = strings.TrimSpace(name)
	if name == "" {
		return "document"
	}
	return name
}

// documentBytes returns the raw bytes from a DocumentSource, same logic as imageBytes.
func documentBytes(src agent.DocumentSource) ([]byte, error) {
	if len(src.Data) > 0 {
		return src.Data, nil
	}
	if src.Base64 != "" {
		b, err := base64.StdEncoding.DecodeString(src.Base64)
		if err != nil {
			return nil, fmt.Errorf("base64 decode: %w", err)
		}
		return b, nil
	}
	if src.URL != "" {
		resp, err := http.Get(src.URL)
		if err != nil {
			return nil, fmt.Errorf("fetch document URL: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch document URL: status %d", resp.StatusCode)
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read document URL body: %w", err)
		}
		return b, nil
	}
	return nil, fmt.Errorf("DocumentSource has no data, base64, or URL")
}

// toBedrockDocFormat maps a MIME type to the Bedrock DocumentFormat enum.
func toBedrockDocFormat(mimeType string) types.DocumentFormat {
	switch mimeType {
	case "application/pdf":
		return types.DocumentFormatPdf
	case "text/csv":
		return types.DocumentFormatCsv
	case "text/html":
		return types.DocumentFormatHtml
	case "text/plain":
		return types.DocumentFormatTxt
	case "text/markdown":
		return types.DocumentFormatMd
	case "application/msword":
		return types.DocumentFormatDoc
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return types.DocumentFormatDocx
	case "application/vnd.ms-excel":
		return types.DocumentFormatXls
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return types.DocumentFormatXlsx
	default:
		return types.DocumentFormatPdf
	}
}

// toToolConfig converts framework ToolSpecs to a Bedrock ToolConfiguration.
func toToolConfig(specs []tool.Spec) *types.ToolConfiguration {
	if len(specs) == 0 {
		return nil
	}
	tools := make([]types.Tool, len(specs))
	for i, s := range specs {
		tools[i] = &types.ToolMemberToolSpec{
			Value: types.ToolSpecification{
				Name:        aws.String(s.Name),
				Description: aws.String(s.Description),
				InputSchema: &types.ToolInputSchemaMemberJson{
					Value: document.NewLazyDocument(s.InputSchema),
				},
			},
		}
	}
	return &types.ToolConfiguration{Tools: tools}
}

// toBedrockToolChoice maps a framework ToolChoice to the Bedrock SDK ToolChoice union type.
func toBedrockToolChoice(tc *tool.Choice) types.ToolChoice {
	if tc == nil {
		return nil
	}
	switch tc.Mode {
	case tool.ChoiceAuto:
		return &types.ToolChoiceMemberAuto{Value: types.AutoToolChoice{}}
	case tool.ChoiceAny:
		return &types.ToolChoiceMemberAny{Value: types.AnyToolChoice{}}
	case tool.ChoiceTool:
		return &types.ToolChoiceMemberTool{Value: types.SpecificToolChoice{
			Name: aws.String(tc.Name),
		}}
	default:
		return nil
	}
}

// addBearerTokenMiddleware returns a smithy middleware that injects an
// Authorization: Bearer <token> header on every outbound request.
func addBearerTokenMiddleware(token string) func(stack *middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Finalize.Add(
			middleware.FinalizeMiddlewareFunc("BedrockBearerToken",
				func(ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler) (middleware.FinalizeOutput, middleware.Metadata, error) {
					req, ok := in.Request.(*smithyhttp.Request)
					if ok {
						req.Header.Set("Authorization", "Bearer "+token)
					}
					return next.HandleFinalize(ctx, in)
				},
			),
			middleware.After,
		)
	}
}

// bearerTransport wraps an http.RoundTripper and injects the Authorization header.
// Used as a fallback if the smithy middleware approach isn't available.
var _ http.RoundTripper = (*bearerTransport)(nil)

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(r)
}
