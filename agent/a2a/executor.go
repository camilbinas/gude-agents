// Package a2a bridges gude-agents to the A2A protocol using the official
// a2a-go/v2 SDK. It provides an AgentExecutor implementation that translates
// between A2A messages and gude-agents invocations.
package a2a

import (
	"context"
	"encoding/json"
	"iter"
	"log/slog"
	"net/http"
	"strings"
	"sync"

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
	// verifyPrincipal is an optional hook called after extracting principal headers.
	// Use it to validate signatures, look up principals in a DB, or strip untrusted
	// fields. Return an error to reject the request with a failed task status.
	// When set, this takes precedence over trustForwardedPrincipal.
	verifyPrincipal func(agent.Principal) (agent.Principal, error)
	// trustForwardedPrincipal explicitly opts into trusting raw
	// X-Agent-Principal-* headers with no verification. It only has an effect
	// when verifyPrincipal is nil. This is unsafe unless the server sits
	// behind a trusted boundary (e.g. an internal service mesh) that strips
	// or authenticates these headers before they reach the agent — see
	// NewExecutorWithTrustedForwardedPrincipal.
	trustForwardedPrincipal bool
	// pending holds the interrupt each input-required task is paused on,
	// keyed by a2a.TaskID. It is process-local: a task paused in one process
	// cannot be resumed by another.
	pending sync.Map
}

// NewExecutor creates an Executor that delegates to the given agent.
// If logger is nil, slog.Default() is used.
//
// By default, forwarded X-Agent-Principal-* headers are ignored: a caller
// cannot set a Principal on the agent context by sending these headers
// alone. Use NewExecutorWithVerify to validate forwarded identity, or
// NewExecutorWithTrustedForwardedPrincipal to explicitly opt into trusting
// it unverified.
func NewExecutor(a *agent.Agent, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{agent: a, logger: logger}
}

// NewExecutorWithVerify creates an Executor with a principal verification hook.
// The hook is called after extracting X-Agent-Principal-* headers and before
// setting the principal on the agent context. It can validate a signature,
// look up the caller in an identity store, or strip untrusted fields.
// If verify returns an error the task fails immediately.
func NewExecutorWithVerify(a *agent.Agent, logger *slog.Logger, verify func(agent.Principal) (agent.Principal, error)) *Executor {
	e := NewExecutor(a, logger)
	e.verifyPrincipal = verify
	return e
}

// NewExecutorWithTrustedForwardedPrincipal creates an Executor that trusts
// raw X-Agent-Principal-* headers as-is, with no verification.
//
// This is unsafe on a public or default deployment: any caller that can set
// HTTP headers can assign itself arbitrary roles/attributes. Only use this
// when the server sits behind a trusted boundary that authenticates the
// caller and strips/rewrites these headers itself (e.g. an internal service
// mesh where the sidecar is the only thing allowed to set them). Prefer
// NewExecutorWithVerify when possible.
func NewExecutorWithTrustedForwardedPrincipal(a *agent.Agent, logger *slog.Logger) *Executor {
	e := NewExecutor(a, logger)
	e.trustForwardedPrincipal = true
	return e
}

// Execute implements a2asrv.AgentExecutor. It streams the underlying agent with
// the user message extracted from the A2A request and emits artifact events
// for each text chunk. When the invocation pauses on an agent.Interrupt the
// task moves to input-required; the next message on that task resumes it
// (see takePending for how replies map to ResumeResponses).
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

		// Propagate principal from inbound headers, subject to trust policy.
		//
		// Raw X-Agent-Principal-* headers are never trusted by default: a
		// caller could otherwise set X-Agent-Principal-Roles: admin and be
		// treated as an admin. The principal is only attached to the agent
		// context when either a verifier is configured (verifyPrincipal) or
		// the executor was explicitly constructed to trust forwarded
		// identity (trustForwardedPrincipal). Otherwise the headers are
		// parsed for observability but discarded.
		if p, ok := principalFromServiceParams(execCtx.ServiceParams); ok {
			switch {
			case e.verifyPrincipal != nil:
				verified, err := e.verifyPrincipal(p)
				if err != nil {
					msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("principal verification failed: "+err.Error()))
					if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, msg), nil) {
						return
					}
					return
				}
				agentCtx = agentCtx.WithPrincipal(verified)
			case e.trustForwardedPrincipal:
				agentCtx = agentCtx.WithPrincipal(p)
			default:
				// Untrusted and unverified: ignore the forwarded headers.
			}
		}

		if len(result.Images) > 0 {
			agentCtx = agentCtx.WithImages(result.Images)
		}
		if len(result.Documents) > 0 {
			agentCtx = agentCtx.WithDocuments(result.Documents)
		}

		// 5. Stream the agent response (a fresh turn, or a resume when the task
		// is paused on an interrupt), emitting artifact events for each chunk.
		var events iter.Seq2[agent.Event, error]
		if in, resp, ok := e.takePending(execCtx, result.Text); ok {
			events = e.agent.ResumeStream(agentCtx, in, resp)
		} else {
			events = e.agent.Stream(agentCtx, result.Text)
		}

		var artifactID a2a.ArtifactID
		var final *agent.Result
		for ev, err := range events {
			if err != nil {
				msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(err.Error()))
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, msg), nil)
				return
			}
			switch ev.Type {
			case agent.EventText:
				if ev.Text == nil || ev.Text.Content == "" {
					continue
				}
				parts := []*a2a.Part{a2a.NewTextPart(ev.Text.Content)}
				var event *a2a.TaskArtifactUpdateEvent
				if artifactID == "" {
					event = a2a.NewArtifactEvent(execCtx, parts...)
					artifactID = event.Artifact.ID
				} else {
					event = a2a.NewArtifactUpdateEvent(execCtx, artifactID, parts...)
				}
				// Breaking out of the range stops the agent stream cleanly.
				if !yield(event, nil) {
					return
				}
			case agent.EventEnd:
				final = ev.Result
			}
		}

		// Inbound media belongs only to the invocation context. Emitting it here
		// would echo user-provided attachments as response artifacts. Genuine
		// agent-generated media requires a dedicated response-output channel.

		if final == nil {
			msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("agent stream ended without a result"))
			if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, msg), nil) {
				return
			}
			return
		}

		// 6. A paused invocation maps to input-required; the next message on
		// the same task resumes it.
		if final.StopReason == agent.StopInterrupt && final.Interrupt != nil {
			e.pending.Store(execCtx.TaskID, final.Interrupt)
			msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(describeInterrupt(final.Interrupt)))
			if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateInputRequired, msg), nil) {
				return
			}
			return
		}

		// 7. Emit completed status.
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil)
	}
}

// takePending removes and returns the interrupt the task is paused on, with
// the ResumeResponse built from the caller's reply text:
//   - human_input interrupts are answered with agent.Respond(text).
//   - approval interrupts are approved only when the reply is exactly
//     "approve" (case-insensitive, surrounding whitespace ignored); any other
//     reply denies every pending call with the reply as the reason.
func (e *Executor) takePending(execCtx *a2asrv.ExecutorContext, reply string) (*agent.Interrupt, agent.ResumeResponse, bool) {
	if execCtx.StoredTask == nil {
		return nil, agent.ResumeResponse{}, false
	}
	v, ok := e.pending.LoadAndDelete(execCtx.TaskID)
	if !ok {
		return nil, agent.ResumeResponse{}, false
	}
	in := v.(*agent.Interrupt)
	if in.Type == agent.InterruptApproval {
		if strings.EqualFold(strings.TrimSpace(reply), ApproveReply) {
			return in, agent.Approve(), true
		}
		return in, agent.Deny(reply), true
	}
	return in, agent.Respond(reply), true
}

// ApproveReply is the reply text that approves a pending approval interrupt
// when an A2A task is in the input-required state. Any other reply denies.
const ApproveReply = "approve"

// describeInterrupt renders the input-required status message for an interrupt.
func describeInterrupt(in *agent.Interrupt) string {
	switch in.Type {
	case agent.InterruptHumanInput:
		if in.Input != nil {
			if in.Input.Reason != "" {
				return in.Input.Question + "\n\n(" + in.Input.Reason + ")"
			}
			return in.Input.Question
		}
	case agent.InterruptApproval:
		var sb strings.Builder
		sb.WriteString("Approval required for:")
		if in.Approval != nil {
			for _, c := range in.Approval.Calls {
				sb.WriteString("\n- ")
				sb.WriteString(c.Name)
				if len(c.Input) > 0 {
					sb.WriteByte(' ')
					sb.Write(c.Input)
				}
			}
		}
		sb.WriteString("\n\nReply \"" + ApproveReply + "\" to approve; any other reply denies.")
		return sb.String()
	}
	return "Input required."
}

// Cancel implements a2asrv.AgentExecutor. It discards any pending interrupt
// for the task and emits a canceled status event.
func (e *Executor) Cancel(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		e.pending.Delete(execCtx.TaskID)
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

// principalFromServiceParams extracts a principal from A2A ServiceParams headers.
func principalFromServiceParams(sp *a2asrv.ServiceParams) (agent.Principal, bool) {
	if sp == nil {
		return agent.Principal{}, false
	}
	ids, ok := sp.Get("X-Agent-Principal-ID")
	if !ok || len(ids) == 0 || ids[0] == "" {
		return agent.Principal{}, false
	}
	p := agent.Principal{ID: ids[0]}
	if rolesVals, ok := sp.Get("X-Agent-Principal-Roles"); ok && len(rolesVals) > 0 {
		for _, raw := range rolesVals {
			for _, role := range strings.Split(raw, ",") {
				if role = strings.TrimSpace(role); role != "" {
					p.Roles = append(p.Roles, role)
				}
			}
		}
	}
	if attrsVals, ok := sp.Get("X-Agent-Principal-Attrs"); ok && len(attrsVals) > 0 {
		var attrs map[string]string
		if err := json.Unmarshal([]byte(attrsVals[0]), &attrs); err == nil {
			p.Attrs = attrs
		}
	}
	return p, true
}

// PrincipalFromRequest extracts an agent.Principal from inbound HTTP request
// headers set by an A2A client that propagated identity. Returns false if the
// X-Agent-Principal-ID header is absent.
//
// This performs no verification: the returned Principal is only as
// trustworthy as the headers themselves. Callers building custom transports
// or middleware are responsible for verifying or stripping these headers at
// a trust boundary before treating the result as authorization state — the
// same policy enforced by Executor via WithPrincipalVerifier /
// WithTrustedForwardedPrincipal for the standard A2A server.
func PrincipalFromRequest(r *http.Request) (agent.Principal, bool) {
	id := r.Header.Get("X-Agent-Principal-ID")
	if id == "" {
		return agent.Principal{}, false
	}
	var roles []string
	if raw := r.Header.Get("X-Agent-Principal-Roles"); raw != "" {
		for _, role := range strings.Split(raw, ",") {
			if role = strings.TrimSpace(role); role != "" {
				roles = append(roles, role)
			}
		}
	}
	var attrs map[string]string
	if raw := r.Header.Get("X-Agent-Principal-Attrs"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &attrs)
	}
	return agent.Principal{ID: id, Roles: roles, Attrs: attrs}, true
}
