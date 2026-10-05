// Run: go run ./memory
//
// Long-term memory is identity-scoped and independent from conversation state.
// The agent can remember, recall, and forget typed facts for the same identity.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

type Fact struct {
	ID       string `json:"id" db:"id,pk"`
	UserID   string `json:"user_id" db:"user_id,identifier"`
	Text     string `json:"fact" db:"fact,content" description:"Fact, preference, or decision" required:"true"`
	Category string `json:"category" db:"category" description:"Optional category"`
}

func main() {
	embedder := bedrock.MustEmbedder(bedrock.TitanEmbedV2())
	store, err := memory.NewStore[Fact](embedder)
	if err != nil {
		log.Fatal(err)
	}
	a, err := agent.New(
		bedrock.Must(bedrock.Standard()),
		"Use remember for durable user facts, recall before answering memory questions, and forget when asked.",
		agent.WithTools(memory.NewRememberTool(store), memory.NewRecallTool(store), memory.NewForgetTool(store)),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := agent.NewContext(context.Background()).WithIdentity("user-123")
	for _, input := range []string{"Remember that I prefer dark mode.", "What preferences do I have?", "Forget that preference."} {
		result, err := a.Invoke(ctx, input)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Q: %s\nA: %s\n\n", input, result.Text)
	}
}
