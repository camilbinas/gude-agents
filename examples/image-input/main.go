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
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	ctx := context.Background()

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: go run ./image-input <image-path>")
		os.Exit(1)
	}
	imagePath := os.Args[1]

	data, err := os.ReadFile(imagePath)
	if err != nil {
		log.Fatal(err)
	}
	mimeType, err := mimeFromExt(filepath.Ext(imagePath))
	if err != nil {
		log.Fatal(err)
	}

	img := agent.ImageBlock{
		Source: agent.ImageSource{Data: data, MIMEType: mimeType},
	}

	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.Text("You are a helpful assistant with vision capabilities. Be concise."),
		nil,
		debug.WithLogging(),
		agent.WithMemory(memory.NewStore(), "demo"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Turn 1 — describe the image.
	fmt.Printf("Image: %s (%s)\n", imagePath, mimeType)
	fmt.Println(strings.Repeat("─", 60))
	imgCtx := agent.WithImages(ctx, []agent.ImageBlock{img})
	if _, err := a.InvokeStream(imgCtx, "What is in this image?", func(c string) { fmt.Print(c) }); err != nil {
		log.Fatal(err)
	}

	// Turn 2 — follow-up without re-attaching the image.
	fmt.Println("\n" + strings.Repeat("─", 60))
	if _, err := a.InvokeStream(ctx, "Suggest a caption for it.", func(c string) { fmt.Print(c) }); err != nil {
		log.Fatal(err)
	}
	fmt.Println()
}

func mimeFromExt(ext string) (string, error) {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg", nil
	case ".png":
		return "image/png", nil
	case ".gif":
		return "image/gif", nil
	case ".webp":
		return "image/webp", nil
	default:
		return "", fmt.Errorf("unsupported extension %q", ext)
	}
}
