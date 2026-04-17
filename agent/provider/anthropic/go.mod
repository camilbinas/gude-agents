module github.com/camilbinas/gude-agents/agent/provider/anthropic

go 1.25.0

require (
	github.com/anthropics/anthropic-sdk-go v1.33.0
	github.com/camilbinas/gude-agents v0.0.0
	pgregory.net/rapid v1.2.0
)

require (
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	golang.org/x/sync v0.19.0 // indirect
)

replace (
	github.com/camilbinas/gude-agents => ../../../
	github.com/camilbinas/gude-agents/agent/mcp => ../../mcp
	github.com/camilbinas/gude-agents/agent/memory/dynamodb => ../../memory/dynamodb
	github.com/camilbinas/gude-agents/agent/memory/redis => ../../memory/redis
	github.com/camilbinas/gude-agents/agent/memory/s3 => ../../memory/s3
	github.com/camilbinas/gude-agents/agent/provider/bedrock => ../bedrock
	github.com/camilbinas/gude-agents/agent/provider/fallback => ../fallback
	github.com/camilbinas/gude-agents/agent/provider/gemini => ../gemini
	github.com/camilbinas/gude-agents/agent/provider/openai => ../openai
	github.com/camilbinas/gude-agents/agent/provider/registry => ../registry
	github.com/camilbinas/gude-agents/agent/rag/bedrock => ../../rag/bedrock
	github.com/camilbinas/gude-agents/agent/rag/openai => ../../rag/openai
	github.com/camilbinas/gude-agents/agent/rag/redis => ../../rag/redis
)
