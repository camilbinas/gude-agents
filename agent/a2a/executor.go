// Package a2a bridges gude-agents to the A2A protocol using the official
// a2a-go/v2 SDK. It provides an AgentExecutor implementation that translates
// between A2A messages and gude-agents invocations.
package a2a

import (
	"context"
	"iter"
	"log/slog"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/camilbinas/gude-agents/agent"
)

// Executor bridges a gude-agents *agent.Agent to the a2asrv.AgentExecutor interface.
// It translates incoming A2A messages into agent invocations and emits A2A events
// from the streaming response.
type Executor struct {
	agent  *agent.Agent
	logger *slog.Logger
}

// NewExecutor creates an Executor that delegates to the given agent.
// If logger is nil, slog.Default() is used.
func NewExecutor(a *agent.Agent, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{agent: a, logger: logger}
}

// Execute implements a2asrv.AgentExecutor. It invokes the underlying agent with
// the user message extracted from the A2A request and streams artifact events
// back through the iterator.
func (e *Executor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		// 1. If this is a new task (no stored task), emit a submitted task event.
		if execCtx.StoredTask == nil {
			task := a2a.NewSubmittedTask(execCtx, execCtx.Message)
			if !yield(task, nil) {
				return
			}
		}

		// 2. Emit working status.
		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
			return
		}

		// 3. Convert inbound message (replaces extractText).
		result := ConvertInbound(execCtx.Message, e.logger)

		// 4. Prepare agent context with conversation ID and multimodal content.
		agentCtx := agent.NewContext(ctx).WithConversationID(string(execCtx.TaskID))
		if len(result.Images) > 0 {
			agentCtx = agentCtx.WithImages(result.Images)
		}
		if len(result.Documents) > 0 {
			agentCtx = agentCtx.WithDocuments(result.Documents)
		}

		// Wire metrics hook from the agent if available.
		if mh := e.agent.MetricsHook(); mh != nil {
			agentCtx = agentCtx.WithMetricsHook(mh)
		}

		// 5. Stream the agent response, emitting artifact events for each chunk.
		var artifactID a2a.ArtifactID
		var stopped bool

		err := e.agent.InvokeStream(agentCtx, result.Text, func(chunk string) {
			if stopped {
				return
			}
			parts := []*a2a.Part{a2a.NewTextPart(chunk)}
			var event *a2a.TaskArtifactUpdateEvent
			if artifactID == "" {
				event = a2a.NewArtifactEvent(execCtx, parts...)
				artifactID = event.Artifact.ID
			} else {
				event = a2a.NewArtifactUpdateEvent(execCtx, artifactID, parts...)
			}
			if !yield(event, nil) {
				stopped = true
			}
		})

		if stopped {
			return
		}

		if err != nil {
			msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(err.Error()))
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, msg), nil)
			return
		}

		// 6. Emit multimodal artifacts from the agent context.
		// After streaming completes, check for ImageBlock/DocumentBlock content
		// and emit them as separate artifact events using outbound converters.
		var multimodalParts []*a2a.Part
		for _, img := range agentCtx.Images() {
			if p := ConvertOutboundImage(img); p != nil {
				multimodalParts = append(multimodalParts, p)
			}
		}
		for _, doc := range agentCtx.Documents() {
			if p := ConvertOutboundDocument(doc); p != nil {
				multimodalParts = append(multimodalParts, p)
			}
		}
		if len(multimodalParts) > 0 {
			event := a2a.NewArtifactEvent(execCtx, multimodalParts...)
			if !yield(event, nil) {
				return
			}
		}

		// 7. Emit completed status.
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil)
	}
}

// Cancel implements a2asrv.AgentExecutor. It emits a canceled status event.
func (e *Executor) Cancel(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil)
	}
}

// extractText concatenates all text parts from an A2A message.
func extractText(msg *a2a.Message) string {
	if msg == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range msg.Parts {
		if t := part.Text(); t != "" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(t)
		}
	}
	return sb.String()
}
