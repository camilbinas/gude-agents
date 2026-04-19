// Package shared provides reusable utilities for examples: trace formatters,
// mock tools, test helpers, and other common building blocks.
//
// Tree exporter usage:
//
//	exp := utils.NewTreeExporter()
//	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
//	// ... run agent/graph ...
//	exp.Flush() // renders all collected traces as trees
package utils

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TreeExporter is a SpanExporter that collects spans and renders them as
// formatted trees on Flush() or Shutdown(). It never renders automatically —
// the caller decides when a trace is complete.
type TreeExporter struct {
	mu     sync.Mutex
	traces map[trace.TraceID][]sdktrace.ReadOnlySpan
}

// NewTreeExporter creates a new tree exporter.
func NewTreeExporter() *TreeExporter {
	return &TreeExporter{
		traces: make(map[trace.TraceID][]sdktrace.ReadOnlySpan),
	}
}

func (e *TreeExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, s := range spans {
		tid := s.SpanContext().TraceID()
		e.traces[tid] = append(e.traces[tid], s)
	}

	return nil
}

// Flush renders all collected traces as trees to stderr and clears the buffer.
// Call this after an agent invocation or graph run completes.
func (e *TreeExporter) Flush() {
	e.mu.Lock()
	traces := e.traces
	e.traces = make(map[trace.TraceID][]sdktrace.ReadOnlySpan)
	e.mu.Unlock()

	for tid, spans := range traces {
		renderTrace(tid, spans)
	}
}

func (e *TreeExporter) Shutdown(_ context.Context) error {
	e.Flush()
	return nil
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

type node struct {
	span     sdktrace.ReadOnlySpan
	children []*node
}

func renderTrace(tid trace.TraceID, spans []sdktrace.ReadOnlySpan) {
	byID := make(map[trace.SpanID]*node, len(spans))
	var roots []*node

	for _, s := range spans {
		n := &node{span: s}
		byID[s.SpanContext().SpanID()] = n
	}

	for _, n := range byID {
		parentID := n.span.Parent().SpanID()
		if parent, ok := byID[parentID]; ok {
			parent.children = append(parent.children, n)
		} else {
			roots = append(roots, n)
		}
	}

	// Sort roots and children by start time.
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].span.StartTime().Before(roots[j].span.StartTime())
	})
	var sortAll func(n *node)
	sortAll = func(n *node) {
		sort.Slice(n.children, func(i, j int) bool {
			return n.children[i].span.StartTime().Before(n.children[j].span.StartTime())
		})
		for _, c := range n.children {
			sortAll(c)
		}
	}
	for _, r := range roots {
		sortAll(r)
	}

	w := os.Stderr
	shortID := tid.String()[:8]

	fmt.Fprintf(w, "\n\033[36m┌ Trace %s\033[0m\n", shortID)
	fmt.Fprintf(w, "\033[36m│\033[0m\n")

	for i, root := range roots {
		printNode(w, root, "\033[36m│\033[0m  ", i == len(roots)-1)
	}

	if len(roots) > 0 {
		dur := roots[0].span.EndTime().Sub(roots[0].span.StartTime())
		fmt.Fprintf(w, "\033[36m│\033[0m\n")
		fmt.Fprintf(w, "\033[36m└ %s total\033[0m\n\n", fmtDuration(dur))
	}
}

func printNode(w *os.File, n *node, prefix string, isLast bool) {
	connector := "├─"
	if isLast {
		connector = "└─"
	}

	dur := n.span.EndTime().Sub(n.span.StartTime())
	status := statusIcon(n.span.Status().Code)
	attrs := formatAttrs(n.span)
	name := n.span.Name()

	fmt.Fprintf(w, "%s%s \033[1m%s\033[0m  \033[2m(%s)\033[0m %s",
		prefix, connector, name, fmtDuration(dur), status)
	if attrs != "" {
		fmt.Fprintf(w, "  \033[33m%s\033[0m", attrs)
	}
	for _, ev := range n.span.Events() {
		fmt.Fprintf(w, "  \033[31m⚡%s\033[0m", ev.Name)
	}
	fmt.Fprintln(w)

	childPrefix := prefix
	if isLast {
		childPrefix += "   "
	} else {
		childPrefix += "│  "
	}

	for i, child := range n.children {
		printNode(w, child, childPrefix, i == len(n.children)-1)
	}
}

func statusIcon(code codes.Code) string {
	switch code {
	case codes.Ok:
		return "\033[32m✓\033[0m"
	case codes.Error:
		return "\033[31m✗\033[0m"
	default:
		return "\033[2m·\033[0m"
	}
}

func formatAttrs(s sdktrace.ReadOnlySpan) string {
	var parts []string
	for _, a := range s.Attributes() {
		key := string(a.Key)
		switch key {
		case "agent.iteration.number":
			parts = append(parts, fmt.Sprintf("iteration=%d", a.Value.AsInt64()))
		case "agent.iteration.final":
			if a.Value.AsBool() {
				parts = append(parts, "final")
			}
		case "agent.iteration.tool_count":
			if n := a.Value.AsInt64(); n > 0 {
				parts = append(parts, fmt.Sprintf("tools=%d", n))
			}
		case "provider.input_tokens":
			parts = append(parts, fmt.Sprintf("in=%d", a.Value.AsInt64()))
		case "provider.output_tokens":
			parts = append(parts, fmt.Sprintf("out=%d", a.Value.AsInt64()))
		case "provider.tool_calls":
			if n := a.Value.AsInt64(); n > 0 {
				parts = append(parts, fmt.Sprintf("tool_calls=%d", n))
			}
		case "agent.token_usage.input":
			parts = append(parts, fmt.Sprintf("total_in=%d", a.Value.AsInt64()))
		case "agent.token_usage.output":
			parts = append(parts, fmt.Sprintf("total_out=%d", a.Value.AsInt64()))
		case "agent.model_id":
			parts = append(parts, a.Value.AsString())
		case "memory.conversation_id":
			parts = append(parts, fmt.Sprintf("conv=%s", a.Value.AsString()))
		case "retriever.document_count":
			parts = append(parts, fmt.Sprintf("docs=%d", a.Value.AsInt64()))
		case "graph.iterations":
			parts = append(parts, fmt.Sprintf("steps=%d", a.Value.AsInt64()))
		}
	}
	return strings.Join(parts, " ")
}

func fmtDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%.1fµs", float64(d.Microseconds()))
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	default:
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
}
