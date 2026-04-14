// Package bedrock implements the agent.Provider interface using the
// AWS Bedrock ConverseStream / Converse APIs.
// Documented in docs/providers.md — update when changing constructor, options, or capabilities.
package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// BedrockProvider implements agent.Provider using the AWS Bedrock runtime.
type BedrockProvider struct {
	client    *bedrockruntime.Client
	model     string
	maxTokens int32
}

// Option configures the BedrockProvider.
type Option func(*options)

type options struct {
	region    string
	maxTokens int32
}

// WithRegion sets a custom AWS region for the Bedrock client.
func WithRegion(region string) Option {
	return func(o *options) { o.region = region }
}

// WithMaxTokens sets the maximum number of tokens in the response. Defaults to 4096.
func WithMaxTokens(n int32) Option {
	return func(o *options) { o.maxTokens = n }
}

// New creates a new BedrockProvider. It loads AWS config from the default
// credential chain and accepts optional configuration.
func New(model string, opts ...Option) (*BedrockProvider, error) {
	o := &options{maxTokens: 8192}
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

	var cfgOpts []func(*awsconfig.LoadOptions) error
	cfgOpts = append(cfgOpts, awsconfig.WithRegion(region))

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	return &BedrockProvider{
		client:    bedrockruntime.NewFromConfig(cfg),
		model:     model,
		maxTokens: o.maxTokens,
	}, nil
}

// Model returns the model ID this provider is configured to use.
func (p *BedrockProvider) ModelId() string { return p.model }

// ---------------------------------------------------------------------------
// Converse (non-streaming)
// ---------------------------------------------------------------------------

// Converse sends messages to Bedrock and returns a complete response.
func (p *BedrockProvider) Converse(ctx context.Context, params agent.ConverseParams) (*agent.ProviderResponse, error) {
	input := &bedrockruntime.ConverseInput{
		ModelId:  aws.String(p.model),
		Messages: toBedrockMessages(params.Messages),
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens: aws.Int32(p.maxTokens),
		},
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

	out, err := p.client.Converse(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("bedrock converse: %w", err)
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
	input := &bedrockruntime.ConverseStreamInput{
		ModelId:  aws.String(p.model),
		Messages: toBedrockMessages(params.Messages),
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens: aws.Int32(p.maxTokens),
		},
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

	out, err := p.client.ConverseStream(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("bedrock converse stream: %w", err)
	}

	resp := &agent.ProviderResponse{}

	// Track in-flight tool use block being assembled from deltas.
	var currentToolName, currentToolID, currentToolInput string

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
			}

		case *types.ConverseStreamOutputMemberContentBlockStop:
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
				currentToolName = ""
				currentToolID = ""
				currentToolInput = ""
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
		return nil, fmt.Errorf("bedrock stream error: %w", err)
	}

	return resp, nil
}

// ---------------------------------------------------------------------------
// Type mapping helpers: framework → Bedrock SDK
// ---------------------------------------------------------------------------

// toBedrockMessages converts framework Messages to Bedrock SDK Messages.
func toBedrockMessages(msgs []agent.Message) []types.Message {
	out := make([]types.Message, len(msgs))
	for i, m := range msgs {
		out[i] = types.Message{
			Role:    toBedrockRole(m.Role),
			Content: toBedrockContentBlocks(m.Content),
		}
	}
	return out
}

func toBedrockRole(r agent.Role) types.ConversationRole {
	switch r {
	case agent.RoleAssistant:
		return types.ConversationRoleAssistant
	default:
		return types.ConversationRoleUser
	}
}

func toBedrockContentBlocks(blocks []agent.ContentBlock) []types.ContentBlock {
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
			if v.IsError {
				trb.Status = types.ToolResultStatusError
			}
			out = append(out, &types.ContentBlockMemberToolResult{Value: trb})
		}
	}
	return out
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

// Capabilities implements agent.CapabilityReporter, advertising what the
// underlying Bedrock model supports based on its model ID prefix.
func (p *BedrockProvider) Capabilities() agent.Capabilities {
	// OpenAI gpt-oss models on Bedrock support text only — no tool use or token usage.
	// Source: https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-openai.html
	// "The OpenAI models support only text input and text output."
	if strings.HasPrefix(p.model, "openai.") {
		return agent.Capabilities{
			ToolUse:    false,
			ToolChoice: false,
			TokenUsage: false,
		}
	}
	// All other Bedrock models (Anthropic, Amazon Nova, Qwen, etc.) support full capabilities.
	return agent.Capabilities{
		ToolUse:    true,
		ToolChoice: true,
		TokenUsage: true,
	}
}
