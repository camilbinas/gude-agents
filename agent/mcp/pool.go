package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"

	"github.com/camilbinas/gude-agents/agent/tool"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

var errPoolClosed = errors.New("mcp pool: closed")

// Pool manages a pool of MCP server connections for high-concurrency use.
// Each tool call acquires a connection from the pool, executes the call,
// and returns the connection. This avoids funneling all requests through
// a single subprocess.
//
// The pool lazily creates connections up to maxSize. When all connections
// are busy, callers block until one is returned or the pool closes.
type Pool struct {
	command string
	args    []string
	env     []string

	mu       sync.Mutex
	cond     *sync.Cond
	size     int
	maxSize  int
	closed   bool
	sessions chan *sdkmcp.ClientSession // buffered channel acts as the idle pool
	toolDefs []*sdkmcp.Tool             // cached tool definitions from first connection
}

// PoolOption configures the connection pool.
type PoolOption func(*poolConfig) error

type poolConfig struct {
	env     []string
	maxSize int
}

// WithPoolEnv sets environment variables for all MCP server subprocesses.
func WithPoolEnv(env ...string) PoolOption {
	return func(c *poolConfig) error {
		c.env = append(c.env, env...)
		return nil
	}
}

// WithPoolSize sets the maximum number of concurrent MCP server connections.
// Defaults to 5 if not set. Returns an error if n is less than 1.
func WithPoolSize(n int) PoolOption {
	return func(c *poolConfig) error {
		if n < 1 {
			return fmt.Errorf("mcp pool: size must be >= 1, got %d", n)
		}
		c.maxSize = n
		return nil
	}
}

// NewPool creates a connection pool for an MCP stdio server. It starts one
// initial connection to discover tools and validate the server, then lazily
// creates additional connections up to maxSize as demand requires.
//
// Use Pool.Tools to get tool.Tool values that automatically use the pool
// for every call. Use Pool.Close to shut down all connections.
func NewPool(ctx context.Context, command string, args []string, opts ...PoolOption) (*Pool, error) {
	cfg := &poolConfig{maxSize: 5}
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, err
		}
	}

	p := &Pool{
		command:  command,
		args:     args,
		env:      cfg.env,
		maxSize:  cfg.maxSize,
		sessions: make(chan *sdkmcp.ClientSession, cfg.maxSize),
	}
	p.cond = sync.NewCond(&p.mu)

	// Start one connection to discover tools and validate the server.
	session, err := p.startSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp pool: initial connection: %w", err)
	}

	// Cache tool definitions from the first connection.
	var toolDefs []*sdkmcp.Tool
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			_ = session.Close()
			return nil, fmt.Errorf("mcp pool: list tools: %w", err)
		}
		toolDefs = append(toolDefs, t)
	}
	p.toolDefs = toolDefs

	p.mu.Lock()
	p.size = 1
	p.sessions <- session
	p.mu.Unlock()

	return p, nil
}

// startSession creates a new MCP server subprocess and connects to it.
func (p *Pool) startSession(ctx context.Context) (*sdkmcp.ClientSession, error) {
	cmd := exec.Command(p.command, p.args...)
	if len(p.env) > 0 {
		cmd.Env = append(cmd.Environ(), p.env...)
	}

	client := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "gude-agents-pool", Version: "v1.0.0"},
		nil,
	)
	transport := &sdkmcp.CommandTransport{Command: cmd}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	return session, nil
}

// acquire gets an idle connection from the channel, or creates a new one
// if the pool hasn't reached maxSize yet. If all connections are busy and
// the pool is at capacity, the caller blocks until one is returned or the
// pool is closed.
func (p *Pool) acquire(ctx context.Context) (*sdkmcp.ClientSession, error) {
	p.mu.Lock()
	for {
		if p.closed {
			p.mu.Unlock()
			return nil, errPoolClosed
		}

		select {
		case session := <-p.sessions:
			p.mu.Unlock()
			return session, nil
		default:
		}

		if p.size < p.maxSize {
			p.size++ // Reserve capacity while the connection is created.
			p.mu.Unlock()

			session, err := p.startSession(ctx)
			if err != nil {
				p.mu.Lock()
				p.size--
				p.cond.Broadcast()
				p.mu.Unlock()
				return nil, err
			}

			p.mu.Lock()
			if p.closed {
				p.size--
				p.cond.Broadcast()
				p.mu.Unlock()
				_ = session.Close()
				return nil, errPoolClosed
			}
			p.mu.Unlock()
			return session, nil
		}

		// A timed context cannot interrupt sync.Cond.Wait, so arrange for
		// cancellation to wake waiters while keeping all channel operations
		// serialized with Close and release.
		if err := ctx.Err(); err != nil {
			p.mu.Unlock()
			return nil, err
		}
		cancelled := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				p.mu.Lock()
				p.cond.Broadcast()
				p.mu.Unlock()
			case <-cancelled:
			}
		}()
		p.cond.Wait()
		close(cancelled)
	}
}

// release returns a connection to the idle pool. After Close, it closes only
// the returned lease; sessions actively in use are never closed by Close.
func (p *Pool) release(session *sdkmcp.ClientSession) {
	p.mu.Lock()
	if p.closed {
		p.size--
		p.cond.Broadcast()
		p.mu.Unlock()
		if session != nil {
			_ = session.Close()
		}
		return
	}
	p.sessions <- session
	p.cond.Signal()
	p.mu.Unlock()
}

// Tools returns tool.Tool values that use the pool for every call.
// Tool definitions are cached from the initial connection — this method
// does not make additional network calls.
//
// Each tool call acquires a connection, executes the MCP call, and returns
// the connection to the pool. Multiple goroutines can safely call these
// tools concurrently.
func (p *Pool) Tools() ([]tool.Tool, error) {
	tools := make([]tool.Tool, 0, len(p.toolDefs))
	for _, t := range p.toolDefs {
		wrapped, err := p.wrapTool(t)
		if err != nil {
			return nil, fmt.Errorf("mcp pool wrap tool %q: %w", t.Name, err)
		}
		tools = append(tools, wrapped)
	}
	return tools, nil
}

// wrapTool converts an MCP tool definition into a tool.Tool whose handler
// acquires a pooled connection for each call.
func (p *Pool) wrapTool(mcpTool *sdkmcp.Tool) (tool.Tool, error) {
	schema, err := toSchemaMap(mcpTool.InputSchema)
	if err != nil {
		return tool.Tool{}, fmt.Errorf("convert schema: %w", err)
	}

	name := mcpTool.Name
	description := mcpTool.Description

	return tool.NewRaw(
		name,
		description,
		schema,
		func(ctx context.Context, input json.RawMessage) (string, error) {
			var args map[string]any
			if len(input) > 0 {
				if err := json.Unmarshal(input, &args); err != nil {
					return "", fmt.Errorf("mcp tool %s: unmarshal input: %w", name, err)
				}
			}

			session, err := p.acquire(ctx)
			if err != nil {
				return "", fmt.Errorf("mcp tool %s: acquire connection: %w", name, err)
			}
			defer p.release(session)

			res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
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

// Close shuts down idle connections in the pool. It is safe to call multiple
// times. Connections already leased to a caller remain usable until release.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true

	idle := make([]*sdkmcp.ClientSession, 0, len(p.sessions))
	for {
		select {
		case session := <-p.sessions:
			idle = append(idle, session)
			p.size--
		default:
			p.cond.Broadcast()
			p.mu.Unlock()
			var firstErr error
			for _, session := range idle {
				if err := session.Close(); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		}
	}
}

// Size returns the current number of active connections in the pool.
func (p *Pool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.size
}
