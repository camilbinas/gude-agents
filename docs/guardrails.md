# Guardrails

Guardrails validate or transform model-facing text. Configure them when constructing the agent:

```go
a, err := agent.New(prov, instructions,
    agent.WithInputGuardrail(checkPrompt),
    agent.WithOutputGuardrail(redactAnswer),
)
```

Input guardrails run before each provider request. Output guardrails run after a final answer is assembled; `Result.Text` contains the processed answer. Multiple guardrails run in registration order.

With an output guardrail, `Stream` and `TextStream` hold the final answer until every output guardrail passes, then send it as one text chunk. If a guardrail fails, no answer text is sent and the iterator ends with a `*agent.GuardrailError`. Text the model sends together with tool calls is not the final answer, so it still streams live. Without output guardrails, text streams live as before.

Tool guards are separate and configured with `tool.WithGuard`. They run in the canonical tool pipeline before approval and middleware. Use [RBAC](rbac.md) for role/attribute authorization and [middleware](middleware.md) for cross-cutting execution behavior.
