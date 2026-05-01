// Example: Conversation summary with media preprocessing.
//
// Demonstrates the media summary feature: when summarization triggers,
// messages containing images are described as text before the main
// SummaryFunc runs, preserving visual context in the condensed history.
//
// Uses https://picsum.photos for a random test image.
//
// Run:
//
//	go run ./conversation-media-summary

package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
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

	provider := bedrock.Must(bedrock.Standard())

	// Summary with media preprocessing enabled.
	// Threshold of 3 turns (6 messages internally, triggers at ~5).
	// When summarization fires, image messages are described as text first.
	store := conversation.NewInMemory()
	summarized, err := conversation.NewSummary(
		store, 3, conversation.DefaultSummaryFunc(provider),
		conversation.WithMediaSummaryFunc(conversation.DefaultMediaSummaryFunc(provider)),
		conversation.WithMediaSummaryConcurrency(3),
		conversation.WithSummaryLogger(log.Default()),
	)
	if err != nil {
		log.Fatal(err)
	}

	a, err := agent.Default(
		provider,
		prompt.Text("You are a helpful assistant with vision capabilities. Be concise."),
		nil,
		agent.WithConversation(summarized, "media-demo"),
		agent.WithSyncConversation(),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Fetch a random image from picsum.photos.
	fmt.Println("Fetching random image from picsum.photos...")
	img, err := fetchImage("https://picsum.photos/500/350")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Got %d bytes of image data\n", len(img.Source.Data))
	fmt.Println(strings.Repeat("─", 60))

	ctx := agent.Background()

	// Turn 1: send the image with a question.
	imgCtx := agent.Background().WithImages([]agent.ImageBlock{img})
	result, err := a.Invoke(imgCtx, "Describe this image in detail.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Turn 1 (image): %s\n\n", result)

	// Turn 2: follow-up about the image.
	result, err = a.Invoke(ctx, "What mood does the image convey?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Turn 2: %s\n\n", result)

	// Turn 3: this should trigger summarization (5+ messages).
	// The image message will be described as text by MediaSummaryFunc.
	result, err = a.Invoke(ctx, "What do you remember about the image I showed you?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Turn 3: %s\n\n", result)

	// Wait for background summarization to complete, then inspect the store.
	summarized.Wait()

	fmt.Println(strings.Repeat("─", 60))
	msgs, _ := store.Load(context.Background(), "media-demo")
	fmt.Printf("Messages in store: %d\n", len(msgs))
	for i, m := range msgs {
		hasImage := false
		for _, b := range m.Content {
			if _, ok := b.(agent.ImageBlock); ok {
				hasImage = true
			}
		}
		for _, b := range m.Content {
			if tb, ok := b.(agent.TextBlock); ok {
				preview := tb.Text
				if len(preview) > 300 {
					preview = preview[:300] + "..."
				}
				tag := ""
				if hasImage {
					tag = " [has image]"
				}
				fmt.Printf("  [%d] %s%s: %s\n", i, m.Role, tag, preview)
			}
		}
	}
}

// fetchImage downloads a JPEG from the given URL (follows redirects)
// and prints the final URL after any redirects.
func fetchImage(url string) (agent.ImageBlock, error) {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return agent.ImageBlock{}, fmt.Errorf("fetch image: %w", err)
	}
	defer resp.Body.Close()

	// Log the final URL after redirects (picsum redirects to the actual image).
	fmt.Printf("Redirected to: %s\n", resp.Request.URL)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return agent.ImageBlock{}, fmt.Errorf("read image: %w", err)
	}

	return agent.ImageBlock{
		Source: agent.ImageSource{Data: data, MIMEType: "image/jpeg"},
	}, nil
}
