// Example: Image input from a local file (vision).
//
// Shows how to attach a local image to an agent invocation using WithImages.
// The agent describes the image, then a follow-up question proves the image
// persists in memory across turns.
//
// Supported formats: .jpg/.jpeg, .png, .gif, .webp
//
// Run:
//
//	go run ./image-input path/to/image.jpg

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: go run ./image-input <image-path>")
		os.Exit(1)
	}
	imagePath := os.Args[1]

	data, err := os.ReadFile(imagePath)
	if err != nil {
		log.Fatal(err)
	}
	mimeType, err := agent.ImageMIMEFromExt(filepath.Ext(imagePath))
	if err != nil {
		log.Fatal(err)
	}

	img := agent.ImageBlock{
		Source: agent.ImageSource{Data: data, MIMEType: mimeType},
	}

	a, err := agent.New(
		bedrock.Must(bedrock.Standard()),
		prompt.Text("You are a helpful assistant with vision capabilities. Be concise.").String(),
		auto.WithLogging(),
		agent.WithConversationStore(conversation.NewInMemory()),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Turn 1 — describe the image.
	fmt.Printf("Image: %s (%s)\n", imagePath, mimeType)
	fmt.Println(strings.Repeat("─", 60))
	imgCtx := agent.Background().WithConversationID("demo").WithImages([]agent.ImageBlock{img})
	result, err := a.Invoke(imgCtx, "What is in this image?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(result.Text)

	// Turn 2 — follow-up without re-attaching the image.
	fmt.Println("\n" + strings.Repeat("─", 60))
	result, err = a.Invoke(agent.Background().WithConversationID("demo"), "Suggest a caption for it.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(result.Text)
	fmt.Println()
}
