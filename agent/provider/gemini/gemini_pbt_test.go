package gemini

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/tool"
	"google.golang.org/genai"
	"pgregory.net/rapid"
)

// ---------------------------------------------------------------------------
// Property 1: Constructor configuration preservation
// ---------------------------------------------------------------------------

func TestProperty_GeminiConstructorConfigPreservation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		modelID := rapid.StringMatching(`[a-z][a-z0-9\-\.]{0,30}`).Draw(t, "modelID")
		maxTokens := rapid.Int64Range(1, 100000).Draw(t, "maxTokens")

		p, err := New(modelID, WithAPIKey("test-key"), WithMaxTokens(maxTokens))
		if err != nil {
			t.Fatalf("New() failed: %v", err)
		}

		if p.ModelID() != modelID {
			t.Fatalf("ModelID() = %q, want %q", p.ModelID(), modelID)
		}
		if p.maxTokens == nil || int64(*p.maxTokens) != maxTokens {
			t.Fatalf("maxTokens = %v, want %d", p.maxTokens, maxTokens)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 2: Response text concatenation
// ---------------------------------------------------------------------------

func TestProperty_GeminiResponseTextConcatenation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(t, "numParts")
		texts := make([]string, n)
		parts := make([]*genai.Part, n)
		for i := 0; i < n; i++ {
			texts[i] = rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, "text")
			parts[i] = &genai.Part{Text: texts[i]}
		}

		resp := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{Content: &genai.Content{Parts: parts}},
			},
		}

		result := parseResponse(resp)
		want := strings.Join(texts, "")
		if result.Text != want {
			t.Fatalf("parseResponse text = %q, want %q", result.Text, want)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 3: Response function call mapping
// ---------------------------------------------------------------------------

func TestProperty_GeminiResponseFunctionCallMapping(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 5).Draw(t, "numCalls")

		type fcEntry struct {
			name string
			args map[string]any
		}
		entries := make([]fcEntry, n)
		parts := make([]*genai.Part, n)

		for i := 0; i < n; i++ {
			name := rapid.StringMatching(`[a-z_][a-z0-9_]{0,20}`).Draw(t, "funcName")
			// Generate simple string-valued args to avoid floating-point JSON round-trip issues.
			numArgs := rapid.IntRange(0, 3).Draw(t, "numArgs")
			args := map[string]any{}
			for j := 0; j < numArgs; j++ {
				key := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "argKey")
				val := rapid.StringMatching(`[a-zA-Z0-9]{0,20}`).Draw(t, "argVal")
				args[key] = val
			}

			entries[i] = fcEntry{name: name, args: args}
			parts[i] = &genai.Part{
				FunctionCall: &genai.FunctionCall{
					Name: name,
					Args: args,
				},
			}
		}

		resp := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{Content: &genai.Content{Parts: parts}},
			},
		}

		result := parseResponse(resp)
		if len(result.ToolCalls) != n {
			t.Fatalf("got %d tool calls, want %d", len(result.ToolCalls), n)
		}

		for i, tc := range result.ToolCalls {
			if tc.Name != entries[i].name {
				t.Fatalf("tool call %d name = %q, want %q", i, tc.Name, entries[i].name)
			}
			if tc.ToolUseID == "" {
				t.Fatalf("tool call %d has empty ToolUseID", i)
			}

			// Verify args round-trip through JSON.
			wantArgs := entries[i].args
			if wantArgs == nil {
				wantArgs = map[string]any{}
			}
			wantJSON, _ := json.Marshal(wantArgs)
			if string(tc.Input) != string(wantJSON) {
				t.Fatalf("tool call %d input = %s, want %s", i, string(tc.Input), string(wantJSON))
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 4: Token usage mapping
// ---------------------------------------------------------------------------

func TestProperty_GeminiTokenUsageMapping(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		promptTokens := rapid.Int32Range(0, 1000000).Draw(t, "promptTokens")
		candidateTokens := rapid.Int32Range(0, 1000000).Draw(t, "candidateTokens")

		resp := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{Content: &genai.Content{Parts: []*genai.Part{{Text: "ok"}}}},
			},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     promptTokens,
				CandidatesTokenCount: candidateTokens,
			},
		}

		result := parseResponse(resp)
		if result.Usage.InputTokens != int(promptTokens) {
			t.Fatalf("InputTokens = %d, want %d", result.Usage.InputTokens, promptTokens)
		}
		if result.Usage.OutputTokens != int(candidateTokens) {
			t.Fatalf("OutputTokens = %d, want %d", result.Usage.OutputTokens, candidateTokens)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 5: Role mapping
// ---------------------------------------------------------------------------

func TestProperty_GeminiRoleMapping(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		role := rapid.SampledFrom([]agent.Role{agent.RoleUser, agent.RoleAssistant}).Draw(t, "role")

		got := toGeminiRole(role)

		switch role {
		case agent.RoleUser:
			if got != "user" {
				t.Fatalf("toGeminiRole(%q) = %q, want %q", role, got, "user")
			}
		case agent.RoleAssistant:
			if got != "model" {
				t.Fatalf("toGeminiRole(%q) = %q, want %q", role, got, "model")
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 6: Content block mapping preserves content
// ---------------------------------------------------------------------------

func TestProperty_GeminiContentBlockMapping(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		kind := rapid.IntRange(0, 2).Draw(t, "blockKind")

		switch kind {
		case 0: // TextBlock
			text := rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(t, "text")
			blocks := []agent.ContentBlock{agent.TextBlock{Text: text}}
			parts, err := toGeminiParts(blocks)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(parts) != 1 {
				t.Fatalf("expected 1 part, got %d", len(parts))
			}
			if parts[0].Text != text {
				t.Fatalf("text part = %q, want %q", parts[0].Text, text)
			}

		case 1: // ToolUseBlock
			name := rapid.StringMatching(`[a-z_][a-z0-9_]{0,20}`).Draw(t, "toolName")
			// Generate simple JSON input with string values.
			numArgs := rapid.IntRange(0, 3).Draw(t, "numArgs")
			argsMap := map[string]any{}
			for j := 0; j < numArgs; j++ {
				key := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "argKey")
				val := rapid.StringMatching(`[a-zA-Z0-9]{0,20}`).Draw(t, "argVal")
				argsMap[key] = val
			}
			inputJSON, _ := json.Marshal(argsMap)

			blocks := []agent.ContentBlock{agent.ToolUseBlock{
				ToolUseID: "tu-1",
				Name:      name,
				Input:     json.RawMessage(inputJSON),
			}}
			parts, err := toGeminiParts(blocks)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(parts) != 1 {
				t.Fatalf("expected 1 part, got %d", len(parts))
			}
			if parts[0].FunctionCall == nil {
				t.Fatal("expected FunctionCall part")
			}
			if parts[0].FunctionCall.Name != name {
				t.Fatalf("FunctionCall.Name = %q, want %q", parts[0].FunctionCall.Name, name)
			}
			// Verify args match.
			gotJSON, _ := json.Marshal(parts[0].FunctionCall.Args)
			if string(gotJSON) != string(inputJSON) {
				t.Fatalf("FunctionCall.Args = %s, want %s", string(gotJSON), string(inputJSON))
			}

		case 2: // ToolResultBlock
			content := rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, "content")
			toolUseID := rapid.StringMatching(`tu_[a-zA-Z0-9]{4,12}`).Draw(t, "toolUseID")
			blocks := []agent.ContentBlock{agent.ToolResultBlock{
				ToolUseID: toolUseID,
				Content:   content,
			}}
			parts, err := toGeminiParts(blocks)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(parts) != 1 {
				t.Fatalf("expected 1 part, got %d", len(parts))
			}
			if parts[0].FunctionResponse == nil {
				t.Fatal("expected FunctionResponse part")
			}
			// The implementation wraps content in map[string]any{"result": content}.
			if parts[0].FunctionResponse.Response["result"] != content {
				t.Fatalf("FunctionResponse.Response[\"result\"] = %v, want %q",
					parts[0].FunctionResponse.Response["result"], content)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 7: ToolChoice mapping
// ---------------------------------------------------------------------------

func TestProperty_GeminiToolChoiceMapping(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mode := rapid.SampledFrom([]tool.ChoiceMode{
			tool.ChoiceAuto,
			tool.ChoiceAny,
			tool.ChoiceTool,
		}).Draw(t, "mode")

		choice := &tool.Choice{Mode: mode}
		if mode == tool.ChoiceTool {
			choice.Name = rapid.StringMatching(`[a-z_][a-z0-9_]{0,20}`).Draw(t, "toolName")
		}

		config := toGeminiToolConfig(choice)
		if config == nil {
			t.Fatal("toGeminiToolConfig returned nil")
		}
		if config.FunctionCallingConfig == nil {
			t.Fatal("FunctionCallingConfig is nil")
		}

		switch mode {
		case tool.ChoiceAuto:
			if config.FunctionCallingConfig.Mode != genai.FunctionCallingConfigModeAuto {
				t.Fatalf("mode = %q, want AUTO", config.FunctionCallingConfig.Mode)
			}
		case tool.ChoiceAny:
			if config.FunctionCallingConfig.Mode != genai.FunctionCallingConfigModeAny {
				t.Fatalf("mode = %q, want ANY", config.FunctionCallingConfig.Mode)
			}
		case tool.ChoiceTool:
			if config.FunctionCallingConfig.Mode != genai.FunctionCallingConfigModeAny {
				t.Fatalf("mode = %q, want ANY for ChoiceTool", config.FunctionCallingConfig.Mode)
			}
			if len(config.FunctionCallingConfig.AllowedFunctionNames) != 1 ||
				config.FunctionCallingConfig.AllowedFunctionNames[0] != choice.Name {
				t.Fatalf("AllowedFunctionNames = %v, want [%q]",
					config.FunctionCallingConfig.AllowedFunctionNames, choice.Name)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 8: ToolSpec mapping
// ---------------------------------------------------------------------------

func TestProperty_GeminiToolSpecMapping(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.StringMatching(`[a-z_][a-z0-9_]{0,20}`).Draw(t, "name")
		desc := rapid.StringMatching(`[a-zA-Z0-9 ]{0,50}`).Draw(t, "description")

		// Generate a simple object schema with string properties.
		numProps := rapid.IntRange(0, 3).Draw(t, "numProps")
		props := map[string]any{}
		for i := 0; i < numProps; i++ {
			propName := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "propName")
			props[propName] = map[string]any{"type": "string"}
		}
		schema := map[string]any{
			"type":       "object",
			"properties": props,
		}

		specs := []tool.Spec{{
			Name:        name,
			Description: desc,
			InputSchema: schema,
		}}

		tools := toGeminiTools(specs)
		if len(tools) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(tools))
		}
		if len(tools[0].FunctionDeclarations) != 1 {
			t.Fatalf("expected 1 declaration, got %d", len(tools[0].FunctionDeclarations))
		}

		decl := tools[0].FunctionDeclarations[0]
		if decl.Name != name {
			t.Fatalf("Name = %q, want %q", decl.Name, name)
		}
		if decl.Description != desc {
			t.Fatalf("Description = %q, want %q", decl.Description, desc)
		}
		if decl.Parameters == nil {
			t.Fatal("Parameters is nil")
		}
		if decl.Parameters.Type != genai.TypeObject {
			t.Fatalf("Parameters.Type = %q, want %q", decl.Parameters.Type, genai.TypeObject)
		}
		// Verify each property exists in the schema.
		for propName := range props {
			if _, ok := decl.Parameters.Properties[propName]; !ok {
				t.Fatalf("missing property %q in Parameters.Properties", propName)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Property 9: Stream text forwarding
// ---------------------------------------------------------------------------

func TestProperty_GeminiStreamTextForwarding(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(t, "numChunks")
		chunks := make([]string, n)
		for i := 0; i < n; i++ {
			chunks[i] = rapid.StringMatching(`[a-zA-Z0-9 ]{1,30}`).Draw(t, "chunk")
		}

		// Simulate the streaming accumulation logic from ConverseStream.
		var accumulated string
		var mu sync.Mutex
		var received []string

		cb := agent.StreamCallback(func(chunk string) {
			mu.Lock()
			defer mu.Unlock()
			received = append(received, chunk)
		})

		for _, chunk := range chunks {
			accumulated += chunk
			if cb != nil {
				cb(chunk)
			}
		}

		// Verify: callback received each chunk in order.
		mu.Lock()
		defer mu.Unlock()
		if len(received) != n {
			t.Fatalf("callback received %d chunks, want %d", len(received), n)
		}
		for i, got := range received {
			if got != chunks[i] {
				t.Fatalf("chunk %d = %q, want %q", i, got, chunks[i])
			}
		}

		// Verify: accumulated text equals concatenation.
		want := strings.Join(chunks, "")
		if accumulated != want {
			t.Fatalf("accumulated = %q, want %q", accumulated, want)
		}
	})
}

// ---------------------------------------------------------------------------
// Property 10: Thinking text storage
// ---------------------------------------------------------------------------

func TestProperty_GeminiThinkingTextStorage(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 5).Draw(t, "numThoughtParts")
		thoughts := make([]string, n)
		parts := make([]*genai.Part, n)
		for i := 0; i < n; i++ {
			thoughts[i] = rapid.StringMatching(`[a-zA-Z0-9 ]{1,50}`).Draw(t, "thought")
			parts[i] = &genai.Part{
				Text:    thoughts[i],
				Thought: true,
			}
		}

		resp := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{Content: &genai.Content{Parts: parts}},
			},
		}

		result := parseResponse(resp)
		if result.Metadata == nil {
			t.Fatal("Metadata is nil, expected thinking text")
		}

		got, ok := result.Metadata["thinking"].(string)
		if !ok {
			t.Fatal("Metadata[\"thinking\"] is not a string")
		}

		want := strings.Join(thoughts, "")
		if got != want {
			t.Fatalf("thinking = %q, want %q", got, want)
		}
	})
}

// ---------------------------------------------------------------------------
// Property: All content block types translate without panicking for Gemini
// **Validates: Requirements 1.4, 8.3**
// ---------------------------------------------------------------------------

// genGeminiTextBlock generates a random TextBlock for Gemini PBT.
func genGeminiTextBlock() *rapid.Generator[agent.TextBlock] {
	return rapid.Custom(func(t *rapid.T) agent.TextBlock {
		text := rapid.StringOf(rapid.RuneFrom([]rune("abcdefghijklmnopqrstuvwxyz ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"))).
			Draw(t, "text")
		return agent.TextBlock{Text: text}
	})
}

// genGeminiToolUseBlock generates a random ToolUseBlock.
func genGeminiToolUseBlock() *rapid.Generator[agent.ToolUseBlock] {
	return rapid.Custom(func(t *rapid.T) agent.ToolUseBlock {
		name := rapid.StringMatching(`[a-z_][a-z0-9_]{0,20}`).Draw(t, "toolName")
		numArgs := rapid.IntRange(0, 3).Draw(t, "numArgs")
		argsMap := map[string]any{}
		for i := 0; i < numArgs; i++ {
			key := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "argKey")
			val := rapid.StringMatching(`[a-zA-Z0-9]{0,20}`).Draw(t, "argVal")
			argsMap[key] = val
		}
		inputJSON, _ := json.Marshal(argsMap)
		return agent.ToolUseBlock{
			ToolUseID: "tu-1",
			Name:      name,
			Input:     json.RawMessage(inputJSON),
		}
	})
}

// genGeminiToolResultBlock generates a random ToolResultBlock (text-only, no images).
func genGeminiToolResultBlock() *rapid.Generator[agent.ToolResultBlock] {
	return rapid.Custom(func(t *rapid.T) agent.ToolResultBlock {
		toolUseID := rapid.StringMatching(`tu_[a-zA-Z0-9]{4,12}`).Draw(t, "toolUseID")
		content := rapid.StringOf(rapid.RuneFrom([]rune("abcdefghijklmnopqrstuvwxyz"))).Draw(t, "content")
		return agent.ToolResultBlock{
			ToolUseID: toolUseID,
			Content:   content,
		}
	})
}

// genGeminiImageBlock generates a fixed-data ImageBlock with base64 encoding.
func genGeminiImageBlock() *rapid.Generator[agent.ImageBlock] {
	return rapid.Custom(func(t *rapid.T) agent.ImageBlock {
		_ = rapid.Bool().Draw(t, "imageVariant")
		// 1x1 transparent PNG.
		pngData := []byte{
			0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
			0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
			0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
			0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
			0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
			0x54, 0x78, 0x9C, 0x62, 0x00, 0x01, 0x00, 0x00,
			0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
			0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
			0x42, 0x60, 0x82,
		}
		encoded := base64.StdEncoding.EncodeToString(pngData)
		return agent.ImageBlock{
			Source: agent.ImageSource{
				Base64:   encoded,
				MIMEType: "image/png",
			},
		}
	})
}

// genGeminiDocumentBlock generates a fixed-data DocumentBlock with base64 encoding.
func genGeminiDocumentBlock() *rapid.Generator[agent.DocumentBlock] {
	return rapid.Custom(func(t *rapid.T) agent.DocumentBlock {
		_ = rapid.Bool().Draw(t, "docVariant")
		pdfData := []byte("%PDF-1.0\n1 0 obj<</Type/Catalog>>endobj\n%%EOF")
		encoded := base64.StdEncoding.EncodeToString(pdfData)
		return agent.DocumentBlock{
			Source: agent.DocumentSource{
				Base64:   encoded,
				MIMEType: "application/pdf",
			},
		}
	})
}

// partsToJSON marshals a slice of *genai.Part to a JSON string for structural comparison.
func partsToJSON(t *rapid.T, parts []*genai.Part) string {
	t.Helper()
	b, err := json.Marshal(parts)
	if err != nil {
		t.Fatalf("failed to marshal parts to JSON: %v", err)
	}
	return string(b)
}

// TestProperty_GeminiAllBlockTypesDontPanic verifies that translating each known
// content block type through toGeminiParts does not panic and produces non-empty output.
//
// **Validates: Requirements 1.4, 8.3**
func TestProperty_GeminiAllBlockTypesDontPanic(t *testing.T) {
	t.Run("TextBlock", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			inner := genGeminiTextBlock().Draw(t, "inner")
			parts, err := toGeminiParts([]agent.ContentBlock{inner})
			if err != nil {
				t.Fatalf("toGeminiParts(TextBlock) error: %v", err)
			}
			if len(parts) == 0 {
				t.Fatal("expected non-empty parts for TextBlock")
			}
		})
	})

	t.Run("ToolUseBlock", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			inner := genGeminiToolUseBlock().Draw(t, "inner")
			parts, err := toGeminiParts([]agent.ContentBlock{inner})
			if err != nil {
				t.Fatalf("toGeminiParts(ToolUseBlock) error: %v", err)
			}
			if len(parts) == 0 {
				t.Fatal("expected non-empty parts for ToolUseBlock")
			}
		})
	})

	t.Run("ToolResultBlock", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			inner := genGeminiToolResultBlock().Draw(t, "inner")
			parts, err := toGeminiParts([]agent.ContentBlock{inner})
			if err != nil {
				t.Fatalf("toGeminiParts(ToolResultBlock) error: %v", err)
			}
			if len(parts) == 0 {
				t.Fatal("expected non-empty parts for ToolResultBlock")
			}
		})
	})

	t.Run("ImageBlock", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			inner := genGeminiImageBlock().Draw(t, "inner")
			parts, err := toGeminiParts([]agent.ContentBlock{inner})
			if err != nil {
				t.Fatalf("toGeminiParts(ImageBlock) error: %v", err)
			}
			if len(parts) == 0 {
				t.Fatal("expected non-empty parts for ImageBlock")
			}
		})
	})

	t.Run("DocumentBlock", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			inner := genGeminiDocumentBlock().Draw(t, "inner")
			parts, err := toGeminiParts([]agent.ContentBlock{inner})
			if err != nil {
				t.Fatalf("toGeminiParts(DocumentBlock) error: %v", err)
			}
			if len(parts) == 0 {
				t.Fatal("expected non-empty parts for DocumentBlock")
			}
		})
	})
}
