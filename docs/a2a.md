# Agent-to-Agent protocol

`agent/a2a` exposes configured agents over A2A and converts remote agent skills into ordinary tools.

## Serve an agent

`DeriveCard` builds an Agent Card from `Agent.Name`, instructions, and current `ToolSpecs`. Override description, version, URL, skills, or capabilities with card options. `NewServer` exposes one agent over JSON-RPC, REST, and gRPC.

The executor consumes the agent's iterator-based `Stream` and continues persisted work with `ResumeStream`, preserving text, tool, and interrupt behavior at the protocol boundary. Configure a conversation and interrupt store when remote callers need durable continuation.

## Call a remote agent

`NewClient(ctx, baseURL)` discovers the remote card. Convert advertised skills to `tool.Tool` values, optionally filtering skills, then pass them to `agent.WithTools`. The client is safe for concurrent use; provide a custom HTTP client for TLS, authentication, proxies, and deadlines.

## Forwarded identity

A2A can propagate a principal through `X-Agent-Principal-*` request headers. These headers are **never trusted by default**: `NewServer` ignores them unless you opt in explicitly, because any caller that can set HTTP headers could otherwise assign itself arbitrary roles or attributes.

- `a2a.WithPrincipalVerifier(func(agent.Principal) (agent.Principal, error))` verifies the forwarded principal (e.g. validate a signature, look up the caller in an identity store) before it is attached to the agent context. Return an error to reject the task.
- `a2a.WithTrustedForwardedPrincipal()` explicitly opts into trusting the raw forwarded headers with no verification. Only use this when the server sits behind a trusted boundary (e.g. an internal service mesh) that authenticates the caller and sets these headers itself.

Without either option, forwarded principal headers are parsed but discarded — the agent context has no principal from them. Server adapters built on the raw HTTP request (rather than `NewServer`) can recover the unverified value with `PrincipalFromRequest`, but are responsible for verifying or discarding it themselves.

Use in-process [`AgentAsTool`](multi-agent.md) when deployment isolation is unnecessary. Use [MCP](mcp.md) for tool servers rather than independently addressable agents.

See [`examples/a2a`](../examples/a2a/): run `server` to expose an Agent Card and `client` to discover remote skills and install them on a local orchestrator.
