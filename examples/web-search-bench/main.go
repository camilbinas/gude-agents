// Example: Benchmark plain text vs markdown formatter for web_fetch.
//
// Runs the same query ("what is the current go version?") 10 times with
// each formatter and prints a comparison of token usage, latency, and
// response length.
//
// Prerequisites:
//
//   - TAVILY_API_KEY: API key from https://app.tavily.com
//
// Run:
//
//	go run ./web-search-bench

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/camilbinas/gude-agents/agent/tool/webfetch"
	"github.com/camilbinas/gude-agents/agent/tool/webfetch/markdown"
	"github.com/camilbinas/gude-agents/agent/tool/websearch/tavily"
	"github.com/joho/godotenv"
)

const (
	query = "what new features were added in Go 1.25 and when was it released?"
	runs  = 10
)

type result struct {
	inputTokens  int
	outputTokens int
	duration     time.Duration
	responseLen  int
}

func main() {
	godotenv.Load() //nolint

	apiKey := requireEnv("TAVILY_API_KEY")
	prov := bedrock.Must(bedrock.Standard())

	sysPrompt := prompt.APE{
		Action:      "Search the web and fetch pages to answer questions with up-to-date information.",
		Purpose:     "Provide accurate, current answers by using web_search before responding, then web_fetch to read the most relevant result in detail.",
		Expectation: "Be concise. Always cite sources with URLs. If search results are sufficient, skip fetching.",
	}

	fmt.Println("=== Web Fetch Formatter Benchmark ===")
	fmt.Printf("Query: %q\nRuns per formatter: %d\n\n", query, runs)

	// Plain text formatter.
	fmt.Println("Running: plain text formatter...")
	plainResults := benchmark(prov, sysPrompt, apiKey, webfetch.New())

	// Markdown formatter.
	fmt.Println("Running: markdown formatter...")
	mdResults := benchmark(prov, sysPrompt, apiKey, webfetch.New(webfetch.WithFormatter(markdown.Formatter())))

	// Print results.
	fmt.Println()
	fmt.Println("┌─────────────────┬────────────┬─────────────┬──────────┬──────────┐")
	fmt.Println("│ Formatter       │ Avg In Tok │ Avg Out Tok │ Avg Time │ Avg Resp │")
	fmt.Println("├─────────────────┼────────────┼─────────────┼──────────┼──────────┤")
	printRow("plain text", plainResults)
	printRow("markdown", mdResults)
	fmt.Println("└─────────────────┴────────────┴─────────────┴──────────┴──────────┘")

	// Delta.
	pAvg := avg(plainResults)
	mAvg := avg(mdResults)
	fmt.Printf("\nDelta (markdown - plain):\n")
	fmt.Printf("  Input tokens:  %+d (%.0f%%)\n", mAvg.inputTokens-pAvg.inputTokens, pct(mAvg.inputTokens, pAvg.inputTokens))
	fmt.Printf("  Output tokens: %+d (%.0f%%)\n", mAvg.outputTokens-pAvg.outputTokens, pct(mAvg.outputTokens, pAvg.outputTokens))
	fmt.Printf("  Latency:       %+v\n", mAvg.duration-pAvg.duration)
}

func benchmark(prov agent.Provider, sysPrompt prompt.Instructions, apiKey string, fetchTool tool.Tool) []result {
	var results []result

	for i := range runs {
		a, err := agent.New(
			prov,
			sysPrompt,
			[]tool.Tool{
				tavily.New(apiKey),
				fetchTool,
			},
			agent.WithMaxIterations(10),
			agent.WithParallelToolExecution(),
		)
		if err != nil {
			log.Fatalf("create agent: %v", err)
		}

		start := time.Now()
		resp, usage, err := a.Invoke(context.Background(), query)
		elapsed := time.Since(start)

		if err != nil {
			fmt.Printf("  run %d: ERROR: %v\n", i+1, err)
			continue
		}

		r := result{
			inputTokens:  usage.InputTokens,
			outputTokens: usage.OutputTokens,
			duration:     elapsed,
			responseLen:  len(resp),
		}
		results = append(results, r)
		fmt.Printf("  run %d: ↑%d ↓%d  %v  (%d chars)\n", i+1, r.inputTokens, r.outputTokens, elapsed.Round(time.Millisecond), r.responseLen)
	}

	return results
}

func avg(results []result) result {
	if len(results) == 0 {
		return result{}
	}
	var sum result
	for _, r := range results {
		sum.inputTokens += r.inputTokens
		sum.outputTokens += r.outputTokens
		sum.duration += r.duration
		sum.responseLen += r.responseLen
	}
	n := len(results)
	return result{
		inputTokens:  sum.inputTokens / n,
		outputTokens: sum.outputTokens / n,
		duration:     sum.duration / time.Duration(n),
		responseLen:  sum.responseLen / n,
	}
}

func printRow(name string, results []result) {
	a := avg(results)
	fmt.Printf("│ %-15s │ %10d │ %11d │ %8s │ %6d c │\n",
		name, a.inputTokens, a.outputTokens, a.duration.Round(time.Millisecond), a.responseLen)
}

func pct(newVal, oldVal int) float64 {
	if oldVal == 0 {
		return 0
	}
	return float64(newVal-oldVal) / float64(oldVal) * 100
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s is required. See the example header for setup instructions.", key)
	}
	return v
}
