// Example: Typed memory with a custom Profile struct.
//
// Uses TypedMemory[T] to store a domain-specific Profile struct instead of
// plain facts, with typed remember/recall tools.
//
// Run:
//
//	go run ./memory-typed
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/examples/utils"
)

// Profile is a custom struct representing a user's identity and skills.
// The struct tags control both JSON serialization and the tool schema
// that the LLM sees when calling the remember tool.
type Profile struct {
	Name   string   `json:"name" description:"The user's name" required:"true"`
	Role   string   `json:"role" description:"Job title or role (e.g. backend engineer, designer)"`
	Skills []string `json:"skills" description:"List of technical skills or languages"`
}

// profileContent extracts the embeddable text from a Profile. This is what
// gets embedded as a vector for similarity search — combine the fields that
// matter most for retrieval.
func profileContent(p Profile) string {
	parts := []string{p.Name}
	if p.Role != "" {
		parts = append(parts, p.Role)
	}
	if len(p.Skills) > 0 {
		parts = append(parts, strings.Join(p.Skills, ", "))
	}
	return strings.Join(parts, " — ")
}

func main() {
	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())

	// 1. Create a typed store: InMemoryStore → TypedMemoryStore.
	memStore := memory.NewInMemoryStore()
	profileStore := memory.NewTypedMemoryStore[Profile](memStore)

	// 2. Create typed tools. The LLM sees the Profile struct as the input
	//    schema for the remember tool, and a query/limit schema for recall.
	tools := []tool.Tool{
		memory.NewTypedRememberTool(
			profileStore, embedder, profileContent,
			tool.GenerateSchema[Profile],
			memory.WithToolName("save_profile"),
			memory.WithToolDescription("Save or update a user profile with their name, role, and skills."),
		),
		memory.NewTypedRecallTool(
			profileStore, embedder,
			memory.WithToolName("lookup_profile"),
			memory.WithToolDescription("Look up user profile information by searching with a natural-language query."),
		),
	}

	store := conversation.NewWindow(conversation.NewInMemory(), 40)

	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.RISEN{
			Role:         "You are a friendly assistant who remembers user profiles.",
			Instructions: "Use save_profile to store the user's name, role, and skills when they share them. Use lookup_profile to retrieve profile information before answering questions about the user.",
			Steps:        "1) When the user introduces themselves or shares personal details, save their profile. 2) Before answering questions about the user, look up their profile. 3) Respond naturally using the profile data.",
			EndGoal:      "Build a rich profile of the user over time and always reference what you know about them.",
			Narrowing:    "Keep responses conversational. Don't dump raw JSON — synthesize profile data into natural sentences.",
		},
		tools,
		debug.WithLogging(),
		agent.WithConversation(store, "typed-memory-session"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 3. Set the identifier on the context.
	ctx := agent.WithIdentifier(context.Background(), "user-123")

	fmt.Println("Chat agent with typed profile memory. Type 'quit' to exit.")
	fmt.Println("Try: 'My name is Alice, I'm a backend engineer, and I know Go and Python'")
	fmt.Println("Then: 'What do you know about me?'")
	fmt.Println()

	utils.Chat(ctx, a)
}
