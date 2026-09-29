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

	a, err := agent.New(
		provider,
		"You are a helpful assistant with vision capabilities. Be concise.",
		agent.WithConversationStore(summarized),
		agent.WithSyncConversation(),
		auto.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Fetching random image from picsum.photos...")
	img, err := fetchImage("https://picsum.photos/500/350")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Got %d bytes of image data\n", len(img.Source.Data))
	fmt.Println(strings.Repeat("─", 60))

	ctx := agent.Background().WithConversationID("media-demo")
	imgCtx := agent.Background().WithConversationID("media-demo").WithImages([]agent.ImageBlock{img})
	result, err := a.Invoke(imgCtx, "Describe this image in detail.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Turn 1 (image): %s\n\n", result.Text)

	result, err = a.Invoke(ctx, "What mood does the image convey?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Turn 2: %s\n\n", result.Text)

	result, err = a.Invoke(ctx, "What do you remember about the image I showed you?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Turn 3: %s\n\n", result.Text)

	if err := summarized.Flush(ctx); err != nil {
		log.Fatal(err)
	}

	fmt.Println(strings.Repeat("─", 60))
	snapshot, err := store.Load(context.Background(), "media-demo")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Messages in store: %d\n", len(snapshot.Messages))
	for i, m := range snapshot.Messages {
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

	fmt.Printf("Redirected to: %s\n", resp.Request.URL)
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return agent.ImageBlock{}, fmt.Errorf("read image: %w", err)
	}

	return agent.ImageBlock{
		Source: agent.ImageSource{Data: data, MIMEType: "image/jpeg"},
	}, nil
}
