# Guardrails

Guardrails validate or transform model-facing text. Configure them when constructing the agent:

```go
a, err := agent.New(prov, instructions,
    agent.WithInputGuardrail(checkPrompt),
    agent.WithOutputGuardrail(redactAnswer),
)
```

Input guardrails run before each provider request. Output guardrails run after a final answer is assembled; `Result.Text` contains the processed answer. Multiple guardrails run in registration order.

For `Stream` and `TextStream`, model text is live: chunks may already have reached the consumer before an output guardrail rejects the assembled answer. A blocked or failed guardrail terminates the iterator with a `*agent.GuardrailError`; it is not a successful stop reason. If no unvalidated bytes may leave the process, use `Invoke` and send only `Result.Text` after success.

Tool guards are separate and configured with `tool.WithGuard`. They run in the canonical tool pipeline before approval and middleware. Use [RBAC](rbac.md) for role/attribute authorization and [middleware](middleware.md) for cross-cutting execution behavior.
