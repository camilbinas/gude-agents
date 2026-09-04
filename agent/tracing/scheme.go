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

// WithScheme configures the tracing hook to use a custom attribute naming
// scheme. If not set (or nil/empty), each role falls back to its default key.
func WithScheme(scheme AttributeScheme) TracingOption {
	return func(h *otelHook) {
		h.scheme = scheme
	}
}
