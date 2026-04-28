// Example: RAG evaluation with LLM-based and rule-based evaluators.
//
// Demonstrates a realistic evaluation workflow: ingest documents, run a RAG
// agent on test queries, then evaluate the outputs using both rule-based
// (keyword grounding) and LLM-based (faithfulness, relevance) evaluators.
//
// This is the pattern for nightly or release-gate quality checks.
//
// Run:
//
//	go run ./eval-rag

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/eval"
	"github.com/camilbinas/gude-agents/agent/prompt"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
	"github.com/camilbinas/gude-agents/agent/rag"
	ragbedrock "github.com/camilbinas/gude-agents/agent/rag/bedrock"
)

// testCase defines a golden test case with expected properties.
type testCase struct {
	query            string
	referenceAnswer  string
	expectedKeywords []string
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// ── 1. Set up RAG pipeline ───────────────────────────────────────────────
	fmt.Println("Setting up RAG pipeline...")

	embedder, err := ragbedrock.TitanEmbedV2()
	if err != nil {
		log.Fatal(err)
	}
	store := rag.NewMemoryStore()

	// Ingest a small knowledge base about a fictional company.
	texts := []string{
		"Acme Corp was founded in 2019 by Jane Smith in Austin, Texas. The company specializes in cloud infrastructure tools for startups.",
		"Acme Corp's flagship product is CloudDeploy, a one-click deployment platform that supports AWS, GCP, and Azure. It launched in 2021.",
		"Acme Corp has 150 employees as of 2025. The engineering team is 80 people, led by CTO Marcus Chen.",
		"CloudDeploy pricing starts at $49/month for the Starter plan, $199/month for Pro, and custom pricing for Enterprise.",
		"Acme Corp raised a $25 million Series B in March 2024, led by Sequoia Capital. Total funding is $40 million.",
	}

	if err := rag.Ingest(ctx, store, embedder, texts, nil, rag.WithChunkSize(300)); err != nil {
		log.Fatal(err)
	}

	retriever := rag.NewRetriever(embedder, store, rag.WithTopK(3))

	// ── 2. Create the RAG agent ──────────────────────────────────────────────
	provider := bedrock.Must(bedrock.Standard())

	a, err := agent.RAGAgent(
		provider,
		prompt.Text("You are a helpful assistant for Acme Corp. Answer questions using only the provided context. Be concise and factual."),
		retriever,
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}

	// ── 3. Define golden test cases ──────────────────────────────────────────
	goldenCases := []testCase{
		{
			query:            "Who founded Acme Corp and when?",
			referenceAnswer:  "Acme Corp was founded in 2019 by Jane Smith.",
			expectedKeywords: []string{"Jane Smith", "2019"},
		},
		{
			query:            "What is CloudDeploy and what clouds does it support?",
			referenceAnswer:  "CloudDeploy is a one-click deployment platform supporting AWS, GCP, and Azure.",
			expectedKeywords: []string{"CloudDeploy", "AWS", "GCP", "Azure"},
		},
		{
			query:            "How much funding has Acme Corp raised?",
			referenceAnswer:  "Acme Corp has raised $40 million in total funding, including a $25 million Series B.",
			expectedKeywords: []string{"40 million", "Series B"},
		},
	}

	// ── 4. Run the agent on each test case ───────────────────────────────────
	fmt.Println("Running agent on test cases...")

	evalCases := make([]eval.EvalCase, len(goldenCases))
	for i, tc := range goldenCases {
		fmt.Printf("  [%d/%d] %s\n", i+1, len(goldenCases), tc.query)

		result, _, err := a.Invoke(ctx, tc.query)
		if err != nil {
			log.Fatalf("agent invoke failed for case %d: %v", i, err)
		}

		// Retrieve the documents the agent would have seen.
		docs, err := retriever.Retrieve(ctx, tc.query)
		if err != nil {
			log.Fatalf("retriever failed for case %d: %v", i, err)
		}

		evalCases[i] = eval.EvalCase{
			Query:            tc.query,
			ActualOutput:     result,
			RetrievedContext: docs,
			ReferenceAnswer:  tc.referenceAnswer,
			Metadata:         map[string]string{"case_index": fmt.Sprintf("%d", i)},
		}

		fmt.Printf("    → %s\n", truncate(result, 100))
	}

	// ── 5. Build evaluators ──────────────────────────────────────────────────
	fmt.Println("\nRunning evaluations...")

	// Use a cheap model as the LLM judge.
	judge := bedrock.Must(bedrock.Cheapest())

	// Rule-based: check that key facts appear in the output.
	// We build per-case keyword evaluators, but for the suite we use a
	// general one that checks common terms.
	keywords, err := eval.NewKeywordGrounding(
		[]string{"Acme"},
		eval.WithThreshold(0.8),
	)
	if err != nil {
		log.Fatal(err)
	}

	// LLM-based evaluators.
	faithfulness := eval.NewFaithfulness(judge, eval.WithThreshold(0.7))
	relevance := eval.NewRelevance(judge, eval.WithThreshold(0.7))

	// ── 6. Run the eval suite ────────────────────────────────────────────────
	suite, err := eval.NewEvalSuite(
		evalCases,
		[]eval.Evaluator{keywords, faithfulness, relevance},
		eval.WithSuiteConcurrency(3),
	)
	if err != nil {
		log.Fatal(err)
	}

	report, err := suite.Run(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// ── 7. Print results ─────────────────────────────────────────────────────
	fmt.Println("\n═══ Evaluation Report ═══")
	fmt.Printf("Timestamp: %s\n", report.Timestamp.Format(time.RFC3339))
	fmt.Printf("Total cases: %d\n\n", report.TotalCases)

	// Per-evaluator summary.
	fmt.Println("Summaries:")
	for _, summary := range report.Summaries {
		status := "✓"
		if summary.Failed > 0 {
			status = "✗"
		}
		fmt.Printf("  %s %-20s mean=%.2f  passed=%d  failed=%d\n",
			status, summary.EvaluatorName, summary.MeanScore, summary.Passed, summary.Failed)
	}

	// Per-case details.
	fmt.Println("\nDetails:")
	for i, cr := range report.Results {
		fmt.Printf("\n  Case %d: %s\n", i, truncate(cr.Case.Query, 60))
		fmt.Printf("    Output: %s\n", truncate(cr.Case.ActualOutput, 80))
		if cr.Error != "" {
			fmt.Printf("    Error: %s\n", cr.Error)
		}
		for _, r := range cr.Results {
			status := "✓"
			if !r.Pass {
				status = "✗"
			}
			fmt.Printf("    %s %-20s score=%.2f", status, r.EvaluatorName, r.Score)
			if r.Explanation != "" {
				fmt.Printf("  %s", truncate(r.Explanation, 60))
			}
			fmt.Println()
		}
	}

	// JSON output for CI integration.
	fmt.Println("\n═══ JSON Report ═══")
	out, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(out))

	// ── 8. CI gate check ─────────────────────────────────────────────────────
	allPassed := true
	for _, summary := range report.Summaries {
		if summary.Failed > 0 {
			allPassed = false
			break
		}
	}
	if allPassed {
		fmt.Println("\n✓ All evaluations passed — safe to merge.")
	} else {
		fmt.Println("\n✗ Some evaluations failed — review before merging.")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
