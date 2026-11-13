package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
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
		t.Fatalf("model rejected native structured output (catalog says %v): %v", catalog, err)
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
