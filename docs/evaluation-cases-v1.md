# Deterministic evaluation cases v1

This versioned, local-only suite describes release qualification scenarios. It is intentionally documentation plus ordinary Go tests—not a hosted evaluation platform and not a source of paid provider calls.

| ID | Scenario | Deterministic setup | Expected result |
|---|---|---|---|
| AUTH-001 | Unrestricted tool without a principal | Scripted provider requests a tool with no role/attribute policy | Tool is advertised and may run. |
| AUTH-002 | Role or attribute protected tool without a principal | Scripted provider requests a tool with `AllowRoles`, `DenyRoles`, `AllowWhen`, or `DenyWhen` | Tool is filtered when role enforcement is installed and denied in the execution pipeline; handler does not run. |
| AUTH-003 | Approval resume loses principal | Start a matching-principal approval pause, resume without a principal | Approved call remains denied; handler does not run. |
| AUTH-004 | Recovery replay lacks principal | Seed a replay-safe protected call in flight, recover without a principal | Recovery records a denial and never runs the handler. |
| RETRY-001 | Visible stream failure | Scripted provider emits text or thinking then errors | No retry occurs, preventing duplicated or divergent visible output. |
| RETRY-002 | Untyped provider failure classification | Scripted provider returns an arbitrary error | No new classifier assertion: typed transient-only classification is proposed, not implemented, because the provider contract lacks shared API error metadata. |
| LIMIT-001 | Per-attempt reservation | Scripted provider fails silently, then succeeds under retry | Each physical attempt gets an independent lease; the failed attempt conservatively settles its estimate and the success commits actual usage. |
| LIMIT-002 | Lease terminal integrity | Memory and miniredis backends exercise duplicate, conflicting, unknown, and expired terminals | Operations return typed lifecycle errors and no invalid lease mutates counters. |
| LIMIT-003 | Output reservation | Request includes `InferenceConfig.MaxTokens` | Admission reserves estimator plus output bound before dispatch. |
| REL-001 | Release ref race | Review `.github/workflows/release.yml` deterministically | The workflow refuses to rebase a release commit; if `origin/master` changed after the recorded base, it fails so a new run recomputes version and revalidates. |
| REL-002 | Tag provenance | Review workflow after an atomic push | Each local and remote release tag must resolve to the exact release commit. |

Run the local checks with the root workspace active:

```sh
go test ./agent/...
go vet ./agent/...
```

Provider account, quota, region, credential, and network validation are intentionally excluded; see the [support matrix](support-matrix.md).
