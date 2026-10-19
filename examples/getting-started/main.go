// Run: go run ./getting-started
//
// The smallest useful Gude program: choose a provider, create an Agent,
// invoke it, and read the Result.
package main

import (
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	// Choose a model that is available in your AWS region and account.
	provider := bedrock.Must(bedrock.GlobalClaudeSonnet5_5())
	a, err := agent.New(provider, "You are a helpful assistant.")
	if err != nil {
		log.Fatal(err)
	}

	result, err := a.Invoke(agent.Background(), "Why is the sky blue?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Text)
}
