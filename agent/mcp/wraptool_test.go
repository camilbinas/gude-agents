package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// inMemoryClient wires an MCP server (with the given tools registered) to a
// Client over an in-memory transport, so wrapTool can be exercised end to end
// without a subprocess or network.
func inMemoryClient(t *testing.T, register func(*sdkmcp.Server)) *Client {
	t.Helper()
	ctx := context.Background()

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test-server", Version: "v0"}, nil)
	register(server)

	clientT, serverT := sdkmcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}

	sdkClient := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "gude-agents", Version: "v1.0.0"}, nil)
	session, err := sdkClient.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	c := &Client{session: session}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestWrapTool_SchemaConversionAndPrefix(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"city": map[string]any{"type": "string"},
		},
		"required": []any{"city"},
	}
	c := inMemoryClient(t, func(s *sdkmcp.Server) {
		s.AddTool(&sdkmcp.Tool{Name: "weather", Description: "get weather", InputSchema: schema},
			func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "sunny"}}}, nil
			})
	})

	tools, err := c.Tools(context.Background(), WithToolPrefix("ext_"))
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	tl := tools[0]
	if tl.Spec.Name != "ext_weather" {
		t.Errorf("exposed name = %q, want ext_weather", tl.Spec.Name)
	}
	if tl.Spec.Description != "get weather" {
		t.Errorf("description = %q", tl.Spec.Description)
	}
	// Schema must be converted to map[string]any with properties preserved.
	if tl.Spec.InputSchema["type"] != "object" {
		t.Errorf("schema type = %v, want object", tl.Spec.InputSchema["type"])
	}
	props, ok := tl.Spec.InputSchema["properties"].(map[string]any)
	if !ok || props["city"] == nil {
		t.Errorf("schema properties not preserved: %v", tl.Spec.InputSchema)
	}

	// Calling the wrapped tool reaches the server under the ORIGINAL name.
	out, err := tl.Handler(context.Background(), json.RawMessage(`{"city":"paris"}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if out != "sunny" {
		t.Errorf("handler output = %q, want sunny", out)
	}
}

func TestWrapTool_IsErrorMapsToError(t *testing.T) {
	c := inMemoryClient(t, func(s *sdkmcp.Server) {
		s.AddTool(&sdkmcp.Tool{Name: "boom", Description: "always fails", InputSchema: map[string]any{"type": "object"}},
			func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				return &sdkmcp.CallToolResult{
					IsError: true,
					Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "upstream exploded"}},
				}, nil
			})
	})

	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	out, err := tools[0].Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected IsError result to map to a Go error")
	}
	// The error wraps the tool name and the error text, and the text is still
	// returned so callers that ignore the error can see it.
	if !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "upstream exploded") {
		t.Errorf("error = %v, want tool name + text", err)
	}
	if out != "upstream exploded" {
		t.Errorf("returned text = %q, want the error content", out)
	}
}

func TestTools_IncludeAndExcludeMutuallyExclusive(t *testing.T) {
	c := inMemoryClient(t, func(s *sdkmcp.Server) {
		s.AddTool(&sdkmcp.Tool{Name: "a", Description: "a", InputSchema: map[string]any{"type": "object"}},
			func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				return &sdkmcp.CallToolResult{}, nil
			})
	})
	_, err := c.Tools(context.Background(), IncludeTools("a"), ExcludeTools("b"))
	if err == nil {
		t.Fatal("expected error when IncludeTools and ExcludeTools are both set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %v, want mutually-exclusive message", err)
	}
}

func TestWrapTool_ConcurrentCallTool(t *testing.T) {
	c := inMemoryClient(t, func(s *sdkmcp.Server) {
		s.AddTool(&sdkmcp.Tool{Name: "echo", Description: "echo", InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"v": map[string]any{"type": "string"}},
		}},
			func(_ context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				var args struct {
					V string `json:"v"`
				}
				_ = json.Unmarshal(req.Params.Arguments, &args)
				return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: args.V}}}, nil
			})
	})
	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	h := tools[0].Handler

	// The single session must tolerate concurrent CallTool (verified against
	// the SDK's jsonrpc2 multiplexing); run under -race to catch a regression.
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			want := string(rune('a' + i%26))
			out, err := h(context.Background(), json.RawMessage(`{"v":"`+want+`"}`))
			if err != nil {
				errs <- err
				return
			}
			if out != want {
				errs <- errMismatch(out, want)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

type mismatchError struct{ got, want string }

func (e mismatchError) Error() string { return "got " + e.got + ", want " + e.want }
func errMismatch(got, want string) error {
	return mismatchError{got: got, want: want}
}
