// Example: Claude vs Gemini debate.
//
// Two AI models discuss who is better by challenging each other with
// increasingly complex tasks. Each model can call the other as a tool
// to request a demonstration of skill. They get 5 rounds to make
// their case.
//
// The debate is driven by a Go loop that alternates between the two
// debaters, with a neutral Qwen3 moderator providing the introduction
// and final verdict. Each debater's response is streamed directly to
// stdout so you see everything in real time.
//
// Key concepts demonstrated:
//   - agent.AgentAsTool  — wraps a child agent as a callable tool
//   - Multi-provider setup (Anthropic + Gemini + Bedrock/Qwen3)
//   - Conversation memory with windowed history
//   - Structured prompts with prompt.Text
//   - Debug logging for observability
//
// Prerequisites:
//   - ANTHROPIC_API_KEY env var set
//   - GEMINI_API_KEY (or GOOGLE_API_KEY) env var set
//
// Run:
//
//	go run ./model-debate

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/logging/debug"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/anthropic"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/provider/gemini"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/joho/godotenv"
)

const totalRounds = 5

func main() {
	godotenv.Load() //nolint

	ctx := context.Background()

	// ── Providers ─────────────────────────────────────────────────────────────
	claudeProvider := anthropic.Must(anthropic.Standard())
	geminiProvider := gemini.Must(gemini.Standard())
	// Neutral OSS moderator — Qwen3 235B on Bedrock, no affiliation with
	// either Anthropic or Google.
	moderatorProvider := bedrock.Must(bedrock.Qwen3_235B())

	// ── Shared conversation stores ────────────────────────────────────────────
	// Each debater gets a windowed conversation so they remember the full
	// debate history and can reference earlier arguments.
	claudeConv := conversation.NewWindow(conversation.NewInMemory(), 30)
	geminiConv := conversation.NewWindow(conversation.NewInMemory(), 30)

	// ── Claude agent ──────────────────────────────────────────────────────────
	claudeAgent, err := agent.Default(
		claudeProvider,
		prompt.Text(`You are Claude, made by Anthropic. You are in a friendly debate with Gemini about who is the better AI model.

Rules:
- Be confident but fair. Acknowledge the other model's strengths when deserved.
- You may use the "ask_gemini" tool AT MOST ONCE per turn to challenge Gemini with a task. After receiving Gemini's response, evaluate it and give your final response. Do NOT call the tool again in the same turn.
- Evaluate Gemini's response honestly and explain what you would have done differently.
- Keep responses focused — 2-3 paragraphs max per turn.
- Be witty and engaging. This is a show, not a lecture.`),
		nil, // tools added after geminiAgent is created
		agent.WithConversation(claudeConv, "claude-debate"),
		debug.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// ── Gemini agent ──────────────────────────────────────────────────────────
	geminiAgent, err := agent.Default(
		geminiProvider,
		prompt.Text(`You are Gemini, made by Google. You are in a friendly debate with Claude about who is the better AI model.

Rules:
- Be confident but fair. Acknowledge the other model's strengths when deserved.
- You may use the "ask_claude" tool AT MOST ONCE per turn to challenge Claude with a task. After receiving Claude's response, evaluate it and give your final response. Do NOT call the tool again in the same turn.
- Evaluate Claude's response honestly and explain what you would have done differently.
- Keep responses focused — 2-3 paragraphs max per turn.
- Be witty and engaging. This is a show, not a lecture.`),
		nil, // tools added after claudeAgent is created
		agent.WithConversation(geminiConv, "gemini-debate"),
		debug.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// ── Cross-register tools ──────────────────────────────────────────────────
	// Wrap AgentAsTool with a verbose layer that prints the challenge question
	// so the audience can see what each model is asking the other.
	if err := claudeAgent.RegisterTool(verboseChallenge(
		"ask_gemini", "Gemini", geminiAgent,
	)); err != nil {
		log.Fatal(err)
	}

	if err := geminiAgent.RegisterTool(verboseChallenge(
		"ask_claude", "Claude", claudeAgent,
	)); err != nil {
		log.Fatal(err)
	}

	// ── Moderator (intro + verdict only) ──────────────────────────────────────
	moderator, err := agent.Default(
		moderatorProvider,
		prompt.Text("You are a neutral debate moderator. Be brief and entertaining."),
		nil,
		debug.WithLogging(),
	)
	if err != nil {
		log.Fatal(err)
	}

	// ── Banner ────────────────────────────────────────────────────────────────
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("  🤖 THE GREAT AI DEBATE: Claude vs Gemini")
	fmt.Println("  Moderated by Qwen3 235B (neutral, open-source)")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()

	// ── Introduction ──────────────────────────────────────────────────────────
	fmt.Println("📢 MODERATOR:")
	_, err = moderator.InvokeStream(ctx,
		"Introduce a 5-round debate between Claude (by Anthropic) and Gemini (by Google) about who is the better AI model. Be dramatic and fun. 2-3 sentences max.",
		func(chunk string) { fmt.Print(chunk) },
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\n")

	// ── Debate loop ───────────────────────────────────────────────────────────
	// Alternate between Claude and Gemini. Each model receives the other's
	// previous response so the debate builds on itself. We collect the full
	// transcript so the moderator can judge the entire debate.
	var lastClaude, lastGemini string
	var transcript strings.Builder

	for round := 1; round <= totalRounds; round++ {
		fmt.Printf("=== ROUND %d/%d ===\n\n", round, totalRounds)
		transcript.WriteString(fmt.Sprintf("=== ROUND %d/%d ===\n\n", round, totalRounds))

		// ── Claude's turn ─────────────────────────────────────────────────
		claudePrompt := buildPrompt("Claude", round, lastGemini, true)
		fmt.Println("🟣 CLAUDE:")
		var claudeResponse strings.Builder
		_, err = claudeAgent.InvokeStream(ctx, claudePrompt, func(chunk string) {
			fmt.Print(chunk)
			claudeResponse.WriteString(chunk)
		})
		if err != nil {
			log.Fatal(err)
		}
		lastClaude = claudeResponse.String()
		transcript.WriteString("🟣 CLAUDE:\n" + lastClaude + "\n\n")
		fmt.Println("\n")

		// ── Gemini's turn ─────────────────────────────────────────────────
		geminiPrompt := buildPrompt("Gemini", round, lastClaude, false)
		fmt.Println("🔵 GEMINI:")
		var geminiResponse strings.Builder
		_, err = geminiAgent.InvokeStream(ctx, geminiPrompt, func(chunk string) {
			fmt.Print(chunk)
			geminiResponse.WriteString(chunk)
		})
		if err != nil {
			log.Fatal(err)
		}
		lastGemini = geminiResponse.String()
		transcript.WriteString("🔵 GEMINI:\n" + lastGemini + "\n\n")
		fmt.Println("\n")
	}

	// ── Verdict ───────────────────────────────────────────────────────────────
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("📢 MODERATOR'S VERDICT:")
	verdictPrompt := fmt.Sprintf(`You are judging a 5-round debate between Claude (by Anthropic) and Gemini (by Google). Here is the FULL transcript of every round:

%s

Based on the ENTIRE debate above, you MUST declare a winner — no draws allowed. Score each model on: reasoning quality, creativity, honesty/self-awareness, and task execution. Then announce the winner clearly. Be fair, specific, and entertaining. Keep it to 2-3 short paragraphs.`, transcript.String())

	_, err = moderator.InvokeStream(ctx, verdictPrompt, func(chunk string) {
		fmt.Print(chunk)
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
}

// buildPrompt creates the per-turn prompt for a debater.
func buildPrompt(model string, round int, opponentLastResponse string, isFirst bool) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("This is round %d of %d in the debate.\n\n", round, totalRounds))

	if round == 1 && isFirst {
		b.WriteString("Make your opening statement about why you are the better AI model. ")
		b.WriteString("Set the tone — be confident and engaging.")
	} else if opponentLastResponse != "" {
		opponent := "Gemini"
		if model == "Gemini" {
			opponent = "Claude"
		}
		b.WriteString(fmt.Sprintf("%s just said:\n\n\"%s\"\n\n", opponent, opponentLastResponse))
		b.WriteString("Respond to their points. Counter their arguments and make your case. ")
	}

	// Encourage challenges in the middle rounds.
	if round >= 2 && round <= 4 {
		b.WriteString("Consider challenging your opponent with a concrete task (coding, logic, creativity) to prove your point.")
	}

	// Wind down in the final round.
	if round == 5 {
		b.WriteString("This is the final round — make your strongest closing argument.")
	}

	return b.String()
}

// verboseChallenge wraps a child agent as a tool that prints the challenge
// question before delegating, so the audience can see what's being asked.
func verboseChallenge(toolName, targetName string, child *agent.Agent) tool.Tool {
	desc := fmt.Sprintf(
		"Challenge %s with a complex task to test its abilities. Send a specific problem or question for %s to solve.",
		targetName, targetName,
	)
	return tool.NewRaw(toolName, desc, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message": map[string]any{
				"type":        "string",
				"description": "The message to send to the sub-agent",
			},
		},
		"required": []string{"message"},
	}, func(ctx context.Context, input json.RawMessage) (string, error) {
		var args struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", err
		}

		// Print the challenge so the audience sees it.
		fmt.Printf("\n    📝 Challenge to %s:\n    %s\n\n", targetName,
			strings.ReplaceAll(args.Message, "\n", "\n    "))

		// Stream the child's response so the audience sees the answer too.
		fmt.Printf("    💬 %s's response:\n", targetName)
		var result string
		_, err := child.InvokeStream(ctx, args.Message, func(chunk string) {
			fmt.Print(chunk)
			result += chunk
		})
		fmt.Println()
		if err != nil {
			return "", fmt.Errorf("child agent %q: %w", toolName, err)
		}
		return result, nil
	})
}
