package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent/tool"
)

type nativeSchemaProvider struct {
	caps ModelCapabilities
	text string
	got  ModelRequest
}

func (*nativeSchemaProvider) Name() string { return "native-test" }
func (p *nativeSchemaProvider) Stream(_ context.Context, req ModelRequest, _ func(ModelEvent)) (*ModelResponse, error) {
	p.got = req
	if req.ResponseFormat != nil {
		return &ModelResponse{Text: p.text}, nil
	}
	return &ModelResponse{ToolCalls: []tool.Call{{ToolUseID: "1", Name: structuredOutputToolName, Input: json.RawMessage(p.text)}}}, nil
}
func (p *nativeSchemaProvider) Capabilities() ModelCapabilities { return p.caps }

var nativeTestSchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{"name": map[string]any{"type": "string"}},
	"required":   []any{"name"},
}

func TestInvokeSchema_UsesNativeModeWhenSupported(t *testing.T) {
	p := &nativeSchemaProvider{caps: ModelCapabilities{NativeStructuredOutput: Supported}, text: `{"name":"gude"}`}
	a, err := New(p, "sys")
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Name string }
	res, err := a.InvokeSchema(Background(), "q", nativeTestSchema, func(raw json.RawMessage) error { return json.Unmarshal(raw, &out) })
	if err != nil {
		t.Fatal(err)
	}
	if p.got.ResponseFormat == nil || p.got.ResponseFormat.Schema == nil {
		t.Fatal("native mode must send ResponseFormat with the schema")
	}
	if len(p.got.Tools) != 0 || p.got.ToolChoice != nil {
		t.Fatalf("native mode must not send the schema tool: tools=%v choice=%v", p.got.Tools, p.got.ToolChoice)
	}
	if out.Name != "gude" || res.Text != `{"name":"gude"}` {
		t.Fatalf("decoded %+v, text %q", out, res.Text)
	}
}

func TestInvokeSchema_NativeOutputIsStillValidated(t *testing.T) {
	p := &nativeSchemaProvider{caps: ModelCapabilities{NativeStructuredOutput: Supported}, text: `{"other":1}`}
	a, err := New(p, "sys")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.InvokeSchema(Background(), "q", nativeTestSchema, func(json.RawMessage) error { return nil })
	var se *StructuredOutputError
	if !errors.As(err, &se) || se.Reason != ReasonSchemaValidation {
		t.Fatalf("err = %v, want schema validation failure", err)
	}
}

func TestInvokeSchema_ToolModeWhenNativeNotSupported(t *testing.T) {
	for _, c := range []Capability{Unknown, Unsupported} {
		p := &nativeSchemaProvider{caps: ModelCapabilities{NativeStructuredOutput: c}, text: `{"name":"gude"}`}
		a, err := New(p, "sys")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.InvokeSchema(Background(), "q", nativeTestSchema, func(json.RawMessage) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if p.got.ResponseFormat != nil || len(p.got.Tools) != 1 {
			t.Fatalf("capability %v: want the schema tool, got format=%v tools=%v", c, p.got.ResponseFormat, p.got.Tools)
		}
	}
}

func TestInvokeSchema_NativeModeWorksWithoutToolUse(t *testing.T) {
	p := &nativeSchemaProvider{caps: ModelCapabilities{NativeStructuredOutput: Supported, ToolUse: Unsupported}, text: `{"name":"x"}`}
	a, err := New(p, "sys")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.InvokeSchema(Background(), "q", nativeTestSchema, func(json.RawMessage) error { return nil }); err != nil {
		t.Fatalf("native mode must not need tool use: %v", err)
	}
}

func TestClosedObjects(t *testing.T) {
	in := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"properties": map[string]any{"type": "string"}, // a field named like the keyword
			"tags":       map[string]any{"type": "object"}, // map field: stays open
			"items": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}},
			},
			"open": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": true},
		},
	}
	out := closedObjects(in)
	if out["additionalProperties"] != false {
		t.Fatal("root object must be closed")
	}
	props := out["properties"].(map[string]any)
	if _, added := props["additionalProperties"]; added {
		t.Fatal("property names must not be treated as a schema")
	}
	if _, set := props["tags"].(map[string]any)["additionalProperties"]; set {
		t.Fatal("an object without properties must stay open")
	}
	item := props["items"].(map[string]any)["items"].(map[string]any)
	if item["additionalProperties"] != false {
		t.Fatal("nested array item objects must be closed")
	}
	if props["open"].(map[string]any)["additionalProperties"] != true {
		t.Fatal("an explicit additionalProperties must be kept")
	}
	if _, touched := in["additionalProperties"]; touched {
		t.Fatal("the caller's schema must not be modified")
	}
}

func TestInvokeSchema_NativeSendsClosedSchemaButValidatesOriginal(t *testing.T) {
	p := &nativeSchemaProvider{caps: ModelCapabilities{NativeStructuredOutput: Supported}, text: `{"name":"gude"}`}
	a, err := New(p, "sys")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.InvokeSchema(Background(), "q", nativeTestSchema, func(json.RawMessage) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if p.got.ResponseFormat.Schema["additionalProperties"] != false {
		t.Fatal("native request must close objects")
	}
	if _, touched := nativeTestSchema["additionalProperties"]; touched {
		t.Fatal("the caller's schema must not be modified")
	}
}
