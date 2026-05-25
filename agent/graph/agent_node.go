package graph

import (
	"context"
	"fmt"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
)

// AgentNode wraps an agent.Invoker (typically *agent.Agent) as a NodeFunc.
// inputKey is the state key to read the user message from.
// outputKey is the state key to write the agent response to.
func AgentNode(a agent.Invoker, inputKey, outputKey string) NodeFunc[State] {
	return func(ctx context.Context, state State) (State, error) {
		msg, ok := state[inputKey].(string)
		if !ok {
			return nil, fmt.Errorf("AgentNode: state[%q] is missing or not a string", inputKey)
		}
		c := agent.NewContext(ctx)
		result, err := a.Invoke(c, msg)
		if err != nil {
			return nil, err
		}
		out := CopyState(state)
		out[outputKey] = result
		out["__usage__"] = c.Usage()
		return out, nil
	}
}

// AgentNodeAccessor defines how to read/write agent input/output from typed state.
// For typed graphs (Graph[S]), set InputKeys and OutputKeys explicitly.
// For map-based graphs (Graph[State]), use In()/Out() on Agent() directly.
type AgentNodeAccessor[S any] struct {
	GetInput   func(S) string
	SetOutput  func(*S, string)
	GetMedia   func(S) ([]agent.ImageBlock, []agent.DocumentBlock) // optional: extract media from state
	InputKeys  []string                                            // state keys this node reads
	OutputKeys []string                                            // state keys this node writes
}

// applyNode implements NodeOpt so that Keys() can be passed directly to Agent().
// This enables backward compatibility: g.Agent("x", a, graph.Keys("out", "in"))
func (a AgentNodeAccessor[S]) applyNode(in *[]string, out *[]string) {
	*in = append(*in, a.InputKeys...)
	*out = append(*out, a.OutputKeys...)
}

// Keys returns an AgentNodeAccessor[State] that reads from one or more map keys
// (concatenating with section headers when multiple) and writes to outputKey.
//
// Values in state are handled by type:
//   - string: included as text (concatenated with section headers when multiple keys)
//   - agent.ImageBlock: passed as an image attachment to the agent
//   - agent.DocumentBlock: passed as a document attachment to the agent
//   - []byte: treated as a PNG image attachment (use ImageBlock for other formats)
//
// This allows multimodal pipelines where upstream nodes produce images or documents
// that flow into agent nodes naturally.
func Keys(outputKey string, inputKeys ...string) AgentNodeAccessor[State] {
	return AgentNodeAccessor[State]{
		GetInput: func(s State) string {
			// Collect only string values for the text prompt.
			var textKeys []string
			for _, key := range inputKeys {
				if _, ok := s[key].(string); ok {
					textKeys = append(textKeys, key)
				}
			}

			if len(textKeys) == 0 {
				return "No data available."
			}
			if len(textKeys) == 1 {
				v, _ := s[textKeys[0]].(string)
				return v
			}
			var sb strings.Builder
			for _, key := range textKeys {
				if v, ok := s[key].(string); ok && v != "" {
					fmt.Fprintf(&sb, "## %s\n%s\n\n", key, v)
				}
			}
			return sb.String()
		},
		GetMedia: func(s State) ([]agent.ImageBlock, []agent.DocumentBlock) {
			var images []agent.ImageBlock
			var docs []agent.DocumentBlock
			for _, key := range inputKeys {
				switch v := s[key].(type) {
				case agent.ImageBlock:
					images = append(images, v)
				case agent.DocumentBlock:
					docs = append(docs, v)
				case []byte:
					// Raw bytes treated as PNG image.
					images = append(images, agent.ImageBlock{
						Source: agent.ImageSource{Data: v, MIMEType: "image/png"},
					})
				}
			}
			return images, docs
		},
		SetOutput: func(s *State, out string) {
			(*s)[outputKey] = out
		},
		InputKeys:  inputKeys,
		OutputKeys: []string{outputKey},
	}
}

// Agent registers a node backed by an *agent.Agent on the graph. Use In/Out
// to declare which state keys the agent reads and writes. For typed graphs,
// use AgentWithAccessor() instead.
func (g *Graph[S]) Agent(name string, a *agent.Agent, opts ...NodeOpt) (*Node[S], error) {
	if a == nil {
		return nil, &GraphValidationError{Message: "Agent: agent must not be nil"}
	}

	// Check if any opt is an AgentNodeAccessor[S] (e.g., Keys() passed directly).
	for _, opt := range opts {
		if acc, ok := any(opt).(AgentNodeAccessor[S]); ok {
			if err := g.registerAgentInternal(name, a, acc); err != nil {
				return nil, err
			}
			return &Node[S]{name: name, graph: g}, nil
		}
	}

	// Otherwise, build accessor from In/Out keys.
	var inKeys, outKeys []string
	for _, opt := range opts {
		opt.applyNode(&inKeys, &outKeys)
	}

	if len(outKeys) == 0 {
		return nil, &GraphValidationError{Message: fmt.Sprintf("Agent %q: at least one Out key is required", name)}
	}

	mapAccessor := Keys(outKeys[0], inKeys...)
	accessor, ok := any(mapAccessor).(AgentNodeAccessor[S])
	if !ok {
		return nil, &GraphValidationError{Message: fmt.Sprintf("Agent %q: In()/Out() only works with Graph[State]; for typed graphs use AgentWithAccessor()", name)}
	}

	if err := g.registerAgentInternal(name, a, accessor); err != nil {
		return nil, err
	}
	return &Node[S]{name: name, graph: g}, nil
}

// AgentWithAccessor registers an agent-backed node using a custom accessor for typed state.
// Use this for Graph[S] where S is a struct type.
//
//	g.AgentWithAccessor("answer", myAgent, graph.AgentNodeAccessor[MyState]{
//	    GetInput:  func(s MyState) string { return s.Question },
//	    SetOutput: func(s *MyState, out string) { s.Answer = out },
//	    InputKeys: []string{"question"},
//	    OutputKeys: []string{"answer"},
//	})
func (g *Graph[S]) AgentWithAccessor(name string, a *agent.Agent, accessor AgentNodeAccessor[S]) (*Node[S], error) {
	if a == nil {
		return nil, &GraphValidationError{Message: "AgentWithAccessor: agent must not be nil"}
	}
	if err := g.registerAgentInternal(name, a, accessor); err != nil {
		return nil, err
	}
	return &Node[S]{name: name, graph: g}, nil
}

// RegisterAgent registers an agent-backed node using the string-based API.
func (g *Graph[S]) RegisterAgent(name string, a *agent.Agent, opts ...NodeOpt) error {
	if a == nil {
		return &GraphValidationError{Message: "Agent: agent must not be nil"}
	}

	// Check if any opt is an AgentNodeAccessor[S] (e.g., Keys() passed directly).
	for _, opt := range opts {
		if acc, ok := any(opt).(AgentNodeAccessor[S]); ok {
			return g.registerAgentInternal(name, a, acc)
		}
	}

	var inKeys, outKeys []string
	for _, opt := range opts {
		opt.applyNode(&inKeys, &outKeys)
	}

	if len(outKeys) == 0 {
		return &GraphValidationError{Message: fmt.Sprintf("Agent %q: at least one Out key is required", name)}
	}

	mapAccessor := Keys(outKeys[0], inKeys...)
	accessor, ok := any(mapAccessor).(AgentNodeAccessor[S])
	if !ok {
		return &GraphValidationError{Message: fmt.Sprintf("Agent %q: In()/Out() only works with Graph[State]; for typed graphs use AgentWithAccessor()", name)}
	}

	return g.registerAgentInternal(name, a, accessor)
}

// registerAgentInternal is the shared implementation for Agent/RegisterAgent.
func (g *Graph[S]) registerAgentInternal(name string, a *agent.Agent, accessor AgentNodeAccessor[S]) error {
	// Require key metadata.
	if len(accessor.OutputKeys) == 0 && len(accessor.InputKeys) == 0 {
		return &GraphValidationError{Message: fmt.Sprintf("Agent %q: no I/O keys; use In()/Out() or provide an accessor with InputKeys/OutputKeys", name)}
	}

	nodeFn := buildAgentNodeFunc(g, name, a, accessor)

	if err := g.addNode(name, nodeFn); err != nil {
		return err
	}

	g.dataflow[name] = DataFlowMeta{
		InputKeys:  accessor.InputKeys,
		OutputKeys: accessor.OutputKeys,
	}

	setAgentNodeMeta(g, name, a)
	g.agentNodes[name] = a
	return nil
}

// AddAgent registers an agent-backed node and returns a *Node[S] handle.
// Deprecated: Use Agent() with In()/Out() directly.
func (g *Graph[S]) AddAgent(name string, a *agent.Agent, opts ...NodeOpt) (*Node[S], error) {
	return g.Agent(name, a, opts...)
}

// buildAgentNodeFunc creates a NodeFunc[S] that uses the streaming path with
// bridge hooks for event forwarding and observability inheritance.
func buildAgentNodeFunc[S any](g *Graph[S], name string, a *agent.Agent, accessor AgentNodeAccessor[S]) NodeFunc[S] {
	return func(ctx context.Context, state S) (S, error) {
		msg := accessor.GetInput(state)

		c := agent.NewContext(ctx)

		// Attach media (images/documents) if the accessor provides them.
		if accessor.GetMedia != nil {
			images, docs := accessor.GetMedia(state)
			if len(images) > 0 {
				c.WithImages(images)
			}
			if len(docs) > 0 {
				c.WithDocuments(docs)
			}
		}

		// Resolve the effective event hook for this run: the graph-level hook
		// composed with any per-call hook injected via runConfig.extraEventHook
		// (e.g. by RunEventStream). Reading from ctx instead of g.eventHook
		// directly is what makes per-call event streaming reach Agent nodes.
		effective := lookupEffectiveHook(ctx, g.eventHook)
		runHook := effective.asGraphEventHook()

		// Configure bridge event hook (only if there's any observer).
		bridgeEvent := newBridgeEventHook(runHook, name, nil)
		if bridgeEvent != nil {
			c.WithEventHook(bridgeEvent)
		}

		// Configure bridge observability hooks via context (race-free, no agent mutation).
		// The agent's hooks() method checks context hooks first, falling back to agent-level hooks.
		if g.tracingHook != nil {
			bridge := newBridgeTracingHook(g.tracingHook, name, a.TracingHook())
			if bridge != nil {
				c.WithTracingHook(bridge)
			}
		}
		if g.metricsHook != nil {
			bridge := newBridgeMetricsHook(g.metricsHook, name, a.MetricsHook())
			if bridge != nil {
				c.WithMetricsHook(bridge)
			}
		}
		if g.loggingHook != nil {
			bridge := newBridgeLoggingHook(g.loggingHook, name, a.LoggingHook())
			if bridge != nil {
				c.WithLoggingHook(bridge)
			}
		}

		// Use streaming invocation path. The runHook here is the effective
		// hook so per-call streams (RunEventStream's channel) receive
		// EventAgentStreaming events for each chunk.
		result, err := agentNodeStream(a, c, msg, runHook, name)
		if err != nil {
			var zero S
			return zero, err
		}

		accessor.SetOutput(&state, result)
		return state, nil
	}
}

// setAgentNodeMeta populates node metadata from the agent's provider and name.
func setAgentNodeMeta[S any](g *Graph[S], name string, a *agent.Agent) {
	prov := a.Provider()
	meta := NodeMeta{
		Label:    name,
		Provider: prov.Name(),
	}

	if mi, ok := prov.(agent.ModelIdentifier); ok {
		meta.Model = mi.ModelID()
	}

	if agentName := a.Name(); agentName != "" {
		meta.Label = agentName
		meta.Agent = agentName
	}
	g.SetNodeMeta(name, meta)
}

// LLMRouter returns a RouterFunc that uses an Invoker to choose the next node.
// validTargets is the list of node names the LLM may choose from.
func LLMRouter(a agent.Invoker, validTargets []string) RouterFunc[State] {
	return func(ctx context.Context, state State) (string, error) {
		prompt := buildRouterPrompt(state, validTargets)
		c := agent.NewContext(ctx)
		result, err := a.Invoke(c, prompt)
		if err != nil {
			return "", err
		}
		next := strings.TrimSpace(result)
		if next == "" {
			return "", nil // end signal
		}
		for _, t := range validTargets {
			if t == next {
				return next, nil
			}
		}
		return "", fmt.Errorf("LLMRouter: model returned unknown node %q", next)
	}
}

// LLMRouterFunc returns a RouterFunc[S] that uses an Invoker to choose the
// next node. promptFn extracts the text to send to the LLM from the typed state.
// validTargets is the list of node names the LLM may choose from.
//
// Example:
//
//	g.Condition("classify", graph.LLMRouterFunc[MyState](
//	    routerAgent,
//	    []string{"code_expert", "devops_expert"},
//	    func(s MyState) string { return s.Question },
//	))
func LLMRouterFunc[S any](a agent.Invoker, validTargets []string, promptFn func(S) string) RouterFunc[S] {
	return func(ctx context.Context, state S) (string, error) {
		input := promptFn(state)
		prompt := buildTypedRouterPrompt(input, validTargets)
		c := agent.NewContext(ctx)
		result, err := a.Invoke(c, prompt)
		if err != nil {
			return "", err
		}
		next := strings.TrimSpace(result)
		if next == "" {
			return "", nil
		}
		for _, t := range validTargets {
			if t == next {
				return next, nil
			}
		}
		return "", fmt.Errorf("LLMRouterFunc: model returned unknown node %q", next)
	}
}

// buildRouterPrompt formats the current state and valid targets into a routing prompt.
func buildRouterPrompt(state State, validTargets []string) string {
	var sb strings.Builder
	sb.WriteString("Based on the current state, choose the next node to execute.\n\n")
	sb.WriteString("Current state:\n")
	for k, v := range state {
		sb.WriteString(fmt.Sprintf("  %s: %v\n", k, v))
	}
	sb.WriteString("\nValid next nodes: ")
	sb.WriteString(strings.Join(validTargets, ", "))
	sb.WriteString("\n\nRespond with ONLY the name of the next node, or an empty string to end execution.")
	return sb.String()
}

// buildTypedRouterPrompt formats a user-provided input and valid targets into a routing prompt.
func buildTypedRouterPrompt(input string, validTargets []string) string {
	var sb strings.Builder
	sb.WriteString("Based on the following input, choose the next node to execute.\n\n")
	sb.WriteString("Input: ")
	sb.WriteString(input)
	sb.WriteString("\n\nValid next nodes: ")
	sb.WriteString(strings.Join(validTargets, ", "))
	sb.WriteString("\n\nRespond with ONLY the name of the next node, or an empty string to end execution.")
	return sb.String()
}
