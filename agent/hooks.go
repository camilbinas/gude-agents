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

func (h *hooks) onInvokeStart(ctx context.Context, params InvokeSpanParams) (context.Context, *invokeFinisher) {
	f := &invokeFinisher{start: time.Now()}
	if h.tracing != nil {
		ctx, f.finishTracing = h.tracing.OnInvokeStart(ctx, params)
	}
	if h.metrics != nil {
		f.finishMetrics = h.metrics.OnInvokeStart()
	}
	if h.logging != nil {
		h.logging.OnInvokeStart(params)
		f.logging = h.logging
	}
	return ctx, f
}

// iterationFinisher is returned by onIterationStart.
type iterationFinisher struct {
	finishTracing func(toolCount int, isFinal bool)
}

func (f *iterationFinisher) finish(toolCount int, isFinal bool) {
	if f.finishTracing != nil {
		f.finishTracing(toolCount, isFinal)
	}
}

func (h *hooks) onIterationStart(ctx context.Context, iteration int) (context.Context, *iterationFinisher) {
	f := &iterationFinisher{}
	if h.tracing != nil {
		ctx, f.finishTracing = h.tracing.OnIterationStart(ctx, iteration)
	}
	if h.metrics != nil {
		h.metrics.OnIterationStart()
	}
	if h.logging != nil {
		h.logging.OnIterationStart(iteration)
	}
	return ctx, f
}

// providerFinisher is returned by onProviderCallStart.
type providerFinisher struct {
	finishTracing func(err error, usage TokenUsage, toolCallCount int, responseText string)
	finishMetrics func(err error, usage TokenUsage)
	logging       LoggingHook
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
}

func (h *hooks) onProviderCallStart(ctx context.Context, params ProviderCallParams, modelID string) (context.Context, *providerFinisher) {
	f := &providerFinisher{start: time.Now()}
	if h.tracing != nil {
		ctx, f.finishTracing = h.tracing.OnProviderCallStart(ctx, params)
	}
	if h.metrics != nil {
		f.finishMetrics = h.metrics.OnProviderCallStart(modelID)
	}
	if h.logging != nil {
		h.logging.OnProviderCallStart(modelID)
		f.logging = h.logging
	}
	return ctx, f
}

// toolFinisher is returned by onToolStart.
type toolFinisher struct {
	finishTracing func(err error, output string)
	finishMetrics func(err error)
	logging       LoggingHook
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
}

func (h *hooks) onToolStart(ctx context.Context, toolName string, input json.RawMessage) (context.Context, *toolFinisher) {
	f := &toolFinisher{toolName: toolName, start: time.Now()}
	if h.tracing != nil {
		ctx, f.finishTracing = h.tracing.OnToolStart(ctx, toolName, input)
	}
	if h.metrics != nil {
		f.finishMetrics = h.metrics.OnToolStart(toolName)
	}
	if h.logging != nil {
		h.logging.OnToolStart(toolName)
		f.logging = h.logging
	}
	return ctx, f
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

func (h *hooks) onGuardrailStart(ctx context.Context, direction string, input string) (context.Context, *guardrailFinisher) {
	f := &guardrailFinisher{direction: direction, metrics: h.metrics, logging: h.logging}
	if h.tracing != nil {
		ctx, f.finishTracing = h.tracing.OnGuardrailStart(ctx, direction, input)
	}
	return ctx, f
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

func (h *hooks) onConversationStart(ctx context.Context, operation string, convID string) (context.Context, *conversationFinisher) {
	f := &conversationFinisher{operation: operation, convID: convID, logging: h.logging, start: time.Now()}
	if h.tracing != nil {
		ctx, f.finishTracing = h.tracing.OnConversationStart(ctx, operation, convID)
	}
	if h.logging != nil {
		h.logging.OnConversationStart(operation, convID)
	}
	return ctx, f
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

func (h *hooks) onRetrieverStart(ctx context.Context, query string) (context.Context, *retrieverFinisher) {
	f := &retrieverFinisher{logging: h.logging, start: time.Now()}
	if h.tracing != nil {
		ctx, f.finishTracing = h.tracing.OnRetrieverStart(ctx, query)
	}
	if h.logging != nil {
		h.logging.OnRetrieverStart(query)
	}
	return ctx, f
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

func (h *hooks) onMaxIterationsExceeded(ctx context.Context, limit int) {
	if h.tracing != nil {
		h.tracing.OnMaxIterationsExceeded(ctx, limit)
	}
	if h.logging != nil {
		h.logging.OnMaxIterationsExceeded(limit)
	}
}
