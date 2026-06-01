package agent

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ─── WidgetBlock ─────────────────────────────────────────────────────────────

// WidgetBlock is a ContentBlock that carries structured, domain-agnostic widget
// data produced by a tool handler. Type is a caller-defined discriminator string
// (e.g. "chart", "table", "progress"). Payload is an opaque json.RawMessage
// whose schema is entirely defined by the caller.
type WidgetBlock struct {
	Type    string          // non-empty discriminator; required
	Payload json.RawMessage // opaque caller-defined JSON; may be nil
}

func (WidgetBlock) contentBlock() {}

// Validate returns a non-nil error if the WidgetBlock is malformed.
// Currently the only hard requirement is a non-empty Type.
func (w WidgetBlock) Validate() error {
	if w.Type == "" {
		return fmt.Errorf("widget block: Type must not be empty")
	}
	return nil
}

// ─── WidgetEmitter ───────────────────────────────────────────────────────────

// WidgetEmitter is an optional companion interface to EventHook. Hooks that
// want to receive widget events emitted via Context.EmitWidget implement this
// method in addition to EventHook. Hooks that do not implement it drop widget
// events silently — the runtime never emits them itself, so there is no
// breakage risk for existing hooks.
//
// The built-in eventStreamHook implements this so widget events flow into the
// same channel as all other events. A user-supplied EventHook can opt in by
// adding an OnWidget(c *Context, block WidgetBlock) method.
type WidgetEmitter interface {
	OnWidget(c *Context, block WidgetBlock)
}

// OnWidget forwards a widget event to the channel and, if the chained hook
// also implements WidgetEmitter, to that hook too.
func (h *eventStreamHook) OnWidget(c *Context, block WidgetBlock) {
	// Defensive copy of Payload to prevent downstream mutation.
	var payloadCopy json.RawMessage
	if block.Payload != nil {
		payloadCopy = make(json.RawMessage, len(block.Payload))
		copy(payloadCopy, block.Payload)
	}
	h.ch <- AgentEvent{
		Type:          EventWidget,
		Timestamp:     time.Now(),
		WidgetType:    block.Type,
		WidgetPayload: payloadCopy,
	}
	if next, ok := h.next.(WidgetEmitter); ok {
		next.OnWidget(c, block)
	}
}

// ─── widgetAccumulator ───────────────────────────────────────────────────────

// widgetAccumulatorKey is the context key for the per-tool-call widget accumulator.
type widgetAccumulatorKey struct{}

// widgetAccumulator collects WidgetBlocks emitted during a single tool call.
// It is safe for concurrent use (required when parallelTools is enabled).
type widgetAccumulator struct {
	mu     sync.Mutex
	blocks []WidgetBlock
}

func (a *widgetAccumulator) append(b WidgetBlock) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.blocks = append(a.blocks, b)
}

func (a *widgetAccumulator) drain() []WidgetBlock {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.blocks
	a.blocks = nil
	return out
}

// ─── stripWidgets ────────────────────────────────────────────────────────────

// stripWidgets returns a new []Message slice in which every WidgetBlock has
// been removed from each Message.Content. Messages whose Content becomes empty
// after stripping are omitted entirely. The input slice and its Message values
// are never mutated.
func stripWidgets(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		filtered := make([]ContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			if _, isWidget := b.(WidgetBlock); !isWidget {
				filtered = append(filtered, b)
			}
		}
		if len(filtered) == 0 {
			continue
		}
		mc := m
		mc.Content = filtered
		out = append(out, mc)
	}
	return out
}
