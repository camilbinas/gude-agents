module github.com/camilbinas/gude-agents/examples

go 1.26.0

require (
	github.com/aws/aws-sdk-go-v2/config v1.32.16
	github.com/camilbinas/gude-agents v0.55.1
	github.com/camilbinas/gude-agents/agent/conversation/dynamodb v0.55.1
	github.com/camilbinas/gude-agents/agent/conversation/postgres v0.55.1
	github.com/camilbinas/gude-agents/agent/conversation/redis v0.55.1
	github.com/camilbinas/gude-agents/agent/conversation/s3 v0.55.1
	github.com/camilbinas/gude-agents/agent/eval v0.55.1
	github.com/camilbinas/gude-agents/agent/logging/debug v0.55.1
	github.com/camilbinas/gude-agents/agent/logging/slog v0.55.1
	github.com/camilbinas/gude-agents/agent/mcp v0.55.1
	github.com/camilbinas/gude-agents/agent/memory/postgres v0.55.1
	github.com/camilbinas/gude-agents/agent/memory/redis v0.55.1
	github.com/camilbinas/gude-agents/agent/metrics/cloudwatch v0.55.1
	github.com/camilbinas/gude-agents/agent/metrics/otel v0.55.1
	github.com/camilbinas/gude-agents/agent/metrics/prometheus v0.55.1
	github.com/camilbinas/gude-agents/agent/provider/anthropic v0.55.1
	github.com/camilbinas/gude-agents/agent/provider/bedrock v0.55.1
	github.com/camilbinas/gude-agents/agent/provider/fallback v0.55.1
	github.com/camilbinas/gude-agents/agent/provider/gemini v0.55.1
	github.com/camilbinas/gude-agents/agent/provider/openai v0.55.1
	github.com/camilbinas/gude-agents/agent/rag/bedrock v0.55.1
	github.com/camilbinas/gude-agents/agent/rag/document/pdf v0.55.1
	github.com/camilbinas/gude-agents/agent/rag/openai v0.55.1
	github.com/camilbinas/gude-agents/agent/rag/postgres v0.55.1
	github.com/camilbinas/gude-agents/agent/rag/redis v0.55.1
	github.com/camilbinas/gude-agents/agent/tool/webfetch/markdown v0.55.1
	github.com/camilbinas/gude-agents/agent/tracing v0.55.1
	github.com/camilbinas/gude-agents/agent/tracing/sentry v0.55.1
	github.com/gofiber/fiber/v3 v3.2.0
	github.com/jackc/pgx/v5 v5.7.5
	github.com/joho/godotenv v1.5.1
	github.com/modelcontextprotocol/go-sdk v1.5.0
	go.opentelemetry.io/otel v1.43.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.43.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.35.0
	go.opentelemetry.io/otel/exporters/stdout/stdoutmetric v1.43.0
	go.opentelemetry.io/otel/sdk v1.43.0
	go.opentelemetry.io/otel/sdk/metric v1.43.0
	go.opentelemetry.io/otel/trace v1.43.0
)

require (
	cloud.google.com/go v0.116.0 // indirect
	cloud.google.com/go/auth v0.9.3 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/JohannesKaufmann/dom v0.2.0 // indirect
	github.com/JohannesKaufmann/html-to-markdown/v2 v2.3.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.57.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.11.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/s3 v1.99.0 // indirect
	github.com/camilbinas/gude-agents/agent/rag/gemini v0.55.1 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/s2a-go v0.1.8 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.4 // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674
	github.com/ledongthuc/pdf v0.0.0-20250511090121-5959a4027728 // indirect
	go.opencensus.io v0.24.0 // indirect
	google.golang.org/genai v1.54.0 // indirect
)

require (
	github.com/andybalholm/brotli v1.2.1 // indirect
	github.com/anthropics/anthropic-sdk-go v1.33.0 // indirect
	github.com/aws/aws-lambda-go v1.54.0 // indirect
	github.com/aws/aws-sdk-go-v2 v1.41.7 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.9 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.15 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.22 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.23 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.23 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.24 // indirect
	github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime v1.51.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/bedrockruntime v1.50.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/cloudwatch v1.56.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/eks v1.83.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.23 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.0.10 // indirect
	github.com/aws/aws-sdk-go-v2/service/sns v1.39.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.30.16 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.35.20 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.42.1 // indirect
	github.com/aws/smithy-go v1.25.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/camilbinas/gude-agents/agent/graph/checkpointer/redis v0.55.1 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/getsentry/sentry-go v0.45.1 // indirect
	github.com/getsentry/sentry-go/otel v0.45.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/gofiber/schema v1.7.1 // indirect
	github.com/gofiber/utils/v2 v2.0.4 // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.28.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.21 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/openai/openai-go/v3 v3.31.0 // indirect
	github.com/pgvector/pgvector-go v0.3.0 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/prometheus/client_golang v1.23.2 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/redis/go-redis/v9 v9.18.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.70.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.43.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.35.0 // indirect
	go.opentelemetry.io/otel/metric v1.43.0 // indirect
	go.opentelemetry.io/proto/otlp v1.10.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.yaml.in/yaml/v2 v2.4.3 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260401024825-9d38bb4040a9 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260401024825-9d38bb4040a9 // indirect
	google.golang.org/grpc v1.80.0 // indirect
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af // indirect
	k8s.io/api v0.36.0 // indirect
	k8s.io/apimachinery v0.36.0 // indirect
	k8s.io/client-go v0.36.0 // indirect
)
