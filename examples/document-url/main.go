// Example: Document input from a URL.
//
// Shows how to attach a URL-based document to an agent invocation using
// WithDocuments. The provider fetches the document directly from the URL —
// no local download needed for most providers.
//
// Run:
//
//	go run ./document-url https://www.w3.org/WAI/ER/tests/xhtml/testfiles/resources/pdf/dummy.pdf

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	ctx := context.Background()

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: go run ./document-url <document-url>")
		fmt.Fprintln(os.Stderr, "Example: go run ./document-url https://example.com/report.pdf")
		os.Exit(1)
	}
	docURL := os.Args[1]

	doc := agent.DocumentBlock{
		Source: agent.DocumentSource{
			URL: docURL,
		},
	}

	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.Text("You are a helpful assistant. Analyze documents thoroughly and answer questions concisely."),
		nil,
		debug.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	docCtx := agent.WithDocuments(ctx, []agent.DocumentBlock{doc})

	fmt.Printf("Document URL: %s\n", docURL)
	fmt.Println(strings.Repeat("─", 60))

	if _, err := a.InvokeStream(docCtx, "Summarize this document. What are the key points?", func(chunk string) {
		fmt.Print(chunk)
	}); err != nil {
		log.Fatal(err)
	}
	fmt.Println()
}
