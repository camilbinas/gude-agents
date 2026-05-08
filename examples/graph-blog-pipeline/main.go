// Run:
//
//	go run ./graph-blog-pipeline
//
// Opens a browser at http://localhost:4040 with a live graph visualization.
// Click "Start" to execute the pipeline. All LLM output streams in real-time.

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/graph"
	"github.com/camilbinas/gude-agents/agent/graph/checkpointer/memory"
	"github.com/camilbinas/gude-agents/agent/logging/auto"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/provider/gemini"
	"github.com/camilbinas/gude-agents/examples/utils"
	"github.com/joho/godotenv"
)

// BlogState is the typed state that flows through every node.
type BlogState struct {
	graph.GraphState
	Topic        string `json:"topic"`
	Outline      string `json:"outline"`
	Post         string `json:"post"`
	Score        int    `json:"score"`
	Feedback     string `json:"feedback"`
	SEO          string `json:"seo"`
	Social       string `json:"social"`
	GateRevise   string `json:"gate_revise,omitempty"`
	GateFinalize string `json:"gate_finalize,omitempty"`
}

// ReviewResult is used with InvokeStructured in the review node.
type ReviewResult struct {
	Score    int    `json:"score"    description:"Quality score from 1 to 10"`
	Feedback string `json:"feedback" description:"One sentence of actionable feedback"`
}

// Pipeline:
//
//	outline → draft → review → gate ─(score<8)─► revise → review (loop)
//	                                 └─(score≥8)─► finalize → [seo_meta, social_copy] → publish

func main() {
	godotenv.Load() //nolint

	bedrockHaiku := bedrock.Must(bedrock.Cheapest())
	gemini3Flash := gemini.Must(gemini.Gemini3Flash())

	outliner, _ := agent.Worker(bedrockHaiku, prompt.Text(
		"You are a blog strategist. Given a topic, produce a concise outline with "+
			"an intro, 3 main sections, and a conclusion. Return only the outline.",
	), nil)
	writer, _ := agent.Worker(bedrockHaiku, prompt.Text(
		"You are a blog writer. Given an outline, write a complete, engaging blog post. "+
			"Return only the post text.",
	), nil)
	reviewer, _ := agent.Worker(gemini3Flash, prompt.Text(
		"You are a content editor. Read the blog post and rate its quality from 1 to 10. "+
			"Provide a score and one sentence of actionable feedback.",
	), nil)
	reviser, _ := agent.Worker(bedrockHaiku, prompt.Text(
		"You are a blog editor. You will receive a draft and feedback. "+
			"Rewrite the post addressing the feedback. Return only the improved post.",
	), nil)
	seoWriter, _ := agent.Worker(bedrockHaiku, prompt.Text(
		"You are an SEO specialist. Given a blog post, write a meta title (max 60 chars) "+
			`and meta description (max 155 chars). Return only JSON: {"title":"...","description":"..."}`,
	), nil)
	socialWriter, _ := agent.Worker(bedrockHaiku, prompt.Text(
		"You are a social media manager. Given a blog post, write a punchy LinkedIn post "+
			"(max 3 sentences) to promote it. Return only the LinkedIn copy.",
	), nil)

	agents := &blogAgents{outliner, writer, reviewer, reviser, seoWriter, socialWriter}

	// Build once to extract structure for the UI.
	templateGraph := buildBlogGraph(agents, nil, nil)
	structure := templateGraph.Structure()

	// In-memory checkpointer for pause/resume.
	cp := memory.New()
	const threadID = "blog-devtools-1"

	dt := utils.NewDevTools(utils.DevToolsConfig{
		Port:      4040,
		Structure: structure,
		RunFunc: func(ctx context.Context, hook *utils.DevToolsHook) error {
			g := buildBlogGraph(agents, hook, cp)

			// Try to resume from checkpoint (paused state). Fall back to fresh run.
			_, err := g.Resume(ctx, threadID, nil)
			if errors.Is(err, graph.ErrCheckpointNotFound) {
				_, err = g.Run(ctx, BlogState{Topic: "Why Go is the best language for building AI agents in 2026"}, graph.WithThreadID(threadID))
			}
			if err != nil {
				return err
			}

			// Clean up checkpoint on success.
			_ = cp.Delete(context.Background(), threadID)

			return nil
		},
	})

	log.Fatal(dt.ListenAndServe())
}

type blogAgents struct {
	outliner, writer, reviewer, reviser, seoWriter, socialWriter *agent.Agent
}

func buildBlogGraph(a *blogAgents, hook *utils.DevToolsHook, cp graph.GraphCheckpointer) *graph.Graph[BlogState] {
	opts := []graph.GraphOption{
		graph.WithMaxIterations(30),
		auto.WithGraphLogging(),
	}
	if hook != nil {
		opts = append(opts, graph.WithEventHook(hook))
	}
	if cp != nil {
		opts = append(opts, graph.WithCheckpointer(cp))
	}

	g, err := graph.New[BlogState](opts...)
	if err != nil {
		log.Fatal(err)
	}

	// Helper: stream an agent call and collect the result.
	stream := func(ctx context.Context, ag *agent.Agent, nodeName, input string) (string, agent.TokenUsage, error) {
		c := agent.NewContext(ctx)
		var result strings.Builder
		cb := func(chunk string) {
			result.WriteString(chunk)
			if hook != nil {
				hook.StreamCallback(nodeName)(chunk)
			}
		}
		if err := ag.InvokeStream(c, input, cb); err != nil {
			return "", agent.TokenUsage{}, err
		}
		return result.String(), c.Usage(), nil
	}

	g.Node("outline", func(ctx context.Context, s BlogState) (BlogState, error) {
		text, usage, err := stream(ctx, a.outliner, "outline", s.Topic)
		if err != nil {
			return s, err
		}
		s.Outline = text
		s.AddUsage(usage)
		return s, nil
	}, graph.In(), graph.Out("outline_out"))

	g.Node("draft", func(ctx context.Context, s BlogState) (BlogState, error) {
		input := fmt.Sprintf("Topic: %s\n\nOutline:\n%s", s.Topic, s.Outline)
		text, usage, err := stream(ctx, a.writer, "draft", input)
		if err != nil {
			return s, err
		}
		s.Post = text
		s.AddUsage(usage)
		return s, nil
	}, graph.In("outline_out"), graph.Out("draft_out"))

	g.Node("review", func(ctx context.Context, s BlogState) (BlogState, error) {
		c := agent.NewContext(ctx)
		review, err := agent.InvokeStructured[ReviewResult](c, a.reviewer, s.Post)
		if err != nil {
			return s, err
		}
		if hook != nil {
			hook.StreamCallback("review")(fmt.Sprintf("Score: %d — %s", review.Score, review.Feedback))
		}
		s.Score = review.Score
		s.Feedback = review.Feedback
		s.AddUsage(c.Usage())
		return s, nil
	}, graph.In("draft_out"), graph.Out("review_out"))

	g.Node("gate", func(_ context.Context, s BlogState) (BlogState, error) {
		// Conditional gating: only write the key for the chosen path.
		// The other path's node won't execute (its input key stays zero).
		if s.Score >= 8 {
			s.GateFinalize = "pass"
		} else {
			s.GateRevise = "needs_work"
		}
		return s, nil
	}, graph.In("review_out"), graph.Out("gate_revise", "gate_finalize"))

	g.Node("revise", func(ctx context.Context, s BlogState) (BlogState, error) {
		input := fmt.Sprintf("Draft:\n%s\n\nFeedback: %s", s.Post, s.Feedback)
		text, usage, err := stream(ctx, a.reviser, "revise", input)
		if err != nil {
			return s, err
		}
		s.Post = text
		s.AddUsage(usage)
		return s, nil
	}, graph.In("gate_revise"), graph.Out("revise_out"))

	g.Node("finalize", func(_ context.Context, s BlogState) (BlogState, error) {
		return s, nil
	}, graph.In("gate_finalize"), graph.Out("finalize_out"))

	g.Node("seo_meta", func(ctx context.Context, s BlogState) (BlogState, error) {
		text, usage, err := stream(ctx, a.seoWriter, "seo_meta", s.Post)
		if err != nil {
			return s, err
		}
		s.SEO = text
		s.AddUsage(usage)
		return s, nil
	}, graph.In("finalize_out"), graph.Out("seo_out"))

	g.Node("social_copy", func(ctx context.Context, s BlogState) (BlogState, error) {
		text, usage, err := stream(ctx, a.socialWriter, "social_copy", s.Post)
		if err != nil {
			return s, err
		}
		s.Social = text
		s.AddUsage(usage)
		return s, nil
	}, graph.In("finalize_out"), graph.Out("social_out"))

	g.Node("publish", func(_ context.Context, s BlogState) (BlogState, error) {
		pkg := strings.Join([]string{
			"━━━ BLOG POST ━━━", s.Post, "",
			"━━━ SEO META ━━━", s.SEO, "",
			"━━━ SOCIAL COPY ━━━", s.Social,
		}, "\n")
		if hook != nil {
			hook.StreamCallback("publish")(pkg)
		}
		fmt.Println(pkg)
		return s, nil
	}, graph.In("seo_out", "social_out"), graph.Out("publish_out"))

	// Wiring

	return g
}
