// Example: Rule-based evaluation — no LLM costs.
//
// Demonstrates the three rule-based evaluators that run instantly and cost
// nothing. Ideal for CI pipelines and every-PR quality gates.
//
//   - KeywordGrounding: checks that required keywords appear in the output
//   - JSONStructure:    validates JSON format and required keys
//   - RetrievalOrdering: measures document ranking quality via NDCG
//
// Run:
//
//	go run ./eval-rule-based

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/eval"
)

func main() {
	ctx := context.Background()

	// ── 1. Keyword grounding ─────────────────────────────────────────────────
	fmt.Println("═══ Keyword Grounding ═══")

	keywords, err := eval.NewKeywordGrounding(
		[]string{"Go", "concurrency", "goroutines"},
		eval.WithThreshold(0.6),
	)
	if err != nil {
		log.Fatal(err)
	}

	kgResult, _ := keywords.Evaluate(ctx, eval.EvalCase{
		Query:        "What makes Go good for concurrent programming?",
		ActualOutput: "Go excels at concurrency through goroutines and channels, making it easy to write parallel programs.",
	})
	printResult(kgResult)

	kgResult2, _ := keywords.Evaluate(ctx, eval.EvalCase{
		Query:        "What makes Go good for concurrent programming?",
		ActualOutput: "Python uses asyncio for asynchronous programming.",
	})
	printResult(kgResult2)

	// ── 2. JSON structure ────────────────────────────────────────────────────
	fmt.Println("\n═══ JSON Structure ═══")

	jsonEval := eval.NewJSONStructure([]string{"name", "age", "email"})

	jsResult, _ := jsonEval.Evaluate(ctx, eval.EvalCase{
		ActualOutput: `{"name": "Alice", "age": 30, "email": "alice@example.com"}`,
	})
	printResult(jsResult)

	jsResult2, _ := jsonEval.Evaluate(ctx, eval.EvalCase{
		ActualOutput: `{"name": "Bob", "age": 25}`,
	})
	printResult(jsResult2)

	jsResult3, _ := jsonEval.Evaluate(ctx, eval.EvalCase{
		ActualOutput: `not json at all`,
	})
	printResult(jsResult3)

	// ── 3. Retrieval ordering (NDCG) ─────────────────────────────────────────
	fmt.Println("\n═══ Retrieval Ordering (NDCG) ═══")

	ndcg, err := eval.NewRetrievalOrdering(
		[]string{"go-concurrency", "go-channels", "go-goroutines"},
		func(d agent.Document) string { return d.Metadata["id"] },
	)
	if err != nil {
		log.Fatal(err)
	}

	// Perfect ordering.
	ndcgResult, _ := ndcg.Evaluate(ctx, eval.EvalCase{
		RetrievedContext: []agent.Document{
			{Content: "Go concurrency patterns", Metadata: map[string]string{"id": "go-concurrency"}},
			{Content: "Go channels deep dive", Metadata: map[string]string{"id": "go-channels"}},
			{Content: "Goroutines explained", Metadata: map[string]string{"id": "go-goroutines"}},
		},
	})
	printResult(ndcgResult)

	// Reversed ordering.
	ndcgResult2, _ := ndcg.Evaluate(ctx, eval.EvalCase{
		RetrievedContext: []agent.Document{
			{Content: "Goroutines explained", Metadata: map[string]string{"id": "go-goroutines"}},
			{Content: "Go channels deep dive", Metadata: map[string]string{"id": "go-channels"}},
			{Content: "Go concurrency patterns", Metadata: map[string]string{"id": "go-concurrency"}},
		},
	})
	printResult(ndcgResult2)

	// ── 4. Suite: run all evaluators together ────────────────────────────────
	fmt.Println("\n═══ Eval Suite ═══")

	cases := []eval.EvalCase{
		{
			Query:        "List users as JSON",
			ActualOutput: `{"name": "Alice", "age": 30, "email": "alice@example.com"}`,
			RetrievedContext: []agent.Document{
				{Content: "User data format", Metadata: map[string]string{"id": "go-concurrency"}},
			},
		},
		{
			Query:        "What is Go?",
			ActualOutput: "Go is a language with great concurrency support using goroutines.",
			RetrievedContext: []agent.Document{
				{Content: "Go concurrency", Metadata: map[string]string{"id": "go-concurrency"}},
				{Content: "Go channels", Metadata: map[string]string{"id": "go-channels"}},
			},
		},
	}

	suite, err := eval.NewEvalSuite(cases, []eval.Evaluator{keywords, jsonEval, ndcg})
	if err != nil {
		log.Fatal(err)
	}

	report, err := suite.Run(ctx)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Total cases: %d\n", report.TotalCases)
	for name, summary := range report.Summaries {
		fmt.Printf("  %-20s mean=%.2f  passed=%d  failed=%d\n",
			name, summary.MeanScore, summary.Passed, summary.Failed)
	}

	// Print as JSON.
	fmt.Println("\nFull report (JSON):")
	out, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(out))
}

func printResult(r eval.EvalResult) {
	status := "✓ PASS"
	if !r.Pass {
		status = "✗ FAIL"
	}
	fmt.Printf("  [%s] %s  score=%.2f", status, r.EvaluatorName, r.Score)
	if r.Explanation != "" {
		fmt.Printf("  (%s)", r.Explanation)
	}
	fmt.Println()
}
