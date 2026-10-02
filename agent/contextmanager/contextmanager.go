// Package contextmanager provides model-context projections for append-only
// conversations. Strategies never mutate canonical history.
package contextmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
)

// Manager is the public alias for agent.ContextManager.
type Manager = agent.ContextManager

// Passthrough sends recent canonical and current messages unchanged.
type Passthrough struct{}

func (Passthrough) HistoryBoundary(context.Context, string) (uint64, error) { return 0, nil }
func (Passthrough) Prepare(_ context.Context, in agent.ContextManagerInput) (agent.ContextManagerOutput, error) {
	messages := append(append([]agent.Message(nil), in.Recent...), in.Transient...)
	messages = append(messages, in.Current...)
	return agent.ContextManagerOutput{Messages: messages}, nil
}

// Window preserves a recent verbatim suffix. It uses metadata-only LoadAfter
// first, then range reads only enough additional history to avoid splitting a
// ToolUse/ToolResult relationship.
type Window struct {
	store agent.ConversationStore
	count uint64
}

func NewWindow(store agent.ConversationStore, messages int) (*Window, error) {
	if store == nil {
		return nil, errors.New("contextmanager: store is required")
	}
	if messages < 1 {
		return nil, fmt.Errorf("contextmanager: window messages must be >= 1, got %d", messages)
	}
	return &Window{store: store, count: uint64(messages)}, nil
}
func (w *Window) HistoryBoundary(ctx context.Context, id string) (uint64, error) {
	meta, err := w.store.LoadAfter(ctx, id, math.MaxUint64)
	if err != nil {
		return 0, err
	}
	if meta.LastSequence <= w.count {
		return 0, nil
	}
	start := meta.LastSequence - w.count
	for {
		tail, err := w.store.LoadAfter(ctx, id, start)
		if err != nil {
			return 0, err
		}
		if !hasOrphanedToolResult(tail.Messages) || start == 0 {
			return start, nil
		}
		start--
	}
}
func (w *Window) Prepare(_ context.Context, in agent.ContextManagerInput) (agent.ContextManagerOutput, error) {
	return agent.ContextManagerOutput{Messages: append(append([]agent.Message(nil), in.Recent...), in.Current...)}, nil
}

// Filter is a model-only text projection. It never changes the canonical
// messages that the store receives.
type Filter struct{ inner agent.ContextManager }

func NewFilter(inner agent.ContextManager) *Filter {
	if inner == nil {
		inner = Passthrough{}
	}
	return &Filter{inner: inner}
}
func (f *Filter) HistoryBoundary(ctx context.Context, id string) (uint64, error) {
	return f.inner.HistoryBoundary(ctx, id)
}
func (f *Filter) Prepare(ctx context.Context, in agent.ContextManagerInput) (agent.ContextManagerOutput, error) {
	out, err := f.inner.Prepare(ctx, in)
	if err != nil {
		return agent.ContextManagerOutput{}, err
	}
	filtered := make([]agent.Message, 0, len(out.Messages))
	for _, msg := range out.Messages {
		blocks := make([]agent.ContentBlock, 0, len(msg.Content))
		for _, block := range msg.Content {
			if text, ok := block.(agent.TextBlock); ok {
				blocks = append(blocks, text)
			}
		}
		if len(blocks) > 0 {
			filtered = append(filtered, agent.Message{Role: msg.Role, Content: blocks})
		}
	}
	out.Messages = filtered
	return out, nil
}

func hasOrphanedToolResult(messages []agent.Message) bool {
	uses := map[string]bool{}
	for _, m := range messages {
		for _, b := range m.Content {
			switch x := b.(type) {
			case agent.ToolUseBlock:
				uses[x.ToolUseID] = true
			case agent.ToolResultBlock:
				if !uses[x.ToolUseID] {
					return true
				}
			}
		}
	}
	return false
}

// Summarizer produces text for a durable rolling summary. Existing contains
// the earlier summary text (possibly empty); messages are only newly covered
// canonical messages. Summarizers must not retain or persist input messages.
type Summarizer func(ctx context.Context, existing string, messages []agent.Message) (string, error)

type rollingState struct {
	Version        int    `json:"version"`
	Summary        string `json:"summary"`
	CoveredThrough uint64 `json:"covered_through"`
	SourceRevision uint64 `json:"source_revision"`
}

// RollingSummary keeps durable derived state separate from canonical history.
// It summarizes only persisted messages, never Current, and always leaves a
// recent verbatim window for the model.
type RollingSummary struct {
	store      agent.ConversationStore
	state      agent.ContextStateStore
	estimator  agent.TokenEstimator
	summarizer Summarizer
	maxInput   int
	preserve   uint64
	key        string
}
type RollingOption func(*RollingSummary) error

func WithStateStore(s agent.ContextStateStore) RollingOption {
	return func(r *RollingSummary) error {
		if s == nil {
			return errors.New("contextmanager: state store is required")
		}
		r.state = s
		return nil
	}
}
func WithTokenEstimator(e agent.TokenEstimator) RollingOption {
	return func(r *RollingSummary) error {
		if e == nil {
			return errors.New("contextmanager: token estimator is required")
		}
		r.estimator = e
		return nil
	}
}
func WithMaxInputTokens(n int) RollingOption {
	return func(r *RollingSummary) error {
		if n < 1 {
			return fmt.Errorf("contextmanager: max input tokens must be >= 1, got %d", n)
		}
		r.maxInput = n
		return nil
	}
}
func WithPreserveRecentTurns(n int) RollingOption {
	return func(r *RollingSummary) error {
		if n < 0 {
			return fmt.Errorf("contextmanager: preserve recent turns must be >= 0, got %d", n)
		}
		r.preserve = uint64(n * 2)
		return nil
	}
}
func WithStateKey(key string) RollingOption {
	return func(r *RollingSummary) error {
		if key == "" {
			return errors.New("contextmanager: state key is required")
		}
		r.key = key
		return nil
	}
}
func NewRollingSummary(store agent.ConversationStore, summarizer Summarizer, opts ...RollingOption) (*RollingSummary, error) {
	if store == nil {
		return nil, errors.New("contextmanager: store is required")
	}
	if summarizer == nil {
		return nil, errors.New("contextmanager: summarizer is required")
	}
	r := &RollingSummary{store: store, summarizer: summarizer, estimator: agent.CharEstimator{}, maxInput: 100000, preserve: 20, key: "rolling_summary"}
	for _, o := range opts {
		if err := o(r); err != nil {
			return nil, err
		}
	}
	if r.state == nil {
		if s, ok := store.(agent.ContextStateStore); ok {
			r.state = s
		} else {
			return nil, errors.New("contextmanager: store does not support ContextStateStore; use WithStateStore")
		}
	}
	return r, nil
}
func (r *RollingSummary) load(ctx context.Context, id string) (rollingState, uint64, error) {
	snapshot, err := r.state.LoadContextState(ctx, id, r.key)
	if err != nil {
		return rollingState{}, 0, err
	}
	if len(snapshot.Data) == 0 {
		return rollingState{Version: 1}, snapshot.Revision, nil
	}
	var state rollingState
	if err := json.Unmarshal(snapshot.Data, &state); err != nil {
		return rollingState{}, 0, fmt.Errorf("contextmanager: decode rolling state: %w", err)
	}
	return state, snapshot.Revision, nil
}
func (r *RollingSummary) HistoryBoundary(ctx context.Context, id string) (uint64, error) {
	state, _, err := r.load(ctx, id)
	return state.CoveredThrough, err
}
func summaryMessage(text string) agent.Message {
	return agent.Message{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "[Automated summary of earlier messages in this conversation — not a new user message]\n\n" + text}}}
}
func (r *RollingSummary) Prepare(ctx context.Context, in agent.ContextManagerInput) (agent.ContextManagerOutput, error) {
	state, stateRev, err := r.load(ctx, in.ConversationID)
	if err != nil {
		return agent.ContextManagerOutput{}, err
	}
	// The engine may call Prepare repeatedly during a tool loop while its
	// initial range is still based on the previous boundary. Do not render
	// messages the freshly loaded state already covers.
	if state.CoveredThrough > in.Boundary {
		skip := state.CoveredThrough - in.Boundary
		if skip > uint64(len(in.Recent)) {
			return agent.ContextManagerOutput{}, fmt.Errorf("contextmanager: state covers sequence %d beyond loaded recent history", state.CoveredThrough)
		}
		in.Recent = in.Recent[skip:]
		in.Boundary = state.CoveredThrough
	}
	build := func(s rollingState) []agent.Message {
		out := make([]agent.Message, 0, len(in.Recent)+len(in.Current)+len(in.Transient)+1)
		if s.Summary != "" {
			out = append(out, summaryMessage(s.Summary))
		}
		out = append(out, in.Recent...)
		out = append(out, in.Transient...)
		return append(out, in.Current...)
	}
	messages := build(state)
	estimate, err := r.estimator.EstimateTokens(ctx, agent.ModelRequest{System: in.System, Tools: in.Tools, Messages: messages})
	if err != nil {
		return agent.ContextManagerOutput{}, fmt.Errorf("contextmanager: estimate tokens: %w", err)
	}
	if estimate <= r.maxInput {
		return agent.ContextManagerOutput{Messages: messages}, nil
	}
	if uint64(len(in.Recent)) <= r.preserve {
		return agent.ContextManagerOutput{Messages: messages}, nil
	}
	// Only summarize persisted Recent. SafeBoundary stops before a tail that
	// might contain unresolved tool work; Current never enters durable state.
	cut := len(in.Recent) - int(r.preserve)
	for cut > 0 && hasUnresolved(in.Recent[:cut]) {
		cut--
	}
	if cut == 0 {
		return agent.ContextManagerOutput{Messages: messages}, nil
	}
	newSummary, err := r.summarizer(ctx, state.Summary, in.Recent[:cut])
	if err != nil {
		return agent.ContextManagerOutput{}, fmt.Errorf("contextmanager: summarize: %w", err)
	}
	state.Version = 1
	state.Summary = newSummary
	state.CoveredThrough = in.Boundary + uint64(cut)
	state.SourceRevision = in.Revision
	encoded, err := json.Marshal(state)
	if err != nil {
		return agent.ContextManagerOutput{}, err
	}
	if _, err := r.state.SaveContextState(ctx, in.ConversationID, r.key, encoded, stateRev); err != nil {
		return agent.ContextManagerOutput{}, fmt.Errorf("contextmanager: save summary state: %w", err)
	}
	stateRev++
	// The active range already begins at the old boundary. Drop newly covered
	// entries locally without loading older history.
	in.Recent = in.Recent[cut:]
	in.Boundary = state.CoveredThrough
	_ = stateRev
	return agent.ContextManagerOutput{Messages: build(state)}, nil
}
func hasUnresolved(messages []agent.Message) bool {
	pending := map[string]int{}
	for _, m := range messages {
		for _, b := range m.Content {
			switch x := b.(type) {
			case agent.ToolUseBlock:
				pending[x.ToolUseID]++
			case agent.ToolResultBlock:
				if pending[x.ToolUseID] > 0 {
					pending[x.ToolUseID]--
					if pending[x.ToolUseID] == 0 {
						delete(pending, x.ToolUseID)
					}
				}
			}
		}
	}
	return len(pending) > 0
}

// DefaultSummaryPrompt is the batteries-included system prompt used by
// DefaultProviderSummarizer.
const DefaultSummaryPrompt = "Summarize the following conversation into a single concise paragraph. " +
	"Preserve all key facts, names, and decisions."

// DefaultProviderSummarizer returns a ProviderSummarizer with
// DefaultSummaryPrompt, so callers don't have to supply a prompt.
func DefaultProviderSummarizer(provider agent.Provider) Summarizer {
	return ProviderSummarizer(provider, DefaultSummaryPrompt)
}

// renderBlock renders one content block as a single text line for the
// summarizer input. Tool calls and tool results are included so the summary
// of a tool-using agent reflects what actually happened; image and document
// blocks are omitted (media summarization is out of scope here). It returns
// "" for blocks that contribute nothing.
func renderBlock(b agent.ContentBlock) string {
	switch x := b.(type) {
	case agent.TextBlock:
		return x.Text
	case agent.ToolUseBlock:
		input := string(x.Input)
		if input == "" {
			input = "{}"
		}
		return fmt.Sprintf("[called tool %q with %s]", x.Name, input)
	case agent.ToolResultBlock:
		if x.IsError {
			return fmt.Sprintf("[tool error: %s]", x.Content)
		}
		return fmt.Sprintf("[tool result: %s]", x.Content)
	default:
		return ""
	}
}

// ProviderSummarizer adapts an agent.Provider into a RollingSummary
// Summarizer. It keeps the summary prompt/model call separate from canonical
// conversation persistence.
//
// The covered messages (and any existing summary) are rendered into a single
// user message rather than replayed as a live transcript. This keeps the
// request provider-portable: the model request always ends with a user
// message, which providers such as Amazon Bedrock, Anthropic, and Gemini
// require (they reject a conversation ending in an assistant turn, treating it
// as an unsupported assistant prefill). It also means the summarizer never
// feeds the model orphaned tool_use/tool_result blocks from the covered
// window.
func ProviderSummarizer(provider agent.Provider, system string) Summarizer {
	return func(ctx context.Context, existing string, messages []agent.Message) (string, error) {
		if provider == nil {
			return "", errors.New("contextmanager: summarizer provider is required")
		}

		var sb strings.Builder
		if existing != "" {
			sb.WriteString("Summary so far:\n")
			sb.WriteString(existing)
			sb.WriteString("\n\nNew messages to fold into the summary:\n")
		}
		for _, m := range messages {
			for _, b := range m.Content {
				line := renderBlock(b)
				if line == "" {
					continue
				}
				sb.WriteString(string(m.Role))
				sb.WriteString(": ")
				sb.WriteString(line)
				sb.WriteString("\n")
			}
		}

		input := []agent.Message{{
			Role:    agent.RoleUser,
			Content: []agent.ContentBlock{agent.TextBlock{Text: sb.String()}},
		}}
		response, err := provider.Stream(ctx, agent.ModelRequest{System: system, Messages: input}, nil)
		if err != nil {
			return "", fmt.Errorf("contextmanager: provider summary: %w", err)
		}
		return response.Text, nil
	}
}
