# Integration tests

These tests exercise the agent loop against real model providers. They live in a
separate module so the main runtime module never depends on a provider.

## Running them

Choose the provider and the model explicitly. There is no default provider, no
default model, and no tier or alias to pick one for you:

```sh
MODEL_PROVIDER=bedrock MODEL_ID=global.anthropic.claude-sonnet-5-5 go test -v -timeout=120s ./...
MODEL_PROVIDER=openai  MODEL_ID=gpt-5.6                            go test -v -timeout=120s ./...
```

| Variable | Meaning |
|---|---|
| `MODEL_PROVIDER` | `openai`, `anthropic`, `bedrock`, `ollama` or `vllm` |
| `MODEL_ID` | The explicit model identifier for that provider, passed to its `New` |
| `GUDE_LIVE_TESTS` | Set to `1` to request a live run |

Without any of these variables the live tests are skipped, so a plain
`go test ./...` never calls a paid API. Setting `GUDE_LIVE_TESTS=1`,
`MODEL_PROVIDER` or `MODEL_ID` requests a live run, and then both
`MODEL_PROVIDER` and `MODEL_ID` are required: a missing or unknown value fails
with a message that says what to set.

`helpers_test.go` builds the selected adapter with a small `switch`; this module
imports every provider module, which is why it is separate from the runtime.

Thinking tests additionally need `MODEL_PROVIDER` to be `bedrock` or `anthropic`
with a model that supports thinking. Embedding tests use `EMBEDDER`,
`BEDROCK_EMBED` and `AWS_REGION` as documented in `rag_test.go`.

`removed_surface_test.go` is not a live test; it always runs and guards against
the removed provider registry, A2A module and tier aliases coming back.
