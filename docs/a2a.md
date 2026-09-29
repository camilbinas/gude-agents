# Agent-to-Agent protocol

`agent/a2a` exposes configured agents over A2A and converts remote agent skills into ordinary tools.

## Serve an agent

`DeriveCard` builds an Agent Card from `Agent.Name`, instructions, and current `ToolSpecs`. Override description, version, URL, skills, or capabilities with card options. `NewServer` exposes one agent; `NewMultiServer` hosts `AgentRegistration` values under distinct path prefixes and provides JSON-RPC, REST, and gRPC integrations.

The executor consumes the agent's iterator-based `Stream` and continues persisted work with `ResumeStream`, preserving text, tool, and interrupt behavior at the protocol boundary. Configure a conversation and interrupt store when remote callers need durable continuation.

## Call a remote agent

`NewClient(ctx, baseURL)` discovers the remote card. Convert advertised skills to `tool.Tool` values, optionally filtering skills, then pass them to `agent.WithTools`. The client is safe for concurrent use; provide a custom HTTP client for TLS, authentication, proxies, and deadlines.

A2A can propagate a principal through request headers. Treat remote headers as untrusted unless an authenticated gateway establishes them; server adapters can recover validated data with `PrincipalFromRequest`.

Use in-process [`AgentAsTool`](multi-agent.md) when deployment isolation is unnecessary. Use [MCP](mcp.md) for tool servers rather than independently addressable agents.
