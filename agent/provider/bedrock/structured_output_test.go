package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/camilbinas/gude-agents/agent"
)

// sampleSchema is a strict-mode-ready JSON schema: an object whose every
// property is required with additionalProperties:false.
func sampleSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":   map[string]any{"type": "string"},
			"urgent":  map[string]any{"type": "boolean"},
			"summary": map[string]any{"type": "string"},
		},
		"required":             []any{"title", "urgent", "summary"},
		"additionalProperties": false,
	}
}

func TestToBedrockOutputConfig_Nil(t *testing.T) {
	if oc := toBedrockOutputConfig(nil); oc != nil {
		t.Fatalf("expected nil OutputConfig for nil ResponseFormat, got %+v", oc)
	}
}

func TestToBedrockOutputConfig_SchemaAsJSONString(t *testing.T) {
	schema := sampleSchema()
	oc := toBedrockOutputConfig(&agent.ResponseFormat{Name: "report", Schema: schema})
	if oc == nil || oc.TextFormat == nil {
		t.Fatal("expected non-nil OutputConfig with TextFormat")
	}
	if oc.TextFormat.Type != types.OutputFormatTypeJsonSchema {
		t.Fatalf("type = %q, want json_schema", oc.TextFormat.Type)
	}
	js, ok := oc.TextFormat.Structure.(*types.OutputFormatStructureMemberJsonSchema)
	if !ok {
		t.Fatalf("structure = %T, want *OutputFormatStructureMemberJsonSchema", oc.TextFormat.Structure)
	}
	if aws.ToString(js.Value.Name) != "report" {
		t.Errorf("name = %q, want report", aws.ToString(js.Value.Name))
	}
	// Schema must be sent as a JSON string, not an object, and round-trip to
	// the original schema unchanged.
	raw := aws.ToString(js.Value.Schema)
	if raw == "" {
		t.Fatal("schema string is empty")
	}
	if !sameJSON(t, []byte(raw), schema) {
		t.Errorf("schema round-trip changed it; got %s", raw)
	}
}

func TestToBedrockOutputConfig_OmitsEmptyName(t *testing.T) {
	oc := toBedrockOutputConfig(&agent.ResponseFormat{Schema: sampleSchema()})
	js := oc.TextFormat.Structure.(*types.OutputFormatStructureMemberJsonSchema)
	if js.Value.Name != nil {
		t.Errorf("name = %q, want nil for empty ResponseFormat.Name", aws.ToString(js.Value.Name))
	}
}

func TestStreamSendsOutputConfigAndNoToolsForResponseFormat(t *testing.T) {
	ct := &captureTransport{response: converseTextStream(`{"title":"hi","urgent":true,"summary":"s"}`)}
	p := providerWithTransport(t, "global.anthropic.claude-sonnet-5-5", ct)

	schema := sampleSchema()
	resp, err := p.Stream(context.Background(), agent.ModelRequest{
		Messages:       []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "extract"}}}},
		ResponseFormat: &agent.ResponseFormat{Name: "report", Schema: schema},
	}, nil)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	// The model's JSON answer comes back as resp.Text, which is what the engine
	// reads for native structured output.
	if resp.Text != `{"title":"hi","urgent":true,"summary":"s"}` {
		t.Fatalf("resp.Text = %q, want the model JSON", resp.Text)
	}

	sent := ct.decodeBody(t)

	oc, ok := sent["outputConfig"].(map[string]any)
	if !ok {
		t.Fatalf("request missing outputConfig; body=%s", ct.body)
	}
	tf, ok := oc["textFormat"].(map[string]any)
	if !ok {
		t.Fatalf("outputConfig missing textFormat; body=%s", ct.body)
	}
	if tf["type"] != "json_schema" {
		t.Errorf("textFormat.type = %v, want json_schema", tf["type"])
	}
	structure, _ := tf["structure"].(map[string]any)
	jsonSchema, _ := structure["jsonSchema"].(map[string]any)
	if jsonSchema == nil {
		t.Fatalf("textFormat.structure.jsonSchema missing; body=%s", ct.body)
	}
	if jsonSchema["name"] != "report" {
		t.Errorf("jsonSchema.name = %v, want report", jsonSchema["name"])
	}
	// The schema travels as a JSON string equal to the requested schema.
	schemaStr, ok := jsonSchema["schema"].(string)
	if !ok {
		t.Fatalf("jsonSchema.schema is %T, want string; body=%s", jsonSchema["schema"], ct.body)
	}
	if !sameJSON(t, []byte(schemaStr), schema) {
		t.Errorf("sent schema differs from requested; got %s", schemaStr)
	}

	// Native structured output sends no tools and no tool choice.
	if _, present := sent["toolConfig"]; present {
		t.Errorf("request carried toolConfig alongside outputConfig; body=%s", ct.body)
	}
}

func TestStreamOmitsOutputConfigWithoutResponseFormat(t *testing.T) {
	ct := &captureTransport{response: converseTextStream("plain answer")}
	p := providerWithTransport(t, "global.anthropic.claude-sonnet-5-5", ct)

	if _, err := p.Stream(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{{Role: agent.RoleUser, Content: []agent.ContentBlock{agent.TextBlock{Text: "hi"}}}},
	}, nil); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	sent := ct.decodeBody(t)
	if _, present := sent["outputConfig"]; present {
		t.Errorf("request carried outputConfig without a ResponseFormat; body=%s", ct.body)
	}
}

// sameJSON reports whether raw decodes to the same JSON value as want.
func sameJSON(t *testing.T, raw []byte, want any) bool {
	t.Helper()
	var got any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	return string(a) == string(b)
}

// captureTransport records the outgoing request body and returns a canned
// ConverseStream eventstream response.
type captureTransport struct {
	body     []byte
	response []byte
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		c.body, _ = io.ReadAll(req.Body)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(c.response)),
		Request:    req,
	}, nil
}

func (c *captureTransport) decodeBody(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(c.body, &m); err != nil {
		t.Fatalf("request body is not JSON: %v\nbody=%s", err, c.body)
	}
	return m
}

// providerWithTransport builds a provider whose SDK client uses rt as its HTTP
// transport, so a test can capture the outgoing Converse request.
func providerWithTransport(t *testing.T, modelID string, rt http.RoundTripper) *BedrockProvider {
	t.Helper()
	// Static sigv4 credentials so the SDK selects the sigv4 auth scheme and
	// signs against the stub transport, avoiding the bearer-token path.
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDTEST")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "SECRETTEST")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	p, err := New(modelID, WithRegion("us-east-1"), withHTTPClient(&http.Client{Transport: rt}))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// converseTextStream encodes the ConverseStream eventstream frames that surface
// a single text content block and an end_turn stop, using the SDK's own
// event-stream encoder so framing and CRCs are correct.
func converseTextStream(text string) []byte {
	delta, _ := json.Marshal(map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": text}})
	stop, _ := json.Marshal(map[string]any{"contentBlockIndex": 0})
	msgStop, _ := json.Marshal(map[string]any{"stopReason": "end_turn"})

	var buf bytes.Buffer
	enc := eventstream.NewEncoder()
	for _, ev := range []struct {
		name    string
		payload []byte
	}{
		{"contentBlockDelta", delta},
		{"contentBlockStop", stop},
		{"messageStop", msgStop},
	} {
		msg := eventstream.Message{
			Headers: eventstream.Headers{
				{Name: ":message-type", Value: eventstream.StringValue("event")},
				{Name: ":event-type", Value: eventstream.StringValue(ev.name)},
				{Name: ":content-type", Value: eventstream.StringValue("application/json")},
			},
			Payload: ev.payload,
		}
		if err := enc.Encode(&buf, msg); err != nil {
			panic(err)
		}
	}
	return buf.Bytes()
}
