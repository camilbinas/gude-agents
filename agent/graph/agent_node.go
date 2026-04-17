package graph

import (
	"context"
	"fmt"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
)

// Invoker is the interface required by AgentNode and LLMRouter.
// *agent.Agent satisfies this interface.
type Invoker interface {
	Invoke(ctx context.Context, userMessage string) (string, agent.TokenUsage, error)
}

// AgentNode wraps an Invoker (typically *agent.Agent) as a NodeFunc.
// inputKey is the state key to read the user message from.
// outputKey is the state key to write the agent response to.
func AgentNode(a Invoker, inputKey, outputKey string) NodeFunc {
	return func(ctx context.Context, state State) (State, error) {
		msg, _ := state[inputKey].(string)
		result, usage, err := a.Invoke(ctx, msg)
		if err != nil {
			return nil, err
		}
		out := CopyState(state)
		out[outputKey] = result
		out["__usage__"] = usage // picked up by runExec to accumulate
		return out, nil
	}
}

// LLMRouter returns a RouterFunc that uses an Invoker to choose the next node.
// validTargets is the list of node names the LLM may choose from.
func LLMRouter(a Invoker, validTargets []string) RouterFunc {
	return func(ctx context.Context, state State) (string, error) {
		prompt := buildRouterPrompt(state, validTargets)
		result, _, err := a.Invoke(ctx, prompt)
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

// TypedLLMRouter returns a TypedRouterFunc that uses an Invoker to choose the
// next node. promptFn extracts the text to send to the LLM from the typed state.
// validTargets is the list of node names the LLM may choose from.
//
// Example:
//
//	g.AddConditionalEdge("classify", graph.TypedLLMRouter[MyState](
//	    routerAgent,
//	    []string{"code_expert", "devops_expert"},
//	    func(s MyState) string { return s.Question },
//	))
func TypedLLMRouter[S any](a Invoker, validTargets []string, promptFn func(S) string) TypedRouterFunc[S] {
	return func(ctx context.Context, state S) (string, error) {
		input := promptFn(state)
		prompt := buildTypedRouterPrompt(input, validTargets)
		result, _, err := a.Invoke(ctx, prompt)
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
		return "", fmt.Errorf("TypedLLMRouter: model returned unknown node %q", next)
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
