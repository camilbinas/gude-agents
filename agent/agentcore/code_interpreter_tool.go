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
	// codeInterpreterToolName is the tool name exposed to the LLM.
	codeInterpreterToolName = "code_interpreter"

	// codeInterpreterToolDescription describes the tool's capabilities.
	codeInterpreterToolDescription = "Execute code in AgentCore's managed sandboxed environment. Supports Python for data analysis, computation, and visualization."

	// maxCodeLength is the maximum allowed code length.
	maxCodeLength = 100000

	// maxCodeOutputLength is the maximum characters returned from code execution.
	maxCodeOutputLength = 50000

	// defaultCodeTimeout is the default timeout for code execution.
	defaultCodeTimeout = 60 * time.Second
)

// codeInterpreterConfig holds configuration for the code interpreter tool.
type codeInterpreterConfig struct {
	timeout           time.Duration
	codeInterpreterID string
}

// CodeInterpreterOption configures the code interpreter tool.
type CodeInterpreterOption func(*codeInterpreterConfig)

// WithCodeTimeout sets the maximum time to wait for code execution.
// The default timeout is 60 seconds.
func WithCodeTimeout(d time.Duration) CodeInterpreterOption {
	return func(cfg *codeInterpreterConfig) {
		cfg.timeout = d
	}
}

// WithCodeInterpreterID sets the code interpreter identifier for AgentCore sessions.
func WithCodeInterpreterID(id string) CodeInterpreterOption {
	return func(cfg *codeInterpreterConfig) {
		cfg.codeInterpreterID = id
	}
}

// codeInterpreterInput is the JSON input schema for the code interpreter tool.
type codeInterpreterInput struct {
	Code     string `json:"code"`
	Language string `json:"language,omitempty"`
}

// NewCodeInterpreterTool returns a tool.Tool wrapping AgentCore's code interpreter.
// It requires an agentCoreClient to make API calls to the code interpreter service.
// For external use, prefer CodeInterpreter which accepts an aws.Config directly.
func NewCodeInterpreterTool(client agentCoreClient, opts ...CodeInterpreterOption) tool.Tool {
	cfg := codeInterpreterConfig{
		timeout:           defaultCodeTimeout,
		codeInterpreterID: "default",
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"code": map[string]any{
				"type":        "string",
				"description": "The Python code to execute in the sandboxed environment.",
				"maxLength":   maxCodeLength,
			},
			"language": map[string]any{
				"type":        "string",
				"description": "The programming language to use. Currently only \"python\" is supported.",
				"default":     "python",
			},
		},
		"required": []string{"code"},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var input codeInterpreterInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return "", fmt.Errorf("unmarshal code interpreter tool input: %w", err)
		}

		// Default language to "python" if not specified.
		if input.Language == "" {
			input.Language = "python"
		}

		// Validate language: only "python" is supported.
		if input.Language != "python" {
			return "", fmt.Errorf("unsupported language %q: supported values are [python]", input.Language)
		}

		// Create a timeout context for the code execution.
		opCtx, cancel := context.WithTimeout(ctx, cfg.timeout)
		defer cancel()

		// Start a code interpreter session.
		startOut, err := client.StartCodeInterpreterSession(opCtx, &bedrockagentcore.StartCodeInterpreterSessionInput{
			CodeInterpreterIdentifier: aws.String(cfg.codeInterpreterID),
			Name:                      aws.String("code-interpreter-session"),
		})
		if err != nil {
			return "", fmt.Errorf("code interpreter service unavailable: %v", err)
		}

		sessionID := ""
		if startOut != nil && startOut.SessionId != nil {
			sessionID = *startOut.SessionId
		}

		// Ensure we stop the session when done.
		defer func() {
			if sessionID != "" {
				_, _ = client.StopCodeInterpreterSession(context.Background(), &bedrockagentcore.StopCodeInterpreterSessionInput{
					CodeInterpreterIdentifier: aws.String(cfg.codeInterpreterID),
					SessionId:                 aws.String(sessionID),
				})
			}
		}()

		// Record start time for execution duration.
		startTime := time.Now()

		// Invoke the code interpreter.
		invokeOut, err := client.InvokeCodeInterpreter(opCtx, &bedrockagentcore.InvokeCodeInterpreterInput{
			CodeInterpreterIdentifier: aws.String(cfg.codeInterpreterID),
			SessionId:                 aws.String(sessionID),
			Name:                      types.ToolNameExecuteCode,
			Arguments: &types.ToolArguments{
				Code:     aws.String(input.Code),
				Language: types.ProgrammingLanguagePython,
			},
		})
		if err != nil {
			return "", fmt.Errorf("code interpreter service unavailable: %v", err)
		}

		// Read results from the event stream.
		output, isError, err := readCodeInterpreterStream(invokeOut)
		if err != nil {
			return "", fmt.Errorf("code execution error: %v", err)
		}

		// Calculate execution duration.
		duration := time.Since(startTime)

		// Truncate output to maxCodeOutputLength.
		if len(output) > maxCodeOutputLength {
			output = output[:maxCodeOutputLength]
		}

		// Append execution time suffix.
		executionSuffix := fmt.Sprintf("\nExecution time: %.2fs", duration.Seconds())
		result := output + executionSuffix

		// If the code produced an error, return it as both the result string and a non-nil error.
		if isError {
			return result, fmt.Errorf("code execution error: %s", output)
		}

		return result, nil
	}

	return tool.NewRaw(codeInterpreterToolName, codeInterpreterToolDescription, schema, handler)
}

// readCodeInterpreterStream reads all events from the code interpreter event stream
// and returns the combined output text, whether it was an error, and any stream error.
func readCodeInterpreterStream(out *bedrockagentcore.InvokeCodeInterpreterOutput) (string, bool, error) {
	if out == nil {
		return "", false, fmt.Errorf("empty response from code interpreter service")
	}

	stream := out.GetStream()
	if stream == nil {
		return "", false, fmt.Errorf("no event stream from code interpreter service")
	}
	defer stream.Close()

	var outputParts []string
	isError := false

	for event := range stream.Events() {
		switch ev := event.(type) {
		case *types.CodeInterpreterStreamOutputMemberResult:
			if ev.Value.IsError != nil && *ev.Value.IsError {
				isError = true
			}
			// Extract text from content blocks.
			for _, block := range ev.Value.Content {
				if block.Text != nil {
					outputParts = append(outputParts, *block.Text)
				}
			}
			// Also check structured content for stdout/stderr.
			if ev.Value.StructuredContent != nil {
				if ev.Value.StructuredContent.Stdout != nil && *ev.Value.StructuredContent.Stdout != "" {
					outputParts = append(outputParts, *ev.Value.StructuredContent.Stdout)
				}
				if ev.Value.StructuredContent.Stderr != nil && *ev.Value.StructuredContent.Stderr != "" {
					outputParts = append(outputParts, *ev.Value.StructuredContent.Stderr)
				}
			}
		}
	}

	if err := stream.Err(); err != nil {
		return "", false, fmt.Errorf("stream error: %v", err)
	}

	return strings.Join(outputParts, ""), isError, nil
}

// CodeInterpreter creates a code interpreter tool.Tool from an AWS config.
// This is the public-facing constructor for use outside the agentcore package.
func CodeInterpreter(cfg aws.Config, opts ...CodeInterpreterOption) tool.Tool {
	client := bedrockagentcore.NewFromConfig(cfg)
	return NewCodeInterpreterTool(client, opts...)
}
