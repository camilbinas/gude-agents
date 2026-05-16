package a2a

import (
	"context"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/tool"
)

func TestDeriveCard_Basic(t *testing.T) {
	a, err := agent.New(
		&fakeProvider{response: "ok"},
		prompt.Text("I help with testing tasks."),
		nil,
		agent.WithName("test-helper"),
	)
	if err != nil {
		t.Fatal(err)
	}

	card := DeriveCard(a)

	if card.Name != "test-helper" {
		t.Errorf("Name = %q, want %q", card.Name, "test-helper")
	}
	if card.Description != "I help with testing tasks." {
		t.Errorf("Description = %q, want %q", card.Description, "I help with testing tasks.")
	}
	if !card.Capabilities.Streaming {
		t.Error("expected Streaming capability to be true")
	}
	if card.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", card.Version, "1.0.0")
	}
}

func TestDeriveCard_WithTools(t *testing.T) {
	tools := []tool.Tool{
		tool.NewSimple("greet", "Greets the user", func(_ context.Context) (string, error) {
			return "hi", nil
		}),
	}

	a, err := agent.New(
		&fakeProvider{response: "ok"},
		prompt.Text("Agent with tools"),
		tools,
		agent.WithName("tool-agent"),
	)
	if err != nil {
		t.Fatal(err)
	}

	card := DeriveCard(a)

	if len(card.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(card.Skills))
	}
	if card.Skills[0].ID != "greet" {
		t.Errorf("skill ID = %q, want %q", card.Skills[0].ID, "greet")
	}
	if card.Skills[0].Description != "Greets the user" {
		t.Errorf("skill Description = %q, want %q", card.Skills[0].Description, "Greets the user")
	}
}

func TestDeriveCard_WithOptions(t *testing.T) {
	a, err := agent.New(
		&fakeProvider{response: "ok"},
		prompt.Text("Short instructions"),
		nil,
		agent.WithName("my-agent"),
	)
	if err != nil {
		t.Fatal(err)
	}

	card := DeriveCard(a,
		WithCardDescription("Custom description"),
		WithCardVersion("2.0.0"),
		WithCardURL("https://example.com/a2a"),
	)

	if card.Description != "Custom description" {
		t.Errorf("Description = %q, want %q", card.Description, "Custom description")
	}
	if card.Version != "2.0.0" {
		t.Errorf("Version = %q, want %q", card.Version, "2.0.0")
	}
	if len(card.SupportedInterfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(card.SupportedInterfaces))
	}
	if card.SupportedInterfaces[0].URL != "https://example.com/a2a" {
		t.Errorf("URL = %q, want %q", card.SupportedInterfaces[0].URL, "https://example.com/a2a")
	}
}

func TestDeriveCard_TruncatesLongDescription(t *testing.T) {
	longInstructions := ""
	for i := 0; i < 300; i++ {
		longInstructions += "x"
	}

	a, err := agent.New(
		&fakeProvider{response: "ok"},
		prompt.Text(longInstructions),
		nil,
		agent.WithName("verbose-agent"),
	)
	if err != nil {
		t.Fatal(err)
	}

	card := DeriveCard(a)

	if len(card.Description) != 200 {
		t.Errorf("Description length = %d, want 200", len(card.Description))
	}
}

func TestWithCardSkills(t *testing.T) {
	a, err := agent.New(
		&fakeProvider{response: "ok"},
		prompt.Text("Agent"),
		nil,
		agent.WithName("agent"),
	)
	if err != nil {
		t.Fatal(err)
	}

	customSkills := []a2a.AgentSkill{
		{ID: "custom", Name: "Custom Skill", Description: "A custom skill", Tags: []string{"custom"}},
	}

	card := DeriveCard(a, WithCardSkills(customSkills))

	if len(card.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(card.Skills))
	}
	if card.Skills[0].ID != "custom" {
		t.Errorf("skill ID = %q, want %q", card.Skills[0].ID, "custom")
	}
}
