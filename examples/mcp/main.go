// Run: go run ./mcp
//
// Connect to a Streamable HTTP MCP server, discover its tools, add them to an
// Agent, and invoke the Agent. This example starts a local server so no
// external MCP service is needed; replace the URL in production.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/mcp"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	ctx := agent.Background()
	endpoint, closeServer := startServer()
	defer closeServer()

	client, err := mcp.NewStreamableClient(ctx, endpoint)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	tools, err := client.Tools(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Discovered %d MCP tool(s)\n", len(tools))

	a, err := agent.New(bedrock.Must(bedrock.Standard()), "Use available tools when useful.", agent.WithTools(tools...))
	if err != nil {
		log.Fatal(err)
	}
	result, err := a.Invoke(ctx, "Use the greet tool to greet Ada.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Text)
}

func startServer() (string, func()) {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "gude-mcp-demo", Version: "1.0"}, nil)
	type input struct {
		Name string `json:"name" jsonschema:"the name to greet"`
	}
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "greet", Description: "Greet a person by name"}, func(_ context.Context, _ *sdkmcp.CallToolRequest, in input) (*sdkmcp.CallToolResult, any, error) {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "Hello, " + in.Name + "!"}}}, nil, nil
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	httpServer := &http.Server{Handler: sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil)}
	go httpServer.Serve(listener)
	return "http://" + listener.Addr().String(), func() { _ = httpServer.Shutdown(context.Background()) }
}
