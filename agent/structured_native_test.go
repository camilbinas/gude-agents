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
