# Amazon Bedrock provider

```go
import "github.com/camilbinas/gude-agents/agent/provider/bedrock"

prov, err := bedrock.New("anthropic.claude-3-5-sonnet-20241022-v2:0",
    bedrock.WithRegion("us-east-1"),
    bedrock.WithMaxTokens(4096),
)
a, err := agent.New(prov, "Answer clearly.")
```

The provider uses the AWS default credential chain. `WithAPIKey` supports Bedrock API keys; options also cover region, maximum tokens, thinking effort/budget, Bedrock Guardrails, system-prompt caching for supported models, and safe URL-fetch timeout/private-network policy for remote attachments.

Use model IDs or inference-profile IDs available in the configured region. Permissions commonly include model invocation and streaming invocation, plus guardrail permissions when enabled. Keep the provider's content guardrail distinct from application [guardrails](../guardrails.md).

Bedrock integrations also include RAG embedders, Knowledge Base retrieval, and reranking; see [RAG](../rag.md).
