# Evaluation Framework

The `agent/eval` package provides a structured framework for measuring output quality. It lives in its own Go module (`github.com/camilbinas/gude-agents/agent/eval`) so you can pull it into test binaries or CI scripts without adding LLM dependencies to your production binary.

The pipeline has three layers:

1. **Test cases** — `EvalCase` structs that capture a query, the agent's output, retrieved documents, and an optional reference answer.
2. **Evaluators** — implementations of the `Evaluator` interface that score a case and return an `EvalResult`. Built-in evaluators cover faithfulness, context precision, JSON structure, keyword grounding, and retrieval ordering. Custom evaluators are straightforward to add.
3. **Suite** — `EvalSuite` runs every evaluator against every case, collects `CaseResults`, and returns an `EvalReport` with per-evaluator `EvalSummary` aggregates.

## Installation

```bash
go get github.com/camilbinas/gude-agents/agent/eval
```

## Core Types

### EvalCase

A single test case passed to every evaluator in the suite.

```go
type EvalCase struct {
    Query            string            // the input question or prompt
    ActualOutput     string            // the agent's response
    RetrievedContext []agent.Document  // documents retrieved by the RAG pipeline
    ReferenceAnswer  string            // optional ground-truth answer
    Metadata         map[string]string // arbitrary key-value annotations
}
```

`RetrievedContext` is used by the faithfulness and context precision evaluators. `ReferenceAnswer` is required by context precision. `Metadata` is carried through to `EvalReport` for filtering and reporting.

### EvalResult

The output of a single evaluator for a single case.

```go
type EvalResult struct {
    EvaluatorName string  // identifies which evaluator produced this result
    Score         float64 // normalized to [0.0, 1.0]
    Pass          bool    // true if Score >= configured threshold
    Explanation   string  // human-readable summary of how the score was reached
}
```

### EvalReport

The aggregated output of `EvalSuite.Run`.

```go
type EvalReport struct {
    Timestamp  time.Time              // when Run was called
    TotalCases int                    // len(cases)
    Results    []CaseResults          // one entry per case, in input order
    Summaries  map[string]EvalSummary // keyed by evaluator name
}
```

`ResultsForEvaluator(name string) []EvalResult` filters `Results` down to the named evaluator's outputs across all cases — useful for per-evaluator CI assertions.

### CaseResults

Groups all evaluator results for one case.

```go
type CaseResults struct {
    Case    EvalCase     // the original input
    Results []EvalResult // one per evaluator (only evaluators that did not error)
    Error   string       // non-empty if one or more evaluators returned an error
}
```

### EvalSummary

Per-evaluator aggregate across all cases.

```go
type EvalSummary struct {
    EvaluatorName string  // evaluator name
    MeanScore     float64 // average score across all cases
    Passed        int     // cases where Pass == true
    Failed        int     // cases where Pass == false or evaluator errored
}
```

### Evaluator Interface

Implement this interface to add a custom evaluator.

```go
type Evaluator interface {
    Evaluate(ctx context.Context, ec EvalCase) (EvalResult, error)
    Name() string
}
```

`Name()` must return a stable string — it is used as the key in `EvalReport.Summaries` and in `ResultsForEvaluator`.

## Evaluator Options

All built-in evaluators accept `EvaluatorOption` values.

| Option | Default | Description |
|--------|---------|-------------|
| `WithThreshold(t float64)` | `0.5` | Pass/fail threshold. A result passes when `Score >= t`. |

```go
faithfulness := eval.NewFaithfulness(provider, eval.WithThreshold(0.7))
```

## Running a Suite

### Sequential (default)

```go
suite, err := eval.NewEvalSuite(cases, []eval.Evaluator{faithfulness, jsonCheck})
if err != nil {
    log.Fatal(err)
}

report, err := suite.Run(ctx)
if err != nil {
    log.Fatal(err)
}

for _, summary := range report.Summaries {
    fmt.Printf("%s: mean=%.2f passed=%d failed=%d\n",
        summary.EvaluatorName, summary.MeanScore, summary.Passed, summary.Failed)
}
```

`NewEvalSuite` returns an error if `cases` or `evaluators` is empty.

### With Concurrency

```go
suite, err := eval.NewEvalSuite(
    cases,
    []eval.Evaluator{faithfulness, contextPrec, jsonCheck},
    eval.WithSuiteConcurrency(5),
)
```

| Suite Option | Default | Description |
|-------------|---------|-------------|
| `WithSuiteConcurrency(n int)` | `1` | Max parallel evaluations. Values below 1 are clamped to 1. |

Each `(case, evaluator)` pair is one unit of work. With three evaluators and ten cases, `WithSuiteConcurrency(5)` keeps at most five LLM calls in flight at once. Context cancellation stops dispatching new work but waits for in-flight calls to complete.

## Built-In Evaluators

### Faithfulness

```go
eval.NewFaithfulness(provider agent.Provider, opts ...EvaluatorOption) *Faithfulness
```

Measures whether the agent's answer is factually grounded in the retrieved context. Uses a two-step LLM prompting approach:

1. Extract factual claims from `ActualOutput`.
2. Judge each claim as "supported" or "unsupported" against `RetrievedContext`.

`Score = supported claims / total claims`. When no claims are found, the score is `1.0`. Requires a populated `RetrievedContext`. Uses the evaluator name `"faithfulness"`.

Use when: you want to catch hallucinations in RAG pipelines. Higher is better.

```go
judge := bedrock.Must(bedrock.Cheapest())
faithfulness := eval.NewFaithfulness(judge, eval.WithThreshold(0.7))
```

### ContextPrecision

```go
eval.NewContextPrecision(provider agent.Provider, opts ...EvaluatorOption) *ContextPrecision
```

Measures how precisely the retrieved documents are relevant to the query. The LLM judges each document in `RetrievedContext` as relevant or not-relevant to `Query`+`ReferenceAnswer`, then computes average precision (AP):

`AP = (1/R) × Σ(Precision@k × rel_k)` where R is the number of relevant documents.

`Score = 0.0` when `RetrievedContext` is empty or no documents are judged relevant. Requires both `RetrievedContext` and `ReferenceAnswer`. Uses the evaluator name `"context_precision"`.

Use when: you want to measure retrieval quality — whether the right documents are coming back at the top of the ranked list.

```go
contextPrec := eval.NewContextPrecision(judge, eval.WithThreshold(0.6))
```

### JSONStructure

```go
eval.NewJSONStructure(requiredKeys []string, opts ...EvaluatorOption) *JSONStructure
```

A rule-based evaluator (no LLM required) that validates `ActualOutput` as JSON and checks for required top-level keys. `Score = 1.0` on success, `0.0` on failure. An empty `requiredKeys` slice checks JSON validity only.

Uses the evaluator name `"json_structure"`.

Use when: your agent is supposed to return structured JSON (function outputs, structured data extraction, tool response validation).

```go
jsonCheck := eval.NewJSONStructure([]string{"name", "email", "age"})
```

### Other Built-In Evaluators

| Constructor | Name | Type | What it measures |
|-------------|------|------|-----------------|
| `NewKeywordGrounding(keywords []string, opts...)` | `"keyword_grounding"` | Rule-based | Fraction of required keywords present in `ActualOutput` (case-insensitive). Returns an error if `keywords` is empty. |
| `NewRelevance(provider, opts...)` | `"relevance"` | LLM-based | How well `ActualOutput` addresses `Query`, rated [0, 1] by an LLM judge. |
| `NewRetrievalOrdering(expectedIDs []string, idExtractor func(agent.Document) string, opts...)` | `"retrieval_ordering"` | Rule-based | NDCG score comparing `RetrievedContext` order to the expected document priority. Returns an error if `expectedIDs` is empty or `idExtractor` is nil. |

## Custom Evaluator

Implement the `Evaluator` interface to add project-specific scoring logic.

```go
// ExactMatchEvaluator checks whether the actual output exactly equals
// the reference answer (case-insensitive).
type ExactMatchEvaluator struct {
    threshold float64
}

func NewExactMatch(opts ...eval.EvaluatorOption) *ExactMatchEvaluator {
    cfg := struct{ threshold float64 }{threshold: 0.5}
    for _, o := range opts {
        // EvaluatorOption is a func(*evaluatorConfig) — replicate the pattern
        // or embed eval.WithThreshold in a wrapper.
    }
    return &ExactMatchEvaluator{threshold: cfg.threshold}
}

func (e *ExactMatchEvaluator) Name() string { return "exact_match" }

func (e *ExactMatchEvaluator) Evaluate(_ context.Context, ec eval.EvalCase) (eval.EvalResult, error) {
    match := strings.EqualFold(
        strings.TrimSpace(ec.ActualOutput),
        strings.TrimSpace(ec.ReferenceAnswer),
    )
    score := 0.0
    if match {
        score = 1.0
    }
    return eval.EvalResult{
        EvaluatorName: e.Name(),
        Score:         score,
        Pass:          score >= e.threshold,
        Explanation:   fmt.Sprintf("exact match: %v", match),
    }, nil
}
```

Wire it into a suite like any built-in evaluator:

```go
suite, err := eval.NewEvalSuite(
    cases,
    []eval.Evaluator{exactMatch, faithfulness},
)
```

## Go Test Integration

`eval.RunT` runs a suite inside a `*testing.T`, turning each case into a named subtest and calling `t.Errorf` for any failing evaluator:

```go
func TestAgentQuality(t *testing.T) {
    kg, err := eval.NewKeywordGrounding([]string{"Go", "concurrency"})
    if err != nil {
        t.Fatal(err)
    }

    eval.RunT(t, []eval.EvalCase{
        {
            Query:        "What makes Go good for concurrent programming?",
            ActualOutput: agentResponse,
        },
    }, []eval.Evaluator{kg})
}
```

For a single assertion, `eval.RunTSingle` is more concise:

```go
eval.RunTSingle(t, kg, eval.EvalCase{ActualOutput: agentResponse})
```

To assert an explicit minimum score independent of the evaluator's configured threshold, use `eval.AssertScore`:

```go
eval.AssertScore(t, faithfulness, myCase, 0.8)
```

## Full Example

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/camilbinas/gude-agents/agent"
    "github.com/camilbinas/gude-agents/agent/eval"
    "github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
    ctx := context.Background()

    cases := []eval.EvalCase{
        {
            Query:           "Who founded Acme Corp?",
            ActualOutput:    "Acme Corp was founded in 2019 by Jane Smith in Austin, Texas.",
            RetrievedContext: []agent.Document{
                {Content: "Acme Corp was founded in 2019 by Jane Smith in Austin, Texas."},
            },
            ReferenceAnswer: "Jane Smith founded Acme Corp in 2019.",
        },
    }

    judge := bedrock.Must(bedrock.Cheapest())
    faithfulness := eval.NewFaithfulness(judge, eval.WithThreshold(0.7))
    contextPrec  := eval.NewContextPrecision(judge, eval.WithThreshold(0.6))
    jsonCheck    := eval.NewJSONStructure(nil) // JSON validity only

    suite, err := eval.NewEvalSuite(
        cases,
        []eval.Evaluator{faithfulness, contextPrec, jsonCheck},
        eval.WithSuiteConcurrency(3),
    )
    if err != nil {
        log.Fatal(err)
    }

    report, err := suite.Run(ctx)
    if err != nil {
        log.Fatal(err)
    }

    for _, s := range report.Summaries {
        fmt.Printf("%-20s mean=%.2f  passed=%d  failed=%d\n",
            s.EvaluatorName, s.MeanScore, s.Passed, s.Failed)
    }

    // Filter results for a specific evaluator.
    for _, r := range report.ResultsForEvaluator("faithfulness") {
        fmt.Printf("score=%.2f pass=%v %s\n", r.Score, r.Pass, r.Explanation)
    }
}
```

See `examples/eval-rag/` for a full RAG quality workflow including agent invocation, retrieval, and combined LLM + rule-based evaluation.

See `examples/eval-rule-based/` for a self-contained rule-based pipeline (no LLM costs) using `KeywordGrounding`, `JSONStructure`, and `RetrievalOrdering`.

## See Also

- [RAG](rag.md) — building the retrieval pipeline that `EvalCase.RetrievedContext` comes from
- [Providers](providers.md) — choosing an `agent.Provider` to use as the LLM judge
- [Agent API](agent-api.md) — invoking an agent to produce `EvalCase.ActualOutput`
- [Structured Output](structured-output.md) — generating JSON outputs that `JSONStructure` can validate
