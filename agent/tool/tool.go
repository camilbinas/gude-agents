package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// ChoiceMode controls how the LLM selects tools.
type ChoiceMode string

const (
	ChoiceAuto ChoiceMode = "auto"
	ChoiceAny  ChoiceMode = "any"
	ChoiceTool ChoiceMode = "tool"
)

// Choice directs the LLM's tool selection behavior.
type Choice struct {
	Mode ChoiceMode
	Name string
}

// Spec is the schema sent to the Provider so the LLM knows about the tool.
type Spec struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// Call represents a single tool invocation request from the LLM.
type Call struct {
	ToolUseID string
	Name      string
	Input     json.RawMessage
}

// Handler is the function signature for typed tool execution.
type Handler[T any] func(ctx context.Context, input T) (string, error)

// Output is a rich tool result that can include text and images.
type Output struct {
	Text   string
	Images []Image
}

// Image holds image data for tool output. Set exactly one of Data, Base64, or URL.
type Image struct {
	Data     []byte
	Base64   string
	URL      string
	MIMEType string
}

// RichHandler is the function signature for tools that return rich output.
type RichHandler[T any] func(ctx context.Context, input T) (*Output, error)

// BackgroundHandler is the function signature for background and detached tools.
type BackgroundHandler[T any] func(ctx context.Context, input T) (string, error)

// Option configures a Tool.
type Option func(*Tool)

// WithReplaySafe declares that a handler can be invoked again after a process
// failure when it receives the same IdempotencyKey. Handlers must pass that
// key to the external system they call; this option does not make an otherwise
// non-idempotent side effect safe by itself.
func WithReplaySafe() Option {
	return func(t *Tool) { t.replaySafe = true }
}

type executionContextKey struct{}

type executionMetadata struct {
	idempotencyKey string
	recoveryReplay bool
}

// WithExecutionMetadata attaches agent-owned execution metadata to a handler
// context. It is exported for agent adapters; applications normally read it
// through IdempotencyKey and IsRecoveryReplay instead.
func WithExecutionMetadata(ctx context.Context, idempotencyKey string, recoveryReplay bool) context.Context {
	return context.WithValue(ctx, executionContextKey{}, executionMetadata{idempotencyKey: idempotencyKey, recoveryReplay: recoveryReplay})
}

// IdempotencyKey returns the durable key assigned to the current tool call.
// The boolean is false when the call is not running under durable execution.
func IdempotencyKey(ctx context.Context) (string, bool) {
	metadata, ok := ctx.Value(executionContextKey{}).(executionMetadata)
	if !ok || metadata.idempotencyKey == "" {
		return "", false
	}
	return metadata.idempotencyKey, true
}

// IsRecoveryReplay reports whether this handler invocation replays a call
// found in-flight after recovery. Such calls are only dispatched for tools
// declared WithReplaySafe.
func IsRecoveryReplay(ctx context.Context) bool {
	metadata, _ := ctx.Value(executionContextKey{}).(executionMetadata)
	return metadata.recoveryReplay
}

// OutcomeUnknownError marks a handler error whose external side effect may
// have happened even though its outcome could not be determined.
type OutcomeUnknownError struct{ Cause error }

func (e *OutcomeUnknownError) Error() string {
	if e == nil || e.Cause == nil {
		return "tool outcome unknown"
	}
	return "tool outcome unknown: " + e.Cause.Error()
}
func (e *OutcomeUnknownError) Unwrap() error { return e.Cause }

// OutcomeUnknown classifies err as an indeterminate external-tool outcome.
// Pass nil to create a generic classifier error.
func OutcomeUnknown(err error) error { return &OutcomeUnknownError{Cause: err} }

// IsOutcomeUnknown reports whether err was classified with OutcomeUnknown.
func IsOutcomeUnknown(err error) bool {
	var unknown *OutcomeUnknownError
	return errors.As(err, &unknown)
}

type executionKind uint8

const (
	executionPlain executionKind = iota
	executionRich
	executionBackground
)

// Tool pairs a provider-facing specification with one canonical handler adapter.
// Tools should be created with NewSimple (no input), New (typed input),
// NewRich (typed input, rich output), NewBackground (typed input, background
// execution), or NewRaw (raw JSON with an explicit schema).
type Tool struct {
	Spec Spec

	// Handler and RichHandler remain readable for low-level adapters while Agent
	// always executes them through its canonical authorization and middleware pipeline.
	Handler     func(context.Context, json.RawMessage) (string, error)
	RichHandler func(context.Context, json.RawMessage) (*Output, error)
	Guard       func(context.Context, json.RawMessage) (Decision, error)

	kind          executionKind
	ack           string
	needsApproval bool
	replaySafe    bool
	rolePolicy    *rolePolicy
}

// RequiresApproval marks a tool as requiring human approval before execution.
func RequiresApproval() Option {
	return func(t *Tool) { t.needsApproval = true }
}

// WithSchema replaces the generated/default input schema. Options run after
// the constructor sets its schema, so WithSchema also overrides the
// positional schema passed to NewRaw.
func WithSchema(schema map[string]any) Option {
	return func(t *Tool) {
		if schema == nil {
			t.Spec.InputSchema = map[string]any{"type": "object"}
			return
		}
		t.Spec.InputSchema = schema
	}
}

// IsBackground reports whether the tool completes asynchronously with conversation re-entry.
func (t Tool) IsBackground() bool {
	return t.kind == executionBackground
}

// IsRich reports whether the tool returns rich output.
func (t Tool) IsRich() bool { return t.kind == executionRich }

// Ack returns the immediate acknowledgement for background or detached tools.
func (t Tool) Ack() string { return t.ack }

// NeedsApproval reports whether this tool requires explicit human approval.
func (t Tool) NeedsApproval() bool { return t.needsApproval }

// ReplaySafe reports whether recovery may replay an in-flight handler with its
// original idempotency key.
func (t Tool) ReplaySafe() bool { return t.replaySafe }

// Validate verifies the tool's provider-facing metadata and executable handler.
func (t Tool) Validate() error {
	if t.Spec.Name == "" {
		return fmt.Errorf("tool name is required")
	}
	if t.Spec.Description == "" {
		return fmt.Errorf("tool %q: description is required", t.Spec.Name)
	}
	if t.Handler == nil && t.RichHandler == nil {
		return fmt.Errorf("tool %q: handler is required", t.Spec.Name)
	}
	if t.IsBackground() {
		if t.ack == "" {
			return fmt.Errorf("tool %q: background and detached tools require a non-empty ack string", t.Spec.Name)
		}
		if t.Handler == nil {
			return fmt.Errorf("tool %q: background and detached tools require a plain handler", t.Spec.Name)
		}
	}
	if t.Spec.InputSchema == nil {
		return fmt.Errorf("tool %q: input schema is required", t.Spec.Name)
	}
	return nil
}

// New creates a Tool from a typed string handler and generates its JSON Schema from T.
func New[T any](name, description string, handler Handler[T], opts ...Option) Tool {
	return newPlain(name, description, GenerateSchema[T](), adaptString(handler), executionPlain, "", opts...)
}

// NewSimple creates a Tool that takes no input. Its provider-facing schema is
// {"type":"object"}; the handler receives only the context. It runs through
// the same canonical pipeline (RBAC, schema validation, guard, approval,
// middleware) as every other tool, and accepts all normal options.
func NewSimple(name, description string, handler func(context.Context) (string, error), opts ...Option) Tool {
	var h func(context.Context, json.RawMessage) (string, error)
	if handler != nil {
		h = func(ctx context.Context, _ json.RawMessage) (string, error) {
			return handler(ctx)
		}
	}
	return newPlain(name, description, map[string]any{"type": "object"}, h, executionPlain, "", opts...)
}

// NewRaw creates a Tool whose handler receives unprocessed JSON and whose
// provider-facing schema is supplied explicitly. A nil schema becomes
// {"type":"object"}. Options are applied after the positional schema, so a
// WithSchema option overrides it.
func NewRaw(name, description string, schema map[string]any, handler Handler[json.RawMessage], opts ...Option) Tool {
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	return newPlain(name, description, schema, handler, executionPlain, "", opts...)
}

// NewRich creates a Tool from a typed rich-output handler.
func NewRich[T any](name, description string, handler RichHandler[T], opts ...Option) Tool {
	t := Tool{
		Spec: Spec{Name: name, Description: description, InputSchema: GenerateSchema[T]()},
		kind: executionRich,
	}
	if handler != nil {
		t.RichHandler = func(ctx context.Context, raw json.RawMessage) (*Output, error) {
			var input T
			if err := json.Unmarshal(raw, &input); err != nil {
				return nil, fmt.Errorf("unmarshal tool input: %w", err)
			}
			return handler(ctx, input)
		}
	}
	applyOptions(&t, opts)
	return t
}

// NewBackground creates a typed asynchronous tool. It immediately returns ack,
// runs the handler on a detached context (not the caller's), completes outside
// the originating model turn, persists its completion, and triggers re-entry.
func NewBackground[T any](name, description, ack string, handler BackgroundHandler[T], opts ...Option) Tool {
	return newPlain(name, description, GenerateSchema[T](), adaptString(handler), executionBackground, ack, opts...)
}

func newPlain(name, description string, schema map[string]any, handler func(context.Context, json.RawMessage) (string, error), kind executionKind, ack string, opts ...Option) Tool {
	t := Tool{
		Spec:    Spec{Name: name, Description: description, InputSchema: schema},
		Handler: handler,
		kind:    kind,
		ack:     ack,
	}
	applyOptions(&t, opts)
	return t
}

func adaptString[T any](handler func(context.Context, T) (string, error)) func(context.Context, json.RawMessage) (string, error) {
	if handler == nil {
		return nil
	}
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var input T
		if err := json.Unmarshal(raw, &input); err != nil {
			return "", fmt.Errorf("unmarshal tool input: %w", err)
		}
		return handler(ctx, input)
	}
}

func applyOptions(t *Tool, opts []Option) {
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
}

// GenerateSchema uses reflection to produce a JSON Schema from Go type T.
func GenerateSchema[T any]() map[string]any {
	t := reflect.TypeOf((*T)(nil)).Elem()
	return buildObjectSchema(t)
}

func buildObjectSchema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return GoTypeToSchema(t)
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
		prop := GoTypeToSchema(field.Type)
		if desc := field.Tag.Get("description"); desc != "" {
			prop["description"] = desc
		}
		if enumTag := field.Tag.Get("enum"); enumTag != "" {
			values := strings.Split(enumTag, ",")
			enumSlice := make([]any, len(values))
			for j, value := range values {
				enumSlice[j] = strings.TrimSpace(value)
			}
			prop["enum"] = enumSlice
		}
		if field.Tag.Get("required") == "true" {
			required = append(required, name)
		}
		properties[name] = prop
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// GoTypeToSchema maps a Go reflect.Type to a JSON Schema type descriptor.
func GoTypeToSchema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": GoTypeToSchema(t.Elem())}
	case reflect.Struct:
		return buildObjectSchema(t)
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": GoTypeToSchema(t.Elem())}
	default:
		return map[string]any{"type": "string"}
	}
}
