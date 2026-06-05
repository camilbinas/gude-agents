package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
)

// wellKnownAgentPath is the well-known path for agent card discovery.
const wellKnownAgentPath = "/.well-known/agent.json"

// Client connects to a remote A2A agent and exposes its skills as tool.Tool values.
// It mirrors the MCP client pattern: construct → Tools() → use → Close().
//
// Client is safe for concurrent use from multiple goroutines after construction.
type Client struct {
	baseURL    string
	httpClient *http.Client
	card       *a2a.AgentCard

	mu    sync.RWMutex
	tools []tool.Tool
}

// ClientOption configures the A2A Client.
type ClientOption func(*clientConfig)

type clientConfig struct {
	httpClient *http.Client
}

// WithClientHTTPClient sets a custom HTTP client for card discovery and task execution.
func WithClientHTTPClient(hc *http.Client) ClientOption {
	return func(cfg *clientConfig) {
		cfg.httpClient = hc
	}
}

// NewClient creates an A2A client by fetching the Agent Card from the remote agent.
// It fetches {baseURL}/.well-known/agent.json, parses the card, and stores it
// for later access. Returns an error if the card cannot be fetched or parsed.
func NewClient(ctx context.Context, baseURL string, opts ...ClientOption) (*Client, error) {
	cfg := &clientConfig{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Build the well-known URL.
	cardURL, err := url.JoinPath(baseURL, wellKnownAgentPath)
	if err != nil {
		return nil, fmt.Errorf("a2a client: invalid base URL: %w", err)
	}

	// Fetch the agent card.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cardURL, nil)
	if err != nil {
		return nil, fmt.Errorf("a2a client: creating request: %w", err)
	}

	resp, err := cfg.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("a2a client: fetching agent card: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("a2a client: agent card fetch returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("a2a client: reading agent card response: %w", err)
	}

	// Parse the agent card.
	var card a2a.AgentCard
	if err := json.Unmarshal(body, &card); err != nil {
		return nil, fmt.Errorf("a2a client: parsing agent card: %w", err)
	}

	client := &Client{
		baseURL:    baseURL,
		httpClient: cfg.httpClient,
		card:       &card,
	}
	client.buildTools()

	return client, nil
}

// Card returns the discovered Agent Card.
func (c *Client) Card() *a2a.AgentCard {
	return c.card
}

// Tools returns tool.Tool values for the remote agent's skills.
// Options can filter which skills are exposed. Each tool sends a tasks/send
// JSON-RPC request when invoked.
func (c *Client) Tools(_ context.Context, opts ...ToolsOption) ([]tool.Tool, error) {
	cfg := &toolsConfig{}
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, fmt.Errorf("a2a client: applying tools option: %w", err)
		}
	}

	c.mu.RLock()
	allTools := c.tools
	c.mu.RUnlock()

	// If no filters, return all tools.
	if cfg.include == nil && cfg.exclude == nil {
		return allTools, nil
	}

	var filtered []tool.Tool
	for _, t := range allTools {
		name := t.Spec.Name

		// If include is set, only include tools in the include set.
		if cfg.include != nil {
			if _, ok := cfg.include[name]; !ok {
				continue
			}
		}

		// If exclude is set, skip tools in the exclude set.
		if cfg.exclude != nil {
			if _, ok := cfg.exclude[name]; ok {
				continue
			}
		}

		filtered = append(filtered, t)
	}

	return filtered, nil
}

// Close releases held resources. Safe to call multiple times.
func (c *Client) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// buildTools constructs tool.Tool values from the card's skills.
// Each tool sends a JSON-RPC SendMessage request to the remote agent when invoked.
func (c *Client) buildTools() {
	skills := c.card.Skills
	tools := make([]tool.Tool, 0, len(skills))

	for _, skill := range skills {
		t := tool.Tool{
			Spec: tool.Spec{
				Name:        skill.ID,
				Description: skill.Description,
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"message": map[string]any{
							"type":        "string",
							"description": "The message to send to the remote agent",
						},
					},
					"required": []string{"message"},
				},
			},
			Handler: c.makeToolHandler(skill.ID),
		}
		tools = append(tools, t)
	}

	c.mu.Lock()
	c.tools = tools
	c.mu.Unlock()
}

// jsonRPCRequest is the JSON-RPC 2.0 request envelope.
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      string `json:"id"`
}

// jsonRPCResponse is the JSON-RPC 2.0 response envelope.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// jsonRPCError is a JSON-RPC 2.0 error object.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// makeToolHandler creates a tool handler function for the given skill.
// The handler sends a SendMessage JSON-RPC request to the remote agent and
// extracts TextPart content from the completed task's artifacts.
func (c *Client) makeToolHandler(_ string) func(ctx context.Context, input json.RawMessage) (string, error) {
	return func(ctx context.Context, input json.RawMessage) (string, error) {
		// Extract "message" from input JSON.
		var params struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(input, &params); err != nil {
			return "", fmt.Errorf("a2a client: unmarshal tool input: %w", err)
		}

		// Build the SendMessage JSON-RPC request.
		msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(params.Message))
		sendReq := &a2a.SendMessageRequest{
			Message: msg,
		}

		rpcReq := &jsonRPCRequest{
			JSONRPC: "2.0",
			Method:  "SendMessage",
			Params:  sendReq,
			ID:      "1",
		}

		reqBody, err := json.Marshal(rpcReq)
		if err != nil {
			return "", fmt.Errorf("a2a client: marshal request: %w", err)
		}

		// Determine the endpoint URL. Use the first supported interface URL if available,
		// otherwise fall back to baseURL.
		endpoint := c.baseURL
		if len(c.card.SupportedInterfaces) > 0 {
			endpoint = c.card.SupportedInterfaces[0].URL
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
		if err != nil {
			return "", fmt.Errorf("a2a client: creating request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		// Propagate principal identity headers if a principal is set.
		if p, ok := agent.PrincipalFrom(ctx); ok {
			httpReq.Header.Set("X-Agent-Principal-ID", p.ID)
			if len(p.Roles) > 0 {
				httpReq.Header.Set("X-Agent-Principal-Roles", strings.Join(p.Roles, ","))
			}
			if len(p.Attrs) > 0 {
				if attrsJSON, err := json.Marshal(p.Attrs); err == nil {
					httpReq.Header.Set("X-Agent-Principal-Attrs", string(attrsJSON))
				}
			}
		}

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return "", fmt.Errorf("a2a client: sending request: %w", err)
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("a2a client: reading response: %w", err)
		}

		// Parse JSON-RPC response.
		var rpcResp jsonRPCResponse
		if err := json.Unmarshal(respBody, &rpcResp); err != nil {
			return "", fmt.Errorf("a2a client: parsing response: %w", err)
		}

		if rpcResp.Error != nil {
			return "", fmt.Errorf("a2a client: remote error: %s", rpcResp.Error.Message)
		}

		// The result can be a Task or a Message. Try Task first since that's
		// the expected response for tasks/send.
		var task a2a.Task
		if err := json.Unmarshal(rpcResp.Result, &task); err != nil {
			return "", fmt.Errorf("a2a client: parsing task result: %w", err)
		}

		// Check task status for failure.
		if task.Status.State == a2a.TaskStateFailed {
			failMsg := "task failed"
			if task.Status.Message != nil {
				// Extract text from the status message parts.
				for _, part := range task.Status.Message.Parts {
					if text := part.Text(); text != "" {
						failMsg = text
						break
					}
				}
			}
			return "", fmt.Errorf("a2a client: %s", failMsg)
		}

		// Extract TextPart content from artifacts.
		var texts []string
		for _, artifact := range task.Artifacts {
			for _, part := range artifact.Parts {
				if text := part.Text(); text != "" {
					texts = append(texts, text)
				}
			}
		}

		return strings.Join(texts, ""), nil
	}
}

// ToolsOption configures which skills are exposed as tools.
type ToolsOption func(*toolsConfig) error

type toolsConfig struct {
	include map[string]struct{}
	exclude map[string]struct{}
}

// IncludeSkills restricts Tools() to only the named skill IDs.
func IncludeSkills(ids ...string) ToolsOption {
	return func(cfg *toolsConfig) error {
		if cfg.include == nil {
			cfg.include = make(map[string]struct{})
		}
		for _, id := range ids {
			cfg.include[id] = struct{}{}
		}
		return nil
	}
}

// ExcludeSkills filters out the named skill IDs from Tools().
func ExcludeSkills(ids ...string) ToolsOption {
	return func(cfg *toolsConfig) error {
		if cfg.exclude == nil {
			cfg.exclude = make(map[string]struct{})
		}
		for _, id := range ids {
			cfg.exclude[id] = struct{}{}
		}
		return nil
	}
}
