module github.com/camilbinas/gude-agents/agent/provider/fallback

go 1.25.0

require (
	github.com/camilbinas/gude-agents v0.0.0
	github.com/stretchr/testify v1.10.0
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/camilbinas/gude-agents => ../../../
	github.com/camilbinas/gude-agents/agent/mcp => ../../mcp
	github.com/camilbinas/gude-agents/agent/memory/dynamodb => ../../memory/dynamodb
	github.com/camilbinas/gude-agents/agent/memory/redis => ../../memory/redis
	github.com/camilbinas/gude-agents/agent/memory/s3 => ../../memory/s3
	github.com/camilbinas/gude-agents/agent/provider/anthropic => ../anthropic
	github.com/camilbinas/gude-agents/agent/provider/bedrock => ../bedrock
	github.com/camilbinas/gude-agents/agent/provider/gemini => ../gemini
	github.com/camilbinas/gude-agents/agent/provider/openai => ../openai
	github.com/camilbinas/gude-agents/agent/provider/registry => ../registry
	github.com/camilbinas/gude-agents/agent/rag/bedrock => ../../rag/bedrock
	github.com/camilbinas/gude-agents/agent/rag/openai => ../../rag/openai
	github.com/camilbinas/gude-agents/agent/rag/redis => ../../rag/redis
)
