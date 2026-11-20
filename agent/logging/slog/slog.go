package slog

import (
	"context"
	"log/slog"

	"github.com/camilbinas/gude-agents/agent"
)

// slogHook implements the agent lifecycle hooks using the
// standard library's log/slog package.
type slogHook struct {
	logger   *slog.Logger
	minLevel slog.Level
	// content enables logging of user and model content (final response
	// text, retrieval queries). Off by default; see WithContentLogging.
	content bool
}

// log emits a structured log entry if the level meets the minimum threshold.
func (h *slogHook) log(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	if level < h.minLevel {
		return
	}
	h.logger.LogAttrs(ctx, level, msg, attrs...)
}

// ---------------------------------------------------------------------------
// Hook methods — agent lifecycle
// ---------------------------------------------------------------------------

// hooks returns the lifecycle hooks this adapter implements.
func (h *slogHook) hooks() agent.Hooks {
	return agent.Hooks{
		Invoke:       h.onInvoke,
		Iteration:    h.onIteration,
		Model:        h.onModel,
		Tool:         h.onTool,
		ToolLog:      h.onToolLog,
		Guardrail:    h.onGuardrail,
		Conversation: h.onConversation,
		Retrieval:    h.onRetrieval,
		Attachment:   h.onAttachment,
		Limit:        h.onLimit,
	}
}

func (h *slogHook) onInvoke(ctx context.Context, record agent.InvokeRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		h.log(ctx, slog.LevelDebug, "invoke.start",
			slog.String("agent.name", record.AgentName),
			slog.String("model.id", record.ModelID),
			slog.String("conversation_id", record.ConversationID),
			slog.Int("max_iterations", record.MaxIterations),
		)
	case agent.End:
		level := slog.LevelInfo
		attrs := []slog.Attr{
			slog.Float64("duration_ms", float64(record.Duration.Milliseconds())),
			slog.Int("input_tokens", record.Usage.InputTokens),
			slog.Int("output_tokens", record.Usage.OutputTokens),
		}
		if record.Usage.CacheReadTokens > 0 {
			attrs = append(attrs, slog.Int("cache_read_tokens", record.Usage.CacheReadTokens))
		}
		if record.Usage.CacheWriteTokens > 0 {
			attrs = append(attrs, slog.Int("cache_write_tokens", record.Usage.CacheWriteTokens))
		}
		if record.Err != nil {
			level = slog.LevelError
			attrs = append(attrs, slog.String("error", record.Err.Error()))
		}
		h.log(ctx, level, "invoke.end", attrs...)
		if h.content && record.Err == nil && record.Response != "" {
			h.log(ctx, slog.LevelInfo, "response.text", slog.String("text", record.Response))
		}
	}
	return ctx
}

func (h *slogHook) onIteration(ctx context.Context, record agent.IterationRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		h.log(ctx, slog.LevelDebug, "iteration.start",
			slog.Int("iteration", record.Iteration),
		)
	case agent.End:
		h.log(ctx, slog.LevelDebug, "iteration.end",
			slog.Int("iteration", record.Iteration),
			slog.Int("tool_count", record.ToolCount),
			slog.Bool("is_final", record.IsFinal),
			slog.Float64("duration_ms", float64(record.Duration.Milliseconds())),
		)
	}
	return ctx
}

func (h *slogHook) onModel(ctx context.Context, record agent.ModelCallRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		h.log(ctx, slog.LevelDebug, "provider_call.start",
			slog.String("model.id", record.ModelID),
		)
	case agent.End:
		level := slog.LevelInfo
		attrs := []slog.Attr{
			slog.Float64("duration_ms", float64(record.Duration.Milliseconds())),
			slog.Int("input_tokens", record.Usage.InputTokens),
			slog.Int("output_tokens", record.Usage.OutputTokens),
			slog.Int("tool_call_count", record.ToolCallCount),
		}
		if record.Usage.CacheReadTokens > 0 {
			attrs = append(attrs, slog.Int("cache_read_tokens", record.Usage.CacheReadTokens))
		}
		if record.Usage.CacheWriteTokens > 0 {
			attrs = append(attrs, slog.Int("cache_write_tokens", record.Usage.CacheWriteTokens))
		}
		if record.Err != nil {
			level = slog.LevelError
			attrs = append(attrs, slog.String("error", record.Err.Error()))
		}
		h.log(ctx, level, "provider_call.end", attrs...)
	}
	return ctx
}

func (h *slogHook) onTool(ctx context.Context, record agent.ToolCallRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		h.log(ctx, slog.LevelDebug, "tool.start",
			slog.String("tool.name", record.Name),
		)
	case agent.End:
		level := slog.LevelInfo
		attrs := []slog.Attr{
			slog.String("tool.name", record.Name),
			slog.Float64("duration_ms", float64(record.Duration.Milliseconds())),
		}
		if record.Err != nil {
			level = slog.LevelError
			attrs = append(attrs, slog.String("error", record.Err.Error()))
		}
		h.log(ctx, level, "tool.end", attrs...)
	}
	return ctx
}

func (h *slogHook) onToolLog(ctx context.Context, record agent.ToolLogRecord) context.Context {
	h.log(ctx, slog.LevelDebug, "tool.log",
		slog.String("tool.name", record.Name),
		slog.String("message", record.Message),
	)
	return ctx
}

func (h *slogHook) onGuardrail(ctx context.Context, record agent.GuardrailRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	level := slog.LevelDebug
	if record.Blocked {
		level = slog.LevelWarn
	}
	attrs := []slog.Attr{
		slog.String("direction", record.Direction),
		slog.Bool("blocked", record.Blocked),
	}
	if record.Err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.String("error", record.Err.Error()))
	}
	h.log(ctx, level, "guardrail.complete", attrs...)
	return ctx
}

func (h *slogHook) onConversation(ctx context.Context, record agent.ConversationRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		h.log(ctx, slog.LevelDebug, "memory.start",
			slog.String("operation", record.Operation),
			slog.String("conversation_id", record.ConversationID),
		)
	case agent.End:
		level := slog.LevelInfo
		attrs := []slog.Attr{
			slog.String("operation", record.Operation),
			slog.String("conversation_id", record.ConversationID),
			slog.Int("message_count", record.MessageCount),
			slog.Float64("duration_ms", float64(record.Duration.Milliseconds())),
		}
		if record.Err != nil {
			level = slog.LevelError
			attrs = append(attrs, slog.String("error", record.Err.Error()))
		}
		h.log(ctx, level, "memory.end", attrs...)
	}
	return ctx
}

func (h *slogHook) onRetrieval(ctx context.Context, record agent.RetrievalRecord) context.Context {
	switch record.Phase {
	case agent.Start:
		var attrs []slog.Attr
		if h.content {
			attrs = append(attrs, slog.String("query", record.Query))
		}
		h.log(ctx, slog.LevelDebug, "retriever.start", attrs...)
	case agent.End:
		level := slog.LevelInfo
		attrs := []slog.Attr{
			slog.Int("doc_count", record.DocumentCount),
			slog.Float64("duration_ms", float64(record.Duration.Milliseconds())),
		}
		if record.Err != nil {
			level = slog.LevelError
			attrs = append(attrs, slog.String("error", record.Err.Error()))
		}
		h.log(ctx, level, "retriever.end", attrs...)
	}
	return ctx
}

func (h *slogHook) onAttachment(ctx context.Context, record agent.AttachmentRecord) context.Context {
	if record.Phase != agent.End {
		return ctx
	}
	if record.ImageCount > 0 {
		h.log(ctx, slog.LevelDebug, "images.attached",
			slog.Int("image_count", record.ImageCount),
		)
	}
	if record.DocumentCount > 0 {
		h.log(ctx, slog.LevelDebug, "documents.attached",
			slog.Int("document_count", record.DocumentCount),
		)
	}
	return ctx
}

func (h *slogHook) onLimit(ctx context.Context, record agent.LimitRecord) context.Context {
	if record.Phase == agent.End && record.Name == "max_iterations" {
		h.log(ctx, slog.LevelWarn, "max_iterations_exceeded",
			slog.Int("limit", record.Limit),
		)
	}
	return ctx
}

// ---------------------------------------------------------------------------
// Option functions — wire the hooks into an agent
// ---------------------------------------------------------------------------

// newSlogHook creates a slogHook with defaults and applies the given options.
func newSlogHook(opts []Option) *slogHook {
	h := &slogHook{
		logger:   slog.Default(),
		minLevel: slog.LevelDebug,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// WithLogging returns an agent.Option that installs the slog-based hooks.
func WithLogging(opts ...Option) agent.Option {
	return agent.WithHooks(newSlogHook(opts).hooks())
}
