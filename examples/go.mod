module github.com/camilbinas/gude-agents/examples

go 1.25.0

require (
	github.com/aws/aws-sdk-go-v2/config v1.29.4
	github.com/camilbinas/gude-agents v0.0.0
	github.com/camilbinas/gude-agents/agent/mcp v0.0.0
	github.com/camilbinas/gude-agents/agent/memory/dynamodb v0.0.0
	github.com/camilbinas/gude-agents/agent/memory/postgres v0.0.0
	github.com/camilbinas/gude-agents/agent/memory/redis v0.0.0
	github.com/camilbinas/gude-agents/agent/memory/s3 v0.0.0
	github.com/camilbinas/gude-agents/agent/provider/anthropic v0.0.0
	github.com/camilbinas/gude-agents/agent/provider/bedrock v0.0.0
	github.com/camilbinas/gude-agents/agent/provider/fallback v0.0.0
	github.com/camilbinas/gude-agents/agent/provider/openai v0.0.0
	github.com/camilbinas/gude-agents/agent/rag/bedrock v0.0.0
	github.com/camilbinas/gude-agents/agent/rag/openai v0.0.0
	github.com/camilbinas/gude-agents/agent/rag/postgres v0.0.0
	github.com/camilbinas/gude-agents/agent/rag/redis v0.0.0
	github.com/jackc/pgx/v5 v5.7.5
	github.com/joho/godotenv v1.5.1
	github.com/modelcontextprotocol/go-sdk v1.5.0
)

require (
	github.com/anthropics/anthropic-sdk-go v1.33.0 // indirect
	github.com/aws/aws-sdk-go-v2 v1.41.5 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.8 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.17.57 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.27 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.21 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.21 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.2 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.22 // indirect
	github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime v1.51.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/bedrockruntime v1.50.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.57.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.11.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/s3 v1.99.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.24.14 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.28.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.33.12 // indirect
	github.com/aws/smithy-go v1.24.2 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/openai/openai-go/v3 v3.31.0 // indirect
	github.com/pgvector/pgvector-go v0.3.0 // indirect
	github.com/redis/go-redis/v9 v9.18.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/crypto v0.40.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.27.0 // indirect
)

replace (
	github.com/camilbinas/gude-agents => ../
	github.com/camilbinas/gude-agents/agent/mcp => ../agent/mcp
	github.com/camilbinas/gude-agents/agent/memory/dynamodb => ../agent/memory/dynamodb
	github.com/camilbinas/gude-agents/agent/memory/postgres => ../agent/memory/postgres
	github.com/camilbinas/gude-agents/agent/memory/redis => ../agent/memory/redis
	github.com/camilbinas/gude-agents/agent/memory/s3 => ../agent/memory/s3
	github.com/camilbinas/gude-agents/agent/provider/anthropic => ../agent/provider/anthropic
	github.com/camilbinas/gude-agents/agent/provider/bedrock => ../agent/provider/bedrock
	github.com/camilbinas/gude-agents/agent/provider/fallback => ../agent/provider/fallback
	github.com/camilbinas/gude-agents/agent/provider/gemini => ../agent/provider/gemini
	github.com/camilbinas/gude-agents/agent/provider/openai => ../agent/provider/openai
	github.com/camilbinas/gude-agents/agent/provider/registry => ../agent/provider/registry
	github.com/camilbinas/gude-agents/agent/rag/bedrock => ../agent/rag/bedrock
	github.com/camilbinas/gude-agents/agent/rag/openai => ../agent/rag/openai
	github.com/camilbinas/gude-agents/agent/rag/postgres => ../agent/rag/postgres
	github.com/camilbinas/gude-agents/agent/rag/redis => ../agent/rag/redis
)
