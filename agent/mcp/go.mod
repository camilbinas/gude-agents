module github.com/camilbinas/gude-agents/agent/mcp

go 1.25.0

require (
	github.com/camilbinas/gude-agents v0.0.0
	github.com/modelcontextprotocol/go-sdk v1.5.0
)

require (
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

replace (
	github.com/camilbinas/gude-agents => ../../
	github.com/camilbinas/gude-agents/agent/memory/dynamodb => ../memory/dynamodb
	github.com/camilbinas/gude-agents/agent/memory/redis => ../memory/redis
	github.com/camilbinas/gude-agents/agent/memory/s3 => ../memory/s3
	github.com/camilbinas/gude-agents/agent/provider/anthropic => ../provider/anthropic
	github.com/camilbinas/gude-agents/agent/provider/bedrock => ../provider/bedrock
	github.com/camilbinas/gude-agents/agent/provider/fallback => ../provider/fallback
	github.com/camilbinas/gude-agents/agent/provider/gemini => ../provider/gemini
	github.com/camilbinas/gude-agents/agent/provider/openai => ../provider/openai
	github.com/camilbinas/gude-agents/agent/provider/registry => ../provider/registry
	github.com/camilbinas/gude-agents/agent/rag/bedrock => ../rag/bedrock
	github.com/camilbinas/gude-agents/agent/rag/openai => ../rag/openai
	github.com/camilbinas/gude-agents/agent/rag/redis => ../rag/redis
)
