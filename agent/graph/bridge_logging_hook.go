package graph

import (
	"time"

	"github.com/camilbinas/gude-agents/agent"
)

// bridgeLoggingHook implements agent.LoggingHook and includes the node name as
// context in all log entries. It bridges the graph-level logging to the agent-level
// logging system.
//
// When inner is non-nil, both the inner hook and the graph hook are called (composition).
// When graphHook is nil, the bridge should not be created at all (zero overhead).
type bridgeLoggingHook struct {
	graphHook GraphLoggingHook
	nodeName  string
	inner     agent.LoggingHook // agent's own hook, may be nil
}

// NodeName returns the node name associated with this bridge logging hook.
// This is used by tests and consumers to verify that the node name context is available.
func (b *bridgeLoggingHook) NodeName() string {
	return b.nodeName
}

// OnInvokeStart is called at the beginning of InvokeStream/Invoke.
func (b *bridgeLoggingHook) OnInvokeStart(params agent.InvokeSpanParams) {
	if b.inner != nil {
		b.inner.OnInvokeStart(params)
	}
	b.graphHook.OnNodeStart(b.nodeName)
}

// OnInvokeEnd is called at the end of InvokeStream/Invoke with the outcome.
func (b *bridgeLoggingHook) OnInvokeEnd(err error, usage agent.TokenUsage, duration time.Duration) {
	if b.inner != nil {
		b.inner.OnInvokeEnd(err, usage, duration)
	}
	b.graphHook.OnNodeEnd(b.nodeName, err, duration)
}

// OnIterationStart is called at the beginning of each agent loop iteration.
func (b *bridgeLoggingHook) OnIterationStart(iteration int) {
	if b.inner != nil {
		b.inner.OnIterationStart(iteration)
	}
}

// OnProviderCallStart is called before each Provider.ConverseStream call.
func (b *bridgeLoggingHook) OnProviderCallStart(modelID string) {
	if b.inner != nil {
		b.inner.OnProviderCallStart(modelID)
	}
}

// OnProviderCallEnd is called after each Provider.ConverseStream call with the outcome.
func (b *bridgeLoggingHook) OnProviderCallEnd(err error, usage agent.TokenUsage, toolCallCount int, duration time.Duration) {
	if b.inner != nil {
		b.inner.OnProviderCallEnd(err, usage, toolCallCount, duration)
	}
}

// OnToolStart is called before each tool execution.
func (b *bridgeLoggingHook) OnToolStart(toolName string) {
	if b.inner != nil {
		b.inner.OnToolStart(toolName)
	}
}

// OnToolEnd is called after each tool execution with the outcome.
func (b *bridgeLoggingHook) OnToolEnd(toolName string, err error, duration time.Duration) {
	if b.inner != nil {
		b.inner.OnToolEnd(toolName, err, duration)
	}
}

// OnToolLog is called when a tool emits a log message.
func (b *bridgeLoggingHook) OnToolLog(toolName string, msg string) {
	if b.inner != nil {
		b.inner.OnToolLog(toolName, msg)
	}
}

// OnGuardrailComplete is called after a guardrail evaluation.
func (b *bridgeLoggingHook) OnGuardrailComplete(direction string, blocked bool, err error) {
	if b.inner != nil {
		b.inner.OnGuardrailComplete(direction, blocked, err)
	}
}

// OnConversationStart is called before conversation Load or Save.
func (b *bridgeLoggingHook) OnConversationStart(operation string, conversationID string) {
	if b.inner != nil {
		b.inner.OnConversationStart(operation, conversationID)
	}
}

// OnConversationEnd is called after conversation Load or Save with the outcome.
func (b *bridgeLoggingHook) OnConversationEnd(operation string, conversationID string, err error, messageCount int, duration time.Duration) {
	if b.inner != nil {
		b.inner.OnConversationEnd(operation, conversationID, err, messageCount, duration)
	}
}

// OnRetrieverStart is called before Retriever.Retrieve.
func (b *bridgeLoggingHook) OnRetrieverStart(query string) {
	if b.inner != nil {
		b.inner.OnRetrieverStart(query)
	}
}

// OnRetrieverEnd is called after Retriever.Retrieve with the outcome.
func (b *bridgeLoggingHook) OnRetrieverEnd(err error, docCount int, duration time.Duration) {
	if b.inner != nil {
		b.inner.OnRetrieverEnd(err, docCount, duration)
	}
}

// OnImagesAttached is called when images are attached to the invocation via WithImages.
func (b *bridgeLoggingHook) OnImagesAttached(imageCount int) {
	if b.inner != nil {
		b.inner.OnImagesAttached(imageCount)
	}
}

// OnDocumentsAttached is called when documents are attached to the invocation via WithDocuments.
func (b *bridgeLoggingHook) OnDocumentsAttached(docCount int) {
	if b.inner != nil {
		b.inner.OnDocumentsAttached(docCount)
	}
}

// OnMaxIterationsExceeded records the max-iterations-exceeded event.
func (b *bridgeLoggingHook) OnMaxIterationsExceeded(limit int) {
	if b.inner != nil {
		b.inner.OnMaxIterationsExceeded(limit)
	}
}

// OnStreamChunk is called for each text chunk during streaming.
// No-op for graph bridge — graph nodes don't stream directly.
func (b *bridgeLoggingHook) OnStreamChunk(text string) {
	if b.inner != nil {
		b.inner.OnStreamChunk(text)
	}
}

// OnResponse is called with the complete response text after a non-streaming Invoke.
// No-op for graph bridge — graph nodes don't stream directly.
func (b *bridgeLoggingHook) OnResponse(text string) {
	if b.inner != nil {
		b.inner.OnResponse(text)
	}
}

// newBridgeLoggingHook creates a bridgeLoggingHook that includes the node name as
// context in all log entries. Returns nil if graphHook is nil (zero overhead when no
// graph logging hook is configured).
// The inner parameter is the agent's own LoggingHook (may be nil).
func newBridgeLoggingHook(graphHook GraphLoggingHook, nodeName string, inner agent.LoggingHook) *bridgeLoggingHook {
	if graphHook == nil {
		return nil
	}
	return &bridgeLoggingHook{
		graphHook: graphHook,
		nodeName:  nodeName,
		inner:     inner,
	}
}
