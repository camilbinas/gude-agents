package graph

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"

	"github.com/camilbinas/gude-agents/agent"
)

// stateOps[S] defines state manipulation operations parameterized by state type.
// The engine calls these methods instead of directly operating on the state,
// allowing zero-cost operations for map[string]any and JSON bridge for structs.
type stateOps[S any] interface {
	// copy returns an isolated copy of the state for safe concurrent use.
	copy(s S) S

	// merge applies the result of a node execution back into the base state.
	merge(base *S, patch S)

	// mergeDiff merges only the keys that changed between snapshot and branch into base.
	// Used by fork/join to prevent branches from overwriting each other.
	mergeDiff(base *S, snapshot, branch S)

	// toMap serializes S to map[string]any for checkpoints, events, and fork/join diff.
	// For map[string]any this is identity (zero-cost).
	toMap(s S) (map[string]any, error)

	// fromMap deserializes map[string]any back to S.
	// For map[string]any this is identity (zero-cost).
	fromMap(m map[string]any) (S, error)

	// hasKey reports whether the given key has a non-zero value in the state.
	// For map[string]any this is a direct map lookup (zero cost).
	// For struct types this uses targeted reflection on the struct field
	// corresponding to the JSON tag, avoiding full json.Marshal.
	hasKey(state S, key string) bool
}

// NodeFunc is the unit of work executed by a node.
// S is the state type passed between nodes.
type NodeFunc[S any] func(ctx context.Context, state S) (S, error)

// RouterFunc decides the next node at runtime.
// Returning "" signals end-of-graph.
type RouterFunc[S any] func(ctx context.Context, state S) (string, error)

// Result is returned by Graph[S].Run on success.
type Result[S any] struct {
	State S
	Usage agent.TokenUsage
}

// StepResult is returned by Graph[S].Step with metadata about the executed step.
type StepResult[S any] struct {
	State    S                `json:"state"`
	NodeName string           `json:"node_name"`
	Version  int              `json:"version"`
	Done     bool             `json:"done"`
	Usage    agent.TokenUsage `json:"usage"`
}

// isMapStringAny reports whether S is map[string]any.
// Used at construction time to select the appropriate stateOps implementation.
func isMapStringAny[S any]() bool {
	var zero S
	_, ok := any(zero).(map[string]any)
	return ok
}

// mapStateOps implements stateOps[State] with direct map operations.
// No JSON marshaling occurs at any point — all operations are zero-cost.
type mapStateOps struct{}

func (mapStateOps) copy(s State) State { return CopyState(s) }

func (mapStateOps) merge(base *State, patch State) { mergeState(*base, patch) }

func (mapStateOps) mergeDiff(base *State, snapshot, branch State) {
	mergeDiff(*base, snapshot, branch)
}

func (mapStateOps) toMap(s State) (map[string]any, error) { return s, nil }

func (mapStateOps) fromMap(m map[string]any) (State, error) { return State(m), nil }

func (mapStateOps) hasKey(state State, key string) bool {
	_, ok := state[key]
	return ok
}

// jsonStateOps[S] implements stateOps[S] for custom struct types.
// Uses JSON marshaling only when crossing boundaries (checkpoint, fork/join, events).
type jsonStateOps[S any] struct{}

func (jsonStateOps[S]) copy(s S) S {
	// JSON round-trip to produce a deep copy.
	b, err := json.Marshal(s)
	if err != nil {
		// If marshaling fails, return the original (best effort).
		return s
	}
	var out S
	if err := json.Unmarshal(b, &out); err != nil {
		return s
	}
	return out
}

func (jsonStateOps[S]) merge(base *S, patch S) {
	// For structs, the node returns the full updated state.
	// The "merge" is simply replacing base with patch.
	*base = patch
}

func (jsonStateOps[S]) mergeDiff(base *S, snapshot, branch S) {
	// Convert to maps, apply mergeDiff, convert back.
	baseMap, err := typedToState(*base)
	if err != nil {
		return
	}
	snapshotMap, err := typedToState(snapshot)
	if err != nil {
		return
	}
	branchMap, err := typedToState(branch)
	if err != nil {
		return
	}
	mergeDiff(baseMap, snapshotMap, branchMap)
	result, err := stateToTyped[S](baseMap)
	if err != nil {
		return
	}
	*base = result
}

func (jsonStateOps[S]) toMap(s S) (map[string]any, error) {
	return typedToState(s)
}

func (jsonStateOps[S]) fromMap(m map[string]any) (S, error) {
	return stateToTyped[S](m)
}

// hasKey uses targeted reflection to check if the struct field corresponding
// to the given JSON tag key has a non-zero value. This avoids the full
// json.Marshal that toMap performs.
func (jsonStateOps[S]) hasKey(state S, key string) bool {
	fieldIdx, ok := jsonFieldIndex[S](key)
	if !ok {
		// Key doesn't correspond to a struct field — treat as abstract scheduling key.
		// Return false so the caller can decide to add it unconditionally.
		return false
	}
	v := reflect.ValueOf(&state).Elem()
	field := v.Field(fieldIdx)
	return !field.IsZero()
}

// jsonFieldCache caches the mapping from JSON tag name to struct field index
// for each type. This avoids repeated reflection on every hasKey call.
var jsonFieldCache sync.Map // map[reflect.Type]map[string]int

// jsonFieldIndex returns the struct field index for the given JSON tag key.
// It caches the mapping per type for performance.
func jsonFieldIndex[S any](key string) (int, bool) {
	var zero S
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return 0, false
	}

	// Check cache first.
	if cached, ok := jsonFieldCache.Load(t); ok {
		m := cached.(map[string]int)
		idx, found := m[key]
		return idx, found
	}

	// Build the mapping.
	m := make(map[string]int, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		// Parse the tag name (before any comma options like ",omitempty").
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}
		m[name] = i
	}

	jsonFieldCache.Store(t, m)
	idx, found := m[key]
	return idx, found
}

// GraphState is an optional base struct that typed graph states can embed to get
// automatic token usage accumulation. When embedded, nodes can call AddUsage to
// accumulate token counts, and the graph will automatically propagate them to the
// underlying usage tracker via the usageCarrier mechanism.
//
// Usage:
//
//	type MyState struct {
//	    graph.GraphState          // embed for automatic token tracking
//	    Topic   string `json:"topic"`
//	    Summary string `json:"summary"`
//	}
//
//	// In a node:
//	c := agent.Background()
//	result, err := myAgent.Invoke(c, s.Topic)
//	s.AddUsage(c.Usage())  // accumulates into the graph's token counter
type GraphState struct {
	pendingUsage agent.TokenUsage `json:"-"`
}

// AddUsage accumulates token usage from an agent call into the graph's counter.
func (g *GraphState) AddUsage(u agent.TokenUsage) {
	g.pendingUsage.InputTokens += u.InputTokens
	g.pendingUsage.OutputTokens += u.OutputTokens
}

// usageCarrier is the interface used internally to extract pending usage from a state.
type usageCarrier interface {
	getPendingUsage() agent.TokenUsage
	clearPendingUsage()
}

func (g *GraphState) getPendingUsage() agent.TokenUsage { return g.pendingUsage }
func (g *GraphState) clearPendingUsage()                { g.pendingUsage = agent.TokenUsage{} }

// --- bridge helpers ---

// typedToState converts a struct to a State map via JSON.
func typedToState[S any](s S) (State, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return State(m), nil
}

// stateToTyped converts a State map back to a struct via JSON.
func stateToTyped[S any](raw State) (S, error) {
	var zero S
	b, err := json.Marshal(map[string]any(raw))
	if err != nil {
		return zero, err
	}
	var s S
	if err := json.Unmarshal(b, &s); err != nil {
		return zero, err
	}
	return s, nil
}
