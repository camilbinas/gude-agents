module github.com/camilbinas/gude-agents/agent/rag/gemini

go 1.25.0

require google.golang.org/genai v1.54.0

require (
	cloud.google.com/go v0.116.0 // indirect
	cloud.google.com/go/auth v0.9.3 // indirect
	cloud.google.com/go/compute/metadata v0.5.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/s2a-go v0.1.8 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.4 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	go.opencensus.io v0.24.0 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/net v0.38.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240903143218-8af14fe29dc1 // indirect
	google.golang.org/grpc v1.66.2 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
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
	github.com/camilbinas/gude-agents/agent/rag/postgres => ../postgres
	github.com/camilbinas/gude-agents/agent/rag/redis => ../redis
)
