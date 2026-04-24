package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"
)

// ChoiceMode controls how the LLM selects tools.
// Documented in docs/tools.md — update when changing constants.
type ChoiceMode string

const (
	ChoiceAuto ChoiceMode = "auto" // LLM decides (default)
	ChoiceAny  ChoiceMode = "any"  // LLM must call some tool
	ChoiceTool ChoiceMode = "tool" // LLM must call a specific tool
)

// Choice directs the LLM's tool selection behavior.
// Documented in docs/tools.md — update when changing fields.
type Choice struct {
	Mode ChoiceMode
	Name string // Only used when Mode == ChoiceTool
}

// Spec is the schema sent to the Provider so the LLM knows about the tool.
// Documented in docs/tools.md — update when changing fields.
type Spec struct {
	Name        string
	Description string
	InputSchema map[string]any // JSON Schema object
}

// Call represents a single tool invocation request from the LLM.
type Call struct {
	ToolUseID string
	Name      string
	Input     json.RawMessage
}

// Handler is the function signature for typed tool execution.
// Documented in docs/tools.md — update when changing signature.
type Handler[T any] func(ctx context.Context, input T) (string, error)

// Output is a rich tool result that can include text and images.
// Use this when a tool needs to return images alongside text (e.g.
// screenshot tools, chart generators, image search).
type Output struct {
	Text   string
	Images []Image
}

// Image holds image data for tool output. Set exactly one of Data, Base64, or URL.
type Image struct {
	Data     []byte // raw image bytes
	Base64   string // pre-encoded base64 string
	URL      string // publicly accessible image URL
	MIMEType string // e.g. "image/png", "image/jpeg"
}

// RichHandler is the function signature for tools that return rich output.
type RichHandler[T any] func(ctx context.Context, input T) (*Output, error)

// Tool pairs a spec with a raw handler.
// Documented in docs/tools.md — update when changing struct fields.
type Tool struct {
	Spec        Spec
	Handler     func(ctx context.Context, input json.RawMessage) (string, error)
	RichHandler func(ctx context.Context, input json.RawMessage) (*Output, error) // optional; takes precedence over Handler
}

// New creates a Tool from a typed handler function.
// It generates the JSON Schema from T's struct tags.
// Documented in docs/tools.md — update when changing schema generation or struct tag support.
func New[T any](name, description string, handler Handler[T]) Tool {
	schema := GenerateSchema[T]()
	return Tool{
		Spec: Spec{
			Name:        name,
			Description: description,
			InputSchema: schema,
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var input T
			if err := json.Unmarshal(raw, &input); err != nil {
				return "", fmt.Errorf("unmarshal tool input: %w", err)
			}
			return handler(ctx, input)
		},
	}
}

// NewSimple creates a Tool that takes no input parameters.
// It uses an empty object schema and a handler that receives no input.
// Documented in docs/tools.md — update when changing signature.
func NewSimple(name, description string, handler func(ctx context.Context) (string, error)) Tool {
	return Tool{
		Spec: Spec{
			Name:        name,
			Description: description,
			InputSchema: map[string]any{"type": "object"},
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			return handler(ctx)
		},
	}
}

// NewString creates a Tool that takes a single required string parameter.
// paramName and paramDesc control the JSON property name and its description
// in the schema. The handler receives the extracted string directly.
// Documented in docs/tools.md — update when changing signature.
func NewString(name, description, paramName, paramDesc string, handler func(ctx context.Context, value string) (string, error)) Tool {
	return Tool{
		Spec: Spec{
			Name:        name,
			Description: description,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					paramName: map[string]any{
						"type":        "string",
						"description": paramDesc,
					},
				},
				"required": []string{paramName},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var params map[string]string
			if err := json.Unmarshal(raw, &params); err != nil {
				return "", fmt.Errorf("unmarshal tool input: %w", err)
			}
			return handler(ctx, params[paramName])
		},
	}
}

// NewConfirm creates a Tool that takes a single required boolean "confirm"
// parameter. Useful for approval flows where the LLM must explicitly confirm
// an action before it proceeds.
// Documented in docs/tools.md — update when changing signature.
func NewConfirm(name, description string, handler func(ctx context.Context, confirmed bool) (string, error)) Tool {
	return Tool{
		Spec: Spec{
			Name:        name,
			Description: description,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"confirm": map[string]any{
						"type":        "boolean",
						"description": "Set to true to confirm the action, false to cancel.",
					},
				},
				"required": []string{"confirm"},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var params struct {
				Confirm bool `json:"confirm"`
			}
			if err := json.Unmarshal(raw, &params); err != nil {
				return "", fmt.Errorf("unmarshal tool input: %w", err)
			}
			return handler(ctx, params.Confirm)
		},
	}
}

// NewRaw creates a Tool with a raw JSON handler (no auto-deserialization).
// If schema is nil, it defaults to {"type": "object"} (no input parameters).
// Documented in docs/tools.md — update when changing signature.
func NewRaw(name, description string, schema map[string]any, handler func(ctx context.Context, input json.RawMessage) (string, error)) Tool {
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	return Tool{
		Spec: Spec{
			Name:        name,
			Description: description,
			InputSchema: schema,
		},
		Handler: handler,
	}
}

// NewRich creates a Tool from a typed handler that returns rich output
// (text + images). Use this for tools that need to return images to the
// LLM, such as screenshot tools, chart generators, or image search.
func NewRich[T any](name, description string, handler RichHandler[T]) Tool {
	return Tool{
		Spec: Spec{
			Name:        name,
			Description: description,
			InputSchema: GenerateSchema[T](),
		},
		RichHandler: func(ctx context.Context, input json.RawMessage) (*Output, error) {
			var v T
			if err := json.Unmarshal(input, &v); err != nil {
				return nil, err
			}
			return handler(ctx, v)
		},
	}
}

// NewRichRaw creates a Tool with a hand-crafted schema and a rich handler
// that returns text + images.
func NewRichRaw(name, description string, schema map[string]any, handler func(ctx context.Context, input json.RawMessage) (*Output, error)) Tool {
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	return Tool{
		Spec: Spec{
			Name:        name,
			Description: description,
			InputSchema: schema,
		},
		RichHandler: handler,
	}
}

// ErrorLogger is an optional callback for reporting errors from background
// goroutines (e.g. async tools). Matches the Printf signature used by
// log.Default() and the agent.Logger interface.
type ErrorLogger func(format string, v ...any)

// AsyncHandler is the function signature for typed async tools.
// It receives the deserialized input but returns nothing — errors are reported
// via the optional ErrorLogger, not sent back to the LLM.
type AsyncHandler[T any] func(ctx context.Context, input T)

// NewAsync creates a Tool whose handler runs in a background goroutine.
// The LLM receives ack immediately without waiting for the handler to complete.
// Use this for side effects that don't affect the conversation: CRM updates,
// webhooks, audit logs, notifications, cache warming, etc.
//
// The background goroutine gets a detached context (context.Background) so it
// isn't cancelled when the HTTP request or agent invocation finishes.
//
// If errLogger is nil, handler panics are silently recovered.
// Documented in docs/tools.md — update when changing signature.
func NewAsync[T any](name, description, ack string, handler AsyncHandler[T], errLogger ErrorLogger) Tool {
	schema := GenerateSchema[T]()
	return Tool{
		Spec: Spec{
			Name:        name,
			Description: description,
			InputSchema: schema,
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var input T
			if err := json.Unmarshal(raw, &input); err != nil {
				return "", fmt.Errorf("unmarshal tool input: %w", err)
			}
			go func() {
				defer func() {
					if r := recover(); r != nil {
						if errLogger != nil {
							errLogger("async tool %q panicked: %v", name, r)
						} else {
							log.Printf("[gude-agents] async tool %q panicked: %v", name, r)
						}
					}
				}()
				handler(context.Background(), input)
			}()
			return ack, nil
		},
	}
}

// NewAsyncRaw creates an async Tool with a raw JSON handler.
// Like NewAsync but without automatic deserialization.
// If schema is nil, it defaults to {"type": "object"}.
// Documented in docs/tools.md — update when changing signature.
func NewAsyncRaw(name, description, ack string, schema map[string]any, handler func(ctx context.Context, input json.RawMessage), errLogger ErrorLogger) Tool {
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	return Tool{
		Spec: Spec{
			Name:        name,
			Description: description,
			InputSchema: schema,
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						if errLogger != nil {
							errLogger("async tool %q panicked: %v", name, r)
						} else {
							log.Printf("[gude-agents] async tool %q panicked: %v", name, r)
						}
					}
				}()
				handler(context.Background(), raw)
			}()
			return ack, nil
		},
	}
}

// GenerateSchema uses reflection to produce a JSON Schema from a Go struct.
// Documented in docs/tools.md and docs/structured-output.md — update when changing tag support.
func GenerateSchema[T any]() map[string]any {
	t := reflect.TypeOf((*T)(nil)).Elem()
	return buildObjectSchema(t)
}

func buildObjectSchema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return goTypeToSchema(t)
	}

	properties := make(map[string]any)
	var required []string

	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		name := field.Name
		if jsonTag := field.Tag.Get("json"); jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			if parts[0] == "-" {
				continue
			}
			if parts[0] != "" {
				name = parts[0]
			}
		}

		prop := goTypeToSchema(field.Type)

		if desc := field.Tag.Get("description"); desc != "" {
			prop["description"] = desc
		}

		if enumTag := field.Tag.Get("enum"); enumTag != "" {
			values := strings.Split(enumTag, ",")
			enumSlice := make([]any, len(values))
			for j, v := range values {
				enumSlice[j] = strings.TrimSpace(v)
			}
			prop["enum"] = enumSlice
		}

		if field.Tag.Get("required") == "true" {
			required = append(required, name)
		}

		properties[name] = prop
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func goTypeToSchema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Slice, reflect.Array:
		return map[string]any{
			"type":  "array",
			"items": goTypeToSchema(t.Elem()),
		}
	case reflect.Struct:
		return buildObjectSchema(t)
	default:
		return map[string]any{"type": "string"}
	}
}
