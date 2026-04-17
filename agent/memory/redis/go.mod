module github.com/camilbinas/gude-agents/agent/memory/redis

go 1.25.0

require (
	github.com/camilbinas/gude-agents v0.0.0
	github.com/redis/go-redis/v9 v9.18.0
	pgregory.net/rapid v1.2.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	go.uber.org/atomic v1.11.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/camilbinas/gude-agents => ../../../
	github.com/camilbinas/gude-agents/agent/mcp => ../../mcp
	github.com/camilbinas/gude-agents/agent/memory/dynamodb => ../dynamodb
	github.com/camilbinas/gude-agents/agent/memory/s3 => ../s3
	github.com/camilbinas/gude-agents/agent/provider/anthropic => ../../provider/anthropic
	github.com/camilbinas/gude-agents/agent/provider/bedrock => ../../provider/bedrock
	github.com/camilbinas/gude-agents/agent/provider/fallback => ../../provider/fallback
	github.com/camilbinas/gude-agents/agent/provider/gemini => ../../provider/gemini
	github.com/camilbinas/gude-agents/agent/provider/openai => ../../provider/openai
	github.com/camilbinas/gude-agents/agent/provider/registry => ../../provider/registry
	github.com/camilbinas/gude-agents/agent/rag/bedrock => ../../rag/bedrock
	github.com/camilbinas/gude-agents/agent/rag/openai => ../../rag/openai
	github.com/camilbinas/gude-agents/agent/rag/redis => ../../rag/redis
)
