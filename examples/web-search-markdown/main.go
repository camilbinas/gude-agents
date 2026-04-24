// Example: Web search with markdown-formatted fetch.
//
// Same as the web-search example, but uses the markdown formatter for
// web_fetch. Fetched pages are converted to clean markdown instead of
// stripped plain text — preserving headings, links, lists, and code
// blocks for better LLM comprehension and lower token usage.
//
// Prerequisites:
//
//   - TAVILY_API_KEY: API key from https://app.tavily.com
//
// Run:
//
//	go run ./web-search-markdown

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
	"github.com/camilbinas/gude-agents/agent/tool/webfetch/markdown"
	"github.com/camilbinas/gude-agents/agent/tool/websearch/tavily"
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
			tavily.New(os.Getenv("TAVILY_API_KEY")),
			webfetch.New(webfetch.WithFormatter(markdown.Formatter())),
		},
		debug.WithLogging(),
		agent.WithParallelToolExecution(),
		agent.WithMaxIterations(10),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Web search agent (markdown) ready. Type 'quit' to exit.")
	fmt.Println("Try: What are the latest Go releases?")
	fmt.Println()

	utils.Chat(context.Background(), a)
}
