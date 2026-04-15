package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
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

	haiku, err := bedrock.ClaudeHaiku4_5()
	if err != nil {
		log.Fatal(err)
	}

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

	// --- Build the typed graph ---

	g, err := agent.NewTypedGraph[BlogState](
		agent.WithGraphMaxIterations(30),
		agent.WithGraphLogger(log.Default()),
	)
	if err != nil {
		log.Fatal(err)
	}

	// outline: drafts a structured outline from the topic
	if err := g.AddNode("outline", func(ctx context.Context, s BlogState) (BlogState, error) {
		outline, _, err := outliner.Invoke(ctx, s.Topic)
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
		input := fmt.Sprintf("Topic: %s\n\nOutline:\n%s", s.Topic, s.Outline)
		post, _, err := writer.Invoke(ctx, input)
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
		review, _, err := agent.InvokeStructured[ReviewResult](ctx, reviewer, s.Post)
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
		input := fmt.Sprintf("Draft:\n%s\n\nFeedback: %s", s.Post, s.Feedback)
		revised, _, err := reviser.Invoke(ctx, input)
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
		seo, _, err := seoWriter.Invoke(ctx, s.Post)
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
		social, _, err := socialWriter.Invoke(ctx, s.Post)
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
		if s.Score < 7 {
			return "revise", nil
		}
		return "finalize", nil
	}))
	mustEdge(g.AddEdge("revise", "review"))
	mustEdge(g.AddFork("finalize", []string{"seo_meta", "social_copy"}))
	mustEdge(g.AddJoin("publish", []string{"seo_meta", "social_copy"}))

	// --- Run ---

	topic := "Why Go is the best language for building AI agents in 2026"
	log.Printf("[pipeline] starting for topic: %q", topic)

	result, err := g.Run(context.Background(), BlogState{Topic: topic})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\n━━━ STATS ━━━\nFinal score : %d\nTokens      : %d in / %d out\n",
		result.State.Score,
		result.Usage.InputTokens,
		result.Usage.OutputTokens,
	)
}

func mustEdge(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
