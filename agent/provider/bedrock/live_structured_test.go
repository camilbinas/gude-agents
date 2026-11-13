package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/structured"
)

// Sends a native JSON-schema response format whatever the catalog says, so a
// live run shows whether the model really accepts it.
func TestLive_NativeStructuredOutput(t *testing.T) {
	p := liveProvider(t)
	schema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"city": map[string]any{"type": "string"}},
		"required":             []any{"city"},
		"additionalProperties": false,
	}
	resp, err := p.Stream(liveContext(t), agent.ModelRequest{
		Messages:       []agent.Message{liveUser(agent.TextBlock{Text: "What is the capital of France?"})},
		ResponseFormat: &agent.ResponseFormat{Name: "answer", Schema: schema},
	}, nil)
	catalog := p.Capabilities().NativeStructuredOutput
	if err != nil {
		if catalog != agent.Supported {
			t.Skipf("model rejects native structured output, and the catalog agrees (%v)", catalog)
		}
		t.Fatalf("model rejected native structured output, but the catalog says Supported: %v", err)
	}
	var out struct {
		City string `json:"city"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &out); err != nil || out.City == "" {
		t.Fatalf("answer %q is not the requested JSON: %v", resp.Text, err)
	}
	if catalog != agent.Supported {
		t.Errorf("model accepts native structured output, but the catalog says %v", catalog)
	}
}

// Runs structured.Invoke end to end, on whichever path the catalog picks.
func TestLive_StructuredInvoke(t *testing.T) {
	p := liveProvider(t)
	a, err := agent.New(p, "Answer briefly.")
	if err != nil {
		t.Fatal(err)
	}
	type answer struct {
		City    string `json:"city" required:"true"`
		Country string `json:"country"`
	}
	res, err := structured.Invoke[answer](agent.Background(), a, "What is the capital of France?")
	if err != nil {
		t.Fatalf("native=%v: %v", p.Capabilities().NativeStructuredOutput, err)
	}
	if res.Value.City == "" {
		t.Fatalf("empty city in %+v", res.Value)
	}
}
