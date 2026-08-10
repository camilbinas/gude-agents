// Example: Web search using SerpAPI.
//
// Gives the agent two tools:
//   - web_search: searches the web using the SerpAPI service (Google by default)
//   - web_fetch: fetches a URL and returns its text content
//
// The agent searches first, then fetches the most relevant result to
// answer the question with up-to-date information.
//
// Prerequisites:
//
//   - SERPAPI_API_KEY: API key from https://serpapi.com/manage-api-key
//
// Run:
//
//	go run ./web-search-serpapi

package main

import (
	"fmt"
	"log"
	"os"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/agent/tool/webfetch"
	"github.com/camilbinas/gude-agents/agent/tool/websearch/serpapi"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.APE{
			Action:      "Search the web and fetch pages to answer questions with up-to-date information.",
			Purpose:     "Provide accurate, current answers by using web_search before responding, then web_fetch to read the most relevant result in detail.",
			Expectation: "Be concise. Always cite sources with URLs. If search results are sufficient, skip fetching.",
		},
		[]tool.Tool{
			serpapi.New(os.Getenv("SERPAPI_API_KEY")),
			webfetch.New(),
		},
		auto.WithLogging(),
		agent.WithMaxIterations(10),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Web search agent ready (SerpAPI). Type 'quit' to exit.")
	fmt.Println("Try: What are the latest Go releases?")
	fmt.Println()

	utils.Chat(agent.Background(), a)
}
