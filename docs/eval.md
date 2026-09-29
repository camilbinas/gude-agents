# Evaluation

`agent/eval` scores recorded cases independently from production invocation. Build each case from an `agent.Result` and, for retrieval metrics, `rag.Document` values.

```go
result, err := a.Invoke(ctx, query)
if err != nil { return err }
caseToScore := eval.EvalCase{
    Query:        query,
    ActualOutput: result.Text,
    ReferenceAnswer: expected,
    RetrievedContext: docs,
}
```

An `Evaluator` implements `Name` and `Evaluate(context.Context, EvalCase)`. Built-ins cover relevance, faithfulness, context precision, retrieval ordering/NDCG, keywords, and JSON structure. LLM-judged evaluators accept the same `agent.Provider` contract used by agents; deterministic evaluators require no provider.

Use `WithThreshold` to set pass criteria. Suites aggregate per-case results and evaluator summaries; test helpers include `RunT`, `RunTSingle`, and `AssertScore`. Keep evaluation providers, prompts, and datasets versioned so score changes are explainable.

Retrieved documents belong to `agent/rag`, not the root agent package. Use [Structured output](structured-output.md) when the production task itself requires typed output.
