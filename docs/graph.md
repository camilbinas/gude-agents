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
g, err := graph.New[graph.State]()
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
type State = map[string]any
```

Use `graph.CopyState(s)` to create a shallow copy when needed.

## NodeFunc

Every node is a `NodeFunc[S]`:

```go
type NodeFunc[S any] func(ctx context.Context, state S) (S, error)
```

For untyped graphs, use `NodeFunc[graph.State]`. The function receives the current state, does its work, and returns the updated state. Return an error to abort the graph.

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

## Result

`Graph[S].Run` returns a `Result[S]`:

```go
type Result[S any] struct {
    State S
    Usage agent.TokenUsage
}
```

`Usage` accumulates token usage from any agent nodes in the graph. `State` contains the final merged state after all nodes have run.

## Options

```go
g, err := graph.New[graph.State](
    graph.WithMaxIterations(50),  // default: 100
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithMaxIterations(n)` | 100 | Max node executions per Run. Returns error if n < 1. |

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

The LLM receives the current state as context and must respond with one of the allowed node names. For typed graphs, use `graph.LLMRouterFunc[S]` with a prompt extraction function.

## Typed State

`Graph[S]` works directly with custom struct types — no `map[string]any`, no type assertions. Use `graph.New[S]()` with any struct type:

```go
type MyState struct {
    graph.GraphState                   // embed for automatic token tracking
    Input    string `json:"input"`
    Category string `json:"category"`
    Output   string `json:"output"`
}

g, err := graph.New[MyState]()
// ... add nodes, edges, set entry ...
result, err := g.Run(ctx, MyState{Input: "Hello"})
fmt.Println(result.State.Output)
```

JSON serialization occurs only at checkpoint boundaries and event snapshots — not between nodes.

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
    g, err := graph.New[graph.State]()
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

## Checkpointing

Checkpointing persists execution state after each node, enabling step-by-step execution, interrupt-based human-in-the-loop flows, and rewind/replay. When no checkpointer is configured, the graph runs identically to before with zero overhead.

```go
import "github.com/camilbinas/gude-agents/agent/graph/checkpointer/memory"

cp := memory.New()
g, _ := graph.New[graph.State](
    graph.WithCheckpointer(cp),
)

// ... add nodes, edges, set entry ...

result, err := g.Run(ctx, graph.State{"input": "hello"}, graph.WithThreadID("thread-1"))
```

| Option | Description |
|--------|-------------|
| `WithCheckpointer(cp)` | Sets the `GraphCheckpointer` backend. Required for Step/Resume/RewindTo. |
| `WithCheckpointOnInterruptOnly()` | Only save checkpoints at interrupt points, not after every node. |
| `WithThreadID(id)` | Run option that sets the thread ID. Required when a checkpointer is configured. |

### Interrupts

Mark nodes to pause execution before or after they run. Requires a checkpointer.

```go
g.AddNode("review", reviewFn)
g.InterruptBefore("review") // pause before review runs

result, err := g.Run(ctx, state, graph.WithThreadID("t1"))
// err is *graph.GraphInterruptError
var intErr *graph.GraphInterruptError
if errors.As(err, &intErr) {
    fmt.Println(intErr.Result.NodeName) // "review"
    fmt.Println(intErr.Result.Type)     // "before"
}
```

`InterruptBefore` saves a checkpoint with the node NOT in the completed set. `InterruptAfter` saves a checkpoint with the node IN the completed set.

### Step

Execute one node at a time. Returns a `StepResult[S]` with the executed node name, version, and whether the graph is done.

```go
res, err := g.Step(ctx, graph.State{"input": "hello"}, "thread-1")
fmt.Println(res.NodeName, res.Version, res.Done)

// Continue stepping until done
for !res.Done {
    res, err = g.Step(ctx, nil, "thread-1")
}
```

### Resume

Continue execution from the latest checkpoint. Optionally merge state updates before resuming.

```go
// After an interrupt, provide human feedback and resume
updates := graph.State{"approved": true}
result, err := g.Resume(ctx, "thread-1", &updates)
```

Returns `Result[S]` on completion or `*GraphInterruptError` if another interrupt is hit.

### RewindTo

Reset execution position to a previous checkpoint version. Does not delete later checkpoints — versions after rewind continue from the global max.

```go
err := g.RewindTo(ctx, "thread-1", 2)
// Next Resume/Step starts from the state at version 2
result, err := g.Resume(ctx, "thread-1", nil)
```

### Typed Checkpointing

`Graph[S]` supports all checkpointing APIs with type-safe state:

```go
g, _ := graph.New[MyState](graph.WithCheckpointer(cp))
// ... add nodes ...
g.InterruptBefore("review")

res, _ := g.Step(ctx, MyState{Input: "hello"}, "thread-1")
fmt.Println(res.State.Input) // typed access

result, _ := g.Resume(ctx, "thread-1", &MyState{Approved: true})
_ = g.RewindTo(ctx, "thread-1", 2)
```

### Event Hook

`WithEventHook` receives structured events at each lifecycle point — useful for frontend visualization, logging, or metrics.

```go
type MyHook struct {
    events []graph.GraphEvent
}

func (h *MyHook) OnEvent(e graph.GraphEvent) {
    h.events = append(h.events, e)
}

g, _ := graph.New[graph.State](
    graph.WithCheckpointer(cp),
    graph.WithEventHook(&MyHook{}),
)
```

| Event Type | When |
|------------|------|
| `EventGraphStarted` | Beginning of execution |
| `EventNodeStarted` | Before each node function runs |
| `EventNodeCompleted` | After each node function completes |
| `EventCheckpointSaved` | After each checkpoint save |
| `EventInterruptFired` | When an interrupt pauses execution |
| `EventResumed` | When Resume is called |
| `EventRewindCompleted` | When RewindTo completes |
| `EventGraphCompleted` | End of execution (success or error) |

`OnEvent` is called synchronously — implementations must not block (use buffered channels or async dispatch).

### Backends

| Backend | Import | Use Case |
|---------|--------|----------|
| InMemory | `graph/checkpointer/memory` | Testing and development |
| Redis | `graph/checkpointer/redis` | Multi-process, low-latency persistence |
| DynamoDB | `graph/checkpointer/dynamodb` | Serverless production workloads |
| Postgres | `graph/checkpointer/postgres` | Traditional server deployments |

All backends implement `GraphCheckpointer` and are safe for concurrent use.

### State Serialization

All values in `State` must be JSON-serializable. The graph validates this before every checkpoint save. Non-serializable values (channels, functions, complex numbers) cause a `StateSerializationError` identifying the offending key and type.

```go
// This will fail at checkpoint time:
state["bad"] = make(chan int) // StateSerializationError{Key: "bad", Type: "chan int"}
```

## See Also

- [Structured Logging](logging.md) — `WithGraphLogging` for structured log output
- [Tracing](tracing.md) — `WithGraphTracing` for OpenTelemetry instrumentation
- [Multi-Agent Composition](multi-agent.md) — `AgentAsTool` for simpler parent-child patterns
- [Agent API Reference](agent-api.md) — agent options and invoke methods
