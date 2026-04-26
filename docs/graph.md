# Graph Workflows

The `graph` package provides a DAG-based state machine for orchestrating multi-step workflows. You define named nodes (units of work), connect them with edges (static, conditional, or parallel), and run the graph with an initial state. The graph engine handles execution order, fork/join parallelism, and iteration limits.

## Core Concepts

A graph has:
- **Nodes** — named functions that receive state and return updated state
- **Edges** — routing rules that determine which node runs next
- **State** — a `map[string]any` passed between nodes
- **Entry** — the first node to execute

## Creating a Graph

```go
g, err := graph.NewGraph()
if err != nil {
    log.Fatal(err)
}

g.AddNode("classify", classifyFn)
g.AddNode("respond", respondFn)
g.AddEdge("classify", "respond")
g.SetEntry("classify")

result, err := g.Run(ctx, graph.State{"input": "Hello"})
fmt.Println(result.State["output"])
```

## State

`State` is `map[string]any` — a shared data container passed between nodes. Each node receives a copy of the current state and returns an updated copy. The graph engine merges the returned state back into the shared state.

```go
type State map[string]any
```

Use `graph.CopyState(s)` to create a shallow copy when needed.

## NodeFunc

Every node is a `NodeFunc`:

```go
type NodeFunc func(ctx context.Context, state State) (State, error)
```

The function receives the current state, does its work, and returns the updated state. Return an error to abort the graph.

```go
classifyFn := func(ctx context.Context, state graph.State) (graph.State, error) {
    input := state["input"].(string)
    // ... classify the input ...
    state["category"] = "billing"
    return state, nil
}
```

## Edges

### Static Edge

Always routes from one node to another:

```go
g.AddEdge("classify", "respond")
```

### Conditional Edge

A `RouterFunc` decides the next node at runtime. Return `""` to end the graph:

```go
g.AddConditionalEdge("classify", func(ctx context.Context, state graph.State) (string, error) {
    category := state["category"].(string)
    switch category {
    case "billing":
        return "billing_handler", nil
    case "technical":
        return "tech_handler", nil
    default:
        return "", nil // end graph
    }
})
```

### Fork (Parallel Execution)

Execute multiple nodes concurrently:

```go
g.AddFork("start", []string{"research", "analyze"})
```

Requires at least 2 targets. Each branch gets a copy of the state. Results are merged in sorted order (deterministic).

### Join (Barrier)

Wait for all predecessors to complete before executing:

```go
g.AddJoin("synthesize", []string{"research", "analyze"})
```

Requires at least 2 predecessors. The join node fires automatically when all predecessors are done.

## GraphResult

`Graph.Run` returns a `GraphResult`:

```go
type GraphResult struct {
    State State
    Usage agent.TokenUsage
}
```

`Usage` accumulates token usage from any agent nodes in the graph. `State` contains the final merged state after all nodes have run.

## Options

```go
g, err := graph.NewGraph(
    graph.WithGraphMaxIterations(50),  // default: 100
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithGraphMaxIterations(n)` | 100 | Max node executions per Run. Returns error if n < 1. |

## AgentNode

Wrap an `agent.Invoker` as a graph node. The node reads the user message from `inputKey` in state and writes the agent's response to `outputKey`. Any type that implements `agent.Invoker` works — `*agent.Agent` satisfies it out of the box:

```go
import "github.com/camilbinas/gude-agents/agent/graph"

node := graph.AgentNode(myAgent, "input", "output")
g.AddNode("agent", node)
```

The node also writes `"__usage__"` to state so token usage is accumulated in the graph result.

## LLMRouter

Use an LLM to decide which node to route to:

```go
router := graph.LLMRouter(routerAgent, []string{"billing", "technical"})
g.AddConditionalEdge("classify", router)
```

The LLM receives the current state as context and must respond with one of the allowed node names.

## TypedGraph

`TypedGraph[S]` provides type-safe state instead of `map[string]any`:

```go
type MyState struct {
    Input    string
    Category string
    Output   string
}

tg := graph.NewTypedGraph[MyState](g)
result, err := tg.Run(ctx, MyState{Input: "Hello"})
fmt.Println(result.State.Output)
```

The typed graph wraps the underlying `Graph` and handles serialization between your struct and `State`.

## Validation

The graph validates its structure at the start of every `Run`:
- Entry node must be registered
- All edge targets must be registered nodes
- All fork targets must be registered
- All join predecessors must be registered
- MaxIterations must be >= 1

Invalid graphs return a `*GraphValidationError`. Exceeding the iteration limit returns a `*GraphIterationError`.

## Code Example

A classification pipeline that routes to different handlers:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/camilbinas/gude-agents/agent/graph"
)

func main() {
    g, err := graph.NewGraph()
    if err != nil {
        log.Fatal(err)
    }

    // Classify input
    g.AddNode("classify", func(ctx context.Context, s graph.State) (graph.State, error) {
        input := s["input"].(string)
        if len(input) > 50 {
            s["category"] = "complex"
        } else {
            s["category"] = "simple"
        }
        return s, nil
    })

    // Simple handler
    g.AddNode("simple", func(ctx context.Context, s graph.State) (graph.State, error) {
        s["output"] = "Quick answer: " + s["input"].(string)
        return s, nil
    })

    // Complex handler
    g.AddNode("complex", func(ctx context.Context, s graph.State) (graph.State, error) {
        s["output"] = "Detailed analysis of: " + s["input"].(string)
        return s, nil
    })

    g.SetEntry("classify")
    g.AddConditionalEdge("classify", func(ctx context.Context, s graph.State) (string, error) {
        return s["category"].(string), nil
    })

    result, err := g.Run(context.Background(), graph.State{"input": "What is Go?"})
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result.State["output"])
}
```

## See Also

- [Structured Logging](logging.md) — `WithGraphLogging` for structured log output
- [Tracing](tracing.md) — `WithGraphTracing` for OpenTelemetry instrumentation
- [Multi-Agent Composition](multi-agent.md) — `AgentAsTool` for simpler parent-child patterns
- [Agent API Reference](agent-api.md) — agent options and invoke methods
