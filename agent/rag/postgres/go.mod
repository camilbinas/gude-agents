module github.com/camilbinas/gude-agents/agent/rag/postgres

go 1.25.0

require (
	github.com/camilbinas/gude-agents v0.0.0
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.7.5
	github.com/pgvector/pgvector-go v0.3.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)

replace (
	github.com/camilbinas/gude-agents => ../../../
	github.com/camilbinas/gude-agents/agent/mcp => ../../mcp
	github.com/camilbinas/gude-agents/agent/memory/dynamodb => ../../memory/dynamodb
	github.com/camilbinas/gude-agents/agent/memory/postgres => ../../memory/postgres
	github.com/camilbinas/gude-agents/agent/memory/redis => ../../memory/redis
	github.com/camilbinas/gude-agents/agent/memory/s3 => ../../memory/s3
	github.com/camilbinas/gude-agents/agent/memory/sqlite => ../../memory/sqlite
	github.com/camilbinas/gude-agents/agent/provider/anthropic => ../../provider/anthropic
	github.com/camilbinas/gude-agents/agent/provider/bedrock => ../../provider/bedrock
	github.com/camilbinas/gude-agents/agent/provider/fallback => ../../provider/fallback
	github.com/camilbinas/gude-agents/agent/provider/gemini => ../../provider/gemini
	github.com/camilbinas/gude-agents/agent/provider/openai => ../../provider/openai
	github.com/camilbinas/gude-agents/agent/provider/registry => ../../provider/registry
	github.com/camilbinas/gude-agents/agent/rag/bedrock => ../bedrock
	github.com/camilbinas/gude-agents/agent/rag/openai => ../openai
	github.com/camilbinas/gude-agents/agent/rag/redis => ../redis
)
