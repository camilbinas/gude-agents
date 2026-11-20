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
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// Client connects to an MCP server and exposes its tools as gude-agents tool.Tool values.
//
// A Client wraps a single MCP session that is safe for concurrent CallTool
// invocations, so tools from Tools may run in parallel without extra locking.
// They share one transport; use Pool for high-throughput workloads.
type Client struct {
	session *sdkmcp.ClientSession
	limits  outputLimits
}

// Option configures the MCP client.
type Option func(*clientConfig)

type clientConfig struct {
	env        []string
	inheritEnv bool
	httpClient *http.Client
	limits     outputLimits
}

func newClientConfig(opts []Option) *clientConfig {
	cfg := &clientConfig{limits: defaultLimits()}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// WithEnv sets environment variables for the MCP server subprocess.
// Each entry should be in "KEY=VALUE" format. They are added to a minimal
// allowlisted subset of the current environment (PATH, HOME, LANG, LC_*,
// TMPDIR, USER, SHELL, ... on unix; SystemRoot, PATH, TEMP, ... on Windows).
// Secrets in the parent environment are NOT inherited unless WithInheritEnv
// is used.
func WithEnv(env ...string) Option {
	return func(c *clientConfig) {
		c.env = append(c.env, env...)
	}
}

// WithInheritEnv makes the stdio MCP server inherit the full parent
// environment, including any API keys and credentials. Opt in only for
// servers you fully trust.
func WithInheritEnv() Option {
	return func(c *clientConfig) { c.inheritEnv = true }
}

// WithMaxOutputBytes caps the text returned by one MCP tool call (default
// DefaultMaxOutputBytes). Longer output is truncated with a marker. n <= 0
// restores the default.
func WithMaxOutputBytes(n int) Option {
	return func(c *clientConfig) {
		if n <= 0 {
			n = DefaultMaxOutputBytes
		}
		c.limits.maxBytes = n
	}
}

// WithImageLimits caps how many images (default 4) and how many bytes per
// image (default 5MiB) are kept from one MCP tool call. Excess images are
// replaced by a text placeholder. Values <= 0 keep the default.
func WithImageLimits(maxImages, maxImageBytes int) Option {
	return func(c *clientConfig) {
		if maxImages > 0 {
			c.limits.maxImages = maxImages
		}
		if maxImageBytes > 0 {
			c.limits.maxImageBytes = maxImageBytes
		}
	}
}

// WithHTTPClient sets a custom HTTP client for Streamable HTTP connections.
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
	cfg := newClientConfig(opts)

	cmd := exec.Command(command, args...)
	cmd.Env = subprocessEnv(cfg.inheritEnv, cfg.env)

	client := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "gude-agents", Version: "v1.0.0"},
		nil,
	)
	transport := &sdkmcp.CommandTransport{Command: cmd}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp connect: %w", err)
	}

	return &Client{session: session, limits: cfg.limits}, nil
}

// NewStreamableClient connects to a remote MCP server using the Streamable HTTP
// transport (the current MCP standard as of 2025-03-26). This is the recommended
// transport for remote MCP servers.
func NewStreamableClient(ctx context.Context, endpoint string, opts ...Option) (*Client, error) {
	cfg := newClientConfig(opts)

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

	return &Client{session: session, limits: cfg.limits}, nil
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
	cfg, err := newToolsConfig(opts)
	if err != nil {
		return nil, err
	}

	var tools []tool.Tool
	seen := make(map[string]struct{})
	for t, err := range c.session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("mcp list tools: %w", err)
		}
		seen[t.Name] = struct{}{}
		if !cfg.allow(t.Name) {
			continue
		}
		wrapped, err := c.wrapTool(t, cfg.prefix)
		if err != nil {
			return nil, fmt.Errorf("mcp wrap tool %q: %w", t.Name, err)
		}
		tools = append(tools, wrapped)
	}
	if err := cfg.checkKnown(seen); err != nil {
		return nil, err
	}
	return tools, nil
}

func newToolsConfig(opts []ToolsOption) (*toolsConfig, error) {
	cfg := &toolsConfig{}
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, err
		}
	}
	if cfg.includeSet && cfg.excludeSet {
		return nil, errors.New("mcp: IncludeTools and ExcludeTools are mutually exclusive; pass only one")
	}
	return cfg, nil
}

// checkKnown fails when a filter names a tool the server does not expose, so
// a typo or renamed tool cannot silently widen or narrow the tool set.
func (c *toolsConfig) checkKnown(seen map[string]struct{}) error {
	var unknown []string
	for _, set := range []map[string]struct{}{c.include, c.exclude} {
		for n := range set {
			if _, ok := seen[n]; !ok {
				unknown = append(unknown, n)
			}
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("mcp: filter names unknown tool(s): %s", strings.Join(unknown, ", "))
}

// ToolsOption configures which tools are returned by Tools().
type ToolsOption func(*toolsConfig) error

type toolsConfig struct {
	include    map[string]struct{}
	exclude    map[string]struct{}
	includeSet bool
	excludeSet bool
	prefix     string
}

// allow fails closed: once IncludeTools was passed, only the named tools are
// allowed, so IncludeTools() with no names allows none.
func (c *toolsConfig) allow(name string) bool {
	if c.includeSet {
		_, ok := c.include[name]
		return ok
	}
	if c.excludeSet {
		_, ok := c.exclude[name]
		return !ok
	}
	return true
}

// WithToolPrefix prepends prefix to every tool name returned by Tools().
// Use this when connecting to multiple MCP servers that have overlapping
// tool names — e.g. two servers both expose a "search" tool:
//
//	webTools, _ := webClient.Tools(ctx, mcp.WithToolPrefix("web_"))
//	dbTools,  _ := dbClient.Tools(ctx,  mcp.WithToolPrefix("db_"))
func WithToolPrefix(prefix string) ToolsOption {
	return func(c *toolsConfig) error {
		c.prefix = prefix
		return nil
	}
}

// IncludeTools restricts Tools() to only the named tools. With no names it
// allows no tools. Naming a tool the server does not expose is an error.
// Cannot be combined with ExcludeTools; passing both returns an error.
func IncludeTools(names ...string) ToolsOption {
	return func(c *toolsConfig) error {
		c.includeSet = true
		if c.include == nil {
			c.include = make(map[string]struct{})
		}
		for _, n := range names {
			c.include[n] = struct{}{}
		}
		return nil
	}
}

// ExcludeTools filters out the named tools from Tools(). Naming a tool the
// server does not expose is an error.
// Cannot be combined with IncludeTools; passing both returns an error.
func ExcludeTools(names ...string) ToolsOption {
	return func(c *toolsConfig) error {
		c.excludeSet = true
		if c.exclude == nil {
			c.exclude = make(map[string]struct{})
		}
		for _, n := range names {
			c.exclude[n] = struct{}{}
		}
		return nil
	}
}

// wrapTool converts a single MCP tool definition into a gude-agents tool.Tool.
// prefix is prepended to the exposed tool name; the original name is used for CallTool.
func (c *Client) wrapTool(mcpTool *sdkmcp.Tool, prefix string) (tool.Tool, error) {
	schema, err := toSchemaMap(mcpTool.InputSchema)
	if err != nil {
		return tool.Tool{}, fmt.Errorf("convert schema: %w", err)
	}

	mcpName := mcpTool.Name // original name for CallTool
	exposedName := prefix + mcpName
	description := mcpTool.Description

	// A rich tool so image blocks reach the model as images; text, embedded
	// text resources and placeholders for non-carryable blocks go in Output.Text.
	return withTextHandler(tool.NewRich(
		exposedName,
		description,
		func(ctx context.Context, input json.RawMessage) (*tool.Output, error) {
			var args map[string]any
			if len(input) > 0 {
				if err := json.Unmarshal(input, &args); err != nil {
					return nil, fmt.Errorf("mcp tool %s: unmarshal input: %w", mcpName, err)
				}
			}

			res, err := c.session.CallTool(ctx, &sdkmcp.CallToolParams{
				Name:      mcpName,
				Arguments: args,
			})
			if err != nil {
				return nil, fmt.Errorf("mcp tool %s: %w", mcpName, err)
			}

			out := toOutputLimited(res.Content, c.limits)
			if res.IsError {
				return out, fmt.Errorf("mcp tool %s returned error: %s", mcpName, truncateText(out.Text, maxErrorTextBytes))
			}
			return out, nil
		},
		tool.WithSchema(schema),
	)), nil
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

// toOutput converts MCP Content blocks into a tool.Output. Text and embedded
// text resources become Text, images become Images, and audio, blobs and
// resource links the SDK cannot carry become a text placeholder naming the
// type and its URI/MIME so nothing disappears silently.
func toOutput(content []sdkmcp.Content) *tool.Output {
	return toOutputLimited(content, defaultLimits())
}

// toOutputLimited is toOutput with the text and image caps in lim applied.
func toOutputLimited(content []sdkmcp.Content, lim outputLimits) *tool.Output {
	d := defaultLimits()
	if lim.maxBytes <= 0 {
		lim.maxBytes = d.maxBytes
	}
	if lim.maxImages <= 0 {
		lim.maxImages = d.maxImages
	}
	if lim.maxImageBytes <= 0 {
		lim.maxImageBytes = d.maxImageBytes
	}
	out := &tool.Output{}
	var lines []string
	addText := func(s string) {
		if s != "" {
			lines = append(lines, s)
		}
	}
	for _, c := range content {
		switch v := c.(type) {
		case *sdkmcp.TextContent:
			addText(v.Text)
		case *sdkmcp.ImageContent:
			switch {
			case len(out.Images) >= lim.maxImages:
				addText(fmt.Sprintf("[image omitted: limit of %d images reached]", lim.maxImages))
			case len(v.Data) > lim.maxImageBytes:
				addText(fmt.Sprintf("[image omitted: %s, %d bytes exceeds limit of %d]", mimeOr(v.MIMEType, "image"), len(v.Data), lim.maxImageBytes))
			default:
				out.Images = append(out.Images, tool.Image{Data: v.Data, MIMEType: v.MIMEType})
			}
		case *sdkmcp.AudioContent:
			addText(fmt.Sprintf("[audio content omitted: %s, %d bytes]", mimeOr(v.MIMEType, "audio"), len(v.Data)))
		case *sdkmcp.EmbeddedResource:
			addText(embeddedResourceText(v))
		case *sdkmcp.ResourceLink:
			addText(fmt.Sprintf("[resource link: %s (%s)]", v.URI, mimeOr(v.MIMEType, "unknown type")))
		default:
			addText(fmt.Sprintf("[unsupported content type: %T]", c))
		}
	}
	out.Text = truncateText(join(lines), lim.maxBytes)
	return out
}

// embeddedResourceText includes embedded text resources inline and leaves a
// placeholder for binary blobs.
func embeddedResourceText(e *sdkmcp.EmbeddedResource) string {
	if e == nil || e.Resource == nil {
		return "[embedded resource omitted]"
	}
	r := e.Resource
	if r.Text != "" {
		return r.Text
	}
	return fmt.Sprintf("[embedded resource omitted: %s (%s), %d bytes]", r.URI, mimeOr(r.MIMEType, "unknown type"), len(r.Blob))
}

func mimeOr(mime, fallback string) string {
	if mime == "" {
		return fallback
	}
	return mime
}

func join(lines []string) string {
	result := ""
	for _, l := range lines {
		if result != "" {
			result += "\n"
		}
		result += l
	}
	return result
}

// withTextHandler also sets the plain Handler, so adapters that read it still
// get the text output.
func withTextHandler(t tool.Tool) tool.Tool {
	rich := t.RichHandler
	t.Handler = func(ctx context.Context, raw json.RawMessage) (string, error) {
		out, err := rich(ctx, raw)
		if out == nil {
			return "", err
		}
		return out.Text, err
	}
	return t
}
