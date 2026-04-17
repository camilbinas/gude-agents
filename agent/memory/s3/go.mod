module github.com/camilbinas/gude-agents/agent/memory/s3

go 1.25.0

require (
	github.com/aws/aws-sdk-go-v2 v1.41.5
	github.com/aws/aws-sdk-go-v2/service/s3 v1.99.0
	github.com/camilbinas/gude-agents v0.0.0
	pgregory.net/rapid v1.2.0
)

require (
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.8 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.21 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.21 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.22 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.21 // indirect
	github.com/aws/smithy-go v1.24.2 // indirect
)

replace (
	github.com/camilbinas/gude-agents => ../../../
	github.com/camilbinas/gude-agents/agent/mcp => ../../mcp
	github.com/camilbinas/gude-agents/agent/memory/dynamodb => ../dynamodb
	github.com/camilbinas/gude-agents/agent/memory/redis => ../redis
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
