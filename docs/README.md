# gude-agents documentation

gude-agents uses three core concepts: an `Agent` is long-lived configuration, a `Context` configures one invocation, and a `Result` is its outcome.

```go
a, err := agent.New(prov, "You are concise.",
    agent.WithTools(searchTool),
    agent.WithConversationStore(store),
)
ctx := agent.NewContext(context.Background()).WithConversationID("thread-42")
result, err := a.Invoke(ctx, "What changed?")
```

`Stream` and `ResumeStream` return `iter.Seq2[agent.Event, error]`; `TextStream` projects only live text. Approvals and human questions both return an `Interrupt`, continued with `Resume`.

## Start here

- [Getting started](getting-started.md) — construct, invoke, stream, and shut down an agent.
- [Agent API](agent-api.md) — `Agent`, `Result`, `Event`, invocation, streaming, and errors.
- [Invocation context](invocation-context.md) — cancellation, identity, principal, strict scopes, conversation IDs, attachments, and invocation KV.
- [Decision guide](decision-guide.md) — choose state, retrieval, streaming, and composition components.

## Build agents

- [Tools](tools.md) and [middleware](middleware.md)
- [Interrupts and resume](interrupts.md)
- [Conversation persistence](conversation.md)
- [RAG](rag.md) and [long-term memory](memory.md)
- [Guardrails](guardrails.md) and [RBAC](rbac.md)
- [Structured output](structured-output.md)
- [Multi-agent composition](multi-agent.md)

## Integrate and operate

- [Providers](providers.md): [Anthropic](providers/anthropic.md), [Amazon Bedrock](providers/bedrock.md), [Gemini](providers/gemini.md), [OpenAI](providers/openai.md), [Ollama](providers/ollama.md), and [vLLM](providers/vllm.md)
- [HTTP services](http.md), [MCP](mcp.md), and [A2A](a2a.md)
- [Observability and audit](observability.md)
- [Checkpointing](checkpoint.md) and [evaluation](eval.md)
