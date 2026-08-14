package tracing

// AttrRole identifies a logical span attribute. The actual key emitted on
// spans is determined by the active AttributeScheme: callers map roles to
// concrete attribute keys, allowing the same tracing hook to emit
// attributes compatible with different observability backends.
//
// Each role's string value is also its default key, so unmapped roles fall
// back to a sensible attribute name without further configuration.
type AttrRole string

// Logical attribute roles. The string value of each role is used as the
// default attribute key (see AttributeScheme.Key for fallback semantics).
const (
	// Agent-level
	RoleAgentName            AttrRole = AttrRole(AttrAgentName)
	RoleAgentConversationID  AttrRole = AttrRole(AttrAgentConversationID)
	RoleAgentMaxIterations   AttrRole = AttrRole(AttrAgentMaxIterations)
	RoleAgentModelID         AttrRole = AttrRole(AttrAgentModelID)
	RoleAgentTokenInput      AttrRole = AttrRole(AttrAgentTokenUsageInput)
	RoleAgentTokenOutput     AttrRole = AttrRole(AttrAgentTokenUsageOutput)
	RoleAgentTokenCacheRead  AttrRole = AttrRole(AttrAgentTokenUsageCacheRead)
	RoleAgentTokenCacheWrite AttrRole = AttrRole(AttrAgentTokenUsageCacheWrite)
	RoleAgentTokenTotal      AttrRole = "agent.token_usage.total"
	RoleAgentError           AttrRole = "agent.error"
	RoleAgentImageCount      AttrRole = AttrRole(AttrAgentImageCount)
	RoleAgentDocumentCount   AttrRole = AttrRole(AttrAgentDocumentCount)
	RoleAgentUserMsgLength   AttrRole = "agent.user_message_length"
	RoleGenAISystem          AttrRole = AttrRole(AttrGenAISystem)

	// Iteration
	RoleIterationNumber    AttrRole = AttrRole(AttrAgentIterationNumber)
	RoleIterationToolCount AttrRole = AttrRole(AttrAgentIterationToolCount)
	RoleIterationFinal     AttrRole = AttrRole(AttrAgentIterationFinal)

	// Provider
	RoleProviderModelID          AttrRole = AttrRole(AttrProviderModelID)
	RoleProviderInputTokens      AttrRole = AttrRole(AttrProviderInputTokens)
	RoleProviderOutputTokens     AttrRole = AttrRole(AttrProviderOutputTokens)
	RoleProviderCacheReadTokens  AttrRole = AttrRole(AttrProviderCacheReadTokens)
	RoleProviderCacheWriteTokens AttrRole = AttrRole(AttrProviderCacheWriteTokens)
	RoleProviderToolCalls        AttrRole = AttrRole(AttrProviderToolCalls)
	RoleProviderMessageCount     AttrRole = AttrRole(AttrProviderMessageCount)

	// Tool
	RoleToolName         AttrRole = AttrRole(AttrToolName)
	RoleToolInput        AttrRole = AttrRole(AttrToolInput)
	RoleToolOutput       AttrRole = AttrRole(AttrToolOutput)
	RoleToolInputLength  AttrRole = "tool.input_length"
	RoleToolOutputLength AttrRole = "tool.output_length"

	// Guardrail
	RoleGuardrailInput  AttrRole = AttrRole(AttrGuardrailInput)
	RoleGuardrailOutput AttrRole = AttrRole(AttrGuardrailOutput)

	// Memory / conversation
	RoleMemoryConversationID AttrRole = AttrRole(AttrMemoryConversationID)

	// Retriever
	RoleRetrieverQuery         AttrRole = AttrRole(AttrRetrieverQuery)
	RoleRetrieverDocumentCount AttrRole = AttrRole(AttrRetrieverDocumentCount)

	// Graph
	RoleGraphIterations AttrRole = AttrRole(AttrGraphIterations)

	// Operation name — emitted per span to classify it for evaluation systems.
	// Default is empty (disabled). Set via scheme to enable.
	RoleOperationName AttrRole = ""

	// Content capture (opt-in via WithContentCapture)
	RoleGenAIPrompt           AttrRole = AttrRole(AttrGenAIPrompt)
	RoleGenAISystemPrompt     AttrRole = AttrRole(AttrGenAISystemPrompt)
	RoleGenAICompletion       AttrRole = AttrRole(AttrGenAICompletion)
	RoleGenAIProviderResponse AttrRole = AttrRole(AttrGenAIProviderResponse)

	// Inference config
	RoleGenAITemperature   AttrRole = AttrRole(AttrGenAITemperature)
	RoleGenAITopP          AttrRole = AttrRole(AttrGenAITopP)
	RoleGenAITopK          AttrRole = AttrRole(AttrGenAITopK)
	RoleGenAIMaxTokens     AttrRole = AttrRole(AttrGenAIMaxTokens)
	RoleGenAIStopSequences AttrRole = AttrRole(AttrGenAIStopSequences)

	// Event names
	RoleEventMaxIterationsExceeded AttrRole = AttrRole(EventMaxIterationsExceeded)
)

// AttributeScheme maps logical attribute roles to concrete span attribute
// key strings. A nil or empty scheme behaves identically to DefaultScheme():
// each Key call falls back to the role's string value.
type AttributeScheme map[AttrRole]string

// Key returns the attribute key configured for role, or the role's default
// string value if no override is configured. Safe to call on a nil map.
func (s AttributeScheme) Key(role AttrRole) string {
	if v, ok := s[role]; ok && v != "" {
		return v
	}
	return string(role)
}

// DefaultScheme returns the default attribute naming scheme used by
// gude-agents. Returning an empty map relies on AttributeScheme.Key's
// fallback to each role's default key, keeping the scheme allocation-free.
func DefaultScheme() AttributeScheme {
	return AttributeScheme{}
}

// AgentCoreScheme returns an attribute naming scheme aligned with OpenTelemetry
// GenAI semantic conventions for AWS Bedrock (gen_ai.*, aws.bedrock.*), which is
// the format AgentCore Observability ingests via ADOT.
func AgentCoreScheme() AttributeScheme {
	return AttributeScheme{
		// Agent — official OTel GenAI agent span attributes.
		RoleAgentName:           "gen_ai.agent.name",
		RoleAgentConversationID: "gen_ai.conversation.id",
		RoleAgentModelID:        "gen_ai.request.model",
		RoleAgentTokenInput:     "gen_ai.usage.input_tokens",
		RoleAgentTokenOutput:    "gen_ai.usage.output_tokens",
		RoleAgentTokenTotal:     "gen_ai.usage.total_tokens",
		RoleGenAISystem:         "gen_ai.provider.name",
		RoleAgentError:          "error.type",

		// Agent — best-effort extensions (no direct semconv match).
		RoleAgentMaxIterations: "gen_ai.agent.max_iterations",
		RoleAgentImageCount:    "gen_ai.agent.image_count",
		RoleAgentDocumentCount: "gen_ai.agent.document_count",
		RoleAgentUserMsgLength: "gen_ai.agent.user_message_length",

		// Iteration — best-effort extensions.
		RoleIterationNumber:    "gen_ai.agent.iteration.number",
		RoleIterationToolCount: "gen_ai.agent.iteration.tool_count",
		RoleIterationFinal:     "gen_ai.agent.iteration.final",

		// Provider — align with OTel GenAI request/usage attributes.
		RoleProviderModelID:      "gen_ai.request.model",
		RoleProviderInputTokens:  "gen_ai.usage.input_tokens",
		RoleProviderOutputTokens: "gen_ai.usage.output_tokens",
		RoleProviderToolCalls:    "gen_ai.agent.tool_call_count",
		RoleProviderMessageCount: "gen_ai.request.message_count",

		// Tool — official OTel execute_tool span attribute.
		RoleToolName:         "gen_ai.tool.name",
		RoleToolInput:        "gen_ai.tool.input",
		RoleToolOutput:       "gen_ai.tool.output",
		RoleToolInputLength:  "gen_ai.tool.input_length",
		RoleToolOutputLength: "gen_ai.tool.output_length",

		// Guardrail — best-effort, AWS Bedrock guardrail registry attribute
		// (aws.bedrock.guardrail.id) is only an identifier, not I/O text.
		RoleGuardrailInput:  "gen_ai.agent.guardrail.input",
		RoleGuardrailOutput: "gen_ai.agent.guardrail.output",

		// Memory / conversation — official OTel attribute.
		RoleMemoryConversationID: "gen_ai.conversation.id",

		// Retriever — official OTel data source attribute for the source ID,
		// best-effort extensions for query and document count.
		RoleRetrieverQuery:         "gen_ai.agent.retriever.query",
		RoleRetrieverDocumentCount: "gen_ai.agent.retriever.document_count",

		// Graph — best-effort extension.
		RoleGraphIterations: "gen_ai.agent.graph.iterations",

		// Content capture — official OTel input/output attributes.
		RoleGenAIPrompt:           "gen_ai.input.messages",
		RoleGenAISystemPrompt:     "gen_ai.system_instructions",
		RoleGenAICompletion:       "gen_ai.output.messages",
		RoleGenAIProviderResponse: "gen_ai.output.messages",

		// Inference config — official OTel request attributes.
		RoleGenAITemperature:   "gen_ai.request.temperature",
		RoleGenAITopP:          "gen_ai.request.top_p",
		RoleGenAITopK:          "gen_ai.request.top_k",
		RoleGenAIMaxTokens:     "gen_ai.request.max_tokens",
		RoleGenAIStopSequences: "gen_ai.request.stop_sequences",

		// Event names — best-effort extension.
		RoleEventMaxIterationsExceeded: "gen_ai.agent.max_iterations_exceeded",
	}
}

// WithScheme configures the tracing hook to use a custom attribute naming
// scheme. If not set (or nil/empty), each role falls back to its default key.
//
// Example for AgentCore-compatible OTel GenAI keys:
//
//	tracing.WithTracing(tp, tracing.WithScheme(tracing.AgentCoreScheme()))
func WithScheme(scheme AttributeScheme) TracingOption {
	return func(h *otelHook) {
		h.scheme = scheme
	}
}
