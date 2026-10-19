# Deterministic evaluation cases v1

This versioned, local-only suite describes release qualification scenarios. It is intentionally documentation plus ordinary Go tests—not a hosted evaluation platform and not a source of paid provider calls.

| ID | Scenario | Deterministic setup | Expected result |
|---|---|---|---|
| AUTH-001 | Unrestricted tool without a principal | Scripted provider requests a tool with no role/attribute policy | Tool is advertised and may run. |
| AUTH-002 | Role or attribute protected tool without a principal | Scripted provider requests a tool with `AllowRoles`, `DenyRoles`, `AllowWhen`, or `DenyWhen` | Tool is hidden when role enforcement is installed and denied in the execution pipeline; handler does not run. |
| AUTH-003 | Approval resume loses principal | Start a matching-principal approval pause, resume without a principal | Approved call remains denied; handler does not run. |
| AUTH-004 | Recovery replay lacks principal | Seed a replay-safe protected call in flight, recover without a principal | Recovery records a denial and never runs the handler. |
| RETRY-001 | Visible stream failure | Scripted provider emits text or thinking then errors | The error is final; the provider is called once, so no duplicated or divergent visible output exists. |
| RETRY-002 | No framework retry | Scripted provider fails once, then would succeed; a second scenario times out under `WithProviderTimeout` | The first failure and the timeout each surface after exactly one provider call. Retry policy belongs to the application. |
| LIMIT-001 | Per-attempt reservation | Scripted provider fails | One physical attempt reserves one lease; the failed attempt conservatively settles its estimate and the agent does not retry it. |
| LIMIT-004 | Application-defined bucket key | Two conversations share an identity selected with `WithRateLimitKey`; other identities; no selector; a global limit | Same selected key shares a budget, different keys are independent, no selector means one shared empty-key bucket, and global limits apply across all keys. |
| LIMIT-002 | Lease terminal integrity | Memory and miniredis backends exercise duplicate, conflicting, unknown, and expired terminals | Operations return typed lifecycle errors and no invalid lease mutates counters. |
| LIMIT-003 | Output reservation | Request includes `InferenceConfig.MaxTokens` | Admission reserves estimator plus output bound before dispatch. |
| REL-001 | Release ref race | Review `.github/scripts/release.sh push` deterministically | The release commit and every tag are pushed in one `--atomic` operation; if `master` advanced after the run started, the push is rejected and nothing is published. |
| REL-002 | Tag provenance | Review `release.sh tags` and `push` | Every root and nested-module tag is annotated and must resolve, locally and on the remote, to the one release commit. |

Run the local checks with the root workspace active:

```sh
go test ./agent/...
go vet ./agent/...
```

Provider account, quota, region, credential, and network validation are intentionally excluded; see the [support matrix](support-matrix.md).
