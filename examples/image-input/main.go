// Example: Image input (vision) with disk memory persistence.
//
// Shows how to attach an image to an agent invocation using WithImages.
// The agent describes the image using a vision-capable model, then the
// conversation is saved to disk. A second invocation loads the conversation
// and asks a follow-up question — proving the ImageBlock survives the
// JSON round-trip through disk memory.
//
// Supported formats: .jpg/.jpeg, .png, .gif, .webp

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
	"github.com/camilbinas/gude-agents/agent/memory/disk"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	ctx := context.Background()

	// Image path is required as the first argument.
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: go run ./image-input <image-path>")
		fmt.Fprintln(os.Stderr, "Example: go run ./image-input photo.jpg")
		os.Exit(1)
	}
	imagePath := os.Args[1]

	// Read the image bytes from disk.
	data, err := os.ReadFile(imagePath)
	if err != nil {
		log.Fatalf("reading image: %v", err)
	}

	// Detect the MIME type from the file extension.
	mimeType, err := mimeFromExt(filepath.Ext(imagePath))
	if err != nil {
		log.Fatal(err)
	}

	// Build the ImageBlock.
	img := agent.ImageBlock{
		Source: agent.ImageSource{
			Data:     data,
			MIMEType: mimeType,
		},
	}

	// Disk-backed memory — conversations persist across invocations as JSON
	// files. This is what exercises the ImageBlock marshal/unmarshal path.
	memDir, err := os.MkdirTemp("", "image-input-memory-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(memDir)

	store, err := disk.New(memDir)
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		bedrock.Must(bedrock.Standard()),
		prompt.Text("You are a helpful assistant with vision capabilities. Describe images clearly and concisely."),
		nil,
		debug.WithLogging(),
		agent.WithMemory(store, "image-demo"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Image: %s (%s)\n", imagePath, mimeType)
	fmt.Printf("Memory: %s\n", memDir)
	fmt.Println(strings.Repeat("─", 60))

	// First turn — attach the image and ask for a description.
	// The ImageBlock gets saved into the conversation history on disk.
	fmt.Println("\nTurn 1: describing the image")
	fmt.Println(strings.Repeat("─", 60))
	imgCtx := agent.WithImages(ctx, []agent.ImageBlock{img})
	if _, err := a.InvokeStream(imgCtx, "What is in this image? Describe it in detail.", func(chunk string) {
		fmt.Print(chunk)
	}); err != nil {
		log.Fatal(err)
	}

	// Second turn — no new image. The agent should remember the previous
	// image from disk-loaded history. If memory didn't round-trip the
	// ImageBlock correctly, the model would have no idea what we're
	// referring to.
	fmt.Println("\n\nTurn 2: follow-up without re-attaching the image")
	fmt.Println(strings.Repeat("─", 60))
	if _, err := a.InvokeStream(ctx, "Based on that image, what would be a good caption for it?", func(chunk string) {
		fmt.Print(chunk)
	}); err != nil {
		log.Fatal(err)
	}
	fmt.Println()
}

// mimeFromExt maps a file extension to a supported image MIME type.
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
		return "", fmt.Errorf("unsupported image extension %q: use .jpg, .png, .gif, or .webp", ext)
	}
}
