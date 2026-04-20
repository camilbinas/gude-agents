module github.com/camilbinas/gude-agents

go 1.25.0

require pgregory.net/rapid v1.2.0

replace (
	github.com/camilbinas/gude-agents/agent/mcp => ./agent/mcp
	github.com/camilbinas/gude-agents/agent/metrics/prometheus => ./agent/metrics/prometheus
	github.com/camilbinas/gude-agents/agent/metrics/otel => ./agent/metrics/otel
	github.com/camilbinas/gude-agents/agent/metrics/cloudwatch => ./agent/metrics/cloudwatch
	github.com/camilbinas/gude-agents/agent/tracing => ./agent/tracing
	github.com/camilbinas/gude-agents/agent/tracing/sentry => ./agent/tracing/sentry
	github.com/camilbinas/gude-agents/agent/memory/dynamodb => ./agent/memory/dynamodb
	github.com/camilbinas/gude-agents/agent/memory/postgres => ./agent/memory/postgres
	github.com/camilbinas/gude-agents/agent/memory/redis => ./agent/memory/redis
	github.com/camilbinas/gude-agents/agent/memory/s3 => ./agent/memory/s3
	github.com/camilbinas/gude-agents/agent/memory/sqlite => ./agent/memory/sqlite
	github.com/camilbinas/gude-agents/agent/provider/anthropic => ./agent/provider/anthropic
	github.com/camilbinas/gude-agents/agent/provider/bedrock => ./agent/provider/bedrock
	github.com/camilbinas/gude-agents/agent/provider/fallback => ./agent/provider/fallback
	github.com/camilbinas/gude-agents/agent/provider/gemini => ./agent/provider/gemini
	github.com/camilbinas/gude-agents/agent/provider/openai => ./agent/provider/openai
	github.com/camilbinas/gude-agents/agent/provider/registry => ./agent/provider/registry
	github.com/camilbinas/gude-agents/agent/rag/bedrock => ./agent/rag/bedrock
	github.com/camilbinas/gude-agents/agent/rag/openai => ./agent/rag/openai
	github.com/camilbinas/gude-agents/agent/rag/redis => ./agent/rag/redis
	github.com/camilbinas/gude-agents/agent/rag/postgres => ./agent/rag/postgres
	github.com/camilbinas/gude-agents/agent/rag/gemini => ./agent/rag/gemini
)
