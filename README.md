# gude-agents

A Go SDK for building agents: a long-lived `Runtime` you configure once, a
`Context` that configures one invocation, and a `Result` you read back. Gude
never picks a model for you — you choose the provider and model explicitly.

## 30-second quickstart

```bash
go get github.com/camilbinas/gude-agents/agent
go get github.com/camilbinas/gude-agents/agent/provider/bedrock
```

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	// Choose a model available in your AWS region and account.
	prov := bedrock.Must(bedrock.GlobalClaudeSonnet5_5())

	a, err := agent.Agent(prov, "You are a helpful assistant.")
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.NewContext(context.Background())
	result, err := a.Invoke(ctx, "Why is the sky blue?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Text)
}
```

This is the [`examples/getting-started`](examples/getting-started/main.go)
program. The three-concept model (`Runtime` / `Context` / `Result`) and the
streaming, interrupt and persistence APIs are in
[Getting started](docs/getting-started.md).

## Modules

Gude is a Go multi-module workspace: the core lives in the root module, and
each provider and backend adapter is its own module, so an application depends
only on what it uses. All modules are released together at one version.

| Area | Import path | What it provides |
| --- | --- | --- |
| Core | `github.com/camilbinas/gude-agents/agent` | `Runtime`, `Context`, `Result`, tools, streaming, interrupts |
| Providers | `.../agent/provider/{anthropic,bedrock,openai,ollama,vllm,fallback}` | Model backends; `fallback` chains them |
| Conversation stores | `.../agent/conversation/{postgres,redis,sqlite}` | Durable conversation history (`conversation.NewInMemory` is in the core module) |
| Execution stores | `.../agent/execution`, `.../agent/execution/{postgres,redis,dynamodb}` | Paused-run storage for interrupts |
| Long-term memory | `.../agent/memory/{postgres,redis}` | Identity-scoped typed memory |
| RAG | `.../agent/rag/{bedrock,openai,postgres,redis}`, `.../agent/rag/document/pdf` | Retrieval and document ingestion |
| Rate limiting | `.../agent/ratelimit/redis` | Distributed rate limiting |
| MCP | `.../agent/mcp` | Model Context Protocol tool discovery |
| Observability | `.../agent/metrics/{otel,prometheus}`, `.../agent/tracing`, `.../agent/tracing/sentry`, `.../agent/logging/{slog,debug}` | Metrics, tracing and logging |
| Token counting | `.../agent/tokencount/tiktoken` | Token estimation |

## Documentation

Full documentation lives in [`docs/`](docs/README.md):

- [Getting started](docs/getting-started.md) — construct, invoke, stream, and resume an agent.
- [Agent API](docs/agent-api.md), [Invocation context](docs/invocation-context.md), [Decision guide](docs/decision-guide.md).
- [Tools](docs/tools.md), [Middleware](docs/middleware.md), [Interrupts](docs/interrupts.md), [Interception](docs/interception.md).
- [Conversation persistence](docs/conversation.md), [RAG](docs/rag.md), [Memory](docs/memory.md).
- [Guardrails](docs/guardrails.md), [RBAC](docs/rbac.md), [Structured output](docs/structured-output.md), [Multi-agent](docs/multi-agent.md).
- [Providers](docs/providers.md), [HTTP services](docs/http.md), [MCP](docs/mcp.md), [Observability](docs/observability.md).
- [Support matrix](docs/support-matrix.md).

Runnable, progressively more advanced programs are in
[`examples/`](examples/README.md).

## Development

Go 1.25 or newer. The workspace is driven by `go.work`; run `go` commands from
inside a module directory and `GOWORK` resolves automatically.

Point the build cache outside the working tree so caches and built binaries
never land inside a module directory (they otherwise accumulate to several GB
and can be mistaken for tracked files):

```bash
export GOCACHE="$HOME/.cache/gude-gocache"   # any path outside the repo
go build ./...
go test ./...
```

CI runs the same per-module checks the release uses, plus race, vulnerability
and lint lanes:

```bash
bash .github/scripts/release.sh test   # build + vet + test every module
bash .github/scripts/release.sh race   # -race on the concurrency-heavy modules
bash .github/scripts/release.sh vuln   # govulncheck ./... (needs govulncheck on PATH)
bash .github/scripts/release.sh lint   # golangci-lint with .golangci.yml
```

golangci-lint must be built by the same Go toolchain that compiles the code, or
it panics on newer standard-library ASTs:

```bash
GOBIN="$HOME/go/bin" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

The release engineering (lockstep multi-module versioning, hermetic tests,
atomic tag push) lives in [`.github/scripts/`](.github/scripts/).

## License

See [LICENSE](LICENSE).
