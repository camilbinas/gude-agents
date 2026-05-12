# Graph Workflows

The `graph` package provides a DAG-based state machine for orchestrating multi-step workflows. You define named nodes with explicit I/O key declarations, and the engine automatically schedules nodes as soon as their declared inputs are available. Concurrent execution happens naturally when multiple nodes become ready simultaneously.

## Core Concepts

A graph has:
- **Nodes** — named functions that declare their input and output state keys
- **Data-flow scheduling** — the engine infers execution order from I/O declarations
- **State** — a `map[string]any` or typed struct passed between nodes
- **Entry** — nodes with empty input keys execute first; auto-detected or set via optional `g.Start()`

## Creating a Graph

```go
g, err := graph.New[graph.State]()
if err != nil {
    log.Fatal(err)
}

classify, _ := g.Node("classify", classifyFn, graph.In("input"), graph.Out("category"))
respond, _ := g.Node("respond", respondFn, graph.In("category"), graph.Out("output"))

result, err := g.Run(ctx, graph.State{"input": "Hello"})
fmt.Println(result.State["output"])
```

## State

`State` is `map[string]any` — a shared data container passed between nodes. Each node receives a copy of the current state and returns an updated copy.

```go
type State = map[string]any
```

Use `graph.CopyState(s)` to create a shallow copy when needed.

## NodeFunc

Every node is a `NodeFunc[S]`:

```go
type NodeFunc[S any] func(ctx context.Context, state S) (S, error)
```

The function receives the current state, does its work, and returns the updated state. Return an error to abort the graph.

## Node

`Node` registers a named node and returns a `*Node[S]` handle for type-safe wiring, interrupt configuration, and metadata access. `In()`/`Out()` declarations are optional — omit them when using `Then()` for pure sequencing:

```go
// Minimal — wire with Then()
fetch, _ := g.Node("fetch", fetchFn)
process, _ := g.Node("process", processFn)
fetch.Then(process)

// With data-flow keys
fetch, _ := g.Node("fetch", fetchFn, graph.Out("article"))
report, _ := g.Node("report", reportFn, graph.In("article"), graph.Out("output"))
```

The engine uses `In()`/`Out()` declarations to determine when a node is ready to execute. A node runs when all its declared input keys are present in the readiness set.

For string-based registration (dynamic graphs, cross-package wiring), use `RegisterNode`:

```go
g.RegisterNode("fetch", fetchFn, graph.Out("article"))
g.Connect("fetch", "report")
```

## Data-Flow Scheduling

The engine schedules nodes automatically based on their I/O declarations:

1. Nodes with empty input keys execute first (entry nodes)
2. After each node completes, its output keys are added to the readiness set
3. Any pending node whose input keys are all in the readiness set becomes ready
4. If multiple nodes become ready simultaneously, they execute concurrently
5. When no more nodes can become ready, the graph terminates

```go
// Diamond topology: entry → (research, analyze) → synthesize
g.Node("entry", entryFn, graph.In(), graph.Out("topic"))
g.Node("research", researchFn, graph.In("topic"), graph.Out("research_result"))
g.Node("analyze", analyzeFn, graph.In("topic"), graph.Out("analysis_result"))
g.Node("synthesize", synthesizeFn, graph.In("research_result", "analysis_result"), graph.Out("output"))
```

Here `research` and `analyze` both depend on `"topic"` and run concurrently after entry. `synthesize` waits for both to complete.

## Connect (Pure Sequencing)

When you need ordering without data flow, use `Then`/`After` on node handles or `Connect` with strings:

```go
fetch, _ := g.Node("fetch", fetchFn)
summarise, _ := g.Node("summarise", summariseFn)
sentiment, _ := g.Node("sentiment", sentimentFn)
report, _ := g.Node("report", reportFn)

fetch.Then(summarise, sentiment)    // fork: both run after fetch
report.After(summarise, sentiment)  // join: report waits for both
```

`Then` generates a synthetic scheduling key internally — nodes don't need to declare or write it. Use `In()`/`Out()` for real data dependencies and `Then`/`Connect` for pure ordering constraints.

### Node Handle Methods

| Method | Description |
|--------|-------------|
| `Name()` / `String()` | Returns the registration name. |
| `InputKeys()` / `OutputKeys()` | Returns copies of declared I/O keys (including synthetic connect keys). |
| `Then(targets ...*Node[S])` | Fork — this node must complete before all targets begin. |
| `After(sources ...*Node[S])` | Join — this node waits for all sources to complete. |
| `InterruptBefore()` / `InterruptAfter()` | Interrupt configuration. |
| `SetMeta(NodeMeta)` | Attaches display metadata. |

### String-Based API

`RegisterNode`, `RegisterAgent`, `Connect`, `InterruptBefore(name)`, `InterruptAfter(name)`, and `SetNodeMeta(name, meta)` remain available for dynamic or cross-package wiring where handles aren't in scope.

## Conditional Execution (Data-Flow Gating)

Conditional routing is achieved by nodes that conditionally write output keys. Downstream nodes only execute when their input keys are actually present:

```go
g.Node("classifier", func(ctx context.Context, s graph.State) (graph.State, error) {
    if s["input"].(string) == "billing" {
        s["route_billing"] = "go"
    } else {
        s["route_tech"] = "go"
    }
    return s, nil
}, graph.In("input"), graph.Out("route_billing", "route_tech"))

g.Node("billing_handler", billingFn, graph.In("route_billing"), graph.Out("output"))
g.Node("tech_handler", techFn, graph.In("route_tech"), graph.Out("output"))
```

Only one handler executes — the one whose input key was written. The graph terminates when no more nodes can become ready.

## Concurrency Control

When multiple nodes execute concurrently:
- Each node receives an isolated copy of the state (no shared references)
- Results are merged in alphabetical node-name order using `mergeDiff` (only changed keys applied)
- If two nodes write the same key, the alphabetically-last node wins (deterministic)

## Result

`Graph[S].Run` returns a `Result[S]`:

```go
type Result[S any] struct {
    State S
    Usage agent.TokenUsage
}
```

`Usage` accumulates token usage from any agent nodes in the graph. `State` contains the final state after all nodes have run.

## Options

```go
g, err := graph.New[graph.State](
    graph.WithMaxIterations(50),  // default: 100
)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WithMaxIterations(n)` | 100 | Max node executions per Run. Returns error if n < 1. |

## Agent Nodes

Agent nodes are graph nodes backed by an `*agent.Agent`. They provide automatic metadata propagation, event bubbling, hook inheritance, and streaming by default.

### Agent

`g.Agent` registers an agent-backed node and returns a `*Node[S]` handle. Uses the same `In()`/`Out()` pattern as `Node` — the agent reads all `In` keys (concatenated with section headers) and writes its response to the `Out` key:

```go
summarise, _ := g.Agent("summarise", myAgent, graph.In("article"), graph.Out("summary"))
investigate, _ := g.Agent("investigate", myAgent, graph.In("pods", "events"), graph.Out("findings"))
```

For typed state, use `AgentWithAccessor`:

```go
answer, _ := g.AgentWithAccessor("answer", myAgent, graph.AgentNodeAccessor[MyState]{
    GetInput:   func(s MyState) string { return s.Question },
    SetOutput:  func(s *MyState, out string) { s.Answer = out },
    InputKeys:  []string{"question"},
    OutputKeys: []string{"answer"},
})
```

### Automatic Metadata Propagation

Provider name, model ID, agent name, and tool specifications are captured automatically. Tools are resolved dynamically at `Structure()` call time.

### Agent Event Types

Agent nodes emit events through the graph's `GraphEventHook` — see the [Event Hook](#event-hook) table for the full list. Events are emitted in chronological order with monotonically non-decreasing timestamps.

### Streaming by Default

Agent nodes use `InvokeStream` rather than `Invoke`. Each chunk is emitted as an `EventAgentStreaming` event and accumulated into the output state key. If the provider doesn't support streaming, the full response is emitted as a single chunk event.

### Multimodal Inputs

`Keys()` automatically detects images and documents in state and passes them to the agent as attachments. No special wiring needed — upstream nodes produce media, agent nodes consume it:

```go
g.Node("capture", captureFn, graph.Out("screenshot", "question"))
g.Agent("describe", visionAgent, graph.Keys("description", "question", "screenshot"))
```

| State value type | Handling |
|-----------------|----------|
| `string` | Text prompt (concatenated with headers if multiple) |
| `agent.ImageBlock` | Passed via `WithImages` |
| `agent.DocumentBlock` | Passed via `WithDocuments` |
| `[]byte` | Treated as PNG image |

### Hook Inheritance

Graph-level observability hooks are automatically inherited by agent nodes. If the agent already has its own hooks, both fire (composition). Tracing creates child spans, metrics reports through the graph context, and logging includes the node name.

### Zero Overhead

Bridge hooks are only created when the corresponding graph hook is configured.

### Custom Node Functions

For custom node functions that invoke an agent (e.g., reading multiple state keys), use `g.EventHook()` and `graph.NewBridgeEventHook` to wire tool call and model event visibility:

```go
g.Node("my_node", func(ctx context.Context, s graph.State) (graph.State, error) {
    c := agent.NewContext(ctx)
    if hook := g.EventHook(); hook != nil {
        c.WithEventHook(graph.NewBridgeEventHook(hook, "my_node", nil))
    }
    result, err := myAgent.Invoke(c, s["input"].(string))
    s["output"] = result
    return s, nil
}, graph.In("input"), graph.Out("output"))
```

## LLMRouter

`LLMRouter` and `LLMRouterFunc` are available for building nodes that use an LLM to decide which output key to write, enabling data-flow gating based on LLM classification.

## Typed State

`Graph[S]` works directly with custom struct types — no `map[string]any`, no type assertions:

```go
type MyState struct {
    Input    string `json:"input"`
    Category string `json:"category"`
    Output   string `json:"output"`
}

g, err := graph.New[MyState]()
g.Node("classify", classifyFn, graph.In("input"), graph.Out("category"))
g.Node("respond", respondFn, graph.In("category"), graph.Out("output"))

result, err := g.Run(ctx, MyState{Input: "Hello"})
fmt.Println(result.State.Output)
```

For typed state, readiness is determined by non-zero struct field values. A field left at its zero value (empty string, 0, false, nil) is not considered "present" for scheduling purposes.

## Validation

The graph validates its structure at the start of every `Run`:
- At least one node with empty input keys must exist (or `Start()` must be called)
- No circular dependencies in data-flow declarations
- Every declared input key must be present in initial state or declared as an output by another node
- No two nodes may declare the same output key
- MaxIterations must be >= 1

Invalid graphs return a `*GraphValidationError`. Exceeding the iteration limit returns a `*GraphIterationError`.

## Code Example

Classification pipeline with conditional routing via data-flow gating:

```go
g, _ := graph.New[graph.State]()

g.Node("classify", func(ctx context.Context, s graph.State) (graph.State, error) {
    if len(s["input"].(string)) > 50 {
        s["complex_input"] = s["input"]
    } else {
        s["simple_input"] = s["input"]
    }
    return s, nil
}, graph.In("input"), graph.Out("simple_input", "complex_input"))

g.Node("simple", func(ctx context.Context, s graph.State) (graph.State, error) {
    s["output"] = "Quick answer: " + s["simple_input"].(string)
    return s, nil
}, graph.In("simple_input"), graph.Out("output"))

g.Node("complex", func(ctx context.Context, s graph.State) (graph.State, error) {
    s["output"] = "Detailed analysis of: " + s["complex_input"].(string)
    return s, nil
}, graph.In("complex_input"), graph.Out("output"))

result, _ := g.Run(context.Background(), graph.State{"input": "What is Go?"})
fmt.Println(result.State["output"])
```

## Checkpointing

Checkpointing persists execution state after each node, enabling step-by-step execution, interrupt-based human-in-the-loop flows, and rewind/replay. When no checkpointer is configured, the graph runs identically with zero overhead.

```go
import "github.com/camilbinas/gude-agents/agent/graph/checkpointer/memory"

cp := memory.New()
g, _ := graph.New[graph.State](
    graph.WithCheckpointer(cp),
)

// ... add nodes ...

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
g.InterruptBefore("review") // pause before review runs

result, err := g.Run(ctx, state, graph.WithThreadID("t1"))
// err is *graph.GraphInterruptError
var intErr *graph.GraphInterruptError
if errors.As(err, &intErr) {
    fmt.Println(intErr.Result.NodeName) // "review"
    fmt.Println(intErr.Result.Type)     // "before"
}
```

`InterruptBefore` saves a checkpoint with the node NOT in the completed set. `InterruptAfter` saves a checkpoint with the node IN the completed set. Checkpoints include the current readiness set so that resume correctly restores scheduling progress.

### Step

Execute one node at a time. Returns a `StepResult[S]` with the executed node name, version, and whether the graph is done.

```go
res, err := g.Step(ctx, graph.State{"input": "hello"}, "thread-1")
fmt.Println(res.NodeName, res.Version, res.Done)

for !res.Done {
    res, err = g.Step(ctx, nil, "thread-1")
}
```

### Resume

Continue execution from the latest checkpoint. Optionally merge state updates before resuming.

```go
updates := graph.State{"approved": true}
result, err := g.Resume(ctx, "thread-1", &updates)
```

Returns `Result[S]` on completion or `*GraphInterruptError` if another interrupt is hit.

### RewindTo

Reset execution position to a previous checkpoint version. Does not delete later checkpoints — versions after rewind continue from the global max.

```go
err := g.RewindTo(ctx, "thread-1", 2)
result, err := g.Resume(ctx, "thread-1", nil)
```

### Typed Checkpointing

`Graph[S]` supports all checkpointing APIs with type-safe state:

```go
g, _ := graph.New[MyState](graph.WithCheckpointer(cp))
g.InterruptBefore("review")

res, _ := g.Step(ctx, MyState{Input: "hello"}, "thread-1")
fmt.Println(res.State.Input) // typed access

result, _ := g.Resume(ctx, "thread-1", &MyState{Approved: true})
_ = g.RewindTo(ctx, "thread-1", 2)
```

### Event Hook

`WithEventHook` receives structured events at each lifecycle point — useful for frontend visualization, logging, or metrics. Use `SetEventHook` to swap the hook after construction (e.g., per-request in a server).

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
| `EventAgentToolCallStart` | Before an agent node invokes a tool |
| `EventAgentToolCallEnd` | After an agent node's tool call completes |
| `EventAgentModelStart` | Before an agent node calls the LLM |
| `EventAgentModelEnd` | After an agent node's LLM call returns |
| `EventAgentThinking` | When the model emits a thinking/reasoning block |
| `EventAgentStreaming` | When the model streams a response chunk |

`OnEvent` is called synchronously — implementations must not block (use buffered channels or async dispatch). Agent-level events include additional fields: `ToolName`, `ToolInput`, `ToolOutput`, and `ToolDuration` for tool calls; `StopReason` for model end; `Chunk` for streaming.

### Graph Introspection

`Structure()` returns the graph's topology as a serializable `GraphStructure`. Each `NodeInfo` includes `ID`, `Label`, `Provider`, `Model`, `Tools`, `InputKeys`, `OutputKeys`, `Layer` (BFS depth), and interrupt flags. Each `DataFlowEdge` has `From` (producer), `To` (consumer), and `Key` (the state key connecting them).

```go
g.Agent("summarise", summariserAgent, graph.Keys("summary", "article"))

structure := g.Structure() // GraphStructure with Nodes []NodeInfo, DataFlowEdges []DataFlowEdge
```

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
