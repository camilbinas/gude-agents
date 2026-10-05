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

// wellKnownAgentCardPath is the current well-known path for agent card discovery.
const wellKnownAgentCardPath = "/.well-known/agent-card.json"

// wellKnownLegacyAgentPath is retained only for compatibility with older agents.
const wellKnownLegacyAgentPath = "/.well-known/agent.json"

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
// It first fetches {baseURL}/.well-known/agent-card.json and falls back to the
// legacy agent.json path only when the current path returns 404. It stores the
// parsed card for later access and returns an error if discovery or parsing fails.
func NewClient(ctx context.Context, baseURL string, opts ...ClientOption) (*Client, error) {
	cfg := &clientConfig{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(cfg)
	}

	cardURL, err := url.JoinPath(baseURL, wellKnownAgentCardPath)
	if err != nil {
		return nil, fmt.Errorf("a2a client: invalid base URL: %w", err)
	}
	body, status, err := fetchAgentCard(ctx, cfg.httpClient, cardURL)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		legacyURL, err := url.JoinPath(baseURL, wellKnownLegacyAgentPath)
		if err != nil {
			return nil, fmt.Errorf("a2a client: invalid base URL: %w", err)
		}
		body, status, err = fetchAgentCard(ctx, cfg.httpClient, legacyURL)
		if err != nil {
			return nil, err
		}
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("a2a client: agent card fetch returned HTTP %d", status)
	}

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

func fetchAgentCard(ctx context.Context, client *http.Client, cardURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cardURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("a2a client: creating request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("a2a client: fetching agent card: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("a2a client: reading agent card response: %w", err)
	}
	return body, resp.StatusCode, nil
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

		// Determine a JSON-RPC-compatible endpoint. Agent cards may advertise
		// other transports first, and interface URLs may be relative to baseURL.
		endpoint, err := c.jsonRPCEndpoint()
		if err != nil {
			return "", err
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

		return extractTextFromResult(rpcResp.Result)
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

// jsonRPCEndpoint selects a JSON-RPC interface from the card. When no such
// interface is advertised, retain the base URL behavior for older cards.
func (c *Client) jsonRPCEndpoint() (string, error) {
	for _, iface := range c.card.SupportedInterfaces {
		if iface == nil || iface.ProtocolBinding != a2a.TransportProtocolJSONRPC {
			continue
		}
		if strings.TrimSpace(iface.URL) == "" {
			return "", fmt.Errorf("a2a client: JSON-RPC interface has an empty URL")
		}
		return resolveEndpointURL(c.baseURL, iface.URL)
	}
	return c.baseURL, nil
}

func resolveEndpointURL(baseURL, endpoint string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		if err == nil {
			err = fmt.Errorf("base URL must be absolute")
		}
		return "", fmt.Errorf("a2a client: invalid base URL: %w", err)
	}
	target, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("a2a client: invalid JSON-RPC interface URL: %w", err)
	}
	if target.IsAbs() {
		return target.String(), nil
	}
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	return base.ResolveReference(target).String(), nil
}

func extractTextFromResult(result json.RawMessage) (string, error) {
	// JSON-RPC handlers may return a SendMessageResult envelope containing a
	// task or message, while older handlers return either value directly.
	// Normalize the envelope first so the rest of the decoder handles both.
	var envelope struct {
		Task    json.RawMessage `json:"task"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(result, &envelope); err == nil {
		switch {
		case len(envelope.Task) > 0:
			result = envelope.Task
		case len(envelope.Message) > 0:
			result = envelope.Message
		}
	}

	var shape struct {
		Parts  json.RawMessage `json:"parts"`
		Status json.RawMessage `json:"status"`
	}
	if err := json.Unmarshal(result, &shape); err != nil {
		return "", fmt.Errorf("a2a client: parsing result: %w", err)
	}

	if len(shape.Parts) > 0 && len(shape.Status) == 0 {
		var message a2a.Message
		if err := json.Unmarshal(result, &message); err != nil {
			return "", fmt.Errorf("a2a client: parsing message result: %w", err)
		}
		return extractTextFromMessage(&message), nil
	}
	if len(shape.Status) == 0 {
		return "", fmt.Errorf("a2a client: result is neither a Task nor a Message")
	}

	var task a2a.Task
	if err := json.Unmarshal(result, &task); err != nil {
		return "", fmt.Errorf("a2a client: parsing task result: %w", err)
	}
	if task.Status.State == a2a.TaskStateFailed {
		failMsg := "task failed"
		if task.Status.Message != nil {
			if text := extractTextFromMessage(task.Status.Message); text != "" {
				failMsg = text
			}
		}
		return "", fmt.Errorf("a2a client: %s", failMsg)
	}

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

func extractTextFromMessage(message *a2a.Message) string {
	var texts []string
	for _, part := range message.Parts {
		if text := part.Text(); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "")
}
