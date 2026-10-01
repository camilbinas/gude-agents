package structured

import (
	"errors"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
)

type schemaAddress struct {
	City string `json:"city" required:"true"`
}

type schemaPerson struct {
	Name    string         `json:"name" required:"true"`
	Age     int            `json:"age"`
	Color   string         `json:"color" enum:"red,green"`
	Address *schemaAddress `json:"address"`
	Tags    []string       `json:"tags"`
}

func structuredReason(err error) string {
	var se *agent.StructuredOutputError
	if errors.As(err, &se) {
		return se.Reason
	}
	return ""
}

func TestInvokeEnforcesSchema(t *testing.T) {
	tests := []struct {
		name   string
		output string
		reason string // "" means success
	}{
		{name: "valid output", output: `{"name":"Ada","age":3,"color":"red","address":{"city":"Lisbon"},"tags":["a"]}`},
		{name: "missing required property", output: `{}`, reason: "schema_validation"},
		{name: "wrong property type", output: `{"name":"Ada","age":"three"}`, reason: "schema_validation"},
		{name: "null for non-nullable property", output: `{"name":null}`, reason: "schema_validation"},
		{name: "enum violation", output: `{"name":"Ada","color":"blue"}`, reason: "schema_validation"},
		{name: "nested schema violation", output: `{"name":"Ada","address":{}}`, reason: "schema_validation"},
		{name: "array item type violation", output: `{"name":"Ada","tags":[1,2]}`, reason: "schema_validation"},
		{name: "malformed JSON", output: `{broken`, reason: "deserialize"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &conversation{}
			a, err := agent.New(&testProvider{response: structuredResponse(tt.output)}, "sys",
				agent.WithConversationStore(store))
			if err != nil {
				t.Fatal(err)
			}
			got, err := Invoke[schemaPerson](agent.Background().WithConversationID("conv"), a, "input")
			store.mu.Lock()
			saves := store.saves
			store.mu.Unlock()
			if tt.reason == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.Value.Name != "Ada" || got.Value.Address == nil || got.Value.Address.City != "Lisbon" {
					t.Fatalf("Value = %#v", got.Value)
				}
				if saves != 1 {
					t.Fatalf("saves = %d, want 1", saves)
				}
				return
			}
			if r := structuredReason(err); r != tt.reason {
				t.Fatalf("err = %v (reason %q), want reason %q", err, r, tt.reason)
			}
			if saves != 0 {
				t.Fatalf("invalid output persisted %d times", saves)
			}
			if got.Value.Name != "" || got.Value.Address != nil || got.Value.Tags != nil {
				t.Fatalf("Value populated on failure: %#v", got.Value)
			}
		})
	}
}

// TestInvokeValidatesGuardrailRewrittenOutput verifies validation runs on the
// final, guardrail-processed JSON: a schema-valid provider output rewritten
// to schema-invalid JSON fails and is not persisted, while token usage of the
// provider call is still accounted.
func TestInvokeValidatesGuardrailRewrittenOutput(t *testing.T) {
	store := &conversation{}
	provider := &testProvider{response: structuredResponse(`{"name":"Ada","count":1}`)}
	provider.response.Usage = agent.TokenUsage{InputTokens: 4, OutputTokens: 2}
	a, err := agent.New(provider, "sys",
		agent.WithConversationStore(store),
		agent.WithOutputGuardrail(func(_ *agent.Context, _ string) (string, error) {
			return `{"count":1}`, nil // drops the required "name"
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Invoke[profile](agent.Background().WithConversationID("conv"), a, "input")
	if r := structuredReason(err); r != "schema_validation" {
		t.Fatalf("err = %v (reason %q), want schema_validation", err, r)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.saves != 0 {
		t.Fatalf("guardrail-invalidated output persisted %d times", store.saves)
	}
	if got.Run.Usage.Total() != 6 {
		t.Fatalf("usage = %#v, want provider usage accounted", got.Run.Usage)
	}
}

// TestInvokeDecodeFailureAfterValidSchema verifies a schema-valid output that
// still cannot decode into T (a Go-specific constraint the JSON schema cannot
// express) reports "deserialize", not "schema_validation".
func TestInvokeDecodeFailureAfterValidSchema(t *testing.T) {
	type small struct {
		Value int8 `json:"value" required:"true"`
	}
	store := &conversation{}
	a, err := agent.New(&testProvider{response: structuredResponse(`{"value":300}`)}, "sys",
		agent.WithConversationStore(store))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Invoke[small](agent.Background().WithConversationID("conv"), a, "input")
	if r := structuredReason(err); r != "deserialize" {
		t.Fatalf("err = %v (reason %q), want deserialize", err, r)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.saves != 0 {
		t.Fatalf("undecodable output persisted %d times", store.saves)
	}
}
