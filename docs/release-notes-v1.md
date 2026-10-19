# v1 release notes: lease-only rate limiting

Rate limiting is a breaking v1 API migration. The engine-facing contract is now `Reserve(ctx, request)`, `Commit(ctx, lease, usage)`, and `Release(ctx, lease)`. Leases are opaque; terminal calls return typed lifecycle errors that support `errors.Is` for duplicate, conflicting, unknown, and expired leases.

Remove direct request admission, usage recording, independent checks, release callbacks, and legacy store implementations. Provide one `agent.RateLimiter` to `agent.WithRateLimiter`; provider attempts, including eligible retries, are then reserved and terminally accounted for by the engine.

RPM and TPM reserve atomically across per-key/global counters. TPM reserves the request estimate plus `MaxTokens` when configured and reconciles confirmed actual usage. Uncertain provider failures settle the reservation conservatively. Memory and Redis retain pending/terminal ledgers long enough to prevent capacity reset and detect invalid terminal actions; pending expiry settles the estimate and returns the typed expired result.

See [rate limiting](rate-limiting.md), the [support matrix](support-matrix.md), and the versioned [evaluation cases](evaluation-cases-v1.md) for operational boundaries and qualification scenarios.

## Removed from the stable surface

These are deliberate breaking scope reductions made before the production v1 release. Each removed feature had no coherent durability or qualification story, and none is replaced by a shim, alias, or fallback. The packages and symbols no longer exist.

### Gemini provider and Gemini RAG embedder

Removed `agent/provider/gemini` and `agent/rag/gemini`, along with their registry entry, model definitions, estimator, and documentation.

Gemini support was removed from the stable framework surface before the
production v1 release. Applications should implement the Provider interface
externally if they need to maintain a private Gemini adapter.

The `Provider` abstraction and the Anthropic, Amazon Bedrock, OpenAI, Ollama, and vLLM adapters are unchanged. Gemini thought-signature support was not added.

### Background tools and automatic re-entry

Removed `tool.NewBackground`, `tool.BackgroundHandler`, `Tool.IsBackground`, `Tool.Ack`, `agent.WithBackgroundNotify`, `agent.ErrAgentShuttingDown`, the dispatch registry, detached handler goroutines, completion notifications, and automatic re-entry model turns.

Run durable asynchronous jobs in an application-owned worker, queue, or
scheduler. A job may invoke an Agent normally, but the framework does not own
cross-process dispatch, ownership, retries, dead-letter handling, or delivery.

`Agent.Shutdown(ctx)` now only flushes a conversation store that implements `agent.Flusher` (both are removed in the conversation durability section below). It no longer waits for background work because none exists. Same-conversation invocations and `RecoverExecution` calls are still serialized within one `Agent`.

### DynamoDB ConversationStore

Removed `agent/conversation/dynamodb`.

DynamoDB is no longer supplied as a ConversationStore. Migrate canonical
conversation history to a supported append-only store before upgrading.
DynamoDB remains available as an `ExecutionStore` through `agent/execution/dynamodb`; that is not a conversation-history migration path.
The supported conversation backends are in-memory, SQLite, PostgreSQL, and Redis.


## Conversation durability, pause persistence, and execution stores

These are deliberate breaking API changes made before the production v1 release. No deprecated aliases or compatibility shims are provided.

### `ConversationStore.Append` commits before it returns

A successful `Append` atomically appends the messages under the revision check, returns a cursor that a later read observes, and is committed before it returns according to the store's documented persistence model. Memory-backed stores satisfy this only within the running process. Persistent stores must not acknowledge a write that can disappear after a crash. This is an ordering guarantee between `ConversationStore` and `ExecutionStore`, not a distributed transaction.

Removed: `agent.WithSyncConversation`, the `agent.Flusher` interface, the engine's post-append and tool-result flush barriers, and `Agent.Shutdown`. `Shutdown` only flushed a `Flusher`, so nothing remains for it to do. Stop serving by stopping the HTTP server and closing the resources you own. A custom store that buffered writes must now commit in `Append`.

### One pause mechanism: persisted execution state

Pausing (tool approval or human input) and resuming now require both a `ConversationStore` and an `ExecutionStore`. The process-local pause registry and `Interrupt.Messages` are removed. An agent that needs to pause without both stores fails with `agent.ErrPauseStorageRequired`; the agent never creates in-memory stores on its own. A conversation store without an execution store remains valid for runs that never pause. A child agent used through `AgentAsTool` follows the same rule.

Migration: configure both stores, and invoke with a conversation ID.

```go
a, err := agent.New(provider, instructions,
    agent.WithConversationStore(conversation.NewInMemory()),
    agent.WithExecutionStore(execution.NewMemory()),
)
```

Memory-backed stores lose the transcript and every paused execution when the process exits and never evict. Use a durable conversation backend and a durable execution store (`agent/execution/postgres`, `agent/execution/redis`, or `agent/execution/dynamodb`) for pauses that must survive a restart.

### Execution stores implement `agent.ExecutionStore` directly

The checkpoint abstraction is removed. Nothing adapts a generic checkpointer to `agent.ExecutionStore` any more; each backend implements the interface itself, with its own compare-and-swap on a version counter. Removed:

- `agent/checkpoint` (`Checkpointer` and the in-memory checkpointer);
- `agent/checkpoint/postgres`, `agent/checkpoint/redis`, `agent/checkpoint/dynamodb`;
- `agent/checkpoint/executionstore` and the `execution.New(checkpointer)` adapter that replaced it.

No forwarding package or alias remains. Checkpoint tables and keys written by earlier releases are neither read nor written by the new stores; old rows can stay in place and be dropped when no deployment uses them.

New packages:

| Package | Constructor | Notes |
| --- | --- | --- |
| `agent/execution` | `execution.NewMemory()` | Process-local; lost on exit and never evicts. |
| `agent/execution/postgres` | `postgres.New(db, postgres.WithTableName(...))` | One row per execution; you create the `executions` table (schema in the package documentation). |
| `agent/execution/redis` | `redis.New(client, redis.WithKeyPrefix(...), redis.WithTTL(...))` | One hash per execution; atomic Lua scripts. Durable only with Redis persistence, and an expired execution is gone. |
| `agent/execution/dynamodb` | `dynamodb.New(client, dynamodb.WithTableName(...), dynamodb.WithKeyPrefix(...))` | One item per execution with a `execution_id` partition key; conditional puts and strongly consistent reads. |

```go
import pgexec "github.com/camilbinas/gude-agents/agent/execution/postgres"

executions := pgexec.New(pool)
a, err := agent.New(provider, instructions,
    agent.WithConversationStore(conversations),
    agent.WithExecutionStore(executions),
)
```

Import the Postgres, Redis, and DynamoDB packages with an alias when you also use a conversation or client package of the same name. `agent.ExecutionStore` keeps its name and stays in the root package; see [execution stores](execution-stores.md) for the contract and schemas.

### Every published module builds on its own

Release commits now carry complete `go.sum` files. Before this change the release rewrote internal requirements to the new version but left `go.sum` without checksums for them, so `GOWORK=off go test ./...` failed inside a nested module before compiling anything. The release workflow now publishes every module into a local file proxy in dependency order, runs `go mod tidy` against it, and verifies a consumer-style build (`GOWORK=off`, read-only `go.mod`/`go.sum`) of every module before it commits or tags. The root module no longer requires `agent/logging/debug` or `agent/logging/slog`, and the root tests no longer import sibling modules, so the internal requirement graph is acyclic.

## API consolidation: agent options, observability, MCP, and rate-limit keys

These are deliberate breaking changes made before the production v1 release. No compatibility alias, deprecated shim, or forwarding wrapper exists for any removed symbol. `agent.RAGAgent` and `Agent.TextStream` are unchanged.

### Removed public symbols

Root package `agent`:

| Removed | Replacement |
|---|---|
| `Agent.Provider()`, `Agent.HasTool`, `Agent.LookupTool`, `Agent.HasConversation`, `Agent.InferenceConfig`, `Agent.MaxIterations`, `Agent.ParallelTools`, `Agent.TokenBudget`, `Agent.Middlewares`, `Agent.InputGuardrails`, `Agent.OutputGuardrails` | None. `Name`, `Instructions` and `ToolSpecs` remain the only inspection methods; there is no configuration snapshot. |
| `Agent.CallProvider` | None. Call your `Provider` directly. |
| `WithSequentialTools()` | `WithToolExecution(ToolExecutionSequential)` |
| `ToolFilter`, `WithToolFilter` | `ToolVisibility`, `WithToolVisibility` |
| `RoleFilter()`, `RequirePrincipal()` | `RoleVisibility()`, `RequirePrincipalVisibility()` (`WithRoleEnforcement` and `WithRequirePrincipal` are unchanged) |
| `PolicyFunc`, `WithPolicy` | `WithToolVisibility` for model-facing visibility; tool guards or middleware for authorization |
| `WithCaching()`, `ModelRequest.CachingEnabled` | Provider-native `WithPromptCaching()` (Anthropic, Claude on Bedrock) |
| `WithProviderRetry(...)` and the retry/backoff machinery | None. The agent loop makes one attempt per model call; retry in your application |

Providers:

| Removed | Replacement |
|---|---|
| `anthropic.WithSystemPromptCaching()` | `anthropic.WithPromptCaching()` (system prompt and document blocks) |
| `bedrock.WithSystemPromptCaching()` | `bedrock.WithPromptCaching()` (Claude models; other models ignore it) |
| `openai.WithSystemPromptCaching()` | None. OpenAI caches automatically; cached tokens are still reported as `TokenUsage.CacheReadTokens` |

Packages and modules:

| Removed | Replacement |
|---|---|
| `agent/logging/auto` | Choose `logging/debug` or `logging/slog` in application code |
| `agent/metrics/cloudwatch` | `metrics/otel` or `metrics/prometheus` |
| `mcp.NewSSEClient` | `mcp.NewStreamableClient` (remote) or `mcp.NewStdioClient` (subprocess) |

Cache read and write token counts keep flowing into `TokenUsage` and observability records. `logging/debug`, `logging/slog`, `metrics/otel`, `metrics/prometheus`, `tracing.WithTracing`, and the MCP pool (`NewPool`, `WithPoolEnv`, `WithPoolSize`) are unchanged in purpose.

### Tool execution policy

```go
type ToolExecutionMode uint8

const (
    ToolExecutionParallel ToolExecutionMode = iota // default
    ToolExecutionSequential
)

func WithToolExecution(mode ToolExecutionMode) Option
func WithMaxConcurrentTools(n int) Option
```

```go
// Before
agent.New(prov, instructions, agent.WithSequentialTools())

// After
agent.New(prov, instructions, agent.WithToolExecution(agent.ToolExecutionSequential))
agent.New(prov, instructions, agent.WithMaxConcurrentTools(4)) // parallel, at most 4 handlers at once
```

Results keep the provider's call order in every mode. An unknown mode or `n < 1` is a construction error, and so is `ToolExecutionSequential` combined with `WithMaxConcurrentTools(n)` for `n > 1`. The policy applies to normal calls, durable execution, approval resume and recovery, and a batch that needs approval is still persisted before any sibling handler runs.

### Tool visibility

```go
type ToolVisibility func(*Context, tool.Tool) bool

func WithToolVisibility(...ToolVisibility) Option
func RoleVisibility() ToolVisibility
func RequirePrincipalVisibility() ToolVisibility
```

```go
// Before
agent.WithToolFilter(func(c *agent.Context, t tool.Tool) bool { return ok(c, t) })
agent.WithPolicy(opaAllows)

// After: visibility only decides what the model is offered.
agent.WithToolVisibility(func(c *agent.Context, t tool.Tool) bool { return ok(c, t) })
// Authorization belongs in tool guards, role/attribute policies, approval or middleware.
```

Rules are still evaluated before every provider call with AND semantics. A hidden tool is omitted from the provider's tool list and from that loop's execution map. Visibility is not authorization: execution-time role and attribute enforcement remains the security boundary. See [RBAC](rbac.md).

### Debug logger options

```go
type Option func(*config) error

func WithWriter(w io.Writer) Option
func WithColor(enabled bool) Option
func WithLogging(opts ...Option) agent.Option
```

```go
agentdebug.WithLogging()                                     // stdout, ANSI colors (unchanged default)
agentdebug.WithLogging(agentdebug.WithWriter(&buf), agentdebug.WithColor(false)) // plain capture
```

`WithWriter(nil)` fails `agent.New`. `WithColor(false)` emits no ANSI escape sequence anywhere, dividers and errors included. Writes remain serialized.

### Rate-limit bucket keys

```go
type RateLimitKeyFunc func(*Context) string

func WithRateLimitKey(fn RateLimitKeyFunc) Option
```

`RateLimitRequest.Key` is now documented as an application-defined bucket key. The engine used to pass the conversation ID, which docs described as a customer budget; it no longer derives a key from anything. Without a selector every attempt uses the empty key and shares one per-key bucket. Opt in to tenant, customer, API-key or user budgets explicitly:

```go
agent.WithRateLimiter(limiter),
agent.WithRateLimitKey(func(c *agent.Context) string { return c.Identity() }),
```

The selector runs once per physical provider attempt. `WithRateLimitKey(nil)` and `WithRateLimitKey` without `WithRateLimiter` fail `agent.New`. Global limits keep applying across all keys. Applications that relied on the old per-conversation buckets must add a selector returning `c.ConversationID()`. See [rate limiting](rate-limiting.md).

### MCP pool behavior

The pool is unchanged in purpose, with one hardening: a session whose call failed in transport, or was cancelled or timed out, is now closed instead of returned to the idle set, and the freed slot lets a later call start a replacement below the configured size. A server's JSON-RPC error or a tool result with `IsError` keeps the session. See [MCP](mcp.md).

## Explicit providers: A2A, the provider registry and tier aliases removed

These are deliberate breaking changes made before the production v1 release. No aliases, deprecated shims, or forwarding wrappers are provided. A Gude application now chooses its provider and its model explicitly:

```go
provider := bedrock.Must(bedrock.GlobalClaudeSonnet5_5())
// or
provider, err := openai.New("gpt-5.6")
```

There is no framework-wide default provider, no global provider registry, no `MODEL_TIER`, and no `Cheapest`, `Standard` or `Smartest` model.

### Removed

| Removed | Replacement |
|---|---|
| The `agent/a2a` module (`a2a.NewServer`, `a2a.NewClient`, card and executor APIs), `examples/a2a`, `docs/a2a.md` | None. `agent.AgentAsTool` still composes agents in process; expose a remote agent behind your own service API and wrap it as a tool, or use MCP for external tool servers |
| The `agent/provider/registry` module: `Register`, `RegisterBuiltins`, `New`, `FromEnv`, `Names`, `Tier`, `Cheapest`, `Standard`, `Smartest` | Construct the provider you want directly. Selecting a provider from `MODEL_PROVIDER` is application code, not framework code |
| `openai.Cheapest/Standard/Smartest`, `anthropic.Cheapest/Standard/Smartest`, `bedrock.Cheapest/Standard/Smartest`, `ollama.Cheapest/Standard/Smartest` | A named constructor such as `openai.GPT5_6`, `anthropic.ClaudeSonnet5_5`, `bedrock.GlobalClaudeSonnet5_5` or `ollama.Qwen35`, or `New("model-id")` |
| The `MODEL_TIER` environment variable and the implicit Bedrock default | `MODEL_PROVIDER` and `MODEL_ID`, required together by the integration tests only |

Migration for the old aliases:

| Old | Previously resolved to | Now |
|---|---|---|
| `bedrock.Standard()` | `GlobalClaudeSonnet5_5` | `bedrock.GlobalClaudeSonnet5_5()` |
| `bedrock.Cheapest()` | `GlobalClaudeHaiku4_5` | `bedrock.GlobalClaudeHaiku4_5()` |
| `bedrock.Smartest()` | `GlobalClaudeOpus5_5` | `bedrock.GlobalClaudeOpus5_5()` |
| `anthropic.Standard()` / `Cheapest()` / `Smartest()` | `ClaudeSonnet5_5` / `ClaudeHaiku4_5` / `ClaudeOpus5_5` | the matching named constructor |
| `openai.Standard()` / `Cheapest()` / `Smartest()` | `gpt-6.1-sol` / `gpt-6-luna` / `gpt-6-astra` | `openai.GPT6_1Sol()` / `GPT6Luna()` / `GPT6Astra()`, or another model you choose |
| `ollama.Standard()` / `Cheapest()` / `Smartest()` | `qwen3.5:9b` / `qwen3.5:2b` / `qwen3.6:27b` | `ollama.New("qwen3.5:9b")` and so on |

Check the target model before copying a row: an alias may have pointed at a model that is wrong for you, which is why they were removed. In particular `openai.GPT6_1Sol` is recorded as not supporting tool calls through Chat Completions.

### Model catalogues drive capability metadata

Each provider package keeps a private catalogue keyed by model ID. `New(modelID, opts...)` consults it before applying your options, so a known model gets the same best-known metadata through its named constructor or through `New("known-id")`. Named constructors are now one-line wrappers around `New`; they no longer carry their own capability blocks.

- Caller options (`WithCapabilities`, `WithToolUse`, `WithToolChoice*`, `WithContextWindowTokens`, `WithMaxOutputTokens`, ...) apply after the catalogue and win, and the fine-grained options can reset a catalogued field to `agent.Unknown`.
- Unknown or custom model IDs stay valid and report only the adapter baseline.
- Bedrock's catalogue also supplies the reasoning request shape (Claude, Nova 2, Grok), and applies to every inference-profile prefix of a model. `bedrock.New("us.anthropic.claude-opus-5-5")` now enables Claude-style thinking without a named constructor.
- Values were added only where the provider documents them: Anthropic limits for all named Claude models, OpenAI limits and function calling for the GPT-4o, GPT-4.1, GPT-5, GPT-5.4, GPT-5.5, GPT-5.6, GPT-6 Astra and o-series models, Bedrock limits for the named Claude, Nova, gpt-oss, GPT-6 and Grok 4.6 models. Everything else is deliberately `Unknown`. See [Providers](providers.md).
- The implicit Anthropic `max_tokens` fallback follows the catalogued output ceiling, so `anthropic.New("claude-haiku-4-5")` now defaults to 64,000 like its constructor instead of 128,000, and the 200K/64K models no longer default above their ceiling.
- `openai.NewCompatible` is added for OpenAI-compatible servers. Ollama and vLLM use it, so a server model name that matches an OpenAI model ID never inherits OpenAI metadata.
- Catalogue limits are advisory metadata, not limits Gude enforces.

### Integration tests

The live-provider helper builds the adapter named by `MODEL_PROVIDER` (`openai`, `anthropic`, `bedrock`, `ollama`, `vllm`) with `MODEL_ID` through a small `switch`. Live tests are skipped unless a live run is requested (`GUDE_LIVE_TESTS=1`, `MODEL_PROVIDER` or `MODEL_ID`), and then both variables are required: a missing value fails with a configuration error. Plain `go test ./...` no longer calls a paid API. See `integration/README.md`.
