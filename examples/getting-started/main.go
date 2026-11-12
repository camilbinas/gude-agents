// Run: go run ./getting-started
//
// The smallest useful Gude program: choose a provider, create an agent,
// invoke it, and read the Result.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	// Choose the model explicitly. Gude never picks one for you. This model
	// must be available in your AWS region and account.
	provider := bedrock.Must(bedrock.GlobalClaudeSonnet5_5())
	// agent.Agent(...) returns a *agent.Runtime. agent.New(...) is the same
	// constructor under its original name and remains available.
	a, err := agent.Agent(provider, "You are a helpful assistant.")
	if err != nil {
		log.Fatal(err)
	}

	// agent.NewContext configures one invocation. This agent has no
	// conversation store, so no conversation ID is required.
	ctx := agent.NewContext(context.Background())

	result, err := a.Invoke(ctx, "Why is the sky blue?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Text)
}
