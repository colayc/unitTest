# Windows protected tool digest reuse

Date: 2026-10-09
Status: presented design approved; written-spec review pending; implementation not authorized.

## 0. Resumed design-only scope and version boundary

On 2026-10-09 the human explicitly paused the eleven-capability product expansion and deferred it to version 2, returning to this Windows protected tool digest reuse design. Reuse this existing spec (first committed as a45902f2), rather than create another competing design. This turn records the design and pause only: no implementation plan, production change, test run or native launch is authorized.

The requested contract remains: reuse tool digests only within one coverage-index attestation; compute the first complete hash while holding a Windows write/delete-denying guard; recheck path and file identity on every reuse; keep both full workspace discoveries and all source/report/permission checks; fall back to original full verification when optional protection cannot be established; fail on bound identity drift; retain at most 128 tool files and release guards on success/error/cancellation. Focused reuse/change/release/concurrency tests precede any future implementation acceptance; Linux retains the original logic and native is not automatically rerun. This reduces repeated hashing, without a promise to eliminate all of the observed 27-second latency.

The partially executed unified-maintainable-case plan is paused. Its Task 1 foundational local commits 53924e42/575208a5 and uncommitted, unreviewed Task 2 work are preserved; they do not establish that the expanded functions are complete. Do not resume that plan without a new explicit request. Existing first-version generation/execution/coverage behavior and the original W4 evidence boundaries are not removed or relabeled by this deferral.

## 1. Purpose and evidence boundary

Reduce redundant hashing of the same verified tool files during one current-coverage-index attestation on Windows, without changing discovery, source freshness, ownership or publication authority.

The single retained native diagnostic expired after 1200.3916953 seconds. Its existing closed-schema trace records startTestGeneration returning after 250591ms and thirteen completed getTestGenerationRun calls totaling 359831ms (average 27679.30769230769ms). Its final call remained pending. These are client-boundary observations, not internal hashing measurements, a permanent hang, worker-state evidence or native acceptance.

Source inspection confirms that Runtime.ReadCurrentCoverageIndex invokes InspectWorkspace twice, and both inspections perform full CMake/toolchain discovery. Toolchain executable snapshots hash at open and repeatedly in Verify around probes. CMake snapshots likewise repeatedly hash protected files. The measured fraction attributable to hashing is unknown. Structural hash-work reduction must not be advertised as a proven repair of all 27–28s latency.

Original W4 remains incomplete. The diagnostic's identified-process-stop-unconfirmed warning, unknown exact stop/cleanup timing, unestablished descendant cleanup and absent acceptance receipt remain unchanged. The human exception accepts the timing evidence limitation only.

## 2. Selected approach and scope

Human approved Windows-first scope, then approach 1: protected digest reuse within one attestation. Across-query discovery caching and start-only resolution deduplication are not selected.

Enable the optimization only inside one actual Runtime.ReadCurrentCoverageIndex invocation on Windows. Its two InspectWorkspace calls may share the same private scope. A later index attestation, even within the same higher-level start request, creates a new scope and recomputes first-use hashes. No service-lifetime, process-global, cross-query, TTL, stored Snapshot/Index or discovery-output cache is introduced.

Non-Windows and non-scoped consumers retain their current full-hash behavior. This does not expand the earlier shared selected-toolchain-ID amendment or change Linux runtime semantics.

## 3. Components and integration

A small private service component owns the Windows file guards, verified digests, bounds and scope lifetime. A private context binding conveys it to eligible tool-snapshot readers; clients cannot create, choose or inject it through IPC.

Integration is limited to:

- Runtime current-index attestation lifetime;
- CMake/paired CTest executable-snapshot hashing;
- Windows toolchain executable-snapshot hashing;
- focused lease, snapshot and runtime tests.

Existing snapshot consumers keep independent read handles and Close ownership. The scope owns separate protection handles, avoiding shared seek offsets and double-close ownership. Shared snapshot structs may carry optional scope-bound evidence; nil/non-Windows paths preserve existing behavior. Do not add unrelated snapshot/refactoring changes.

Eligible files are tool executables already resolved and validated by the existing CMake/toolchain authority paths. Do not infer eligibility from a client-supplied filename or a general filesystem cache.

User source, editable test files, workspace configuration, preset/declaration inputs, database files, reports, license/manifest/state files, environment scripts, and the separately authenticated selected SDK library are outside digest reuse. Their existing verification remains intact.

## 4. Guarded digest contract

First use must acquire a direct regular-file Windows read handle that denies write/delete sharing before calculating the complete SHA-256. Ordinary toolchain os.Open plus its current rehash is not such a protection lease and cannot authorize reuse by itself.

An entry binds the validated direct/canonical path, current filesystem-object identity, protected handle, size and complete digest. Reuse requires:

1. The scope and guard remain live.
2. Current path and consumer handle still identify the exact protected file object.
3. Existing path/root, regular-file, executable-mode and per-consumer size rules still succeed.
4. Alias/reparse restrictions are not broadened.
5. No previously bound guard or identity check has failed.

Recheck path/handle identities at each reuse. Pathname, timestamp, size or a previous digest alone is insufficient. Do not use a cache hit to skip existing root or publication-boundary validation, or equate different hard-link paths merely because bytes match.

This uses the existing product-controlled tool publication model and ordinary Windows handle protections; it does not claim protection against privileged/kernel manipulation or arbitrary pre-existing writable memory mappings. If a supported publication path cannot establish the required immutability assurance, reuse is unavailable and full verification remains mandatory. Preserve existing publication assumptions rather than silently broadening them.

## 5. Data flow and unchanged authority

Keep current query validation and report/run/project binding. For a valid Windows query, open the private scope and register cleanup before starting inspection.

First InspectWorkspace performs complete discovery. Eligible files receive guarded first-use hashes; repeated verification of an unchanged protected object may reuse that digest. CMake/compiler/environment/preset probes still run with their original identities, arguments, budgets and before/after checks.

Read the persisted attested coverage graph and validate every indexed source against current bytes as before. Second InspectWorkspace also performs complete discovery and may reuse eligible protected tool digests. Both workspace-generation comparisons still execute.

Release the scope before returning. Returned indexes contain no reusable lease, scope capability or authority transferable to a later call. A subsequent progress query starts fresh.

Managed target/source-digest equality, selected current toolchain/profile membership, owner binding and immediate post-preparation re-attestation before persistence are unchanged. No direct shortcut to legacy protocol 1.5 is introduced.

## 6. Bounds, errors and concurrency

Retain at most 128 distinct tool-file entries. Store metadata/digests only; stream file hashes rather than buffering complete executables. Reuse eligibility is bounded to 512MiB per tool; all existing stricter consumer limits still apply. A larger tool currently valid under a different consumer's original contract is verified through that original path, not rejected merely by the optional reuse budget.

Noneligible files, capacity exhaustion or inability to establish optional protection use the existing complete validation path. They never authorize an unchecked digest. Once evidence is bound, detected file/identity drift, invalidated protection, digest mismatch, cancellation or internal guard errors must fail closed through the existing error mapping rather than become harmless cache misses.

Inspector's concurrent CMake/toolchain branches may share the scope. Serialize first hash/entry publication for a shared object; publish no partial digests. Do not hold a scope mutex across external probe execution. Consumer handles remain independent.

Close must atomically forbid new acquisition/reuse, coordinate active hashing/identity checks and release each owned guard exactly once. Cancellation checks remain active during streaming. Cancellation may cause Inspector to return while discovery goroutines are unwinding: no use of closed handles, handle-number reuse, stale evidence publication or leaked entries is allowed. Cleanup failures cannot be disguised as successful current-index attestation.

Temporary write/delete exclusion affects only eligible tool files during one check, not the IDE's entire lifetime or maintainable user tests. Successful completion, error, cancellation and repeated Close must release the scope without creating a new unbounded wait or extending process/request budgets.

## 7. Focused verification and success criteria

Implementation requires genuine behavioral RED/GREEN and independent review of the actual changed source, with no claim based on a missing API/compile error alone.

Required focused cases:

- One complete hash per protected file within a scope despite repeated open/Verify use.
- A new independent attestation hashes current bytes again and observes changed bytes.
- Real Windows write/delete denial during protection and successful release afterward.
- Direct-path/object-identity and forbidden-alias drift rejection.
- Noneligible/protection-unavailable/capacity paths execute original full hashing.
- Original per-file limits remain enforced; optional limits do not change accepted original inputs.
- Closed/cancelled/hash-error/cleanup-error paths cannot return cached authority.
- Concurrent duplicate acquisition/close produces no partial evidence, shared-offset race or leaked guard.
- Both Inspector invocations and all-source/report/workspace freshness checks remain active.
- Non-scoped and Linux paths preserve original behavior.

Hash-call/byte counters are structural test evidence, not a measured native latency result. Record focused timings/counters accurately; do not promise a particular request duration. Any real latency comparison or native attempt requires separately bounded authorization and complete cleanup/evidence handling. If latency remains high, disclose it and investigate the remaining cost rather than expanding this scope or increasing timeouts.

Reuse existing accepted W1–W3, identity, UI and fixture evidence. Do not repeat whole suites, hosted workflows, packaging or native runs to substitute for focused proof. Relevant new lease/integration race checks belong to the later implementation plan, not this design-only turn.

## 8. Exclusions and handoff

No public protocol, UI/helper contract, generated-test maintenance, publisher, coverage metric, source assertion or selected SDK library policy change. No removal of full discoveries or fresh index reads; no start-stage deduplication; no process/request/poll/diagnostic deadline changes. Existing dirty source and retained audit artifacts stay intact.

No push, PR update, master synchronization, merge, force operation, Release, signing, dependency download or installation is authorized by this design. Document-only local commit is separate from implementation/native acceptance.

The newly requested all-function/class boundary/random generation, manual/data-driven wizard, true statement/MC/DC metrics, static-test management and transfer/report features are a separate version 2 requirements expansion, explicitly paused by the human. This deferral includes the unified-maintainable-case implementation plan; preserve its completed and partial work for later resumption. These features must not broaden this performance fix or be reported as implemented by it.

Written-spec review is required before writing-plans. The later plan must define exact source ownership, focused tests, review gates and return to original W4. Original dual-framework/native/maintenance/clean-SHA/global/whole-PR/hosted/package and Phase 8 release gates remain open.

## References

- [Windows CMake coverage compatibility](2026-10-08-windows-cmake-coverage-compatibility-design.md)
- [Bounded operation-trace diagnostic](2026-10-09-native-operation-trace-diagnostic-design.md)
- [Selected-toolchain identity contract](2026-10-09-selected-toolchain-identity-contract-design.md)
