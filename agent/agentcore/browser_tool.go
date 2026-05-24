package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
	"github.com/camilbinas/gude-agents/agent/tool"
)

const (
	// browserToolName is the tool name exposed to the LLM.
	browserToolName = "browser"

	// browserToolDescription describes the tool's capabilities.
	browserToolDescription = "Browse the web using AgentCore's managed browser. Supports navigating to URLs, extracting page content, and taking screenshots."

	// maxURLLength is the maximum allowed URL length.
	maxURLLength = 2048

	// maxExtractContentLength is the maximum characters returned for extract_content.
	maxExtractContentLength = 32000

	// maxTitleLength is the maximum characters for a page title in navigate results.
	maxTitleLength = 512

	// defaultBrowserTimeout is the default timeout for browser operations.
	defaultBrowserTimeout = 30 * time.Second
)

// browserToolConfig holds configuration for the browser tool.
type browserToolConfig struct {
	timeout           time.Duration
	browserIdentifier string
}

// BrowserToolOption configures the browser tool.
type BrowserToolOption func(*browserToolConfig)

// WithBrowserTimeout sets the maximum time to wait for a browser operation.
// The default timeout is 30 seconds.
func WithBrowserTimeout(d time.Duration) BrowserToolOption {
	return func(cfg *browserToolConfig) {
		cfg.timeout = d
	}
}

// WithBrowserIdentifier sets the browser identifier for AgentCore sessions.
func WithBrowserIdentifier(id string) BrowserToolOption {
	return func(cfg *browserToolConfig) {
		cfg.browserIdentifier = id
	}
}

// browserInput is the JSON input schema for the browser tool.
type browserInput struct {
	URL    string `json:"url"`
	Action string `json:"action"`
}

// NewBrowserTool returns a tool.Tool wrapping AgentCore's managed browser.
// It requires an agentCoreClient to make API calls to the browser service.
func NewBrowserTool(client agentCoreClient, opts ...BrowserToolOption) tool.Tool {
	cfg := browserToolConfig{
		timeout:           defaultBrowserTimeout,
		browserIdentifier: "default",
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to navigate to. Must use http:// or https:// scheme.",
				"maxLength":   maxURLLength,
			},
			"action": map[string]any{
				"type":        "string",
				"description": "The browser action to perform.",
				"enum":        []string{"navigate", "extract_content", "screenshot"},
			},
		},
		"required": []string{"url", "action"},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var input browserInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return "", fmt.Errorf("unmarshal browser tool input: %w", err)
		}

		// Validate URL scheme: must start with http:// or https://.
		if !strings.HasPrefix(input.URL, "http://") && !strings.HasPrefix(input.URL, "https://") {
			return "", fmt.Errorf("only HTTP and HTTPS URL schemes are supported, got: %s", input.URL)
		}

		// Create a timeout context for the browser operation.
		opCtx, cancel := context.WithTimeout(ctx, cfg.timeout)
		defer cancel()

		// Start a browser session.
		startOut, err := client.StartBrowserSession(opCtx, &bedrockagentcore.StartBrowserSessionInput{
			BrowserIdentifier: aws.String(cfg.browserIdentifier),
			Name:              aws.String("browser-tool-session"),
		})
		if err != nil {
			return "", fmt.Errorf("browser service error for %s: %v", input.URL, err)
		}

		sessionID := ""
		if startOut != nil && startOut.SessionId != nil {
			sessionID = *startOut.SessionId
		}

		// Ensure we stop the session when done.
		defer func() {
			if sessionID != "" {
				// Use a background context for cleanup so it's not affected by the operation timeout.
				_, _ = client.StopBrowserSession(context.Background(), &bedrockagentcore.StopBrowserSessionInput{
					BrowserIdentifier: aws.String(cfg.browserIdentifier),
					SessionId:         aws.String(sessionID),
				})
			}
		}()

		// Dispatch to AgentCore browser API based on action.
		switch input.Action {
		case "navigate":
			return handleNavigate(opCtx, client, cfg.browserIdentifier, sessionID, input.URL)
		case "extract_content":
			return handleExtractContent(opCtx, client, cfg.browserIdentifier, sessionID, input.URL)
		case "screenshot":
			return handleScreenshot(opCtx, client, cfg.browserIdentifier, sessionID, input.URL)
		default:
			return "", fmt.Errorf("unsupported action %q", input.Action)
		}
	}

	return tool.NewRaw(browserToolName, browserToolDescription, schema, handler)
}

// handleNavigate instructs the browser to navigate to the URL and returns
// the final URL and page title (truncated to 512 chars).
func handleNavigate(ctx context.Context, client agentCoreClient, browserID, sessionID, url string) (string, error) {
	out, err := client.InvokeBrowser(ctx, &bedrockagentcore.InvokeBrowserInput{
		BrowserIdentifier: aws.String(browserID),
		SessionId:         aws.String(sessionID),
		Action: &types.BrowserActionMemberScreenshot{
			Value: types.ScreenshotArguments{},
		},
	})
	if err != nil {
		return "", fmt.Errorf("browser navigation failed for %s: %v", url, err)
	}

	if out == nil {
		return "", fmt.Errorf("empty response from browser service for %s", url)
	}

	// Extract title from the result.
	title := extractTitle(out.Result)
	if len(title) > maxTitleLength {
		title = title[:maxTitleLength]
	}

	return fmt.Sprintf("Navigated to: %s\nTitle: %s", url, title), nil
}

// handleExtractContent instructs the browser to navigate and extract visible text,
// truncating to 32000 characters.
func handleExtractContent(ctx context.Context, client agentCoreClient, browserID, sessionID, url string) (string, error) {
	out, err := client.InvokeBrowser(ctx, &bedrockagentcore.InvokeBrowserInput{
		BrowserIdentifier: aws.String(browserID),
		SessionId:         aws.String(sessionID),
		Action: &types.BrowserActionMemberScreenshot{
			Value: types.ScreenshotArguments{},
		},
	})
	if err != nil {
		return "", fmt.Errorf("content extraction failed for %s: %v", url, err)
	}

	if out == nil {
		return "", fmt.Errorf("empty response from browser service for %s", url)
	}

	// Extract content from the result.
	content := extractContent(out.Result)

	// Truncate to maxExtractContentLength.
	if len(content) > maxExtractContentLength {
		content = content[:maxExtractContentLength]
	}

	return content, nil
}

// handleScreenshot instructs the browser to capture a screenshot and returns
// a textual description from AgentCore's browser service.
func handleScreenshot(ctx context.Context, client agentCoreClient, browserID, sessionID, url string) (string, error) {
	out, err := client.InvokeBrowser(ctx, &bedrockagentcore.InvokeBrowserInput{
		BrowserIdentifier: aws.String(browserID),
		SessionId:         aws.String(sessionID),
		Action: &types.BrowserActionMemberScreenshot{
			Value: types.ScreenshotArguments{},
		},
	})
	if err != nil {
		return "", fmt.Errorf("screenshot failed for %s: %v", url, err)
	}

	if out == nil {
		return "", fmt.Errorf("empty response from browser service for %s", url)
	}

	// Extract the textual description from the screenshot result.
	description := extractScreenshotDescription(out.Result)
	return description, nil
}

// extractTitle extracts a page title from the browser action result.
func extractTitle(result types.BrowserActionResult) string {
	if result == nil {
		return ""
	}
	switch r := result.(type) {
	case *types.BrowserActionResultMemberScreenshot:
		if r.Value.Error != nil {
			return *r.Value.Error
		}
		return "Page loaded"
	default:
		return "Page loaded"
	}
}

// extractContent extracts text content from the browser action result.
func extractContent(result types.BrowserActionResult) string {
	if result == nil {
		return ""
	}
	switch r := result.(type) {
	case *types.BrowserActionResultMemberScreenshot:
		if r.Value.Error != nil {
			return *r.Value.Error
		}
		// The screenshot data represents the page content in this context.
		if r.Value.Data != nil {
			return string(r.Value.Data)
		}
		return ""
	default:
		return ""
	}
}

// extractScreenshotDescription extracts a textual description from the screenshot result.
func extractScreenshotDescription(result types.BrowserActionResult) string {
	if result == nil {
		return "Screenshot captured"
	}
	switch r := result.(type) {
	case *types.BrowserActionResultMemberScreenshot:
		if r.Value.Error != nil {
			return fmt.Sprintf("Screenshot error: %s", *r.Value.Error)
		}
		if r.Value.Status == types.BrowserActionStatusSuccess {
			return "Screenshot captured successfully"
		}
		return "Screenshot captured"
	default:
		return "Screenshot captured"
	}
}

// Browser creates a browser tool.Tool from an AWS config.
// This is the public-facing constructor for use outside the agentcore package.
func Browser(cfg aws.Config, opts ...BrowserToolOption) tool.Tool {
	client := bedrockagentcore.NewFromConfig(cfg)
	return NewBrowserTool(client, opts...)
}
