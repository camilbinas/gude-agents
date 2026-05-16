package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"pgregory.net/rapid"
)

// **Validates: Requirements 6.1, 6.2, 6.3**

// TestProperty_SkillToToolMappingCorrectness verifies that for any Agent Card with N skills,
// Tools() returns exactly N tools where each tool's Spec.Name equals the corresponding skill ID,
// Spec.Description equals the skill description, and Spec.InputSchema contains a required
// "message" string property.
func TestProperty_SkillToToolMappingCorrectness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 1-10 random skills.
		numSkills := rapid.IntRange(1, 10).Draw(t, "numSkills")

		skills := make([]a2a.AgentSkill, 0, numSkills)
		seenIDs := make(map[string]struct{})
		for i := 0; i < numSkills; i++ {
			// Generate unique skill IDs.
			var id string
			for {
				id = rapid.StringMatching(`[a-z][a-z0-9_]{2,20}`).Draw(t, "skillID")
				if _, exists := seenIDs[id]; !exists {
					seenIDs[id] = struct{}{}
					break
				}
			}
			desc := rapid.StringMatching(`[A-Za-z ]{5,50}`).Draw(t, "skillDesc")

			skills = append(skills, a2a.AgentSkill{
				ID:          id,
				Name:        id,
				Description: desc,
				Tags:        []string{},
			})
		}

		// Build a fake agent card with the generated skills.
		card := a2a.AgentCard{
			Name:        "test-agent",
			Description: "A test agent",
			Version:     "1.0.0",
			Skills:      skills,
		}

		// Serve the card from a test HTTP server.
		cardJSON, err := json.Marshal(card)
		if err != nil {
			t.Fatalf("failed to marshal card: %v", err)
		}

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/agent.json" {
				w.Header().Set("Content-Type", "application/json")
				w.Write(cardJSON)
				return
			}
			http.NotFound(w, r)
		}))
		defer ts.Close()

		// Create a Client pointing at the test server.
		client, err := NewClient(context.Background(), ts.URL)
		if err != nil {
			t.Fatalf("NewClient failed: %v", err)
		}
		defer client.Close()

		// Get tools.
		tools, err := client.Tools(context.Background())
		if err != nil {
			t.Fatalf("Tools() failed: %v", err)
		}

		// Verify: exactly N tools returned.
		if len(tools) != numSkills {
			t.Fatalf("expected %d tools, got %d", numSkills, len(tools))
		}

		// Verify each tool maps correctly to its skill.
		for i, tool := range tools {
			skill := skills[i]

			// Spec.Name == skill ID
			if tool.Spec.Name != skill.ID {
				t.Fatalf("tool[%d].Spec.Name = %q, want %q", i, tool.Spec.Name, skill.ID)
			}

			// Spec.Description == skill description
			if tool.Spec.Description != skill.Description {
				t.Fatalf("tool[%d].Spec.Description = %q, want %q", i, tool.Spec.Description, skill.Description)
			}

			// Spec.InputSchema has a required "message" string property.
			schema := tool.Spec.InputSchema
			if schema == nil {
				t.Fatalf("tool[%d].Spec.InputSchema is nil", i)
			}

			// Check type is "object".
			if schemaType, ok := schema["type"].(string); !ok || schemaType != "object" {
				t.Fatalf("tool[%d].Spec.InputSchema.type = %v, want \"object\"", i, schema["type"])
			}

			// Check properties contains "message" with type "string".
			props, ok := schema["properties"].(map[string]any)
			if !ok {
				t.Fatalf("tool[%d].Spec.InputSchema.properties is not a map", i)
			}

			msgProp, ok := props["message"].(map[string]any)
			if !ok {
				t.Fatalf("tool[%d].Spec.InputSchema.properties.message is missing or not a map", i)
			}

			if msgType, ok := msgProp["type"].(string); !ok || msgType != "string" {
				t.Fatalf("tool[%d].Spec.InputSchema.properties.message.type = %v, want \"string\"", i, msgProp["type"])
			}

			// Check "message" is in the required array.
			required, ok := schema["required"].([]string)
			if !ok {
				// Try []any (JSON unmarshaling may produce []any).
				reqAny, ok2 := schema["required"].([]any)
				if !ok2 {
					t.Fatalf("tool[%d].Spec.InputSchema.required is not a string slice", i)
				}
				found := false
				for _, r := range reqAny {
					if r == "message" {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("tool[%d].Spec.InputSchema.required does not contain \"message\"", i)
				}
			} else {
				found := false
				for _, r := range required {
					if r == "message" {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("tool[%d].Spec.InputSchema.required does not contain \"message\"", i)
				}
			}
		}
	})
}

// **Validates: Requirements 7.2**

// TestProperty_IncludeExcludeSkillFiltering verifies that:
// - When IncludeSkills is used, only the specified skills appear in the result
// - When ExcludeSkills is used, the specified skills are excluded from the result
// - When both are used, include takes precedence (only included skills minus excluded ones)
func TestProperty_IncludeExcludeSkillFiltering(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 2-10 unique skills for the agent card.
		numSkills := rapid.IntRange(2, 10).Draw(t, "numSkills")

		skills := make([]a2a.AgentSkill, 0, numSkills)
		skillIDs := make([]string, 0, numSkills)
		seen := make(map[string]struct{})
		for len(skills) < numSkills {
			id := rapid.StringMatching(`[a-z][a-z0-9_]{2,12}`).Draw(t, "skillID")
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			skillIDs = append(skillIDs, id)
			skills = append(skills, a2a.AgentSkill{
				ID:          id,
				Name:        "Skill " + id,
				Description: "Description for " + id,
			})
		}

		// Set up a test HTTP server that serves the agent card with these skills.
		card := a2a.AgentCard{
			Name:        "test-agent",
			Description: "A test agent",
			Version:     "1.0.0",
			Skills:      skills,
		}

		cardJSON, err := json.Marshal(card)
		if err != nil {
			t.Fatalf("failed to marshal card: %v", err)
		}

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/agent.json" {
				w.Header().Set("Content-Type", "application/json")
				w.Write(cardJSON)
				return
			}
			http.NotFound(w, r)
		}))
		defer ts.Close()

		// Create the client.
		client, err := NewClient(context.Background(), ts.URL)
		if err != nil {
			t.Fatalf("NewClient failed: %v", err)
		}
		defer client.Close()

		// Choose a filter mode: include-only, exclude-only, or both.
		mode := rapid.IntRange(0, 2).Draw(t, "filterMode")

		// Pick a random subset of skill IDs for include/exclude.
		includeCount := rapid.IntRange(1, len(skillIDs)).Draw(t, "includeCount")
		excludeCount := rapid.IntRange(1, len(skillIDs)).Draw(t, "excludeCount")

		includeIDs := rapid.Permutation(skillIDs).Draw(t, "includeOrder")[:includeCount]
		excludeIDs := rapid.Permutation(skillIDs).Draw(t, "excludeOrder")[:excludeCount]

		ctx := context.Background()

		switch mode {
		case 0:
			// Include-only: only included skills should appear.
			tools, err := client.Tools(ctx, IncludeSkills(includeIDs...))
			if err != nil {
				t.Fatalf("Tools() with IncludeSkills failed: %v", err)
			}

			// Build expected set.
			includeSet := make(map[string]struct{})
			for _, id := range includeIDs {
				includeSet[id] = struct{}{}
			}

			// Verify only included skills are present.
			for _, tool := range tools {
				if _, ok := includeSet[tool.Spec.Name]; !ok {
					t.Fatalf("tool %q should not be present (not in include set)", tool.Spec.Name)
				}
			}

			// Verify all included skills are present.
			toolNames := make(map[string]struct{})
			for _, tool := range tools {
				toolNames[tool.Spec.Name] = struct{}{}
			}
			for _, id := range includeIDs {
				if _, ok := toolNames[id]; !ok {
					t.Fatalf("tool %q should be present (in include set)", id)
				}
			}

		case 1:
			// Exclude-only: excluded skills should not appear.
			tools, err := client.Tools(ctx, ExcludeSkills(excludeIDs...))
			if err != nil {
				t.Fatalf("Tools() with ExcludeSkills failed: %v", err)
			}

			excludeSet := make(map[string]struct{})
			for _, id := range excludeIDs {
				excludeSet[id] = struct{}{}
			}

			// Verify no excluded skills are present.
			for _, tool := range tools {
				if _, ok := excludeSet[tool.Spec.Name]; ok {
					t.Fatalf("tool %q should not be present (in exclude set)", tool.Spec.Name)
				}
			}

			// Verify all non-excluded skills are present.
			toolNames := make(map[string]struct{})
			for _, tool := range tools {
				toolNames[tool.Spec.Name] = struct{}{}
			}
			for _, id := range skillIDs {
				if _, inExclude := excludeSet[id]; inExclude {
					continue
				}
				if _, ok := toolNames[id]; !ok {
					t.Fatalf("tool %q should be present (not in exclude set)", id)
				}
			}

		case 2:
			// Both include and exclude: include takes precedence, then exclude removes from included.
			tools, err := client.Tools(ctx, IncludeSkills(includeIDs...), ExcludeSkills(excludeIDs...))
			if err != nil {
				t.Fatalf("Tools() with both filters failed: %v", err)
			}

			includeSet := make(map[string]struct{})
			for _, id := range includeIDs {
				includeSet[id] = struct{}{}
			}
			excludeSet := make(map[string]struct{})
			for _, id := range excludeIDs {
				excludeSet[id] = struct{}{}
			}

			// Expected: skills that are in include AND not in exclude.
			expected := make(map[string]struct{})
			for _, id := range includeIDs {
				if _, inExclude := excludeSet[id]; !inExclude {
					expected[id] = struct{}{}
				}
			}

			// Verify exact match.
			toolNames := make(map[string]struct{})
			for _, tool := range tools {
				toolNames[tool.Spec.Name] = struct{}{}
			}

			for _, tool := range tools {
				if _, ok := expected[tool.Spec.Name]; !ok {
					t.Fatalf("tool %q should not be present (not in expected set: included minus excluded)", tool.Spec.Name)
				}
			}
			for id := range expected {
				if _, ok := toolNames[id]; !ok {
					t.Fatalf("tool %q should be present (in expected set: included minus excluded)", id)
				}
			}
		}
	})
}
