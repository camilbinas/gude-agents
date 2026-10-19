# Amazon Bedrock provider

```go
import "github.com/camilbinas/gude-agents/agent/provider/bedrock"

// Choose a model that is available in your AWS region and account.
prov, err := bedrock.GlobalClaudeSonnet5_5(
    bedrock.WithRegion("us-east-1"),
    bedrock.WithMaxTokens(4096),
)
// The same model by ID; known IDs get the same capability metadata.
prov, err = bedrock.New("global.anthropic.claude-sonnet-5-5")
a, err := agent.New(prov, "Answer clearly.")
```

The provider uses the AWS default credential chain. `WithAPIKey` supports Bedrock API keys; options also cover region, maximum tokens, thinking effort/budget, Bedrock Guardrails, `WithPromptCaching()` (cache points after the system prompt and document blocks, Claude models only; other models ignore it), and safe URL-fetch timeout/private-network policy for remote attachments.

Use model IDs or inference-profile IDs available in the configured region. Gude does not pick a model for you. Known models (including every inference-profile prefix of them) get their catalogued limits, tool-choice facts and the reasoning request shape Bedrock needs; other IDs, such as application inference profile ARNs, work and report only the adapter baseline until you add `WithCapabilities`. See [Providers](../providers.md). Permissions commonly include model invocation and streaming invocation, plus guardrail permissions when enabled. Keep the provider's content guardrail distinct from application [guardrails](../guardrails.md).

Bedrock integrations also include RAG embedders, Knowledge Base retrieval, and reranking; see [RAG](../rag.md).
