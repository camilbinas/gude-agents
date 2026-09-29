// Example: Document input (PDF, DOCX, etc.).
//
// Shows how to attach a document to an agent invocation using WithDocuments.
// The agent reads the document and answers questions about it.
//
// Supported formats: .pdf, .txt, .html, .csv, .md, .doc, .docx, .xls, .xlsx
//
// Run:
//
//	go run ./document-input path/to/document.pdf
//	go run ./document-input path/to/report.docx

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: go run ./document-input <document-path>")
		fmt.Fprintln(os.Stderr, "Example: go run ./document-input report.pdf")
		os.Exit(1)
	}
	docPath := os.Args[1]

	data, err := os.ReadFile(docPath)
	if err != nil {
		log.Fatalf("reading document: %v", err)
	}

	mimeType, err := agent.DocumentMIMEFromExt(filepath.Ext(docPath))
	if err != nil {
		log.Fatal(err)
	}

	doc := agent.DocumentBlock{
		Source: agent.DocumentSource{
			Data:     data,
			MIMEType: mimeType,
			Name:     filepath.Base(docPath),
		},
	}

	a, err := agent.New(
		bedrock.Must(bedrock.Standard()),
		"You are a helpful assistant. Analyze documents thoroughly and answer questions concisely.",
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	docCtx := agent.Background().WithDocuments([]agent.DocumentBlock{doc})

	fmt.Printf("Document: %s (%s, %d bytes)\n", filepath.Base(docPath), mimeType, len(data))
	fmt.Println(strings.Repeat("─", 60))

	for chunk, err := range a.TextStream(docCtx, "Summarize this document. What are the key points?") {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(chunk)
	}
	fmt.Println()
}
