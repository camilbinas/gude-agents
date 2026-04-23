// Run:
//
//	go run ./prompts

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	provider := bedrock.Must(bedrock.Standard())

	// APE prompt — concise, good for tool-heavy agents.
	apeAgent, err := agent.Default(
		provider,
		prompt.APE{
			Action:      "Search the knowledge base and answer customer questions.",
			Purpose:     "Help support agents resolve tickets faster.",
			Expectation: "Provide concise answers with source links. Say 'I don't know' when unsure.",
		},
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	// RISEN prompt — good for task-focused agents.
	risenAgent, err := agent.Default(
		provider,
		prompt.RISEN{
			Role:         "You are a travel planning assistant.",
			Instructions: "Help users plan trips by suggesting destinations, activities, and logistics.",
			Steps:        "1) Ask about preferences. 2) Suggest destinations. 3) Outline a day-by-day itinerary.",
			EndGoal:      "Provide a practical, ready-to-use travel plan.",
			Narrowing:    "Focus on Europe. Budget-friendly options. Keep it under 7 days.",
		},
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	// CO-STAR prompt — good for user-facing assistants with tone control.
	costarAgent, err := agent.Default(
		provider,
		prompt.COSTAR{
			Context:   "You are a customer support assistant for a SaaS product.",
			Objective: "Help users troubleshoot issues and find answers quickly.",
			Style:     "Clear and structured. Use numbered steps for instructions.",
			Tone:      "Friendly and patient. Never blame the user.",
			Audience:  "Non-technical users who may be frustrated.",
			Response:  "Keep answers under 3 paragraphs. Use bullet points for lists.",
		},
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	// TRACE prompt — example-driven, good for complex tasks.
	traceAgent, err := agent.Default(
		provider,
		prompt.TRACE{
			Task:    "You are a code review assistant.",
			Request: "Review code snippets for bugs, style issues, and security concerns.",
			Action:  "Read the code, identify issues, and suggest fixes with corrected code.",
			Context: "The codebase is a Go web service using the standard library and pgx for Postgres.",
			Example: "Input: db.Query(\"SELECT * FROM users WHERE id=\" + id)\nOutput: SQL injection — use parameterized queries: db.Query(\"SELECT * FROM users WHERE id=$1\", id)",
		},
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	fmt.Println("\n=== APE Agent ===")
	result, _, err := apeAgent.Invoke(ctx, "How do I reset my password?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)

	fmt.Println("=== RISEN Agent ===")
	result, _, err = risenAgent.Invoke(ctx, "I want a short trip from Munich.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)

	fmt.Println("\n=== CO-STAR Agent ===")
	result, _, err = costarAgent.Invoke(ctx, "I can't log in to my account.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)

	fmt.Println("\n=== TRACE Agent ===")
	result, _, err = traceAgent.Invoke(ctx, "Review this: func getUser(id string) { db.Query(\"SELECT * FROM users WHERE id=\" + id) }")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
