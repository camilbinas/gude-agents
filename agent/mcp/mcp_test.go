package mcp

import (
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---------------------------------------------------------------------------
// toSchemaMap
// ---------------------------------------------------------------------------

func TestToSchemaMap_Nil(t *testing.T) {
	m, err := toSchemaMap(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["type"] != "object" {
		t.Errorf("expected type=object for nil input, got %v", m["type"])
	}
	if _, ok := m["properties"]; !ok {
		t.Error("expected properties key for nil input")
	}
}

func TestToSchemaMap_MapPassthrough(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}
	m, err := toSchemaMap(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["type"] != "object" {
		t.Errorf("expected type=object, got %v", m["type"])
	}
	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties to be map[string]any")
	}
	if _, ok := props["name"]; !ok {
		t.Error("expected name property to be preserved")
	}
}

func TestToSchemaMap_JSONRoundTrip(t *testing.T) {
	// A struct-like value that isn't map[string]any — goes through JSON round-trip.
	type schemaLike struct {
		Type string `json:"type"`
	}
	input := schemaLike{Type: "string"}
	m, err := toSchemaMap(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["type"] != "string" {
		t.Errorf("expected type=string after JSON round-trip, got %v", m["type"])
	}
}

// ---------------------------------------------------------------------------
// extractText
// ---------------------------------------------------------------------------

func TestExtractText_Empty(t *testing.T) {
	result := extractText(nil)
	if result != "" {
		t.Errorf("expected empty string for nil content, got %q", result)
	}
}

func TestExtractText_EmptySlice(t *testing.T) {
	result := extractText([]sdkmcp.Content{})
	if result != "" {
		t.Errorf("expected empty string for empty content, got %q", result)
	}
}

func TestExtractText_SingleTextBlock(t *testing.T) {
	content := []sdkmcp.Content{
		&sdkmcp.TextContent{Text: "hello world"},
	}
	result := extractText(content)
	if result != "hello world" {
		t.Errorf("expected %q, got %q", "hello world", result)
	}
}

func TestExtractText_MultipleTextBlocks(t *testing.T) {
	content := []sdkmcp.Content{
		&sdkmcp.TextContent{Text: "first"},
		&sdkmcp.TextContent{Text: "second"},
	}
	result := extractText(content)
	expected := "first\nsecond"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestExtractText_EmptyTextBlockSkipped(t *testing.T) {
	content := []sdkmcp.Content{
		&sdkmcp.TextContent{Text: ""},
		&sdkmcp.TextContent{Text: "non-empty"},
	}
	result := extractText(content)
	if result != "non-empty" {
		t.Errorf("expected %q, got %q", "non-empty", result)
	}
}

// ---------------------------------------------------------------------------
// IncludeTools / ExcludeTools filtering
// ---------------------------------------------------------------------------

func TestToolsConfig_AllowAll(t *testing.T) {
	cfg := &toolsConfig{}
	for _, name := range []string{"read_file", "write_file", "delete_file"} {
		if !cfg.allow(name) {
			t.Errorf("expected %q to be allowed with no filters", name)
		}
	}
}

func TestToolsConfig_IncludeTools(t *testing.T) {
	cfg := &toolsConfig{}
	if err := IncludeTools("read_file", "write_file")(cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.allow("read_file") {
		t.Error("expected read_file to be allowed")
	}
	if !cfg.allow("write_file") {
		t.Error("expected write_file to be allowed")
	}
	if cfg.allow("delete_file") {
		t.Error("expected delete_file to be excluded")
	}
}

func TestToolsConfig_ExcludeTools(t *testing.T) {
	cfg := &toolsConfig{}
	if err := ExcludeTools("delete_file")(cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.allow("read_file") {
		t.Error("expected read_file to be allowed")
	}
	if cfg.allow("delete_file") {
		t.Error("expected delete_file to be excluded")
	}
}

func TestToolsConfig_IncludeTakesPrecedenceOverExclude(t *testing.T) {
	cfg := &toolsConfig{}
	if err := IncludeTools("read_file")(cfg); err != nil {
		t.Fatal(err)
	}
	if err := ExcludeTools("read_file")(cfg); err != nil {
		t.Fatal(err)
	}
	// Include takes precedence — read_file should still be allowed.
	if !cfg.allow("read_file") {
		t.Error("expected include to take precedence over exclude")
	}
	// write_file is not in include list — should be excluded.
	if cfg.allow("write_file") {
		t.Error("expected write_file to be excluded when include list is set")
	}
}

func TestToolsConfig_IncludeEmpty(t *testing.T) {
	cfg := &toolsConfig{}
	if err := IncludeTools()(cfg); err != nil {
		t.Fatal(err)
	}
	// Empty include list is a no-op — all tools are allowed.
	if !cfg.allow("read_file") {
		t.Error("expected all tools to be allowed with empty include list (no-op)")
	}
}
