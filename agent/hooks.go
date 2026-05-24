package agent

import (
	"context"
	"encoding/json"
	"time"
)

// hooks is a composite that dispatches to tracing, metrics, and logging hooks.
// All methods are safe to call regardless of which hooks are configured —
// nil hooks are skipped internally. This eliminates the 3-way nil-check
// pattern that otherwise clutters the agent loop.
type hooks struct {
	tracing TracingHook
	metrics MetricsHook
	logging LoggingHook
	event   EventHook
}

// invokeFinisher is returned by onInvokeStart and called when the invocation ends.
type invokeFinisher struct {
	finishTracing func(error, TokenUsage, string)
	finishMetrics func(error, TokenUsage)
	logging       LoggingHook
	start         time.Time
}

func (f *invokeFinisher) finish(err error, usage TokenUsage) {
	if f.finishTracing != nil {
		f.finishTracing(err, usage, "")
	}
	if f.finishMetrics != nil {
		f.finishMetrics(err, usage)
	}
	if f.logging != nil {
		f.logging.OnInvokeEnd(err, usage, time.Since(f.start))
	}
}

func (h *hooks) onInvokeStart(c *Context, params InvokeSpanParams) (*Context, *invokeFinisher) {
	f := &invokeFinisher{start: time.Now()}
	ctx := context.Context(c)
	if h.tracing != nil {
		newCtx, finish := h.tracing.OnInvokeStart(ctx, params)
		f.finishTracing = finish
		if newCtx != ctx {
			c = c.withContext(newCtx)
		}
	}
	if h.metrics != nil {
		f.finishMetrics = h.metrics.OnInvokeStart()
	}
	if h.logging != nil {
		h.logging.OnInvokeStart(params)
		f.logging = h.logging
	}
	return c, f
}

// iterationFinisher is returned by onIterationStart.
type iterationFinisher struct {
	finishTracing func(toolCount int, isFinal bool)
	metrics       MetricsHook
	logging       LoggingHook
	event         EventHook
	c             *Context
	iteration     int
	start         time.Time
}

func (f *iterationFinisher) finish(toolCount int, isFinal bool) {
	if f.finishTracing != nil {
		f.finishTracing(toolCount, isFinal)
	}
	if f.metrics != nil {
		f.metrics.OnIterationEnd(toolCount, isFinal)
	}
	if f.logging != nil {
		f.logging.OnIterationEnd(f.iteration, toolCount, isFinal, time.Since(f.start))
	}
	if f.event != nil {
		f.event.OnIterationEnd(f.c, f.iteration, toolCount, isFinal, time.Since(f.start))
	}
}

func (h *hooks) onIterationStart(c *Context, iteration int) (*Context, *iterationFinisher) {
	f := &iterationFinisher{iteration: iteration, start: time.Now(), c: c}
	ctx := context.Context(c)
	if h.tracing != nil {
		newCtx, finish := h.tracing.OnIterationStart(ctx, iteration)
		f.finishTracing = finish
		if newCtx != ctx {
			c = c.withContext(newCtx)
			f.c = c
		}
	}
	if h.metrics != nil {
		h.metrics.OnIterationStart()
		f.metrics = h.metrics
	}
	if h.logging != nil {
		h.logging.OnIterationStart(iteration)
		f.logging = h.logging
	}
	if h.event != nil {
		h.event.OnIterationStart(c, iteration)
		f.event = h.event
		f.c = c
	}
	return c, f
}

// providerFinisher is returned by onProviderCallStart.
type providerFinisher struct {
	finishTracing func(err error, usage TokenUsage, toolCallCount int, responseText string)
	finishMetrics func(err error, usage TokenUsage)
	logging       LoggingHook
	event         EventHook
	c             *Context
	start         time.Time
}

func (f *providerFinisher) finish(err error, usage TokenUsage, toolCallCount int, responseText string) {
	if f.finishTracing != nil {
		f.finishTracing(err, usage, toolCallCount, responseText)
	}
	if f.finishMetrics != nil {
		f.finishMetrics(err, usage)
	}
	if f.logging != nil {
		f.logging.OnProviderCallEnd(err, usage, toolCallCount, time.Since(f.start))
	}
	if f.event != nil {
		f.event.OnModelEnd(f.c, deriveStopReason(toolCallCount, err))
	}
}

func (h *hooks) onProviderCallStart(c *Context, params ProviderCallParams, modelID string) (*Context, *providerFinisher) {
	f := &providerFinisher{start: time.Now(), c: c}
	ctx := context.Context(c)
	if h.tracing != nil {
		newCtx, finish := h.tracing.OnProviderCallStart(ctx, params)
		f.finishTracing = finish
		if newCtx != ctx {
			c = c.withContext(newCtx)
			f.c = c
		}
	}
	if h.metrics != nil {
		f.finishMetrics = h.metrics.OnProviderCallStart(modelID)
	}
	if h.logging != nil {
		h.logging.OnProviderCallStart(modelID)
		f.logging = h.logging
	}
	if h.event != nil {
		h.event.OnModelStart(c)
		f.event = h.event
		f.c = c
	}
	return c, f
}

// toolFinisher is returned by onToolStart.
type toolFinisher struct {
	finishTracing func(err error, output string)
	finishMetrics func(err error)
	logging       LoggingHook
	event         EventHook
	c             *Context
	toolName      string
	start         time.Time
}

func (f *toolFinisher) finish(err error, output string) {
	if f.finishTracing != nil {
		f.finishTracing(err, output)
	}
	if f.finishMetrics != nil {
		f.finishMetrics(err)
	}
	if f.logging != nil {
		f.logging.OnToolEnd(f.toolName, err, time.Since(f.start))
	}
	if f.event != nil {
		f.event.OnToolCallEnd(f.c, f.toolName, output, err, time.Since(f.start))
	}
}

func (h *hooks) onToolStart(c *Context, toolName string, input json.RawMessage) (*Context, *toolFinisher) {
	f := &toolFinisher{toolName: toolName, start: time.Now(), c: c}
	ctx := context.Context(c)
	if h.tracing != nil {
		newCtx, finish := h.tracing.OnToolStart(ctx, toolName, input)
		f.finishTracing = finish
		if newCtx != ctx {
			c = c.withContext(newCtx)
			f.c = c
		}
	}
	if h.metrics != nil {
		f.finishMetrics = h.metrics.OnToolStart(toolName)
	}
	if h.logging != nil {
		h.logging.OnToolStart(toolName)
		f.logging = h.logging

		// Inject a ToolLogger into the context so tools can emit log messages.
		c = c.withContext(withToolLogger(c.Context, &hookToolLogger{
			hook:     h.logging,
			toolName: toolName,
		}))
		f.c = c
	}
	if h.event != nil {
		h.event.OnToolCallStart(c, toolName, input)
		f.event = h.event
		f.c = c
	}
	return c, f
}

// guardrailFinisher is returned by onGuardrailStart.
type guardrailFinisher struct {
	finishTracing func(err error, output string)
	metrics       MetricsHook
	logging       LoggingHook
	direction     string
}

func (f *guardrailFinisher) finish(err error, output string) {
	if f.finishTracing != nil {
		f.finishTracing(err, output)
	}
	if f.metrics != nil {
		f.metrics.OnGuardrailComplete(f.direction, err != nil)
	}
	if f.logging != nil {
		f.logging.OnGuardrailComplete(f.direction, err != nil, err)
	}
}

func (h *hooks) onGuardrailStart(c *Context, direction string, input string) (*Context, *guardrailFinisher) {
	f := &guardrailFinisher{direction: direction, metrics: h.metrics, logging: h.logging}
	ctx := context.Context(c)
	if h.tracing != nil {
		newCtx, finish := h.tracing.OnGuardrailStart(ctx, direction, input)
		f.finishTracing = finish
		if newCtx != ctx {
			c = c.withContext(newCtx)
		}
	}
	return c, f
}

// conversationFinisher is returned by onConversationStart.
type conversationFinisher struct {
	finishTracing func(err error)
	logging       LoggingHook
	operation     string
	convID        string
	start         time.Time
}

func (f *conversationFinisher) finish(err error, messageCount int) {
	if f.finishTracing != nil {
		f.finishTracing(err)
	}
	if f.logging != nil {
		f.logging.OnConversationEnd(f.operation, f.convID, err, messageCount, time.Since(f.start))
	}
}

func (h *hooks) onConversationStart(c *Context, operation string, convID string) (*Context, *conversationFinisher) {
	f := &conversationFinisher{operation: operation, convID: convID, logging: h.logging, start: time.Now()}
	ctx := context.Context(c)
	if h.tracing != nil {
		newCtx, finish := h.tracing.OnConversationStart(ctx, operation, convID)
		f.finishTracing = finish
		if newCtx != ctx {
			c = c.withContext(newCtx)
		}
	}
	if h.logging != nil {
		h.logging.OnConversationStart(operation, convID)
	}
	return c, f
}

// retrieverFinisher is returned by onRetrieverStart.
type retrieverFinisher struct {
	finishTracing func(err error, docCount int)
	logging       LoggingHook
	start         time.Time
}

func (f *retrieverFinisher) finish(err error, docCount int) {
	if f.finishTracing != nil {
		f.finishTracing(err, docCount)
	}
	if f.logging != nil {
		f.logging.OnRetrieverEnd(err, docCount, time.Since(f.start))
	}
}

func (h *hooks) onRetrieverStart(c *Context, query string) (*Context, *retrieverFinisher) {
	f := &retrieverFinisher{logging: h.logging, start: time.Now()}
	ctx := context.Context(c)
	if h.tracing != nil {
		newCtx, finish := h.tracing.OnRetrieverStart(ctx, query)
		f.finishTracing = finish
		if newCtx != ctx {
			c = c.withContext(newCtx)
		}
	}
	if h.logging != nil {
		h.logging.OnRetrieverStart(query)
	}
	return c, f
}

func (h *hooks) onImagesAttached(count int) {
	if h.metrics != nil {
		h.metrics.OnImagesAttached(count)
	}
	if h.logging != nil {
		h.logging.OnImagesAttached(count)
	}
}

func (h *hooks) onDocumentsAttached(count int) {
	if h.metrics != nil {
		h.metrics.OnDocumentsAttached(count)
	}
	if h.logging != nil {
		h.logging.OnDocumentsAttached(count)
	}
}

func (h *hooks) onMaxIterationsExceeded(c *Context, limit int) {
	if h.tracing != nil {
		h.tracing.OnMaxIterationsExceeded(c, limit)
	}
	if h.logging != nil {
		h.logging.OnMaxIterationsExceeded(limit)
	}
	if h.event != nil {
		h.event.OnMaxIterationsExceeded(c, limit)
	}
}

func (h *hooks) onStreamChunk(text string) {
	if h.logging != nil {
		h.logging.OnStreamChunk(text)
	}
}

func (h *hooks) onResponse(text string) {
	if h.logging != nil {
		h.logging.OnResponse(text)
	}
}

// deriveStopReason returns the stop reason string for OnModelEnd based on the
// provider call outcome. If err is non-nil, it returns "error". If tool calls
// were present (toolCallCount > 0), it returns "tool_use". Otherwise "end_turn".
func deriveStopReason(toolCallCount int, err error) string {
	if err != nil {
		return StopReasonError
	}
	if toolCallCount > 0 {
		return StopReasonToolUse
	}
	return StopReasonEndTurn
}
