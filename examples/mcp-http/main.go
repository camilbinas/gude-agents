// Example: Connecting to a remote MCP server over HTTP.
//
// This example shows both transport options:
//   - Streamable HTTP (current MCP spec, 2025-03-26+)
//   - SSE (legacy transport, pre-2025-03-26)
//
// It spins up a local in-process MCP server to demonstrate the connection
// without requiring an external server. In production, replace the URL with
// your remote MCP server endpoint.
//
// Run:
//
//	go run ./examples/mcp-http
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/mcp"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	ctx := context.Background()

	// --- Start a local MCP server for demonstration ---
	// In production, replace these URLs with your remote server:
	//   streamableURL := "https://my-mcp-server.example.com/mcp"
	//   sseURL        := "https://legacy-mcp-server.example.com/sse"
	addr, shutdown := startLocalMCPServer()
	defer shutdown()

	streamableURL := "http://" + addr + "/mcp"
	sseURL := "http://" + addr + "/sse"

	// --- Option A: Streamable HTTP (current MCP spec) ---
	fmt.Println("Connecting via Streamable HTTP...")
	streamableClient, err := mcp.NewStreamableClient(ctx, streamableURL)
	if err != nil {
		log.Fatalf("streamable connect: %v", err)
	}
	defer streamableClient.Close()

	tools, err := streamableClient.Tools(ctx)
	if err != nil {
		log.Fatalf("tools: %v", err)
	}
	fmt.Printf("Discovered %d tools via Streamable HTTP\n", len(tools))
	for _, t := range tools {
		fmt.Printf("  - %s: %s\n", t.Spec.Name, t.Spec.Description)
	}

	// --- Option B: SSE (legacy transport) ---
	fmt.Println("\nConnecting via SSE...")
	sseClient, err := mcp.NewSSEClient(ctx, sseURL)
	if err != nil {
		log.Fatalf("sse connect: %v", err)
	}
	defer sseClient.Close()

	sseTools, err := sseClient.Tools(ctx)
	if err != nil {
		log.Fatalf("sse tools: %v", err)
	}
	fmt.Printf("Discovered %d tools via SSE\n\n", len(sseTools))

	// --- Use the tools with an agent ---
	provider, err := bedrock.ClaudeSonnet4_6()
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant. Use the available tools when needed. Be concise."),
		tools,
	)
	if err != nil {
		log.Fatal(err)
	}

	result, usage, err := a.Invoke(ctx, "Use the greet tool to greet the world.")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Response:", result)
	fmt.Printf("Tokens: %d in, %d out\n", usage.InputTokens, usage.OutputTokens)
}

// startLocalMCPServer starts an in-process MCP server with a demo tool
// and returns its address and a shutdown function.
func startLocalMCPServer() (addr string, shutdown func()) {
	s := sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: "demo-server", Version: "v1.0.0"},
		nil,
	)

	type greetInput struct {
		Name string `json:"name" jsonschema:"description=The name to greet"`
	}

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "greet",
		Description: "Greet someone by name",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in greetInput) (*sdkmcp.CallToolResult, any, error) {
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: fmt.Sprintf("Hello, %s! Greetings from the HTTP MCP server.", in.Name)},
			},
		}, nil, nil
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", sdkmcp.NewStreamableHTTPHandler(func(r *http.Request) *sdkmcp.Server { return s }, nil))
	mux.Handle("/sse", sdkmcp.NewSSEHandler(func(r *http.Request) *sdkmcp.Server { return s }, nil))

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)

	return ln.Addr().String(), func() { srv.Close() }
}
