package graph

import (
	"context"
	"fmt"
	"time"
)

// DefaultEventStreamBuffer is the default buffer size for RunEventStream's
// channel. A modest buffer absorbs short consumer stalls without blocking the
// graph engine, while still applying back-pressure if the consumer falls behind.
const DefaultEventStreamBuffer = 64

// EventStreamOption configures RunEventStream behavior. It also accepts any
// RunOption (e.g. WithThreadID) which is forwarded to the underlying Run call.
type EventStreamOption func(*eventStreamConfig)

type eventStreamConfig struct {
	buffer  int
	runOpts []RunOption
}

// WithEventStreamBuffer sets the channel buffer size for RunEventStream.
// Larger buffers reduce the chance of blocking the graph engine on slow
// consumers at the cost of more in-flight memory; smaller buffers (including 0)
// increase back-pressure and keep events closer to real-time at the consumer.
//
// A negative or zero value falls back to DefaultEventStreamBuffer; pass a
// positive value to override.
func WithEventStreamBuffer(n int) EventStreamOption {
	return func(c *eventStreamConfig) { c.buffer = n }
}

// WithRunOption forwards a RunOption (e.g. WithThreadID) to the underlying
// Run call. Pass one for each RunOption you need:
//
//	g.RunEventStream(ctx, s,
//	    graph.WithRunOption(graph.WithThreadID("t1")),
//	    graph.WithEventStreamBuffer(128),
//	)
func WithRunOption(opt RunOption) EventStreamOption {
	return func(c *eventStreamConfig) { c.runOpts = append(c.runOpts, opt) }
}

// EventStream is the handle returned by Graph[S].RunEventStream. It carries
// both the live event channel and the typed Result delivered when the run
// completes.
//
// Typical usage:
//
//	stream := g.RunEventStream(ctx, initial)
//	for ev := range stream.Events() {
//	    // handle event (UI update, log, persist, etc.)
//	}
//	res, err := stream.Result()
//
// The Events channel closes when the run finishes (success or error). The
// final event delivered before close is always EventGraphCompleted, with its
// Error field populated on failure. Result blocks until the run finishes; it
// is safe to call before, during, or after draining Events.
type EventStream[S any] struct {
	events chan GraphEvent
	done   chan struct{}
	result Result[S]
	err    error
}

// Events returns the channel of GraphEvents emitted during this run. The
// channel is closed exactly once when the run finishes; consumers must drain
// it to completion or cancel the run via the context they passed to
// RunEventStream to avoid blocking the engine on a full buffer.
func (s *EventStream[S]) Events() <-chan GraphEvent {
	return s.events
}

// Result blocks until the run completes and returns the final Result and
// error. Calling Result does not require Events to have been fully drained,
// but the engine will block on full event buffers if the consumer is slower
// than event production.
func (s *EventStream[S]) Result() (Result[S], error) {
	<-s.done
	return s.result, s.err
}

// RunEventStream runs the graph and returns an EventStream that exposes
// every GraphEvent emitted during execution as a channel, plus the typed
// Result and error once the run completes.
//
// Unlike Run, this method does not block — it spawns the graph execution on
// a goroutine and returns immediately so callers can consume events live.
//
// The events channel always ends with a single EventGraphCompleted event
// (carrying the final state, usage, and error if any), then closes.
//
// RunEventStream coexists with WithEventHook / SetEventHook on the graph:
// the per-call channel hook is layered on top of any graph-level hook so
// existing observers keep firing. Multiple concurrent RunEventStream calls
// on the same graph are safe — each gets its own channel and per-call hook.
func (g *Graph[S]) RunEventStream(ctx context.Context, initial S, opts ...EventStreamOption) *EventStream[S] {
	cfg := &eventStreamConfig{buffer: DefaultEventStreamBuffer}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.buffer <= 0 {
		cfg.buffer = DefaultEventStreamBuffer
	}

	s := &EventStream[S]{
		events: make(chan GraphEvent, cfg.buffer),
		done:   make(chan struct{}),
	}

	// Per-run hook that fans events into the stream channel.
	hook := &channelHook{ch: s.events}

	go func() {
		defer close(s.events)
		defer close(s.done)

		// Convert a panic in the engine into a terminal EventGraphCompleted
		// event so consumers always see a clean shutdown before the channel
		// closes. Without this, a panic would leak past the goroutine boundary
		// and consumers would just see a closed channel.
		defer func() {
			if r := recover(); r != nil {
				s.err = fmt.Errorf("graph: panic in RunEventStream: %v", r)
				// Emit a synthetic GraphCompleted event so consumers' loops
				// terminate predictably with the panic surfaced as Error.
				select {
				case s.events <- GraphEvent{
					Type:      EventGraphCompleted,
					Timestamp: time.Now(),
					Error:     s.err,
				}:
				default:
					// Channel buffer was full; consumer can still read s.err
					// via Result(). Best effort.
				}
			}
		}()

		// Layer the channel hook on top of any user-supplied RunOptions.
		runOpts := append([]RunOption{withExtraEventHook(hook)}, cfg.runOpts...)

		s.result, s.err = g.Run(ctx, initial, runOpts...)
	}()

	return s
}

// withExtraEventHook is an internal RunOption that injects an extra GraphEventHook
// for the duration of one Run call. Used by RunEventStream to fan events into a
// per-call channel without mutating the shared graph.eventHook field.
func withExtraEventHook(h GraphEventHook) RunOption {
	return func(c *runConfig) { c.extraEventHook = h }
}

// channelHook is a GraphEventHook that forwards every event into a buffered
// channel. Sends are synchronous — a slow consumer back-pressures the engine.
type channelHook struct {
	ch chan<- GraphEvent
}

func (h *channelHook) OnEvent(event GraphEvent) {
	h.ch <- event
}
