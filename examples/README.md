# Examples

The top-level examples are a learning path, not an adapter matrix. Each directory demonstrates one workflow; backend, provider, exporter, and transport variants belong in package or feature documentation.

## Start here

1. [`getting-started`](getting-started/) — create an Agent, invoke it, and print a result.
2. [`tools`](tools/) — register typed tools for a customer-support workflow.
3. [`streaming`](streaming/) — consume text, thinking, tool, and completion events with cancellation.
4. [`conversation`](conversation/) — keep a canonical transcript while a ContextManager projects summary plus recent history.
5. [`human-in-the-loop`](human-in-the-loop/) — approve a destructive action or provide missing information, then resume.
6. [`structured-output`](structured-output/) — receive schema-validated typed output.

## Build larger agents

7. [`rag`](rag/) — ingest documents into an in-memory vector store and create a RAGAgent.
8. [`memory`](memory/) — use identity-scoped typed long-term memory.
9. [`multi-agent`](multi-agent/) — delegate from an orchestrator to in-process specialists through `AgentAsTool`.
10. [`mcp`](mcp/) — discover Streamable HTTP MCP tools and add them to an Agent.

## Deploy and interoperate

11. [`http-server`](http-server/) — expose one long-lived Agent over `net/http` with SSE and interrupt resume.
12. [`a2a`](a2a/) — run an A2A server and a remote-client orchestrator.
13. [`multimodal`](multimodal/) — attach local or URL-backed images and documents.

Most examples use Bedrock and therefore require ordinary AWS credentials. The canonical workflow examples use in-memory state or local in-process servers where possible. See [`../docs/README.md`](../docs/README.md) for provider, backend, observability, security, and adapter-specific configuration.
