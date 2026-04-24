// Example: Web search and fetch tools.
//
// Gives the agent two tools:
//   - web_search: searches the web using the Tavily Search API
//   - web_fetch: fetches a URL and returns its text content
//
// The agent searches first, then fetches the most relevant result to
// answer the question with up-to-date information.
//
// Prerequisites:
//
//   - TAVILY_API_KEY: API key from https://app.tavily.com
//
// Run:
//
//	go run ./web-search

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/agent/tool/webfetch"
	"github.com/camilbinas/gude-agents/agent/tool/websearch/tavily"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	apiKey := requireEnv("TAVILY_API_KEY")

	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.Text("You are a helpful research assistant with access to web search and fetch tools. "+
			"Always search the web before answering questions about current events, recent data, or anything "+
			"that might have changed since your training. After searching, fetch the most relevant result "+
			"to get detailed information. Be concise and cite your sources."),
		[]tool.Tool{
			tavily.New(apiKey),
			webfetch.New(),
		},
		debug.WithLogging(),
		agent.WithParallelToolExecution(),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Web search agent ready. Type 'quit' to exit.")
	fmt.Println("Try: What are the latest Go releases?")
	fmt.Println()

	utils.Chat(context.Background(), a)
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s is required. See the example header for setup instructions.", key)
	}
	return v
}
