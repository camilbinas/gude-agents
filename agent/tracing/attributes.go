package tracing

// Attribute key constants for span attributes.
// Users can reference these in custom instrumentation.
const (
	AttrGenAISystem             = "gen_ai.system"
	AttrAgentMaxIterations      = "agent.max_iterations"
	AttrAgentModelID            = "agent.model_id"
	AttrAgentConversationID     = "agent.conversation_id"
	AttrAgentName               = "agent.name"
	AttrAgentTokenUsageInput    = "agent.token_usage.input"
	AttrAgentTokenUsageOutput   = "agent.token_usage.output"
	AttrAgentIterationNumber    = "agent.iteration.number"
	AttrAgentIterationToolCount = "agent.iteration.tool_count"
	AttrAgentIterationFinal     = "agent.iteration.final"
	AttrProviderModelID         = "provider.model_id"
	AttrProviderInputTokens     = "provider.input_tokens"
	AttrProviderOutputTokens    = "provider.output_tokens"
	AttrProviderToolCalls       = "provider.tool_calls"
	AttrProviderMessageCount    = "provider.message_count"
	AttrToolName                = "tool.name"
	AttrToolInput               = "tool.input"
	AttrToolOutput              = "tool.output"
	AttrGuardrailInput          = "guardrail.input"
	AttrGuardrailOutput         = "guardrail.output"
	AttrMemoryConversationID    = "memory.conversation_id"
	AttrRetrieverQuery          = "retriever.query"
	AttrRetrieverDocumentCount  = "retriever.document_count"
	AttrGraphIterations         = "graph.iterations"
)

// Content capture attributes (opt-in via WithContentCapture).
const (
	AttrGenAIPrompt           = "gen_ai.prompt"
	AttrGenAISystemPrompt     = "gen_ai.system_prompt"
	AttrGenAICompletion       = "gen_ai.completion"
	AttrGenAIProviderResponse = "gen_ai.provider.response"
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
