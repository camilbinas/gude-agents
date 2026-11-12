package tracing

// Attribute key constants for span attributes.
// Users can reference these in custom instrumentation.
const (
	AttrGenAISystem               = "gen_ai.system"
	AttrAgentMaxIterations        = "agent.max_iterations"
	AttrAgentModelID              = "agent.model_id"
	AttrAgentConversationID       = "agent.conversation_id"
	AttrAgentName                 = "agent.name"
	AttrAgentTokenUsageInput      = "agent.token_usage.input"
	AttrAgentTokenUsageOutput     = "agent.token_usage.output"
	AttrAgentTokenUsageCacheRead  = "agent.token_usage.cache_read"
	AttrAgentTokenUsageCacheWrite = "agent.token_usage.cache_write"
	AttrAgentIterationNumber      = "agent.iteration.number"
	AttrAgentIterationToolCount   = "agent.iteration.tool_count"
	AttrAgentIterationFinal       = "agent.iteration.final"
	AttrProviderModelID           = "provider.model_id"
	AttrProviderInputTokens       = "provider.input_tokens"
	AttrProviderOutputTokens      = "provider.output_tokens"
	AttrProviderCacheReadTokens   = "provider.cache_read_tokens"
	AttrProviderCacheWriteTokens  = "provider.cache_write_tokens"
	AttrProviderToolCalls         = "provider.tool_calls"
	AttrProviderMessageCount      = "provider.message_count"
	AttrToolName                  = "tool.name"
	AttrToolInput                 = "tool.input"
	AttrToolOutput                = "tool.output"
	AttrGuardrailInput            = "guardrail.input"
	AttrGuardrailOutput           = "guardrail.output"
	AttrMemoryConversationID      = "memory.conversation_id"
	AttrRetrieverQuery            = "retriever.query"
	AttrRetrieverDocumentCount    = "retriever.document_count"
	AttrAgentImageCount           = "agent.image_count"
	AttrAgentDocumentCount        = "agent.document_count"
)

// SemconvGenAIVersion documents the OpenTelemetry GenAI semantic-conventions
// version these attribute names target. The GenAI conventions are published
// (and versioned) separately from the stable OTel semconv packages in the
// open-telemetry/semantic-conventions-genai repository and are currently at
// Development stability, so the gen_ai.* keys below are hand-pinned string
// constants rather than imported from a generated semconv package. Consult the
// changelog of that repository before bumping this value.
//
// See: https://github.com/open-telemetry/semantic-conventions-genai
const SemconvGenAIVersion = "1.37.0"

// GenAI operation names, per gen_ai.operation.name well-known values.
// Emitted by default on the corresponding spans; used to classify spans for
// GenAI-aware observability backends and evaluation systems.
const (
	OperationInvokeAgent = "invoke_agent"
	OperationChat        = "chat"
	OperationExecuteTool = "execute_tool"
)

// Content capture attributes (opt-in via WithContentCapture).
//
// gen_ai.prompt and gen_ai.completion are DEPRECATED in the OpenTelemetry
// GenAI semantic conventions in favor of the structured gen_ai.input.messages
// and gen_ai.output.messages attributes (see AttrGenAIInputMessages /
// AttrGenAIOutputMessages). They remain emitted for backward compatibility
// with existing dashboards and can be turned off with WithoutLegacyContentAttributes.
const (
	AttrGenAIPrompt           = "gen_ai.prompt"
	AttrGenAISystemPrompt     = "gen_ai.system_prompt"
	AttrGenAICompletion       = "gen_ai.completion"
	AttrGenAIProviderResponse = "gen_ai.provider.response"

	// Structured content attributes per current GenAI semconv. These carry a
	// JSON-encoded array of role/parts message objects.
	AttrGenAIInputMessages  = "gen_ai.input.messages"
	AttrGenAIOutputMessages = "gen_ai.output.messages"

	// AttrGenAIOperationName classifies the span's operation (chat,
	// execute_tool, invoke_agent). Emitted by default.
	AttrGenAIOperationName = "gen_ai.operation.name"
)

// Inference config attributes (always recorded when set).
const (
	AttrGenAITemperature   = "gen_ai.request.temperature"
	AttrGenAITopP          = "gen_ai.request.top_p"
	AttrGenAITopK          = "gen_ai.request.top_k"
	AttrGenAIMaxTokens     = "gen_ai.request.max_tokens"
	AttrGenAIStopSequences = "gen_ai.request.stop_sequences"
)

// Event names.
const (
	EventMaxIterationsExceeded = "agent.max_iterations_exceeded"
)
