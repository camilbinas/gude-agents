# Code Review — gude-agents (Fourth Pass)

**Date:** April 21, 2026
**Scope:** Production readiness for 100,000 concurrent agents, maintained for years.
**Method:** Line-by-line audit of every production source file in lib/agent/.

---

## Executive Summary

All 6 issues from the third-pass review have been fixed. The framework is in strong shape. This fourth pass found 2 real bugs, 2 design risks, and 2 performance concerns. The most impactful is the swarm parallel handoff leaving uninitialized results — a correctness issue under concurrency.

---

## STATUS OF PREVIOUS ISSUES — All 6 Fixed

### 1. Swarm bypasses WithTimeout/WithRetry — FIXED
`swarm.go:475` now calls `a.CallProvider()` which delegates to `callProviderWithRetry()`.

### 2. Slice mutation bug in swarm middleware merging — FIXED
`swarm.go:617` now uses `make([]Middleware, 0, ...)` before appending. No shared backing array.

### 3. Nil provider not validated in New() — FIXED
`agent.go:52`: `if provider == nil { return nil, fmt.Errorf("provider is required") }`.

### 4. Swarm active-agent save error silently ignored — FIXED
`swarm.go:408` now logs: `s.logf("[swarm] failed to save active agent for %s: %v", convID, err)`.

### 5. Async tool panics silently when errLogger is nil — FIXED
Both `NewAsync` and `NewAsyncRaw` in `tool/tool.go` now fall back to `log.Printf` when errLogger is nil.

### 6. FrameworkTool ghost terminology — FIXED
`rag.go` no longer references "FrameworkTool".

---

## CRITICAL — Fix Before Production

### 1. Swarm parallel handoff leaves uninitialized tool results

```go
// swarm.go:663-673
if a.ParallelTools() {
    var wg sync.WaitGroup
    for i, tc := range calls {
        wg.Add(1)
        go func(i int, tc tool.Call) {
            defer wg.Done()
            exec(i, tc)
        }(i, tc)
    }
    wg.Wait()
}
```

When `ParallelTools()` is true and a handoff tool fires, the code returns `results, handedOff` immediately after `wg.Wait()`. But unlike the sequential path (which fills remaining slots with "Skipped — handoff in progress."), the parallel path does not. In parallel mode all goroutines run to completion, so every `results[i]` slot gets written — but the caller receives a mix of real tool results and the single handoff result with no way to distinguish which tools ran uselessly alongside the handoff.

The sequential path handles this correctly at lines 676-684 by filling remaining slots with skip notices. The parallel path at lines 663-673 has no equivalent.

**File:** `lib/agent/swarm.go:663-673`
**Impact:** Inconsistent tool results after parallel handoff. Not a crash (all slots are initialized by `make`), but the caller sees stale results from tools that ran alongside the handoff.
**Fix:** After `wg.Wait()`, if `handedOff` is true, scan results and fill any slot that wasn't the handoff tool with a skip notice:
```go
wg.Wait()
if handedOff {
    for i, r := range results {
        if r.Content != "Handoff accepted. Transferring conversation." && r.ToolUseID == "" {
            results[i] = ToolResultBlock{
                ToolUseID: calls[i].ToolUseID,
                Content:   "Skipped — handoff in progress.",
            }
        }
    }
}
```

---

## HIGH — Should Fix Before Scale

### 2. Summary re-load happens inside mutex, blocking all conversations

```go
// memory/summary.go:277-278
s.mu.Lock()
latest, loadErr := s.inner.Load(ctx, conversationID)
```

`runSummarize` acquires `s.mu.Lock()` and then calls `s.inner.Load()` — a potentially slow I/O operation (network call to Redis, DynamoDB, Postgres, etc.). While this lock is held, **every other conversation's** `Save()` call blocks at `s.mu.Lock()` on line 230. At 100k concurrent agents, this serializes all memory saves behind a single slow Load.

**File:** `lib/agent/memory/summary.go:277`
**Impact:** Lock contention bottleneck. One slow backend Load blocks all conversations from saving.
**Fix:** Load outside the lock, then re-acquire to merge:
```go
latest, loadErr := s.inner.Load(ctx, conversationID)
if loadErr != nil { ... return }

s.mu.Lock()
// re-validate state under lock, then merge
```

This is safe because the cutoff index is already captured, and the worst case of a concurrent save is that we re-summarize slightly more — which is already handled by the tail-preservation logic.

---

## MEDIUM — Maintainability & Correctness

### 3. Graph conditional router errors are not wrapped

```go
// graph/graph.go:343-346
next, err := r.conditional(ctx, currentState)
if err != nil {
    return err
}
```

When a conditional router returns an error, it propagates unwrapped. All other graph errors use `GraphValidationError` or `GraphIterationError`, making them distinguishable via `errors.As`. Router errors are bare, so callers can't tell if an error came from a router, a node, or the graph engine.

**File:** `lib/agent/graph/graph.go:343-346`
**Impact:** Error handling inconsistency. Callers using `errors.As` to distinguish graph errors miss router failures.
**Fix:** Wrap the error:
```go
if err != nil {
    return fmt.Errorf("graph: conditional router for %q: %w", nodeName, err)
}
```

### 4. Swarm parallel tool execution doesn't cancel on handoff

```go
// swarm.go:663-673
if a.ParallelTools() {
    var wg sync.WaitGroup
    for i, tc := range calls {
        wg.Add(1)
        go func(i int, tc tool.Call) {
            defer wg.Done()
            exec(i, tc)
        }(i, tc)
    }
    wg.Wait()
}
```

When a handoff fires in parallel mode, all other goroutines run to completion. Their results are discarded. At scale with slow tools (MCP calls, API requests), this wastes resources.

**File:** `lib/agent/swarm.go:663-673`
**Impact:** Wasted CPU/memory/network. Not a leak (goroutines complete), but wasteful at 100k scale.
**Fix:** Use a cancellable context:
```go
toolCtx, cancel := context.WithCancel(ctx)
defer cancel()
// In exec, after detecting handoff:
cancel() // signal other goroutines to stop
```

---

## LOW — Design Notes

### 5. ContentBlock is sealed with no extension path

Adding a new block type (ImageBlock, AudioBlock) requires coordinated changes across all 4 providers, all 7 memory backends, the normalizer, and the filter strategy.

**File:** `lib/agent/provider.go:30-55`
**Impact:** Framework evolution is expensive but infrequent.
**Mitigation:** Acceptable. Document the process when the time comes.

### 6. Graph state copying is O(n) per node

Every node execution copies the entire state map via `CopyState`. With large states (100+ keys) and deep graphs, this adds up.

**File:** `lib/agent/graph/graph.go:278-280`
**Impact:** Performance degradation with large states at scale.
**Mitigation:** Acceptable for current state sizes. Consider copy-on-write if states grow beyond 50 keys.

---

## VERIFIED FALSE POSITIVES

These were investigated and found to be correct:

- **Graph fork state merging race:** Not a race. `wg.Wait()` ensures all branches complete before merge. Merge happens under `e.mu.Lock()`. Join checks happen after merge. The flow is strictly sequential: branches → wait → merge (locked) → check joins.

- **Graph fork completed map copy:** Each branch gets its own `runExec` with `isBranch: true`. Branch completed maps are merged back into the parent under lock after `wg.Wait()`. No concurrent access.

- **MCP pool acquire/release race:** The pool uses a buffered channel as the idle pool with atomic `closed` flag. `acquire` checks closed, tries non-blocking receive, then tries to grow under mutex, then blocks on channel. `release` checks closed before sending. Clean design.

- **Summary re-trigger goroutine leak:** `reTriggered` flag prevents the deferred cleanup from clearing `summarizing[conv]`, so the new goroutine inherits the lock. `wg.Add(1)` is called before `go`, so `Wait()` tracks it. Correct.

- **Agent loop memory save after guardrail:** The final text saved to memory is `resp.Text` (line 400), not `finalText` (the guardrail-modified version). This is intentional — memory stores the raw provider response, and guardrails are re-applied on each load. Correct design choice.

---

## NAMING AUDIT

| Current | Correct? | Notes |
|---------|----------|-------|
| `ModelID()` | ✓ | |
| `ProviderError` / `ProviderCreationError` | ✓ | |
| `ToolError` / `GuardrailError` | ✓ | |
| `ErrTokenBudgetExceeded` | ✓ | Sentinel, correct pattern |
| `ErrHandoffRequested` | ✓ | Sentinel, correct pattern |
| `WithTimeout` / `WithRetry` | ✓ | |
| `WithMemory` / `WithSharedMemory` | ✓ | Clear distinction |
| `NormMerge` / `NormFill` / `NormRemove` | ✓ | |
| `SwarmMember` / `Handoff` | ✓ | |
| `handoffSentinel` vs `handoffSentinelHuman` | ✓ | Different: swarm vs human handoff |
| `InvokeStructured[T]` | ⚠️ | Go limitation (package-level generic func), documented |
| `CallProvider` (exported on Agent) | ✓ | Used by swarm, clean API |
| `NewSummaryFunc` vs `DefaultSummaryFunc` | ✓ | Custom vs batteries-included |

---

## PRIORITY ORDER

1. **Fix swarm parallel handoff result initialization** — 5 line fix, prevents inconsistent results
2. **Move summary re-load outside mutex** — 3 line refactor, prevents lock contention at scale
3. **Wrap graph conditional router errors** — 1 line fix, improves error handling consistency
4. **Cancel parallel tools on handoff** — 3 line fix, reduces resource waste
5. **Document ContentBlock extension process** — when needed
6. **Document graph state copying limitation** — when state sizes grow
