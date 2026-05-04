// Run:
//
//	go run ./graph-blog-pipeline
//
// Supports Ctrl+C: the pipeline checkpoints after each node, so you can
// interrupt and resume later. State is persisted to disk.

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	rediscp "github.com/camilbinas/gude-agents/agent/graph/checkpointer/redis"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/joho/godotenv"
)

// BlogState is the typed state that flows through every node.
// No type assertions anywhere — just plain struct fields.
type BlogState struct {
	Topic    string `json:"topic"`
	Outline  string `json:"outline"`
	Post     string `json:"post"`
	Score    int    `json:"score"`
	Feedback string `json:"feedback"`
	SEO      string `json:"seo"`
	Social   string `json:"social"`
}

// ReviewResult is used with InvokeStructured in the review node.
type ReviewResult struct {
	Score    int    `json:"score"    description:"Quality score from 1 to 10"`
	Feedback string `json:"feedback" description:"One sentence of actionable feedback"`
}

// Blog post writing pipeline
//
//	topic
//	  │
//	outline          ← drafts a structured outline
//	  │
//	draft            ← writes the full post
//	  │
//	review           ← scores quality via structured output
//	  │
//	gate ── score < 7 ──► revise ──► review (loop)
//	  │ score >= 7
//	finalize
//	  │
//	[seo_meta, social_copy]   ← fork: run in parallel
//	  │
//	publish (join)   ← assembles the final package
func main() {
	godotenv.Load() //nolint

	haiku := bedrock.Must(bedrock.Cheapest())

	// --- Agents ---

	outliner, err := agent.Worker(haiku, prompt.Text(
		"You are a blog strategist. Given a topic, produce a concise outline with "+
			"an intro, 3 main sections, and a conclusion. Return only the outline.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	writer, err := agent.Worker(haiku, prompt.Text(
		"You are a blog writer. Given an outline, write a complete, engaging blog post. "+
			"Return only the post text.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	reviewer, err := agent.Worker(haiku, prompt.Text(
		"You are a content editor. Read the blog post and rate its quality from 1 to 10. "+
			"Provide a score and one sentence of actionable feedback.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	reviser, err := agent.Worker(haiku, prompt.Text(
		"You are a blog editor. You will receive a draft and feedback. "+
			"Rewrite the post addressing the feedback. Return only the improved post.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	seoWriter, err := agent.Worker(haiku, prompt.Text(
		"You are an SEO specialist. Given a blog post, write a meta title (max 60 chars) "+
			`and meta description (max 155 chars). Return only JSON: {"title":"...","description":"..."}`,
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	socialWriter, err := agent.Worker(haiku, prompt.Text(
		"You are a social media manager. Given a blog post, write a punchy LinkedIn post "+
			"(max 3 sentences) to promote it. Return only the LinkedIn copy.",
	), nil)
	if err != nil {
		log.Fatal(err)
	}

	// --- Build the typed graph with checkpointing ---

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "127.0.0.1:6379"
	}
	cp, err := rediscp.New(rediscp.Options{Addr: redisAddr})
	if err != nil {
		log.Fatalf("redis checkpointer: %v", err)
	}
	defer cp.Close()

	g, err := graph.New[BlogState](
		graph.WithMaxIterations(30),
		graph.WithCheckpointer(cp),
		auto.WithGraphLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// outline: drafts a structured outline from the topic
	if err := g.AddNode("outline", func(ctx context.Context, s BlogState) (BlogState, error) {
		c := agent.NewContext(ctx)
		outline, err := outliner.Invoke(c, s.Topic)
		if err != nil {
			return s, err
		}
		s.Outline = outline
		return s, nil
	}); err != nil {
		log.Fatal(err)
	}

	// draft: writes the full post from the outline
	if err := g.AddNode("draft", func(ctx context.Context, s BlogState) (BlogState, error) {
		c := agent.NewContext(ctx)
		input := fmt.Sprintf("Topic: %s\n\nOutline:\n%s", s.Topic, s.Outline)
		post, err := writer.Invoke(c, input)
		if err != nil {
			return s, err
		}
		s.Post = post
		return s, nil
	}); err != nil {
		log.Fatal(err)
	}

	// review: scores the draft using structured output — typed, no JSON parsing
	if err := g.AddNode("review", func(ctx context.Context, s BlogState) (BlogState, error) {
		c := agent.NewContext(ctx)
		review, err := agent.InvokeStructured[ReviewResult](c, reviewer, s.Post)
		if err != nil {
			return s, err
		}
		log.Printf("[pipeline] review score: %d — %s", review.Score, review.Feedback)
		s.Score = review.Score
		s.Feedback = review.Feedback
		return s, nil
	}); err != nil {
		log.Fatal(err)
	}

	// gate: pass-through; routing is in the conditional edge
	if err := g.AddNode("gate", func(_ context.Context, s BlogState) (BlogState, error) {
		return s, nil
	}); err != nil {
		log.Fatal(err)
	}

	// revise: rewrites the post using the reviewer's feedback
	if err := g.AddNode("revise", func(ctx context.Context, s BlogState) (BlogState, error) {
		c := agent.NewContext(ctx)
		input := fmt.Sprintf("Draft:\n%s\n\nFeedback: %s", s.Post, s.Feedback)
		revised, err := reviser.Invoke(c, input)
		if err != nil {
			return s, err
		}
		s.Post = revised
		return s, nil
	}); err != nil {
		log.Fatal(err)
	}

	// finalize: pass-through before the fork
	if err := g.AddNode("finalize", func(_ context.Context, s BlogState) (BlogState, error) {
		return s, nil
	}); err != nil {
		log.Fatal(err)
	}

	// seo_meta: generates SEO title + description
	if err := g.AddNode("seo_meta", func(ctx context.Context, s BlogState) (BlogState, error) {
		c := agent.NewContext(ctx)
		seo, err := seoWriter.Invoke(c, s.Post)
		if err != nil {
			return s, err
		}
		s.SEO = seo
		return s, nil
	}); err != nil {
		log.Fatal(err)
	}

	// social_copy: generates LinkedIn copy
	if err := g.AddNode("social_copy", func(ctx context.Context, s BlogState) (BlogState, error) {
		c := agent.NewContext(ctx)
		social, err := socialWriter.Invoke(c, s.Post)
		if err != nil {
			return s, err
		}
		s.Social = social
		return s, nil
	}); err != nil {
		log.Fatal(err)
	}

	// publish: assembles the final deliverable
	if err := g.AddNode("publish", func(_ context.Context, s BlogState) (BlogState, error) {
		pkg := strings.Join([]string{
			"━━━ BLOG POST ━━━",
			s.Post,
			"",
			"━━━ SEO META ━━━",
			s.SEO,
			"",
			"━━━ SOCIAL COPY ━━━",
			s.Social,
		}, "\n")
		fmt.Println(pkg)
		return s, nil
	}); err != nil {
		log.Fatal(err)
	}

	// --- Wiring ---

	g.SetEntry("outline")
	mustEdge(g.AddEdge("outline", "draft"))
	mustEdge(g.AddEdge("draft", "review"))
	mustEdge(g.AddEdge("review", "gate"))
	mustEdge(g.AddConditionalEdge("gate", func(_ context.Context, s BlogState) (string, error) {
		if s.Score < 8 {
			return "revise", nil
		}
		return "finalize", nil
	}))
	mustEdge(g.AddEdge("revise", "review"))
	mustEdge(g.AddFork("finalize", []string{"seo_meta", "social_copy"}))
	mustEdge(g.AddJoin("publish", []string{"seo_meta", "social_copy"}))

	// --- Run with graceful shutdown ---

	topic := "Why Go is the best language for building AI agents in 2026"
	threadID := "blog-pipeline-1"

	// Ctrl+C cancels the context; the engine finishes the current node and stops.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("[pipeline] starting for topic: %q", topic)

	// Try to resume from a previous checkpoint first.
	result, err := g.Resume(ctx, threadID, nil)
	if errors.Is(err, graph.ErrCheckpointNotFound) {
		// No previous run — start fresh.
		result, err = g.Run(ctx, BlogState{Topic: topic}, graph.WithThreadID(threadID))
	}

	if errors.Is(err, context.Canceled) {
		log.Printf("[pipeline] interrupted — resume later with the same threadID")
		return
	}
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\n━━━ STATS ━━━\nFinal score : %d\nTokens      : %d in / %d out\n",
		result.State.Score,
		result.Usage.InputTokens,
		result.Usage.OutputTokens,
	)

	// Clean up checkpoint after successful completion.
	_ = cp.Delete(context.Background(), threadID)
}

func mustEdge(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
