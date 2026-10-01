package tool

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

var objectSchema = map[string]any{"type": "object"}

func TestNewSimple_SchemaAndHandler(t *testing.T) {
	calls := 0
	tl := NewSimple("health", "Check service health", func(ctx context.Context) (string, error) {
		calls++
		return "healthy", nil
	})
	if err := tl.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !reflect.DeepEqual(tl.Spec.InputSchema, objectSchema) {
		t.Fatalf("schema = %#v, want %#v", tl.Spec.InputSchema, objectSchema)
	}
	if tl.IsRich() || tl.IsBackground() || tl.RichHandler != nil {
		t.Fatal("NewSimple must produce a plain tool")
	}
	for _, input := range []string{`{}`, `{"ignored":1}`} {
		out, err := tl.Handler(context.Background(), json.RawMessage(input))
		if err != nil || out != "healthy" {
			t.Fatalf("Handler(%s) = %q, %v", input, out, err)
		}
	}
	if calls != 2 {
		t.Fatalf("handler calls = %d, want 2", calls)
	}
}

func TestNewSimple_PropagatesHandlerError(t *testing.T) {
	want := errors.New("down")
	tl := NewSimple("health", "Check", func(context.Context) (string, error) { return "", want })
	if _, err := tl.Handler(context.Background(), json.RawMessage(`{}`)); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestNewSimple_NilHandlerFailsValidation(t *testing.T) {
	if err := NewSimple("health", "Check", nil).Validate(); err == nil {
		t.Fatal("expected validation error for nil handler")
	}
}

func TestNewSimple_AcceptsAllOptions(t *testing.T) {
	custom := map[string]any{"type": "object", "properties": map[string]any{}}
	tl := NewSimple("purge", "Purge caches", func(context.Context) (string, error) { return "ok", nil },
		RequiresApproval(),
		WithGuard(func(context.Context, json.RawMessage) (Decision, error) { return Allow(), nil }),
		AllowRoles("admin"),
		DenyRoles("guest"),
		AllowWhen(func(attrs map[string]string) bool { return attrs["org"] == "acme" }),
		WithSchema(custom),
	)
	if !tl.NeedsApproval() {
		t.Error("RequiresApproval not applied")
	}
	if tl.Guard == nil {
		t.Error("WithGuard not applied")
	}
	if tl.AllowedWithAttrs([]string{"admin"}, map[string]string{"org": "other"}) {
		t.Error("AllowWhen not applied")
	}
	if !tl.AllowedWithAttrs([]string{"admin"}, map[string]string{"org": "acme"}) {
		t.Error("AllowRoles/AllowWhen rejected a permitted caller")
	}
	if tl.RolesAllowed([]string{"admin", "guest"}) {
		t.Error("DenyRoles not applied")
	}
	if !reflect.DeepEqual(tl.Spec.InputSchema, custom) {
		t.Errorf("WithSchema not applied: %#v", tl.Spec.InputSchema)
	}
}

func TestNewRaw_PositionalSchema(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
		"required":   []string{"order_id"},
	}
	tl := NewRaw("delete_order", "Delete", schema,
		func(_ context.Context, in json.RawMessage) (string, error) { return string(in), nil },
		RequiresApproval(),
	)
	if !reflect.DeepEqual(tl.Spec.InputSchema, schema) {
		t.Fatalf("schema = %#v, want positional schema", tl.Spec.InputSchema)
	}
	if !tl.NeedsApproval() {
		t.Fatal("options after the handler must still apply")
	}
}

func TestNewRaw_NilSchemaDefaultsToObject(t *testing.T) {
	tl := NewRaw("echo", "Echo", nil, func(context.Context, json.RawMessage) (string, error) { return "", nil })
	if !reflect.DeepEqual(tl.Spec.InputSchema, objectSchema) {
		t.Fatalf("schema = %#v, want %#v", tl.Spec.InputSchema, objectSchema)
	}
	if err := tl.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// TestNewRaw_WithSchemaOverridesPositional documents option precedence:
// options run after the positional schema is set, so WithSchema wins.
func TestNewRaw_WithSchemaOverridesPositional(t *testing.T) {
	schemaA := map[string]any{"type": "object", "title": "A"}
	schemaB := map[string]any{"type": "object", "title": "B"}
	tl := NewRaw("t", "d", schemaA,
		func(context.Context, json.RawMessage) (string, error) { return "", nil },
		WithSchema(schemaB),
	)
	if !reflect.DeepEqual(tl.Spec.InputSchema, schemaB) {
		t.Fatalf("schema = %#v, want WithSchema override %#v", tl.Spec.InputSchema, schemaB)
	}
}

func TestWithSchema_OverridesTypedSchema(t *testing.T) {
	type Input struct {
		Query string `json:"query"`
	}
	custom := map[string]any{"type": "object", "title": "custom"}
	tl := New("search", "Search", func(context.Context, Input) (string, error) { return "", nil }, WithSchema(custom))
	if !reflect.DeepEqual(tl.Spec.InputSchema, custom) {
		t.Fatalf("schema = %#v, want %#v", tl.Spec.InputSchema, custom)
	}
}
