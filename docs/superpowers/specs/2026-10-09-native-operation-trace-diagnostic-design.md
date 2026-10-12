# Native operation trace and bounded diagnostic execution

Date: 2026-10-09

Status: Human approved the written spec with “确认设计文档” on 2026-10-09. Implementation-plan execution choice is pending; no diagnostic code or new native execution has been performed by this document alone.

## Purpose and evidence limit

Locate the actual awaited boundary in the existing Windows native managed-test-generation route, without changing product request semantics or confusing diagnostics with acceptance.

The accepted selected-toolchain correction is at `c6b66881b0ace85d3bd820a70fd52477f5cf1e81`. The original Windows W4 work remains dirty and incomplete. In the single post-correction attempt `win32-d84Sns`, ordinary build and discovery succeeded; baseline normalized/JUnit/HTML output contained lines 5/5 and branches 2/4. There was no observed formal coverage terminal, generation/candidate/apply/maintenance/Unity receipt, or captured natural terminal error. Session 19230 returned exit 1 and empty output concurrently with a controller-requested Ctrl-C. Causal origin and pre-interruption service liveness are not established.

Existing operation waiters cannot bound a blocked ordinary RPC: their deadlines begin after start calls return and are checked after awaited polling calls return. The client connection has no ordinary request timer. This explains a missing wait bound, not the unique root of the native obstruction. A trace must distinguish a call entered, a call returned, and a still-pending call; artifact creation alone cannot establish those states.

## Chosen approach and alternatives

Option 1, selected: add opt-in observation only in the native harness; execute one externally bounded diagnostic attempt. Delegate all real client operations unchanged. Find the actual boundary before deciding whether any product fix is warranted.

Option 2, not selected: introduce general RPC deadlines and cancellation in the production client. This involves transport, pending-request cleanup, task cancellation and service lifecycle behavior. It is outside this diagnostic correction and does not by itself explain the observed obstruction.

No visual companion is needed for this text-only scope decision. The intent, secret-handling constraints and acceptance distinction have already been established; the written-spec review is the next human gate.

## Scope

Exactly two existing source paths may change after written-spec and implementation-plan approval:

- `tools/service-probe/src/native-test-generation.ts`
- `tools/service-probe/src/native-test-generation.test.ts`

The design/plan and the controller-owned original W4 report/evidence may record the diagnostic outcome. Runtime logs remain ignored artifacts, not source or acceptance receipts.

Both source paths already contain uncommitted original W4 changes. Preserve those changes and their pre-diagnostic hashes; this diagnostic must not prematurely stage/commit the whole files and thereby consume the original W4 changes before its native gate. Diagnostic source commits are explicitly deferred to the original W4's conditional exact-path source commit. This exception to the usual per-subtask commit flow was included in the human-approved written spec. Before editing, retain each pre-edit file's raw Git blob identity and SHA-256; the task review must receive the actual before/after blob diff plus full two-file context and hashes, not a commit-only package that omits the dirty trace. Review approval is bound to those working-file hashes, not a clean commit. Design/plan document-only commits remain separate.

Do not change `test-generation-e2e.ts`, `probe.ts`, packages/test-client, protocol schemas, any Go code, registry/selection authority, CMake fixtures, recipes, manifests, bundle locks, public generation DTOs, or workflow files. Preserve all existing dirty W4 source and retained attempts. No dependency installation, new worktree, database/SQLite inspection, token access, network request, remote push, merge, Release or signing is included.

## Observation component

Use a harness-local trace context and a private client facade at the existing `inspectNativeFirstCoverageReport` setup boundary. A small named export for focused tests is acceptable; it is not a new package API, protocol route or receipt DTO.

Activation is exactly `UTIDE_NATIVE_OPERATION_TRACE=1`. Otherwise return the original client, create no observation timer and emit no new trace text. The flag does not activate existing raw service-debug output or alter its redaction/retention behavior.

Cache one facade per real client inside this trace context. The same client must return the same facade; an actual restarted client must return a different facade. Use the existing restart closure, not a new fixture, service or connection. Reuse this facade for profile selection, baseline coverage, the existing managed-generation callback, applied coverage, and the genuine maintenance reopen/rerun. Do not manufacture a fresh-client proof through a new wrapper around the same real client.

Preserve `withNativeFirstCoverageReport(root, platform, framework, inspect)` and its two-argument inspect callback. `executeNativeManagedGeneration` stays unchanged and receives the same observed client through its existing context. Existing request binding, selected-instance authority, compile/test/no-regression checks, managed confirmation, actual source edit and formal maintenance rerun remain load-bearing gates.

### Delegation invariants

- Invoke each original method exactly once, with the real client as receiver and the original argument objects unchanged.
- Return the original result, including the exact Promise object. Observe its settlement without returning a replacement Promise or awaiting inside the delegation wrapper.
- Preserve the exact synchronous thrown object and asynchronous rejection object; do not stringify, transform, retry or swallow product failures.
- Observation callbacks and sinks must not introduce thrown errors, rejected observer chains or altered method results. A trace-sink fault makes the diagnostic evidence inconclusive; it is not a product failure.
- Do not mutate the client or its prototype, replace a connection, add a request/deadline/cancellation timer, or perform extra client calls. Non-audited methods must still delegate correctly but produce no arbitrary method-name output.

## Trace contract

Write newline-delimited JSON to stderr with the fixed prefix `native-operation-trace: `. This stream is separate from the authoritative native report publisher.

Allowed fields are a closed set: local integer event/call/client sequences, monotonic elapsed milliseconds, fixed framework, fixed phase, fixed operation name, fixed event kind, and pending counts/sequences. Sequences are local diagnostics, never real request/run/report IDs. No argument, result, property value, error message/stack, path, endpoint, READY text, stdout body, credential, token, database value or arbitrary user string may be emitted. Even hostile sentinels in arguments/results/errors must be absent. Read no object payload solely to construct a trace.

Audited RPC method allowlist:

`inspectWorkspace`, `startCMakeBuild`, `getTask`, `discoverTests`, `getTestCatalog`, `startCoverage`, `getCoverageRun`, `getCoverageProject`, `listCoverageFiles`, `listCoverageFunctions`, `listCoverageLines`, `getCapabilities`, `startTestGeneration`, `getTestGenerationRun`, `getManagedReview`, `applyManagedReview`, `listManagedTests`, `listArtifacts`, `readArtifact`, `getCoverageReport`.

Fixed phases:

`service-start`, `profile-selection`, `baseline-coverage`, `managed-generation`, `applied-coverage`, `maintenance-edit`, `maintenance-reopen`, `maintained-coverage`, `evidence`, `dispose`.

Emit a phase event before the corresponding existing awaited phase. Emit `before` immediately before invoking an audited method, then `returned` or `threw` only on actual settlement. A synchronous throw is also `threw`. A pending request must never produce a fabricated terminal event. In particular, observe the call to `startTestGeneration` and calls to `getTestGenerationRun` inside the unchanged shared helper.

The setup and restart phase records may reveal that no observed ordinary RPC has yet begun. They do not claim to trace the private connection handshake or service internals. Do not add subscriptions or progress RPCs.

### Bounded history without hiding the pending operation

Limit detailed `phase`/`before`/`returned`/`threw` history to 4,096 records per framework trace context, with one fixed `history-budget-exhausted` record. Do not stop delegating calls when the history budget is reached.

Independently retain an aggregate of outstanding calls indexed by the fixed phase and audited method; each entry has an in-flight count and first/latest local call sequences. This finite key space, rather than an unbounded list of completed calls, retains every outstanding operation kind even after detail suppression. The current pipeline delegates RPCs serially, but overlapping test calls must not disappear from the aggregate. Client slots are local identifiers of the existing initial/reopened clients, not product identities.

Sequence bounds are explicitly cohort bounds: first/latest calls initiated during the group's nonempty in-flight epoch. They do not assert that both endpoint calls still remain unresolved. The count proves how many calls in that group remain outstanding; individual sequence settlement comes from the detailed events only. This avoids both false per-request attribution and an unbounded per-call list.

Every 10 seconds emit one `pending` snapshot only when an observed call remains unresolved, including the fixed current phase and the outstanding aggregate. Limit this channel to 128 snapshots per framework context; the 20-minute external run budget gives at most 120 periodic ticks. Emit a final `closed` snapshot when the trace context is disposed normally. Each trace line is bounded to 65,536 UTF-8 bytes; serialize only the closed schema and finite aggregate. If any diagnostic bound is unexpectedly exceeded, emit only a fixed `trace-inconclusive` marker when possible and report that limit, never fabricate a located root.

The observation heartbeat does not query the product or terminate a request. Unref it and clear it in the existing finally path so tracing cannot keep a finished CLI alive. If the external process is terminated, a normal final snapshot is not guaranteed; the last captured pending snapshot remains observational evidence only.

## External execution and stop budget

One distinct retained Windows diagnostic attempt, no automatic retry. Total execution budget is 1,200 seconds (20 minutes), beginning when the owned native Node process starts, including staging and service startup. It is intentionally a diagnostic cap, not a statement of the normal duration or a timeout increase for either framework. It may end before Unity or maintenance; missing results remain missing.

Before launching, record the executable path, process handle/PID and creation time of the owned native Node process. Use a hidden `Start-Process -PassThru` or equivalent owned-process launcher, with distinct redirected output files under `.native-e2e/artifacts/windows/`. No command-line secret dump. Supply the opt-in flag, the unchanged existing retention flag and the truthful dirty candidate SHA through process-local environment only. Do not change machine environment, execution policy, privileges or runner configuration.

Use the existing compiled native entry `tools/service-probe/dist/native-test-generation.js --platform win32` after its source has been compiled and reviewed. This entry still calls the existing two-framework acceptance route; do not invent a new generation path or bypass any route assertion. The currently rebuilt unchanged service may be reused after confirming its recorded source/executable identity. Rebuild only a changed/stale necessary dependency, recording any unavoidable build overlap; no extra baseline suite is implied by preparation.

While this attempt is running, obtain only the metadata needed to register a direct owned service child: parent PID lineage, executable path and creation time. Do not read its command line or credentials. At expiry, first record expiry and last safe trace; terminate only the still-matching recorded native Node process and, if needed, its positively identified direct service child. Revalidate PID/creation time/path immediately before any stop to avoid PID-reuse errors. Never kill by name, wildcard, broad ancestry or guessed PID. Allow at most 15 seconds for those identified processes' stop confirmation; if identity/stop confirmation is unavailable, report the cleanup limitation and escalate instead of broadening the kill. This is not proof that all Windows descendants are cleaned up.

Use waits of no more than 60 seconds per controller interaction. Keep the externally owned start/deadline/process identities and current phase in this plan's recovery ledger before the run; context recovery must not reset the deadline, relaunch the attempt or consume another agent's exec output.

Interpretation is explicit:

- `before`/`pending` without settlement locates an unresolved client/phase boundary, not a server root or product failure.
- A natural error/exit is recorded with the actual safe trace and original artifacts; no claim beyond captured evidence.
- Budget expiry or externally requested stop is diagnostic inconclusive, never native PASS or a natural test failure. Record observed exit separately from uncertain signal causality.
- Completed authentic receipts may support the existing dirty gate only after all its original assertions are met. They are not clean-source, Linux, hosted, packaging or whole-PR acceptance.

The controller must resolve a newly observed product boundary before permitting a product fix or another native attempt. None is implied by this diagnostic design.

## Focused verification and review

Use behavioral RED/GREEN only in the existing native helper test. Cover unresolved and settled calls, exact result/Promise/rejection/receiver/argument identity, single invocation, disabled silence, fixed allowlist, hostile-secret redaction, same-client caching and genuine restarted-client distinction, phase transitions, detail-budget suppression with retained outstanding evidence, periodic snapshots, and heartbeat disposal. Include a sink-failure case that preserves product behavior and reports the evidence limit. Do not use arbitrary sleeps to prove timer behavior; inject clock/sink scheduling at the diagnostic-only seam.

The implementation report must contain the actual commands, original outputs, exits, skips/warnings and source identities. A compiler/import failure is not behavioral RED. A reviewer independently checks the two-file diff and the privacy/delegation/budget/identity contracts before the one diagnostic execution. Re-review only changes implicated by findings; do not repeat unchanged generation, UI, SDK, recipe, full Go or global suites.

Do not run `pnpm verify`, the SDK race, a clean-SHA native attempt, hosted workflow or packaging as part of this diagnostic correction. Those original W4 gates remain conditional and open. No source trace is a replacement for actual generated tests, real coverage improvement, maintained-source reopen/rerun, CppUTest and Unity receipts, or the later cumulative task and whole-PR reviews.

## Plan ownership and return to W4

The forthcoming diagnostic implementation plan owns its own SDD workspace, ledger, brief, report and review package. It must not read/write the sibling selected-toolchain correction workspace or commandeer the original W4 workspace. The controller supplies accepted interfaces and preserved hashes in its brief. Only the controller switches ownership back to the original Windows plan after the diagnostic disposition, then appends the real observation/remaining blocker there. Original W1/W2/W3 are complete and must not be re-dispatched.

References:

- [Original Windows plan](../plans/2026-10-08-windows-cmake-coverage-compatibility.md)
- [Original Windows design](2026-10-08-windows-cmake-coverage-compatibility-design.md)
- [Accepted selected-toolchain identity design](2026-10-09-selected-toolchain-identity-contract-design.md)

## Completion criteria

Diagnostic correction is ready for the one bounded attempt only when focused behavioral tests and independent review approve the trace contracts. Diagnostic disposition is complete when that single attempt's observed boundary or evidence limit, actual deadline/exit/cancellation/cleanup status and retained artifact locations are recorded accurately. A precise observed pending method is sufficient to improve diagnosis, not sufficient to assert the underlying product is fixed. If the trace still cannot identify a boundary, stop and report that limitation without another blind retry.
