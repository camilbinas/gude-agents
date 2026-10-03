// Package bedrock implements the agent.Provider interface using the
// AWS Bedrock ConverseStream / Converse APIs.
package bedrock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
	thinkingStyleGrok                        // {"reasoning_effort": "..."}

	defaultURLFetchTimeout          = 30 * time.Second
	defaultURLFetchMaxResponseBytes = int64(10 * 1024 * 1024)
	defaultURLFetchMaxRedirects     = 3
)

// urlFetchPolicy controls which network targets URL media sources may access.
// The zero value permits only publicly routable HTTPS targets.
type urlFetchPolicy struct {
	allowPrivateNetworks bool
	lookupIPAddr         func(context.Context, string) ([]net.IPAddr, error)
}

var (
	urlFetchMetadataIPv4 = netip.MustParseAddr("169.254.169.254")
	urlFetchMetadataIPv6 = netip.MustParseAddr("fd00:ec2::254")
)

// BedrockProvider implements agent.Provider using the AWS Bedrock runtime.
type BedrockProvider struct {
	client           *bedrockruntime.Client
	fetchClient      *http.Client
	urlFetchPolicy   urlFetchPolicy
	model            string
	maxTokens        *int32              // nil = no explicit limit (provider default)
	thinkingStyle    thinkingStyle       // set by model constructors
	thinkingEffort   pvdr.ThinkingEffort // empty = effort not set
	thinkingBudget   int64               // 0 = budget not set; takes precedence over effort (Claude only)
	guardrailID      string              // empty = no guardrail
	guardrailVersion string
	cachingEnabled   bool
	capabilities     agent.ModelCapabilities
}

// Option configures the BedrockProvider.
type Option func(*options)

type options struct {
	region                 string
	maxTokens              *int32 // nil = no explicit limit
	thinkingEffort         pvdr.ThinkingEffort
	thinkingBudget         int64
	thinkingStyle          thinkingStyle
	apiKey                 string
	guardrailID            string
	guardrailVersion       string
	cachingEnabled         bool
	urlFetchTimeout        time.Duration
	allowPrivateURLFetches bool
	capabilities           agent.ModelCapabilities
}

// WithRegion sets a custom AWS region for the Bedrock client.
func WithRegion(region string) Option {
	return func(o *options) { o.region = region }
}

// WithCapabilities partially overrides best-known capability metadata. Only
// non-zero numeric values and non-Unknown capability values replace existing
// defaults. Use the fine-grained capability options to explicitly reset a
// field to Unknown.
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

// WithMaxOutputTokens overrides advisory maximum output-token metadata.
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

// WithToolUse overrides the effective tool-use capability, including an
// explicit reset to agent.Unknown.
func WithToolUse(capability agent.Capability) Option {
	return func(o *options) { o.capabilities.ToolUse = capability }
}

// WithToolChoice partially overrides tool-choice metadata. Use the per-mode
// options below when an explicit reset to agent.Unknown is required.
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

// WithURLFetchTimeout sets the timeout for downloading image and document URL sources.
// Non-positive values retain the finite default timeout.
func WithURLFetchTimeout(timeout time.Duration) Option {
	return func(o *options) {
		if timeout > 0 {
			o.urlFetchTimeout = timeout
		}
	}
}

// WithURLFetchAllowPrivateNetworks permits HTTPS URL media sources that resolve
// to private network addresses. It is intended only for trusted internal
// deployments. HTTP, loopback, link-local, metadata, multicast, and
// unspecified targets remain blocked, and redirects are still revalidated.
func WithURLFetchAllowPrivateNetworks() Option {
	return func(o *options) { o.allowPrivateURLFetches = true }
}

func newURLFetchClient(timeout time.Duration, policies ...urlFetchPolicy) *http.Client {
	if timeout <= 0 {
		timeout = defaultURLFetchTimeout
	}
	policy := urlFetchPolicy{}
	if len(policies) > 0 {
		policy = policies[0]
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Do not delegate media URL resolution to an environment-configured proxy.
	// The custom dialer validates and pins each direct connection to a checked IP.
	transport.Proxy = nil
	transport.DialContext = safeURLFetchDialContext(policy)
	return &http.Client{Timeout: timeout, Transport: transport}
}

func safeURLFetchDialContext(policy urlFetchPolicy) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid URL fetch address %q: %w", address, err)
		}
		ips, err := resolveURLFetchHost(ctx, host, policy)
		if err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
}

// WithMaxTokens sets the maximum number of tokens in the response.
func WithMaxTokens(n int64) Option {
	v := int32(n)
	return func(o *options) { o.maxTokens = &v }
}

// WithThinking enables extended thinking at the given effort level. For Claude
// models the effort is mapped to a token budget via provider.ThinkingBudgets;
// for Nova 2 the effort string is sent directly as maxReasoningEffort.
//
// For direct control over the Claude token budget, use WithThinkingBudget
// instead — it takes precedence when both are set. For Claude models, the
// resolved budget is added on top of MaxTokens so the model has headroom to
// both reason and answer.
func WithThinking(effort pvdr.ThinkingEffort) Option {
	return func(o *options) { o.thinkingEffort = effort }
}

// WithThinkingBudget enables extended thinking with an explicit token budget.
// Only meaningful for Claude models — Nova 2 uses an enum effort level and
// ignores this option. Takes precedence over WithThinking when both are set.
//
// The budget is added on top of MaxTokens so the model has headroom to both
// reason and produce a final answer.
func WithThinkingBudget(tokens int64) Option {
	return func(o *options) { o.thinkingBudget = tokens }
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

// WithSystemPromptCaching enables prompt caching for Claude models on Bedrock.
// Non-Claude models silently ignore this option.
func WithSystemPromptCaching() Option {
	return func(o *options) { o.cachingEnabled = true }
}

// Must is a helper that wraps a (*BedrockProvider, error) call and panics on error.
// Use it to collapse provider creation and agent creation into a single error check
// in examples, scripts, and CLI tools where a provider failure is fatal.
//
//	provider := bedrock.Must(bedrock.Standard())
//	a, err := agent.New(provider, instructions)
func Must(p *BedrockProvider, err error) *BedrockProvider {
	if err != nil {
		panic("bedrock: " + err.Error())
	}
	return p
}

// New creates a new BedrockProvider. It loads AWS config from the default
// credential chain and accepts optional configuration.
func New(model string, opts ...Option) (*BedrockProvider, error) {
	o := &options{capabilities: agent.ModelCapabilities{NativeStructuredOutput: agent.Unsupported}}
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
		fetchClient:      newURLFetchClient(o.urlFetchTimeout, urlFetchPolicy{allowPrivateNetworks: o.allowPrivateURLFetches}),
		urlFetchPolicy:   urlFetchPolicy{allowPrivateNetworks: o.allowPrivateURLFetches},
		model:            model,
		maxTokens:        o.maxTokens,
		thinkingStyle:    o.thinkingStyle,
		thinkingEffort:   o.thinkingEffort,
		thinkingBudget:   o.thinkingBudget,
		guardrailID:      o.guardrailID,
		guardrailVersion: o.guardrailVersion,
		cachingEnabled:   o.cachingEnabled,
		capabilities:     o.capabilities,
	}, nil
}

// Model returns the model ID this provider is configured to use.
func (p *BedrockProvider) ModelID() string { return p.model }

// Capabilities returns effective adapter/model capabilities captured during
// construction. The returned value may be changed by the caller without
// mutating this provider.
func (p *BedrockProvider) Capabilities() agent.ModelCapabilities { return p.capabilities }

// Client returns the underlying AWS Bedrock runtime client.
// Use this for direct SDK access when you need provider-specific features
// not exposed through the agent.Provider interface.
func (p *BedrockProvider) Client() *bedrockruntime.Client { return p.client }

// Compile-time checks: BedrockProvider satisfies the required and optional provider contracts.
var _ agent.Provider = (*BedrockProvider)(nil)
var _ agent.CapabilityProvider = (*BedrockProvider)(nil)

// Name returns a human-readable identifier for this provider instance.
func (p *BedrockProvider) Name() string { return "bedrock" }

// Stream sends messages to Bedrock using its streaming API. Text and thinking
// deltas are emitted as typed events while the complete response is accumulated.
func (p *BedrockProvider) Stream(ctx context.Context, req agent.ModelRequest, emit func(agent.ModelEvent)) (*agent.ModelResponse, error) {
	infCfg := p.buildInferenceConfiguration(req.InferenceConfig)
	cachingEnabled := req.CachingEnabled || p.cachingEnabled
	msgs, err := toBedrockMessagesWithFetcher(ctx, p.fetchClient, p.urlFetchPolicy, req.Messages, p.model, cachingEnabled)
	if err != nil {
		return nil, &agent.ProviderError{Cause: err}
	}
	input := &bedrockruntime.ConverseStreamInput{
		ModelId:         aws.String(p.model),
		Messages:        msgs,
		InferenceConfig: infCfg,
	}
	if req.System != "" {
		if cachingEnabled && isClaudeModel(p.model) {
			input.System = []types.SystemContentBlock{
				&types.SystemContentBlockMemberText{Value: req.System},
				&types.SystemContentBlockMemberCachePoint{
					Value: types.CachePointBlock{Type: types.CachePointTypeDefault},
				},
			}
		} else {
			input.System = []types.SystemContentBlock{
				&types.SystemContentBlockMemberText{Value: req.System},
			}
		}
	}
	if tc := toToolConfig(req.Tools); tc != nil {
		if bc := toBedrockToolChoice(req.ToolChoice); bc != nil {
			tc.ToolChoice = bc
		}
		input.ToolConfig = tc
	}
	input.AdditionalModelRequestFields = p.buildAdditionalFields(req.InferenceConfig)

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

	resp := &agent.ModelResponse{}
	state := streamState{}
	stream := out.GetStream()
	for event := range stream.Events() {
		applyStreamEvent(resp, &state, event, emit)
	}
	stream.Close()
	if err := stream.Err(); err != nil {
		return nil, &agent.ProviderError{Cause: err}
	}

	return resp, nil
}

type streamState struct {
	toolName  string
	toolID    string
	toolInput string
	reasoning string
}

func applyStreamEvent(resp *agent.ModelResponse, state *streamState, event types.ConverseStreamOutput, emit func(agent.ModelEvent)) {
	switch ev := event.(type) {
	case *types.ConverseStreamOutputMemberContentBlockStart:
		if toolStart, ok := ev.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
			state.toolName = aws.ToString(toolStart.Value.Name)
			state.toolID = aws.ToString(toolStart.Value.ToolUseId)
			state.toolInput = ""
		}

	case *types.ConverseStreamOutputMemberContentBlockDelta:
		switch delta := ev.Value.Delta.(type) {
		case *types.ContentBlockDeltaMemberText:
			resp.Text += delta.Value
			if emit != nil {
				emit(agent.ModelEvent{Type: agent.ModelEventText, Text: delta.Value})
			}
		case *types.ContentBlockDeltaMemberToolUse:
			state.toolInput += aws.ToString(delta.Value.Input)
		case *types.ContentBlockDeltaMemberReasoningContent:
			if text, ok := delta.Value.(*types.ReasoningContentBlockDeltaMemberText); ok {
				state.reasoning += text.Value
				if emit != nil {
					emit(agent.ModelEvent{Type: agent.ModelEventThinking, Text: text.Value})
				}
			}
		}

	case *types.ConverseStreamOutputMemberContentBlockStop:
		if state.toolName != "" {
			input := json.RawMessage(state.toolInput)
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			resp.ToolCalls = append(resp.ToolCalls, tool.Call{
				ToolUseID: state.toolID,
				Name:      state.toolName,
				Input:     input,
			})
			state.toolName = ""
			state.toolID = ""
			state.toolInput = ""
		}
		if state.reasoning != "" {
			if resp.Metadata == nil {
				resp.Metadata = map[string]any{}
			}
			existing, _ := resp.Metadata["thinking"].(string)
			resp.Metadata["thinking"] = existing + state.reasoning
			state.reasoning = ""
		}

	case *types.ConverseStreamOutputMemberMetadata:
		applyTokenUsage(resp, ev.Value.Usage)
	}
}

func applyTokenUsage(resp *agent.ModelResponse, usage *types.TokenUsage) {
	if usage == nil {
		return
	}
	resp.Usage.InputTokens = int(aws.ToInt32(usage.InputTokens))
	resp.Usage.OutputTokens = int(aws.ToInt32(usage.OutputTokens))
	if usage.CacheReadInputTokens != nil {
		resp.Usage.CacheReadTokens = int(aws.ToInt32(usage.CacheReadInputTokens))
	}
	if usage.CacheWriteInputTokens != nil {
		resp.Usage.CacheWriteTokens = int(aws.ToInt32(usage.CacheWriteInputTokens))
	}
}

// ---------------------------------------------------------------------------
// Inference config helpers
// ---------------------------------------------------------------------------

// resolveThinkingBudget returns the active Claude thinking budget in tokens.
// An explicit budget set via WithThinkingBudget takes precedence over an
// effort level set via WithThinking. Returns 0 when thinking is disabled,
// the model is not a Claude-style thinking model, or the effort level is
// unknown.
func (p *BedrockProvider) resolveThinkingBudget() int64 {
	if p.thinkingStyle != thinkingStyleClaude {
		return 0
	}
	if p.thinkingBudget > 0 {
		return p.thinkingBudget
	}
	if p.thinkingEffort != "" {
		return pvdr.ThinkingBudgets[p.thinkingEffort]
	}
	return 0
}

// buildInferenceConfiguration builds the Bedrock InferenceConfiguration from
// the provider's constructor defaults and the optional per-call InferenceConfig.
// Temperature, TopP, StopSequences, and MaxTokens are mapped here.
// TopK is handled separately via buildAdditionalFields.
//
// When thinking is enabled on a Claude model, the resolved thinking budget
// is added on top of MaxTokens so the model has room to both reason and answer.
func (p *BedrockProvider) buildInferenceConfiguration(cfg *agent.InferenceConfig) *types.InferenceConfiguration {
	// Determine maxTokens: per-call override > provider-level > nil (omit).
	var maxTokens *int32
	if p.maxTokens != nil {
		v := *p.maxTokens
		maxTokens = &v
	}
	if cfg != nil && cfg.MaxTokens != nil {
		v := int32(*cfg.MaxTokens)
		maxTokens = &v
	}
	if budget := p.resolveThinkingBudget(); budget > 0 {
		// Claude requires max_tokens > budget_tokens. When no explicit limit is
		// set, use ThinkingOutputHeadroom as the answer headroom on top of the budget.
		base := int32(pvdr.ThinkingOutputHeadroom)
		if maxTokens != nil {
			base = *maxTokens
		}
		v := base + int32(budget)
		maxTokens = &v
	}

	ic := &types.InferenceConfiguration{
		MaxTokens: maxTokens,
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
	return ic
}

// buildAdditionalFields builds the AdditionalModelRequestFields document,
// merging thinking configuration (if enabled) with TopK (if provided).
func (p *BedrockProvider) buildAdditionalFields(cfg *agent.InferenceConfig) document.Interface {
	hasTopK := cfg != nil && cfg.TopK != nil

	// Thinking config differs by model style.
	var thinkingFields map[string]any
	switch p.thinkingStyle {
	case thinkingStyleClaude:
		if budget := p.resolveThinkingBudget(); budget > 0 {
			thinkingFields = map[string]any{
				"thinking": map[string]any{
					"type":          "enabled",
					"budget_tokens": budget,
				},
			}
		}
	case thinkingStyleNova2:
		// Nova 2 takes the effort string directly. Budget is not applicable.
		if p.thinkingEffort != "" {
			thinkingFields = map[string]any{
				"reasoningConfig": map[string]any{
					"type":               "enabled",
					"maxReasoningEffort": string(p.thinkingEffort),
				},
			}
		}
	case thinkingStyleGrok:
		// Grok takes a flat "reasoning_effort" string on Converse. Budget is
		// not applicable. Reasoning is always on; this only controls effort.
		if p.thinkingEffort != "" {
			thinkingFields = map[string]any{
				"reasoning_effort": string(p.thinkingEffort),
			}
		}
	}

	if thinkingFields == nil && !hasTopK {
		return nil
	}

	fields := map[string]any{}
	for k, v := range thinkingFields {
		fields[k] = v
	}
	if hasTopK {
		fields["top_k"] = *cfg.TopK
	}

	return document.NewLazyDocument(fields)
}

// ---------------------------------------------------------------------------
// Type mapping helpers: framework → Bedrock SDK
// ---------------------------------------------------------------------------

// isClaudeModel reports whether modelID is an Anthropic Claude model on Bedrock.
// All Claude model IDs contain the substring "anthropic." regardless of the
// cross-region routing prefix (us., eu., global., or none).
func isClaudeModel(modelID string) bool {
	return strings.Contains(modelID, "anthropic.")
}

// toBedrockMessages converts framework Messages to Bedrock SDK Messages.
// When cachingEnabled is true and the model is a Claude model, a CachePoint
// is injected after every DocumentBlock in user messages.
func toBedrockMessages(msgs []agent.Message, modelID string, cachingEnabled bool) ([]types.Message, error) {
	return toBedrockMessagesWithFetcher(context.Background(), newURLFetchClient(0), urlFetchPolicy{}, msgs, modelID, cachingEnabled)
}

func toBedrockMessagesWithFetcher(ctx context.Context, fetchClient *http.Client, fetchPolicy urlFetchPolicy, msgs []agent.Message, modelID string, cachingEnabled bool) ([]types.Message, error) {
	injectCachePoints := cachingEnabled && isClaudeModel(modelID)
	out := make([]types.Message, len(msgs))
	for i, m := range msgs {
		blocks, err := toBedrockContentBlocksWithFetcher(ctx, fetchClient, fetchPolicy, m.Content, modelID, injectCachePoints)
		if err != nil {
			return nil, err
		}
		// Bedrock requires every message to carry at least one content block.
		// Dropping blank text (above) can leave a message empty; substitute a
		// minimal placeholder so the request stays valid.
		if len(blocks) == 0 {
			blocks = []types.ContentBlock{&types.ContentBlockMemberText{Value: "(no content)"}}
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

func toBedrockContentBlocks(blocks []agent.ContentBlock, modelID string, injectCachePoints bool) ([]types.ContentBlock, error) {
	return toBedrockContentBlocksWithFetcher(context.Background(), newURLFetchClient(0), urlFetchPolicy{}, blocks, modelID, injectCachePoints)
}

func toBedrockContentBlocksWithFetcher(ctx context.Context, fetchClient *http.Client, fetchPolicy urlFetchPolicy, blocks []agent.ContentBlock, modelID string, injectCachePoints bool) ([]types.ContentBlock, error) {
	out := make([]types.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch v := b.(type) {
		case agent.TextBlock:
			// Bedrock rejects blank text content blocks ("The text field in
			// the ContentBlock object ... is blank"). Drop empty text rather
			// than emit an invalid block; the empty-content guard below keeps
			// a message from becoming entirely contentless.
			if v.Text == "" {
				continue
			}
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
				bytes, err := imageBytesWithFetcherAndPolicy(ctx, fetchClient, fetchPolicy, img.Source)
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
			bytes, err := imageBytesWithFetcher(ctx, fetchClient, v.Source)
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
			var source types.DocumentSource
			if v.Source.S3URI != "" {
				s3Location := types.S3Location{Uri: aws.String(v.Source.S3URI)}
				if v.Source.S3BucketOwner != "" {
					s3Location.BucketOwner = aws.String(v.Source.S3BucketOwner)
				}
				source = &types.DocumentSourceMemberS3Location{Value: s3Location}
			} else {
				if v.Source.FileID != "" {
					return nil, fmt.Errorf("DocumentBlock: Bedrock does not support provider file IDs")
				}
				bytes, err := documentBytesWithFetcherAndPolicy(ctx, fetchClient, fetchPolicy, v.Source)
				if err != nil {
					return nil, fmt.Errorf("DocumentBlock: %w", err)
				}
				source = &types.DocumentSourceMemberBytes{Value: bytes}
			}
			name := sanitizeDocName(v.Source.Name)
			out = append(out, &types.ContentBlockMemberDocument{
				Value: types.DocumentBlock{
					Name:   aws.String(name),
					Format: toBedrockDocFormat(v.Source.MIMEType),
					Source: source,
				},
			})
			// When caching is enabled on Claude models, inject a CachePoint
			// immediately after each DocumentBlock.
			if injectCachePoints {
				out = append(out, &types.ContentBlockMemberCachePoint{
					Value: types.CachePointBlock{Type: types.CachePointTypeDefault},
				})
			}
		}
	}
	return out, nil
}

// imageBytes returns the raw bytes from an ImageSource.
// If Source.Data is set, it is returned directly.
// If Source.Base64 is set, it is decoded from standard base64.
// If Source.URL is set, the image is fetched via a bounded HTTP GET.
func imageBytes(src agent.ImageSource) ([]byte, error) {
	return imageBytesWithFetcher(context.Background(), newURLFetchClient(0), src)
}

func imageBytesWithFetcher(ctx context.Context, fetchClient *http.Client, src agent.ImageSource) ([]byte, error) {
	return imageBytesWithFetcherAndPolicy(ctx, fetchClient, urlFetchPolicy{}, src)
}

func imageBytesWithFetcherAndPolicy(ctx context.Context, fetchClient *http.Client, fetchPolicy urlFetchPolicy, src agent.ImageSource) ([]byte, error) {
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
		return fetchURLBytesWithPolicy(ctx, fetchClient, fetchPolicy, src.URL, "image")
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
	return documentBytesWithFetcher(context.Background(), newURLFetchClient(0), src)
}

func documentBytesWithFetcher(ctx context.Context, fetchClient *http.Client, src agent.DocumentSource) ([]byte, error) {
	return documentBytesWithFetcherAndPolicy(ctx, fetchClient, urlFetchPolicy{}, src)
}

func documentBytesWithFetcherAndPolicy(ctx context.Context, fetchClient *http.Client, fetchPolicy urlFetchPolicy, src agent.DocumentSource) ([]byte, error) {
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
		return fetchURLBytesWithPolicy(ctx, fetchClient, fetchPolicy, src.URL, "document")
	}
	return nil, fmt.Errorf("DocumentSource has no data, base64, or URL")
}

func fetchURLBytes(ctx context.Context, fetchClient *http.Client, rawURL, resource string) ([]byte, error) {
	return fetchURLBytesWithPolicy(ctx, fetchClient, urlFetchPolicy{}, rawURL, resource)
}

func fetchURLBytesWithPolicy(ctx context.Context, fetchClient *http.Client, fetchPolicy urlFetchPolicy, rawURL, resource string) ([]byte, error) {
	if fetchClient == nil {
		fetchClient = newURLFetchClient(0, fetchPolicy)
	}

	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch %s URL: %w", resource, err)
	}
	if err := validateURLFetchTarget(ctx, target, fetchPolicy); err != nil {
		return nil, fmt.Errorf("fetch %s URL: %w", resource, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("fetch %s URL: %w", resource, err)
	}

	client := *fetchClient
	priorCheckRedirect := client.CheckRedirect
	client.CheckRedirect = func(redirect *http.Request, via []*http.Request) error {
		if len(via) > defaultURLFetchMaxRedirects {
			return fmt.Errorf("URL fetch exceeded %d redirects", defaultURLFetchMaxRedirects)
		}
		if err := validateURLFetchTarget(redirect.Context(), redirect.URL, fetchPolicy); err != nil {
			return fmt.Errorf("unsafe redirect target: %w", err)
		}
		if priorCheckRedirect != nil {
			return priorCheckRedirect(redirect, via)
		}
		return nil
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s URL: %w", resource, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch %s URL: status %d", resource, resp.StatusCode)
	}
	if resp.ContentLength > defaultURLFetchMaxResponseBytes {
		return nil, fmt.Errorf("fetch %s URL: response exceeds %d-byte limit", resource, defaultURLFetchMaxResponseBytes)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, defaultURLFetchMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s URL body: %w", resource, err)
	}
	if int64(len(body)) > defaultURLFetchMaxResponseBytes {
		return nil, fmt.Errorf("fetch %s URL: response exceeds %d-byte limit", resource, defaultURLFetchMaxResponseBytes)
	}
	return body, nil
}

func validateURLFetchTarget(ctx context.Context, target *url.URL, policy urlFetchPolicy) error {
	if target == nil || target.Scheme != "https" {
		return fmt.Errorf("only HTTPS URL targets are allowed")
	}
	if target.User != nil {
		return fmt.Errorf("URL user credentials are not allowed")
	}
	host := target.Hostname()
	if host == "" {
		return fmt.Errorf("URL host is required")
	}
	_, err := resolveURLFetchHost(ctx, host, policy)
	return err
}

func resolveURLFetchHost(ctx context.Context, host string, policy urlFetchPolicy) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		if err := validateURLFetchIP(addr, policy); err != nil {
			return nil, err
		}
		return []netip.Addr{addr}, nil
	}

	lookupIPAddr := policy.lookupIPAddr
	if lookupIPAddr == nil {
		lookupIPAddr = net.DefaultResolver.LookupIPAddr
	}
	resolved, err := lookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve URL host %q: %w", host, err)
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("resolve URL host %q: no addresses", host)
	}

	ips := make([]netip.Addr, 0, len(resolved))
	for _, resolvedIP := range resolved {
		addr, ok := netip.AddrFromSlice(resolvedIP.IP)
		if !ok {
			return nil, fmt.Errorf("resolve URL host %q: invalid address", host)
		}
		addr = addr.Unmap()
		if err := validateURLFetchIP(addr, policy); err != nil {
			return nil, fmt.Errorf("resolve URL host %q: %w", host, err)
		}
		ips = append(ips, addr)
	}
	return ips, nil
}

func validateURLFetchIP(addr netip.Addr, policy urlFetchPolicy) error {
	switch {
	case addr == urlFetchMetadataIPv4 || addr == urlFetchMetadataIPv6:
		return fmt.Errorf("metadata IP address %s is not allowed", addr)
	case addr.IsLoopback():
		return fmt.Errorf("loopback IP address %s is not allowed", addr)
	case addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast():
		return fmt.Errorf("link-local IP address %s is not allowed", addr)
	case addr.IsPrivate() && !policy.allowPrivateNetworks:
		return fmt.Errorf("private IP address %s is not allowed", addr)
	case addr.IsUnspecified():
		return fmt.Errorf("unspecified IP address %s is not allowed", addr)
	case addr.IsMulticast():
		return fmt.Errorf("multicast IP address %s is not allowed", addr)
	}
	return nil
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
