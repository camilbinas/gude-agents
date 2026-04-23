// Example: Image input from a URL (vision).
//
// Shows how to attach a URL-based image to an agent invocation using
// WithImages. The provider fetches the image directly from the URL —
// no local download or MIME type detection needed.
//
// This is useful for web applications where images are already hosted
// (user avatars, product photos, uploaded files on S3/CDN, etc.).
//
// Run:
//
//	go run ./image-url https://...

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
		fmt.Fprintln(os.Stderr, "Usage: go run ./image-url <image-url>")
		fmt.Fprintln(os.Stderr, "Example: go run ./image-url https://example.com/photo.jpg")
		os.Exit(1)
	}
	imageURL := os.Args[1]

	// Build the ImageBlock with just a URL — no bytes, no MIME type.
	img := agent.ImageBlock{
		Source: agent.ImageSource{
			URL: imageURL,
		},
	}

	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.Text("You are a helpful assistant with vision capabilities. Describe images clearly and concisely."),
		nil,
		debug.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	imgCtx := agent.WithImages(ctx, []agent.ImageBlock{img})

	fmt.Printf("Image URL: %s\n", imageURL)
	fmt.Println(strings.Repeat("─", 60))

	if _, err := a.InvokeStream(imgCtx, "What is in this image? Describe it in detail.", func(chunk string) {
		fmt.Print(chunk)
	}); err != nil {
		log.Fatal(err)
	}
	fmt.Println()
}
