package agentcore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/camilbinas/gude-agents/agent"
	gudea2a "github.com/camilbinas/gude-agents/agent/a2a"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/testutil"
	"pgregory.net/rapid"
)

// Feature: agentcore-enhanced, Property 13: WithA2A mounts AgentCard endpoint

// TestProperty_WithA2A_AgentCardEndpoint verifies that for any randomly generated
// AgentCard values (name, description, version), creating an A2A server with a card
// override and querying /.well-known/agent-card.json returns a response containing
// the JSON-serialized AgentCard with matching fields.
//
// **Validates: Requirements 14.1, 14.3**
func TestProperty_WithA2A_AgentCardEndpoint(t *testing.T) {
	// Create a test agent outside the rapid callback (needs *testing.T).
	prov := testutil.NewMockProvider(testutil.WithResponses(&agent.ProviderResponse{Text: "ok"}))
	a, err := agent.New(prov, prompt.Text("test system prompt"), nil, agent.WithName("test-agent"))
	if err != nil {
		t.Fatalf("failed to create test agent: %v", err)
	}

	rapid.Check(t, func(rt *rapid.T) {
		// Generate random AgentCard fields.
		cardName := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9 _\-]{0,49}`).Draw(rt, "cardName")
		cardDesc := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9 .,!?]{0,99}`).Draw(rt, "cardDescription")
		cardVersion := rapid.StringMatching(`[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}`).Draw(rt, "cardVersion")
		cardURL := rapid.StringMatching(`https://[a-z]{3,10}\.[a-z]{2,5}/[a-z]{1,10}`).Draw(rt, "cardURL")

		// Build the AgentCard that the user would pass to WithA2A.
		userCard := a2a.AgentCard{
			Name:        cardName,
			Description: cardDesc,
			Version:     cardVersion,
			SupportedInterfaces: []*a2a.AgentInterface{
				a2a.NewAgentInterface(cardURL, a2a.TransportProtocolJSONRPC),
			},
			Capabilities: a2a.AgentCapabilities{
				Streaming: true,
			},
			DefaultInputModes:  []string{"text/plain"},
			DefaultOutputModes: []string{"text/plain"},
			Skills:             []a2a.AgentSkill{},
		}

		// Replicate the runtime's pattern: create an a2a.Server with a card override.
		cardOverride := gudea2a.CardOption(func(c *a2a.AgentCard) {
			*c = userCard
		})

		a2aSrv, err := gudea2a.NewServer(a, []gudea2a.CardOption{cardOverride})
		if err != nil {
			rt.Fatalf("failed to create A2A server: %v", err)
		}

		// Get the handler (same as what the runtime mounts on the HTTP server).
		handler := a2aSrv.Handler()

		// Query the well-known agent card endpoint.
		req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		// Verify HTTP 200 response.
		if rec.Code != http.StatusOK {
			rt.Fatalf("expected status 200, got %d; body: %s", rec.Code, rec.Body.String())
		}

		// Verify Content-Type is JSON.
		ct := rec.Header().Get("Content-Type")
		if ct != "application/json" {
			rt.Fatalf("expected Content-Type application/json, got %q", ct)
		}

		// Decode the response body into an AgentCard.
		var responseCard a2a.AgentCard
		if err := json.NewDecoder(rec.Body).Decode(&responseCard); err != nil {
			rt.Fatalf("failed to decode response body as AgentCard: %v", err)
		}

		// Verify the response card matches the user-provided card fields.
		if responseCard.Name != cardName {
			rt.Fatalf("expected card name %q, got %q", cardName, responseCard.Name)
		}
		if responseCard.Description != cardDesc {
			rt.Fatalf("expected card description %q, got %q", cardDesc, responseCard.Description)
		}
		if responseCard.Version != cardVersion {
			rt.Fatalf("expected card version %q, got %q", cardVersion, responseCard.Version)
		}
	})
}
