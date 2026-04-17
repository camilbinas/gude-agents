// Package mcp provides an MCP (Model Context Protocol) client that connects
// to MCP servers and exposes their tools as gude-agents tool.Tool values.
//
// MCP tools are indistinguishable from local tools — they work with all
// agent features including middleware, guardrails, parallel execution,
// and multi-agent orchestration.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// Client connects to an MCP server and exposes its tools as gude-agents tool.Tool values.
type Client struct {
	session *sdkmcp.ClientSession
}

// Option configures the MCP client.
type Option func(*clientConfig)

type clientConfig struct {
	env        []string
	httpClient *http.Client
}

// WithEnv sets environment variables for the MCP server subprocess.
// Each entry should be in "KEY=VALUE" format. These are appended to
// the current process environment.
func WithEnv(env ...string) Option {
	return func(c *clientConfig) {
		c.env = append(c.env, env...)
	}
}

// WithHTTPClient sets a custom HTTP client for SSE and Streamable HTTP connections.
// Use this to configure timeouts, TLS, authentication headers, or proxies.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *clientConfig) {
		c.httpClient = hc
	}
}

// NewStdioClient connects to an MCP server via stdin/stdout of a subprocess.
// The command and args specify the server binary to run. Use WithEnv to pass
// environment variables (e.g., API keys) to the server process.
func NewStdioClient(ctx context.Context, command string, args []string, opts ...Option) (*Client, error) {
	cfg := &clientConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	cmd := exec.Command(command, args...)
	if len(cfg.env) > 0 {
		cmd.Env = append(cmd.Environ(), cfg.env...)
	}

	client := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "gude-agents", Version: "v1.0.0"},
		nil,
	)
	transport := &sdkmcp.CommandTransport{Command: cmd}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp connect: %w", err)
	}

	return &Client{session: session}, nil
}

// NewSSEClient connects to a remote MCP server using the legacy SSE transport
// (HTTP GET for server-sent events). Use this for servers that implement the
// pre-2025-03-26 MCP spec. For newer servers, prefer NewStreamableClient.
func NewSSEClient(ctx context.Context, endpoint string, opts ...Option) (*Client, error) {
	cfg := &clientConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	client := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "gude-agents", Version: "v1.0.0"},
		nil,
	)
	transport := &sdkmcp.SSEClientTransport{
		Endpoint:   endpoint,
		HTTPClient: cfg.httpClient,
	}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp sse connect: %w", err)
	}

	return &Client{session: session}, nil
}

// NewStreamableClient connects to a remote MCP server using the Streamable HTTP
// transport (the current MCP standard as of 2025-03-26). This is the recommended
// transport for remote MCP servers.
func NewStreamableClient(ctx context.Context, endpoint string, opts ...Option) (*Client, error) {
	cfg := &clientConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	client := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "gude-agents", Version: "v1.0.0"},
		nil,
	)
	transport := &sdkmcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: cfg.httpClient,
	}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp streamable connect: %w", err)
	}

	return &Client{session: session}, nil
}

// Tools discovers all tools from the MCP server and returns them as
// gude-agents tool.Tool values, ready to pass to agent.New or any preset.
// Handles pagination automatically if the server returns multiple pages.
//
// Use IncludeTools or ExcludeTools to filter which tools are returned:
//
//	client.Tools(ctx, mcp.IncludeTools("read_file", "write_file"))
//	client.Tools(ctx, mcp.ExcludeTools("delete_file"))
func (c *Client) Tools(ctx context.Context, opts ...ToolsOption) ([]tool.Tool, error) {
	cfg := &toolsConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	var tools []tool.Tool
	for t, err := range c.session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("mcp list tools: %w", err)
		}
		if !cfg.allow(t.Name) {
			continue
		}
		wrapped, err := c.wrapTool(t)
		if err != nil {
			return nil, fmt.Errorf("mcp wrap tool %q: %w", t.Name, err)
		}
		tools = append(tools, wrapped)
	}
	return tools, nil
}

// ToolsOption configures which tools are returned by Tools().
type ToolsOption func(*toolsConfig)

type toolsConfig struct {
	include map[string]struct{}
	exclude map[string]struct{}
}

func (c *toolsConfig) allow(name string) bool {
	if len(c.include) > 0 {
		_, ok := c.include[name]
		return ok
	}
	if len(c.exclude) > 0 {
		_, ok := c.exclude[name]
		return !ok
	}
	return true
}

// IncludeTools restricts Tools() to only the named tools.
// Cannot be combined with ExcludeTools; IncludeTools takes precedence.
func IncludeTools(names ...string) ToolsOption {
	return func(c *toolsConfig) {
		if c.include == nil {
			c.include = make(map[string]struct{})
		}
		for _, n := range names {
			c.include[n] = struct{}{}
		}
	}
}

// ExcludeTools filters out the named tools from Tools().
// Ignored if IncludeTools is also provided.
func ExcludeTools(names ...string) ToolsOption {
	return func(c *toolsConfig) {
		if c.exclude == nil {
			c.exclude = make(map[string]struct{})
		}
		for _, n := range names {
			c.exclude[n] = struct{}{}
		}
	}
}

// wrapTool converts a single MCP tool definition into a gude-agents tool.Tool.
func (c *Client) wrapTool(mcpTool *sdkmcp.Tool) (tool.Tool, error) {
	// Convert InputSchema (any) to map[string]any via JSON round-trip.
	// From the client side, the SDK deserializes this as map[string]any.
	schema, err := toSchemaMap(mcpTool.InputSchema)
	if err != nil {
		return tool.Tool{}, fmt.Errorf("convert schema: %w", err)
	}

	name := mcpTool.Name
	description := mcpTool.Description

	return tool.NewRaw(name, description, schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var args map[string]any
			if len(input) > 0 {
				if err := json.Unmarshal(input, &args); err != nil {
					return "", fmt.Errorf("mcp tool %s: unmarshal input: %w", name, err)
				}
			}

			res, err := c.session.CallTool(ctx, &sdkmcp.CallToolParams{
				Name:      name,
				Arguments: args,
			})
			if err != nil {
				return "", fmt.Errorf("mcp tool %s: %w", name, err)
			}

			text := extractText(res.Content)

			if res.IsError {
				return text, fmt.Errorf("mcp tool %s returned error: %s", name, text)
			}

			return text, nil
		},
	), nil
}

// Close disconnects from the MCP server and terminates the subprocess.
func (c *Client) Close() error {
	return c.session.Close()
}

// toSchemaMap converts an InputSchema value to map[string]any.
// The MCP SDK deserializes client-side schemas as map[string]any,
// but we handle other types via JSON round-trip as a fallback.
func toSchemaMap(schema any) (map[string]any, error) {
	if schema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}, nil
	}
	if m, ok := schema.(map[string]any); ok {
		return m, nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// extractText pulls text content from MCP Content interface values.
func extractText(content []sdkmcp.Content) string {
	var result string
	for _, c := range content {
		if tc, ok := c.(*sdkmcp.TextContent); ok && tc.Text != "" {
			if result != "" {
				result += "\n"
			}
			result += tc.Text
		}
	}
	return result
}
