package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// callRich runs the wrapped tool's rich handler and fails if it is absent.
func callRich(t *testing.T, tl tool.Tool, input string) *tool.Output {
	t.Helper()
	if tl.RichHandler == nil {
		t.Fatal("wrapped MCP tool has no RichHandler; non-text content cannot be carried")
	}
	out, err := tl.RichHandler(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("rich handler error: %v", err)
	}
	if out == nil {
		t.Fatal("rich handler returned nil output")
	}
	return out
}

func toolReturning(t *testing.T, content []sdkmcp.Content, isError bool) tool.Tool {
	t.Helper()
	c := inMemoryClient(t, func(s *sdkmcp.Server) {
		s.AddTool(&sdkmcp.Tool{Name: "x", Description: "x", InputSchema: map[string]any{"type": "object"}},
			func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				return &sdkmcp.CallToolResult{IsError: isError, Content: content}, nil
			})
	})
	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	return tools[0]
}

func TestWrapTool_ImageContentBecomesImage(t *testing.T) {
	tl := toolReturning(t, []sdkmcp.Content{
		&sdkmcp.TextContent{Text: "here is a chart"},
		&sdkmcp.ImageContent{MIMEType: "image/png", Data: []byte("PNGDATA")},
	}, false)

	out := callRich(t, tl, `{}`)
	if out.Text != "here is a chart" {
		t.Errorf("text = %q, want %q", out.Text, "here is a chart")
	}
	if len(out.Images) != 1 {
		t.Fatalf("images = %d, want 1", len(out.Images))
	}
	img := out.Images[0]
	if img.MIMEType != "image/png" {
		t.Errorf("image mime = %q, want image/png", img.MIMEType)
	}
	if string(img.Data) != "PNGDATA" {
		t.Errorf("image data = %q, want PNGDATA", string(img.Data))
	}
}

func TestWrapTool_EmbeddedTextResourceIncludedAsText(t *testing.T) {
	tl := toolReturning(t, []sdkmcp.Content{
		&sdkmcp.EmbeddedResource{Resource: &sdkmcp.ResourceContents{
			URI: "file:///notes.txt", MIMEType: "text/plain", Text: "embedded body",
		}},
	}, false)

	out := callRich(t, tl, `{}`)
	if !strings.Contains(out.Text, "embedded body") {
		t.Errorf("text = %q, want it to contain the embedded resource text", out.Text)
	}
}

func TestWrapTool_AudioBlobAndLinkGetPlaceholders(t *testing.T) {
	size := int64(42)
	tl := toolReturning(t, []sdkmcp.Content{
		&sdkmcp.AudioContent{MIMEType: "audio/wav", Data: []byte("WAV")},
		&sdkmcp.EmbeddedResource{Resource: &sdkmcp.ResourceContents{
			URI: "file:///pic.bin", MIMEType: "application/octet-stream", Blob: []byte("BLOB"),
		}},
		&sdkmcp.ResourceLink{URI: "https://example.com/doc", MIMEType: "application/pdf", Name: "doc", Size: &size},
	}, false)

	out := callRich(t, tl, `{}`)
	// Nothing should disappear silently: each non-carryable block leaves a trace.
	for _, want := range []string{"audio/wav", "file:///pic.bin", "https://example.com/doc"} {
		if !strings.Contains(out.Text, want) {
			t.Errorf("placeholder text %q missing from %q", want, out.Text)
		}
	}
	if len(out.Images) != 0 {
		t.Errorf("images = %d, want 0 (audio/blob/link are not images)", len(out.Images))
	}
}

func TestWrapTool_IsErrorPreservedOnRichPath(t *testing.T) {
	tl := toolReturning(t, []sdkmcp.Content{
		&sdkmcp.TextContent{Text: "upstream exploded"},
	}, true)

	if tl.RichHandler == nil {
		t.Fatal("wrapped MCP tool has no RichHandler")
	}
	_, err := tl.RichHandler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected IsError result to map to a Go error")
	}
	if !strings.Contains(err.Error(), "upstream exploded") {
		t.Errorf("error = %v, want upstream text", err)
	}
}

// Adapters that read the plain Handler still get the text, and errors.
func TestWrapTool_PlainHandlerStillWorks(t *testing.T) {
	tl := toolReturning(t, []sdkmcp.Content{
		&sdkmcp.TextContent{Text: "hello"},
		&sdkmcp.ImageContent{MIMEType: "image/png", Data: []byte("X")},
	}, false)
	if tl.Handler == nil {
		t.Fatal("plain Handler must stay set")
	}
	text, err := tl.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil || text != "hello" {
		t.Fatalf("Handler = %q, %v", text, err)
	}
	bad := toolReturning(t, []sdkmcp.Content{&sdkmcp.TextContent{Text: "boom"}}, true)
	if _, err := bad.Handler(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("IsError must surface through the plain Handler")
	}
}
