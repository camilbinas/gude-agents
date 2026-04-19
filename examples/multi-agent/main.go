// Example: Multi-agent orchestration.
//
// An orchestrator agent routes questions about a dev team to three specialist workers:
//   - repo analyst:  searches repositories by name, language, or description
//   - PR reviewer:   looks up open pull requests by repo or author
//   - team lookup:   finds team members by name or role
//
// The orchestrator decides which specialist(s) to call, potentially in
// parallel, then synthesizes their answers into a single response.
//
// Key concepts demonstrated:
//   - agent.Worker       — lightweight child agent optimized for tool use
//   - agent.Orchestrator — parent agent with parallel tool execution enabled
//   - agent.AgentAsTool  — wraps a child agent as a callable tool
//   - prompt.RISEN / prompt.COSTAR — structured prompt templates
//
// Run:
//
//	go run ./multi-agent

package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/memory"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/tool"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load() //nolint

	haiku := bedrock.Must(bedrock.ClaudeHaiku4_5())

	sonnet := bedrock.Must(bedrock.ClaudeSonnet4_6())

	// ── Worker 1: Repo analyst ────────────────────────────────────────────────
	// Searches the repo list and reports stats. Fast model — just tool + format.
	repoAnalyst, err := agent.Worker(
		haiku,
		prompt.RISEN{
			Role:         "You are a repository analyst.",
			Instructions: "Use the search_repos tool to find repositories matching the query.",
			Steps:        "1) Search repos. 2) List each match with its language, description, open PRs, open issues, and last commit time.",
			EndGoal:      "Give the caller a clear snapshot of the matching repositories.",
			Narrowing:    "Report only what the tool returns. No speculation.",
		},
		[]tool.Tool{searchReposTool()},
	)
	if err != nil {
		log.Fatal(err)
	}

	// ── Worker 2: PR reviewer ─────────────────────────────────────────────────
	// Looks up open pull requests filtered by repo or author.
	prReviewer, err := agent.Worker(
		haiku,
		prompt.RISEN{
			Role:         "You are a pull request analyst.",
			Instructions: "Use the search_prs tool to find open pull requests. Filter by repo or author as needed.",
			Steps:        "1) Search PRs with the appropriate filters. 2) List each PR with its ID, repo, title, author, and comment count.",
			EndGoal:      "Give the caller a clear list of relevant open PRs.",
			Narrowing:    "Only report open PRs. Do not invent status or context.",
		},
		[]tool.Tool{searchPRsTool()},
	)
	if err != nil {
		log.Fatal(err)
	}

	// ── Worker 3: Team lookup ─────────────────────────────────────────────────
	// Finds team members by name or role.
	teamLookup, err := agent.Worker(
		haiku,
		prompt.RISEN{
			Role:         "You are a team directory assistant.",
			Instructions: "Use the search_team tool to find team members by name or role.",
			Steps:        "1) Search the team. 2) For each match, report their name, role, repos they own, and open PR count.",
			EndGoal:      "Give the caller a clear picture of who's on the team and what they're working on.",
			Narrowing:    "Only report what the tool returns. Do not guess workload or availability.",
		},
		[]tool.Tool{searchTeamTool()},
	)
	if err != nil {
		log.Fatal(err)
	}

	// ── Orchestrator ──────────────────────────────────────────────────────────
	// Routes to the right specialist(s) and synthesizes the answer.
	// agent.Orchestrator enables WithParallelToolExecution — so for questions
	// that span repos + people, both workers are called simultaneously.
	orchestrator, err := agent.Orchestrator(
		sonnet,
		prompt.COSTAR{
			Context: `You are an internal dev-team assistant. You have three specialists:
- ask_repo_analyst: finds repos by name, language, or description
- ask_pr_reviewer:  lists open pull requests by repo or author
- ask_team_lookup:  finds team members by name or role`,
			Objective: "Answer the user's question by calling the right specialist(s). For questions that span multiple domains (e.g. 'who owns the Go repos and what PRs do they have open?'), call specialists in parallel.",
			Style:     "Concise and scannable. Use bullet points. Lead with the direct answer.",
			Tone:      "Straightforward — like a knowledgeable colleague, not a help desk.",
			Audience:  "Developers and engineering managers who want quick, accurate answers.",
			Response:  "Answer directly. Use the specialists' output as-is where possible.",
		},
		[]tool.Tool{
			agent.AgentAsTool(
				"ask_repo_analyst",
				"Search repositories by name, language, or description. Input: a search query.",
				repoAnalyst,
			),
			agent.AgentAsTool(
				"ask_pr_reviewer",
				"List open pull requests. Input: describe what to filter by (repo name, author name, or both).",
				prReviewer,
			),
			agent.AgentAsTool(
				"ask_team_lookup",
				"Find team members by name or role. Input: a name or role keyword.",
				teamLookup,
			),
		},
		agent.WithMemory(
			memory.NewWindow(memory.NewStore(), 20),
			"dev-team-session",
		),
	)
	if err != nil {
		log.Fatal(err)
	}

	// ── Interactive loop ──────────────────────────────────────────────────────
	fmt.Println("Dev team assistant ready.")
	fmt.Println("Try: 'What Go repos do we have?' or 'Show me Tom's open PRs' or 'Who are the backend engineers?'")
	fmt.Println("Type 'quit' to exit.")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "quit" {
			break
		}

		fmt.Println()
		usage, err := orchestrator.InvokeStream(context.Background(), input, func(chunk string) {
			fmt.Print(chunk)
		})
		if err != nil {
			fmt.Printf("\nError: %v\n", err)
			continue
		}
		fmt.Printf("\n\n[tokens: %d in / %d out]\n", usage.InputTokens, usage.OutputTokens)
	}
}
