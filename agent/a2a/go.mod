module github.com/camilbinas/gude-agents/agent/a2a

go 1.25.0

require (
	github.com/a2aproject/a2a-go/v2 v2.3.1
	github.com/camilbinas/gude-agents v0.81.0
	google.golang.org/grpc v1.80.0
	pgregory.net/rapid v1.2.0
)

require (
	github.com/google/uuid v1.6.0 // indirect
	go.opentelemetry.io/otel v1.43.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.43.0 // indirect
	golang.org/x/mod v0.36.0 // indirect
	golang.org/x/net v0.54.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260427160629-7cedc36a6bc4 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260427160629-7cedc36a6bc4 // indirect
	google.golang.org/protobuf v1.36.12-0.20260120151049-f2248ac996af // indirect
)

replace github.com/camilbinas/gude-agents => ../../
