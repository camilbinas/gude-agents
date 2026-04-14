package tool

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	_ "pgregory.net/rapid" // register -rapid.checks flag
)

// --- Struct tag parsing tests ---

type TagParsingInput struct {
	Query  string `json:"query" description:"Search term" required:"true"`
	Status string `json:"status" description:"Filter by status" enum:"draft,active,archived"`
	Limit  int    `json:"limit" description:"Max results"`
}

func TestStructTagParsing(t *testing.T) {
	schema := GenerateSchema[TagParsingInput]()

	if schema["type"] != "object" {
		t.Fatalf("expected type=object, got %v", schema["type"])
	}

	props := schema["properties"].(map[string]any)

	// json tag → field name
	if _, ok := props["query"]; !ok {
		t.Fatal("expected property 'query' from json tag")
	}
	if _, ok := props["status"]; !ok {
		t.Fatal("expected property 'status' from json tag")
	}
	if _, ok := props["limit"]; !ok {
		t.Fatal("expected property 'limit' from json tag")
	}

	// description tag
	q := props["query"].(map[string]any)
	if q["description"] != "Search term" {
		t.Errorf("expected description='Search term', got %v", q["description"])
	}

	// enum tag
	s := props["status"].(map[string]any)
	enumVals := s["enum"].([]any)
	expected := []any{"draft", "active", "archived"}
	if !reflect.DeepEqual(enumVals, expected) {
		t.Errorf("expected enum=%v, got %v", expected, enumVals)
	}

	// required tag
	req := schema["required"].([]string)
	if len(req) != 1 || req[0] != "query" {
		t.Errorf("expected required=[query], got %v", req)
	}
}

// --- Go-to-JSON-Schema type mapping tests ---

type TypeMappingInput struct {
	S    string   `json:"s"`
	I    int      `json:"i"`
	I64  int64    `json:"i64"`
	F32  float32  `json:"f32"`
	F64  float64  `json:"f64"`
	B    bool     `json:"b"`
	Tags []string `json:"tags"`
	Nums []int    `json:"nums"`
}

func TestGoTypeToSchemaMapping(t *testing.T) {
	schema := GenerateSchema[TypeMappingInput]()
	props := schema["properties"].(map[string]any)

	tests := []struct {
		field    string
		wantType string
	}{
		{"s", "string"},
		{"i", "integer"},
		{"i64", "integer"},
		{"f32", "number"},
		{"f64", "number"},
		{"b", "boolean"},
	}

	for _, tt := range tests {
		p := props[tt.field].(map[string]any)
		if p["type"] != tt.wantType {
			t.Errorf("field %q: expected type=%q, got %v", tt.field, tt.wantType, p["type"])
		}
	}

	// Slice of strings
	tags := props["tags"].(map[string]any)
	if tags["type"] != "array" {
		t.Errorf("tags: expected type=array, got %v", tags["type"])
	}
	items := tags["items"].(map[string]any)
	if items["type"] != "string" {
		t.Errorf("tags items: expected type=string, got %v", items["type"])
	}

	// Slice of ints
	nums := props["nums"].(map[string]any)
	numItems := nums["items"].(map[string]any)
	if numItems["type"] != "integer" {
		t.Errorf("nums items: expected type=integer, got %v", numItems["type"])
	}
}

// --- Nested struct test ---

type Address struct {
	Street string `json:"street" description:"Street address"`
	City   string `json:"city"`
}

type PersonInput struct {
	Name    string  `json:"name" required:"true"`
	Address Address `json:"address" description:"Home address"`
}

func TestNestedStructSchema(t *testing.T) {
	schema := GenerateSchema[PersonInput]()
	props := schema["properties"].(map[string]any)

	addr := props["address"].(map[string]any)
	if addr["type"] != "object" {
		t.Fatalf("address: expected type=object, got %v", addr["type"])
	}

	addrProps := addr["properties"].(map[string]any)
	street := addrProps["street"].(map[string]any)
	if street["type"] != "string" {
		t.Errorf("street: expected type=string, got %v", street["type"])
	}
	if street["description"] != "Street address" {
		t.Errorf("street description: expected 'Street address', got %v", street["description"])
	}
}

// --- Edge cases ---

type EmptyStruct struct{}

func TestEmptyStruct(t *testing.T) {
	schema := GenerateSchema[EmptyStruct]()
	if schema["type"] != "object" {
		t.Fatalf("expected type=object, got %v", schema["type"])
	}
	props := schema["properties"].(map[string]any)
	if len(props) != 0 {
		t.Errorf("expected 0 properties, got %d", len(props))
	}
	if _, ok := schema["required"]; ok {
		t.Error("expected no 'required' key for empty struct")
	}
}

type UnexportedFieldsStruct struct {
	Public  string `json:"public"`
	private string //nolint:unused
}

func TestUnexportedFieldsSkipped(t *testing.T) {
	schema := GenerateSchema[UnexportedFieldsStruct]()
	props := schema["properties"].(map[string]any)
	if len(props) != 1 {
		t.Errorf("expected 1 property, got %d", len(props))
	}
	if _, ok := props["public"]; !ok {
		t.Error("expected 'public' property")
	}
}

type Base struct {
	ID string `json:"id" required:"true"`
}

type WithAnonymousField struct {
	Base
	Name string `json:"name"`
}

func TestAnonymousEmbeddedField(t *testing.T) {
	schema := GenerateSchema[WithAnonymousField]()
	props := schema["properties"].(map[string]any)

	// Embedded struct is promoted — its fields should appear at the top level
	// or the embedded struct itself appears as a property depending on implementation.
	// The current implementation treats anonymous fields as exported struct fields.
	if _, hasName := props["name"]; !hasName {
		t.Error("expected 'name' property")
	}

	// The anonymous Base field is exported, so it should appear.
	// It will be treated as a nested object named "Base" since there's no json tag.
	if _, hasBase := props["Base"]; !hasBase {
		t.Error("expected 'Base' property for anonymous embedded struct")
	}
}

type JsonDashField struct {
	Visible string `json:"visible"`
	Hidden  string `json:"-"`
}

func TestJsonDashFieldSkipped(t *testing.T) {
	schema := GenerateSchema[JsonDashField]()
	props := schema["properties"].(map[string]any)
	if len(props) != 1 {
		t.Errorf("expected 1 property, got %d", len(props))
	}
	if _, ok := props["visible"]; !ok {
		t.Error("expected 'visible' property")
	}
	if _, ok := props["Hidden"]; ok {
		t.Error("did not expect 'Hidden' property (json:\"-\")")
	}
}

func TestNoRequiredFieldOmitsKey(t *testing.T) {
	type NoRequired struct {
		A string `json:"a"`
		B int    `json:"b"`
	}
	schema := GenerateSchema[NoRequired]()
	if _, ok := schema["required"]; ok {
		t.Error("expected no 'required' key when no fields are required")
	}
}

func TestNew_TypedHandler(t *testing.T) {
	type Input struct {
		Name string `json:"name" required:"true"`
	}

	tl := New("greet", "Greet someone", func(_ context.Context, in Input) (string, error) {
		return "hello " + in.Name, nil
	})

	if tl.Spec.Name != "greet" {
		t.Fatalf("expected name %q, got %q", "greet", tl.Spec.Name)
	}
	if tl.Spec.Description != "Greet someone" {
		t.Fatalf("expected description %q, got %q", "Greet someone", tl.Spec.Description)
	}
	if tl.Spec.InputSchema == nil {
		t.Fatal("expected non-nil InputSchema")
	}

	result, err := tl.Handler(context.Background(), json.RawMessage(`{"name":"Alice"}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result != "hello Alice" {
		t.Fatalf("expected %q, got %q", "hello Alice", result)
	}
}

func TestNew_InvalidJSON(t *testing.T) {
	type Input struct {
		X int `json:"x"`
	}

	tl := New("add", "Add", func(_ context.Context, in Input) (string, error) {
		return "", nil
	})

	_, err := tl.Handler(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestNewRaw_Handler(t *testing.T) {
	tl := NewRaw("echo", "Echo input", map[string]any{"type": "object"}, func(_ context.Context, input json.RawMessage) (string, error) {
		return string(input), nil
	})

	if tl.Spec.Name != "echo" {
		t.Fatalf("expected name %q, got %q", "echo", tl.Spec.Name)
	}

	result, err := tl.Handler(context.Background(), json.RawMessage(`{"msg":"hi"}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result != `{"msg":"hi"}` {
		t.Fatalf("expected %q, got %q", `{"msg":"hi"}`, result)
	}
}
