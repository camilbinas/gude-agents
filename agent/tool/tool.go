// Package tool defines the Tool type and the constructors that expose Go
// functions to a model as callable tools with a JSON schema.
//
// For the typed constructors (New and NewRich) the input schema is reflected
// from T. A field is required only when tagged required:"true"; all others are
// optional. NewRaw takes an explicit schema, so set its "required" list directly.
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
)

// Tool pairs a provider-facing specification with one canonical handler adapter.
// Tools should be created with NewSimple (no input), New (typed input),
// NewRich (typed input, rich output), or NewRaw (raw JSON with an explicit
// schema).
type Tool struct {
	Spec Spec

	// Handler and RichHandler remain readable for low-level adapters while the Runtime
	// always executes them through its canonical authorization and middleware pipeline.
	Handler     func(context.Context, json.RawMessage) (string, error)
	RichHandler func(context.Context, json.RawMessage) (*Output, error)
	Guard       func(context.Context, json.RawMessage) (Decision, error)

	kind          executionKind
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

// IsRich reports whether the tool returns rich output.
func (t Tool) IsRich() bool { return t.kind == executionRich }

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
	if t.Spec.InputSchema == nil {
		return fmt.Errorf("tool %q: input schema is required", t.Spec.Name)
	}
	return nil
}

// New creates a Tool from a typed string handler and generates its JSON Schema
// from T. A field of T is required only when tagged required:"true".
func New[T any](name, description string, handler Handler[T], opts ...Option) Tool {
	return newPlain(name, description, GenerateSchema[T](), adaptString(handler), opts...)
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
	return newPlain(name, description, map[string]any{"type": "object"}, h, opts...)
}

// NewRaw creates a Tool whose handler receives unprocessed JSON and whose
// provider-facing schema is supplied explicitly. A nil schema becomes
// {"type":"object"}. Options are applied after the positional schema, so a
// WithSchema option overrides it.
func NewRaw(name, description string, schema map[string]any, handler Handler[json.RawMessage], opts ...Option) Tool {
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	return newPlain(name, description, schema, handler, opts...)
}

// NewRich creates a Tool from a typed rich-output handler. Like New, a field of
// T is required only when tagged required:"true".
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

func newPlain(name, description string, schema map[string]any, handler func(context.Context, json.RawMessage) (string, error), opts ...Option) Tool {
	t := Tool{
		Spec:    Spec{Name: name, Description: description, InputSchema: schema},
		Handler: handler,
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
	return buildObjectSchema(t, nil)
}

// buildObjectSchema builds the schema for a struct. visiting tracks struct
// types on the recursion stack so a self-referential type does not recurse
// forever.
func buildObjectSchema(t reflect.Type, visiting map[reflect.Type]bool) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return goTypeToSchema(t, visiting)
	}
	if visiting[t] {
		// Cycle: emit an open object and stop descending.
		return map[string]any{"type": "object"}
	}
	if visiting == nil {
		visiting = make(map[reflect.Type]bool)
	}
	visiting[t] = true
	defer delete(visiting, t)

	properties := make(map[string]any)
	var required []string
	for _, f := range schemaFields(t) {
		field, name := f.field, f.name
		if _, seen := properties[name]; seen {
			continue
		}
		prop := goTypeToSchema(field.Type, visiting)
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

type schemaField struct {
	field reflect.StructField
	name  string
}

// schemaFields lists the JSON fields of struct t the way encoding/json sees
// them: untagged embedded structs are flattened, and shallower fields win.
func schemaFields(t reflect.Type) []schemaField {
	return collectSchemaFields(t, map[reflect.Type]bool{})
}

func collectSchemaFields(t reflect.Type, seen map[reflect.Type]bool) []schemaField {
	if seen[t] {
		return nil
	}
	seen[t] = true
	defer delete(seen, t)
	var direct, promoted []schemaField
	for i := range t.NumField() {
		field := t.Field(i)
		name, tagged, skip := jsonFieldName(field)
		if skip {
			continue
		}
		if field.Anonymous && !tagged {
			ft := field.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				promoted = append(promoted, collectSchemaFields(ft, seen)...)
				continue
			}
		}
		if !field.IsExported() {
			continue
		}
		direct = append(direct, schemaField{field: field, name: name})
	}
	return append(direct, promoted...)
}

// jsonFieldName returns the JSON name of field, whether the tag set a name,
// and whether encoding/json ignores the field.
func jsonFieldName(field reflect.StructField) (name string, tagged, skip bool) {
	name = field.Name
	tag := field.Tag.Get("json")
	if tag == "" {
		return name, false, false
	}
	parts := strings.Split(tag, ",")
	if parts[0] == "-" && len(parts) == 1 {
		return "", false, true
	}
	if parts[0] != "" {
		return parts[0], true, false
	}
	return name, false, false
}

// GoTypeToSchema maps a Go reflect.Type to a JSON Schema type descriptor.
// Self-referential struct types emit a generic {"type":"object"}.
func GoTypeToSchema(t reflect.Type) map[string]any {
	return goTypeToSchema(t, nil)
}

func goTypeToSchema(t reflect.Type, visiting map[reflect.Type]bool) map[string]any {
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
		return map[string]any{"type": "array", "items": goTypeToSchema(t.Elem(), visiting)}
	case reflect.Struct:
		return buildObjectSchema(t, visiting)
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": goTypeToSchema(t.Elem(), visiting)}
	default:
		return map[string]any{"type": "string"}
	}
}
