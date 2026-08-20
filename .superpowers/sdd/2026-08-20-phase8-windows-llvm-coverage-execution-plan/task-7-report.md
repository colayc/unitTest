# Phase 8 Task 7 Report

## Status and summary

Implemented `coverageexec.Coordinator` as the sole live owner of one CoverageRun, its one existing Task, one embedded TestRun, and at most one final CoverageReport. The coordinator constructs the closed execution plan, retains the native execution boundary, revalidates all identities and trust/cancellation state at continuations, drives the embedded test/profile/LLVM/report pipeline, and projects exactly one terminal aggregate.

The permitted phase order is:

1. `coverage-configure`
2. `coverage-build`
3. one or more `coverage-test` waves
4. `coverage-merge`
5. `coverage-normalize`
6. `coverage-report` action
7. `coverage-publish` action

Runtime construction and resume wiring remain Phase 8 Task 8 as specified by the execution plan.

## TDD RED evidence

The repository's default Go build cache was not readable in this Windows environment (`C:\Users\DELL\AppData\Local\go-build` ACL). The exact requested command was attempted first and failed before package compilation for that environmental reason. All meaningful RED/GREEN runs therefore set `GOENV=off`, `GOTOOLCHAIN=local`, and a worktree or `%TEMP%` `GOCACHE`.

### Coordinator and closed pipeline RED

Command:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; $env:GOCACHE=(Join-Path (Get-Location) '.gocache-task7'); & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coverageexec ./apps/test-service/internal/coveragerun -run 'Coordinator|Planner|Completion|Outcome|Boundary' -count=1
```

Observed failures before implementation:

```text
undefined: retainExecutionRoot
undefined: projectCoverageOutcome
undefined: NewCoordinator
undefined: Config
undefined: Coordinator
undefined: rewriteBuildPlan
merge failure got profile_collection_failed; want merge_failed
FAIL
```

### Coverage artifact ownership RED

Command:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; $env:GOCACHE=(Join-Path (Get-Location) '.gocache-task7'); & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/task -run 'CoverageTaskArtifactsAreOwnedOnlyByCompletionPreparer' -count=1
```

Observed failure:

```text
generic manager committed coverage JSON artifacts: [execution-plan]
FAIL
```

### Closed report replay RED

The first available-completion test failed with:

```text
invalid coverage report set: summary or source snapshots
```

The cause was `cloneSet` collapsing a canonical non-nil empty `Sources` slice to nil. The implementation now preserves that distinction, with a direct replay regression test.

### Fault-injection RED found during test construction

The initial replaced-root test could not rename the retained directory on Windows because `os.Open` did not grant delete sharing. The retained Windows handle now uses `FILE_SHARE_DELETE`; the test can replace the path and proves cleanup refuses to follow it.

## GREEN verification

All commands below were run after the final source changes, using new `%TEMP%` cache directories.

### Full coordinator/store/task/artifact and retained component gate

Command:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; $env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-final'); & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coverageexec ./apps/test-service/internal/coveragerun ./apps/test-service/internal/coveragedomain ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/coverageparser/llvm ./apps/test-service/internal/coveragenormalize ./apps/test-service/internal/coveragereport ./apps/test-service/internal/build ./apps/test-service/internal/testrun ./apps/test-service/internal/task ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore -count=1
```

Output:

```text
ok  unit-test-ide.local/test-service/internal/coverageexec        0.540s
ok  unit-test-ide.local/test-service/internal/coveragerun         0.253s
ok  unit-test-ide.local/test-service/internal/coveragedomain      0.401s
ok  unit-test-ide.local/test-service/internal/coveragellvm        1.406s
ok  unit-test-ide.local/test-service/internal/coverageparser/llvm 0.252s
ok  unit-test-ide.local/test-service/internal/coveragenormalize   0.351s
ok  unit-test-ide.local/test-service/internal/coveragereport      0.337s
ok  unit-test-ide.local/test-service/internal/build               0.968s
ok  unit-test-ide.local/test-service/internal/testrun             0.409s
ok  unit-test-ide.local/test-service/internal/task                0.899s
ok  unit-test-ide.local/test-service/internal/taskstore           11.839s
ok  unit-test-ide.local/test-service/internal/artifactstore       1.124s
```

### Race gate

Command:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; $env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-final-race'); & 'D:\Program Files\Go\bin\go.exe' test -race ./apps/test-service/internal/coverageexec ./apps/test-service/internal/coveragerun ./apps/test-service/internal/task ./apps/test-service/internal/taskstore -run 'Coverage|Coordinator|Completion|Cancel|Restart' -count=1
```

Output:

```text
ok  unit-test-ide.local/test-service/internal/coverageexec 1.722s
ok  unit-test-ide.local/test-service/internal/coveragerun  1.125s
ok  unit-test-ide.local/test-service/internal/task         1.300s
ok  unit-test-ide.local/test-service/internal/taskstore    59.867s
```

### Focused fault, cancellation, replay, and ownership gate

Command groups:

```powershell
& 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/taskstore -run 'CoverageCompletionFaults|CoverageTerminalReplay|RecoverInterruptedCoverage|CoverageCompletionPersists|CoverageCompletionRejects|CoverageArtifactContract' -count=1
& 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/task -run 'ManagerCancellationPreventsInterpreterAndContinuation|ManagerResultOutputFailureTerminatesBeforeDomainCompletion|CoverageTaskArtifacts|ServiceAction|Cancel|Restart|Panic' -count=1
& 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/artifactstore ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/build -run 'Coverage|Cancellation|Replaced|Replacement|Release|Close' -count=1
```

Output:

```text
ok  unit-test-ide.local/test-service/internal/taskstore     3.697s
ok  unit-test-ide.local/test-service/internal/task          0.191s
ok  unit-test-ide.local/test-service/internal/artifactstore 0.205s
ok  unit-test-ide.local/test-service/internal/coveragellvm   0.162s
ok  unit-test-ide.local/test-service/internal/build          0.420s
```

### Static and patch verification

Command:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; $env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-final-vet'); & 'D:\Program Files\Go\bin\go.exe' vet ./apps/test-service/internal/coverageexec ./apps/test-service/internal/coveragerun ./apps/test-service/internal/coveragereport ./apps/test-service/internal/task; git diff --check
```

Output: no output; exit code 0.

## Requirement evidence

### Phase and continuation evidence

- Planner tests admit only configure/build, embedded test wave(s), merge, normalize, report, publish in that order and reject missing or unexpected retained build phases.
- Every process continuation passes through the coordinator validation path, which reloads Task/CoverageRun/TestRun/profile/catalog and rechecks workspace generation, toolchain identity/version, instrumentation identity, retained targets, and current trust/cancellation state.
- Runtime process specifications retain executable, argv, environment, unset variables, and cwd privately. Public task commands carry scrubbed phase labels only.
- Process authorization binds executable, every argument, environment entry, unset entry, and directory; mutation of any bound field is rejected.

### Outcome evidence

- Assertion failure closes the embedded TestRun as failed but retains collectable coverage and proceeds to an available report.
- Normal-success missing profile is unavailable infrastructure failure.
- Crash, timeout, or failed invocation can be exact partial coverage through the profile manifest; missing evidence is recorded as partial reason and is not converted into uncovered lines.
- Merge failures project the exact closed reason `merge_failed`; instrumentation, profile collection, normalization, report generation, publication, cancellation, timeout, trust loss, and restart use closed reason mappings.
- Unavailable/cancelled completion produces no CoverageReport and no Coverage JSON/JUnit/HTML public artifacts.

### Terminal completion and replay evidence

- Available/partial completion validates the immutable `coveragereport.Set` and its domain graph before staging exactly `coverage-json`, `coverage-junit`, and `coverage-html`, in that order.
- Blob validation/staging completes before the single SQLite terminal aggregate mutation; broker publication follows the successful store commit.
- The embedded TestRun terminal event is deferred into the same aggregate completion.
- Duplicate completion with the same terminal aggregate is idempotent; conflicting replay fails closed.
- `TestCoordinatorUnsupportedCompletesOneRealSQLiteAggregate` integrates a real SQLite store, real ArtifactStore, and real Task Manager and proves exactly one existing Task, one TestRun, one CoverageRun, no report, exact `instrumentation_failed`, only stdout/stderr/diagnostics generic artifacts, and one terminal event per aggregate member.

### Fault, cancellation, and ownership evidence

- Replacement fault injection proves root cleanup validates the retained directory identity and does not follow a replaced native path.
- Isolated instrumentation/profile/build roots prevent instrumentation setup from receiving a non-empty owner root.
- Delegate, adapter, retained build handles, manifest/profile handles, and execution root use once-only cleanup paths across normal, error, duplicate resume, and Close paths.
- Duplicate live `Resume` returns the one existing Task and does not create nested Tasks or another live execution owner.
- Existing Task Manager cancellation/restart/panic tests plus the coordinator continuation barriers prove a stopped or invalidated run cannot advance into later phases or publication.
- Generic Task artifact finalization for CoverageRun is limited to stdout/stderr/diagnostics. The completion preparer is the only owner allowed to stage the three public CoverageReport blobs.
- Protocol/domain/report objects are built from stable IDs, normalized source identities, and report bytes; native executable/path/argv/env/cwd/token/profile/profdata/export names remain inside the private execution boundary.

## Files changed

- Added `apps/test-service/internal/coverageexec/model.go`
- Added `apps/test-service/internal/coverageexec/planner.go` and planner tests
- Added `apps/test-service/internal/coverageexec/boundary.go`, platform retained-handle files, and boundary tests
- Added `apps/test-service/internal/coverageexec/coordinator.go` and coordinator tests
- Added `apps/test-service/internal/coverageexec/completion.go` and completion tests
- Updated `apps/test-service/internal/coveragerun/state.go` and tests for exact merge failure projection
- Updated `apps/test-service/internal/coveragereport/report.go` and tests to preserve canonical empty source sets
- Updated `apps/test-service/internal/task/manager_artifacts.go`
- Added `apps/test-service/internal/task/manager_coverage_artifacts_internal_test.go`

## Self-review

- Re-read the Task 7 brief against the final diff and checked the public interface/configuration names and closed phase/reason values.
- Corrected a review-found isolation regression: the coverage adapter receives the empty `instrumentation` subdirectory, not the populated overall execution root, preserving `coveragellvm.WriteInstrumentation`'s empty-root precondition.
- Corrected a late source-consistency validation placement error and used semantic slice equality so nil/empty request source lists do not create a false mismatch while the immutable report representation still preserves its canonical non-nil empty set.
- Confirmed no worktree Go cache or native execution byproduct is included in the change.
- Final full, race, focused fault/replay/cancel, vet, and diff checks were rerun after these corrections.

## Deferred ledger status

- **Task 1 — stronger task-layer finished aggregate validation:** closed for the Task 7 evidence boundary by existing taskstore atomic/replay tests plus the new real SQLite/ArtifactStore/Task Manager unsupported completion integration and CoverageRun artifact-ownership test.
- **Task 1 — direct successful/action-error continuations:** action error is exercised end-to-end by the real unsupported completion; success is exercised through planner, report, completion, and Task action tests. A fully runtime-constructed normal-success Coordinator is deliberately Task 8 wiring.
- **Task 5 — cancellation-specific allocator/root/profile release once:** root/delegate/adapter close-once and replacement are directly covered here; Task Manager, LLVM, build, artifact cancellation/release tests are included in the focused gate. A single concrete-adapter live cancellation test belongs with Task 8 construction/shutdown wiring.

## Concerns and exact evidence boundary

- Task 8 must construct the concrete adapter/toolset/profile/source/report dependencies, register resume, and order runtime shutdown. `Coordinator.Close` invalidates and releases retained coordinator resources; active process-tree cancellation is owned by Task Manager shutdown in that runtime sequence.
- Task 7 does not invoke a host-installed `clang-cl`, `llvm-profdata`, and `llvm-cov` process tree. The phase engine, target authorization, parser/normalizer/report integration, terminal store/artifact mutation, faults, replay, and ownership are covered with bounded adapters plus the real unsupported SQLite completion. The concrete normal-success native integration is the Task 8 boundary.
- The machine's default Go cache ACL is unusable from this environment, so verification requires an explicit writable `GOCACHE`; this is environmental and does not affect repository behavior.

---

## Fix Round 1 (2026-08-20)

This section supersedes the original report wherever the two conflict. In particular, a CoverageRun now publishes no generic stdout/stderr/diagnostic artifacts, and `Coordinator.Close` now owns active Task cancellation and bounded waiting before releasing retained execution resources.

### Summary

All seven blocking findings from round 1 were addressed on top of `125554e`:

1. Coverage process output is private to the bounded `ResultOutputObserver`; Task Manager never sends it to the generic artifact sink, output buffers, or diagnostic conversion. ArtifactStore independently discards CoverageRun output/diagnostic appends and finalizes only a validated three-blob report set.
2. `coveragellvm.InstrumentationFingerprint()` is the deterministic retained instrumentation contract used by `WriteInstrumentation`, the Windows Phase 7 producer, and the coordinator. Toolchain identity/version remain separately retained and revalidated.
3. `PrepareCompletion` freezes the first successful terminal request and deep-cloned completion. Identical replay returns the same IDs/graph without staging blobs or calling the ID generator; changed terminal input returns `task.ErrConflict`.
4. Every preparation error runs a minimal real Manager action to one exact unavailable aggregate. A failed report blob stage aborts the old sink, creates a clean sink, and atomically terminalizes as `persistence_failed` without report rows, public artifacts, broker report events, or committed partial blobs.
5. Crash classification is platform-specific: Windows recognizes high-bit NTSTATUS values such as positive amd64 `0xC0000005`; non-Windows retains negative signal semantics; timeout takes precedence.
6. A Windows integration harness now uses real temporary SQLite/ArtifactStore/Task Manager, retained fake LLVM executables, real instrumentation/profile allocation and sealing, and a fake process factory for the complete phase/fault matrix.
7. `Coordinator.Close` closes admission, shares one result across concurrent callers, waits for preparation, cancels active Tasks, waits boundedly for terminal store state, and only then releases each execution once.

### RED evidence

The regressions were observed failing against `125554e` before production changes.

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/task -run TestCoverageProcessOutputIsPrivateToResultObserver -count=1 -v
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/artifactstore -run TestCoverageArtifactSinkNeverPersistsRawProcessOutputOrDiagnostics -count=1 -v
```

Output: the first test found the raw sentinel in the generic sink/output buffer; the second finalized three generic artifacts. Both packages ended `FAIL`.

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/runtime ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/coverageexec -run 'InstrumentationContract|CompletionCommitsClosedReportSet' -count=1 -v
```

Output: the runtime producer fingerprint differed from retained instrumentation; a second coordinator completion generated fresh IDs and staged all three blobs again. Command ended `FAIL`.

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coverageexec -run 'PreparationFailureCompletesRealAggregate|AdapterPreparationFailureCompletesRealAggregate|CoverageBlobStageFailureFallsBackToOneUnavailableRealAggregate' -count=1 -v
```

Output: preparation errors returned directly with queued rows; each blob error left a running Task and unhealthy Manager instead of one unavailable aggregate. Command ended `FAIL`.

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coverageexec -run 'InvocationOutcomeClassifiesPlatformCrash|CoordinatorCloseCancelsWaitsThenReleasesActiveExecutionOnce' -count=1 -v
```

Output included `0xC0000005 was classified as an ordinary failure` and `coordinator_test.go:102: Close did not cancel the active Task`; command ended `FAIL`.

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coverageexec -run 'CoordinatorRealManager|CoordinatorAssertion|CoordinatorMissingProfile|CoordinatorReportAndPublish|CoordinatorEveryPreparation|CoordinatorCloseCancelsReal' -count=1 -v
```

Initial RED exposed inherited Windows ACLs rejecting the real retained instrumentation root and normal-success missing-profile evidence not reaching the TestRun terminal boundary. A test-only assertion classifier issue was also corrected. The remaining production defects were then fixed and the suite became GREEN.

### Final GREEN verification

Focused fault/replay/ownership integration:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; $env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix1-fault-replay'); & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/coverageexec ./apps/test-service/internal/task ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore ./apps/test-service/internal/coveragellvm -run 'Test(CoordinatorRealManagerRunsExactPrivateCoveragePhaseSequence|CoordinatorAssertionFailureContinuesToAvailableRealAggregate|CoordinatorMissingProfileOutcomesUseExactRealInvocationEvidence|CoordinatorReportAndPublishRevalidationFailureNeverPublishesRealAggregate|CoordinatorEveryPreparationBoundaryGetsOneUnavailableRealAggregate|CoordinatorCloseCancelsRealActiveManagerProcessBeforeReleasing|CoverageBlobStageFailureFallsBackToOneUnavailableRealAggregate|CompletionCommitsClosedReportSetBeforeReportBearingGraph|CoverageProcessOutputIsPrivateToResultObserver|CoverageTaskArtifactsAreOwnedOnlyByCompletionPreparer|CoverageCompletionFaultsRollBackEveryTerminalRow|CoverageTerminalReplayIsIdempotentAndImmutable|CoverageArtifactSinkNeverPersistsRawProcessOutputOrDiagnostics|ExecutionRootCleanupDoesNotFollowReplacedPath|ExecutionBoundaryReleasesDelegateAdapterAndRootExactlyOnce|LLVMToolsetSingleOwnerClaimRollsBackAndClosesExactlyOnce|LLVMToolsetRetainsSameInstallationAndClosesExactlyOnce|ProfileAllocatorEnforcesUniqueConcurrentCapacity|SealedProfileManifestRejectsReplacement)$' -count=1 -v
```

Output: exit 0 in 29.7s; `coverageexec 5.321s`, `task 0.151s`, `taskstore 0.627s`, `artifactstore 0.142s`, `coveragellvm 0.523s`. This includes 3 blob faults, 9 preparation boundaries, 4 missing-profile cases, 2 late revalidations, complete phases, active Close, replay, privacy and release-once tests.

Fresh race gate:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; $env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix1-final-race'); & 'D:\Program Files\Go\bin\go.exe' test -race ./apps/test-service/internal/coverageexec ./apps/test-service/internal/coveragerun ./apps/test-service/internal/task ./apps/test-service/internal/taskstore -run 'Coverage|Coordinator|Completion|Cancel|Restart|Orchestration|Preparation' -count=1
```

```text
ok  unit-test-ide.local/test-service/internal/coverageexec 19.074s
ok  unit-test-ide.local/test-service/internal/coveragerun   1.131s
ok  unit-test-ide.local/test-service/internal/task          1.305s
ok  unit-test-ide.local/test-service/internal/taskstore    61.229s
```

Full service gate:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; $env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix1-final-full'); & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/... -count=1
```

Output: exit 0 in 69.2s; every test-bearing package was `ok`, including `coverageexec 13.326s`, `taskstore 19.635s`, `processcontrol 35.574s`, `runtime 4.356s`, and `toolchain 16.490s`.

Static and patch verification:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; $env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix1-final-vet'); & 'D:\Program Files\Go\bin\go.exe' vet ./apps/test-service/internal/coverageexec ./apps/test-service/internal/coveragerun ./apps/test-service/internal/task ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/runtime
git diff --check
```

Output: both commands produced no diagnostics and exited 0.

### Phase/outcome/fault/cancel/replay/ownership evidence

- Real process order is exactly `configure, build, test, merge, normalize, report, publish`. Raw sentinels with a native drive path, profile, environment/token-like text and export output are absent from artifacts and events on success, configure/build/test/normalize output, failure and cancellation.
- Assertion exit 1 produces a failed TestRun while coverage continues to an `available` CoverageRun and succeeded Task.
- Missing evidence is exact: access violation becomes `partial/crashed`, child timeout `partial/timed_out`, ordinary failed invocation `partial/missing_failed_invocation`, and normal exit 0 unavailable `profile_collection_failed`; missing evidence is never uncovered lines.
- Report/publish revalidation faults produce unavailable `report_generation_failed`/`persistence_failed`, no report ID/artifacts/report-available event. The nine preparation boundaries produce exactly one terminal aggregate, zero processes and zero public artifacts.
- Each report blob failure aborts partial staging and commits one unavailable graph. The SQLite fault matrix proves rollback at final event, artifact metadata/link, TestRun, report and CoverageRun; broker publication follows commit.
- Concurrent duplicate Resume/two concurrent Close calls around blocked configure cause one termination, no later phase, one adapter/allocator/root cleanup and no public artifact.
- Identical coordinator replay returns the frozen completion and IDs with zero new staging/ID calls; changed terminal input conflicts. SQLite replay is also immutable/idempotent.
- Protected owner-only Windows execution directories satisfy retained instrumentation. Root cleanup does not follow replacement; toolset, profile manifest/allocator and boundary handles close exactly once on the tested success, error, cancel, duplicate and concurrent Close paths.

### Files changed

- Task/ArtifactStore: `task/manager.go`, `task/manager_execution.go`, `task/manager_coverage_artifacts_internal_test.go`, `artifactstore/task_sink.go`, `artifactstore/task_sink_test.go`.
- Fingerprint producer: `coveragellvm/instrumentation.go`, `coveragellvm/instrumentation_test.go`, `runtime/coverage_backend.go`, `runtime/coverage_backend_test.go`.
- Coordinator: `coverageexec/model.go`, `coordinator.go`, `coordinator_test.go`, `completion.go`, `completion_test.go`, plus new `crash_{windows,nonwindows}.go`, `crash_test.go`, `execution_directory_{windows,nonwindows}.go`, `failure_integration_test.go`, and `orchestration_windows_test.go`.

### Self-review

- Re-read the brief and all seven findings against the final production diff after fresh gates.
- Verified privacy at both owners: Manager never calls a generic sink for CoverageRun output and ArtifactStore cannot finalize generic CoverageRun output/diagnostics.
- Verified the shared fingerprint is instrumentation version plus exact include digest; toolchain ID/version/toolset identity remain separate validations.
- Verified caching occurs only after all three blobs succeed, clones pointer/slice payloads, and compares the full terminal request. A failed stage is not cached and can use the clean unavailable fallback.
- Verified fallback aborts the old sink before a clean sink and recomputes Task transition, step mutation, domain completion and terminal transaction.
- Verified active Close never releases an execution before its Task is finished; timeout retains ownership and returns an error.
- Verified no cache, SQLite, artifact, LLVM fixture, profile/profdata or execution-root byproduct is in the patch.

### Deferred ledger status

- **Task 1 stronger task-layer finished aggregate validation: remains open.** This round adds real coordinator/Manager/SQLite graphs and uses the existing transaction rollback matrix, but adds no stronger standalone Task-layer validator.
- **Task 1 direct successful/action-error continuations: closed for Task 7.** The real harness executes every successful continuation through publish; report/publish continuation errors terminalize exactly without later publication or nested Tasks.
- **Task 5 cancellation-specific allocator/root/profile release-once: closed for Task 7.** The real active-process test asserts one termination, adapter/allocator close once, root removal, no later phase and no artifact.

### Concerns and evidence boundary

- Task 8 must adapt concrete `build.Coordinator` to the widened `coverageexec.BuildPreparer` return interface; `*build.PreparedPlan` already implements `PreparedBuild`, but Go return types are invariant, so a thin adapter is required.
- The harness retains real temporary fake LLVM executable files and real LLVM toolset/profile/instrumentation ownership, but does not execute host-installed LLVM tools; process behavior is supplied by the bounded fake factory.
- CommitBlob fault injection and SQLite rollback are covered. A host filesystem failure partway through final artifact renames remains governed by ArtifactStore recovery/cleanup, not a new Task 7 fault hook.
- Final implementation commit subject is `fix: harden coverage execution ownership`; the final ID is in the agent handoff because a commit cannot contain its own hash.

---

## Fix Round 2 (2026-08-20)

This section supersedes the round-1 evidence boundary for filesystem publication,
terminal SQLite failure/recovery, the real process fault matrix, continuation
revalidation, and cancellation after profile sealing.

### Summary

- The real temporary SQLite + ArtifactStore + Task Manager harness now injects
  per-phase process results and output. It covers configure/build failure, merge
  failure, malformed LLVM export, normalization failure, renderer failure, a
  Task timeout distinct from child timeout, and all requested retained-state
  mutations.
- The coordinator has an injectable report renderer whose production default is
  `coveragereport.Render`; this makes the report action itself fault-testable
  without bypassing Manager orchestration.
- ArtifactStore rolls back report files already published when a later report
  file cannot finalize. Rollback reopens the pinned parent, validates canonical
  metadata, file identity, size and SHA-256, refuses links/replacements, removes
  in reverse order, and syncs the directory.
- A finalized CoverageRun sink remains privately owned until the SQLite terminal
  mutation commits. On transaction failure, Manager calls the exact sink's
  verified rollback before quiescing; no artifact metadata or broker terminal
  event becomes visible. Real `RecoverInterrupted` then commits exactly one
  interrupted TestRun/CoverageRun/Task graph.
- If report-file finalization itself fails, Manager discards the frozen
  report-bearing completion, opens a clean sink, and prepares one exact
  `infrastructure_failed` / `unavailable,persistence_failed` completion. No
  report row, public artifact metadata, regular report file, or report-available
  event survives.
- Cancellation after the real allocator has allocated and `SealProfiles` has
  retained a manifest proves the manifest, allocator, adapter, retained root,
  and process tree are released exactly once, even across duplicate coordinator
  `Close` calls.

### RED evidence

ArtifactStore initially left the first published report file behind when the
second publication failed:

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache-task7-fix2'); go test ./apps/test-service/internal/artifactstore -run TestCoverageArtifactSinkRollsBackPublishedReportFilesWhenFinalizationFailsPartway -count=1 -v
```

Output: `FAIL`; the assertion reported one remaining `.coverage.html` file.

The real phase matrix initially could not inject the report action:

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache-task7-fix2'); go test ./apps/test-service/internal/coverageexec -run TestCoordinatorRealManagerTerminalizesExactPhaseFaultMatrix -count=1 -v
```

Output: compile failure, `Config.RenderReport undefined`.

The first post-seal cancellation run exposed an incomplete embedded-run test
double rather than a production coordinator failure:

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache-task7-fix2'); go test ./apps/test-service/internal/coverageexec -run TestCoordinatorCancellationAfterProfileSealingReleasesRealManifestTreeOnce -count=1 -v
```

Output: `FAIL`; the fake accepted only `task.OutcomeSucceeded`, so cancellation
was misprojected as Task `infrastructure_failed` and CoverageRun `merge_failed`.
The fake was aligned with the production embedded contract's cancelled,
timed-out, interrupted, command-failed and infrastructure-failed mappings.

The full persistence RED reproduced both blocking defects:

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache-task7-fix2'); go test ./apps/test-service/internal/coverageexec -run 'TestCoordinator(RealArtifactFinalizationFailureRollsBackAndTerminalizesUnavailable|TerminalSQLiteFailureKeepsBrokerInvisibleAndRecoversExactly)' -count=1 -v
```

Output: `FAIL`. The filesystem-finalization case left the Task running at
`coverage-publish` with an unhealthy Manager; the terminal SQLite failure left
all three report files on disk even though the transaction and broker terminal
publication had not committed.

The first fresh full-service verification also caught two load-sensitive test
timing defects: the 50 ms Task timeout could fire before configure started, and
the test could observe the terminal row just before asynchronous process-close
removed the execution root. The regression now uses a 2 second Task timeout and
condition-based close/root waiting. Three consecutive focused runs passed before
the full gate was repeated.

### Final GREEN verification

Fresh real fault/cancel/revalidation/recovery matrix:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix2-final'); go test ./apps/test-service/internal/coverageexec -run 'TestCoordinator(RealManagerTerminalizesExactPhaseFaultMatrix|TaskTimeoutStopsCurrentTreeBeforeLaterPhase|RevalidatesEveryRetainedBoundaryBeforeContinuation|CancellationAfterProfileSealingReleasesRealManifestTreeOnce|RealArtifactFinalizationFailureRollsBackAndTerminalizesUnavailable|TerminalSQLiteFailureKeepsBrokerInvisibleAndRecoversExactly|AssertionFailureContinuesToAvailableRealAggregate|MissingProfileOutcomesUseExactRealInvocationEvidence)' -count=1 -v
```

Output: exit 0; all listed tests and subtests passed; package
`coverageexec` completed in `6.173s`. This includes six injected phase faults,
six retained-boundary mutations, four missing-profile classifications, Task
timeout, assertion continuation, post-seal cancellation, filesystem rollback,
and SQLite recovery.

Fresh full service gate after timing stabilization:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix2-final'); go test ./apps/test-service/... -count=1
```

Output: exit 0 in `35.9s`; every test-bearing package was `ok`, including
`coverageexec 19.246s`, `artifactstore 1.372s`, `task 1.009s`,
`taskstore 16.866s`, `runtime 4.017s`, and `processcontrol 31.695s`.

Fresh race gate:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix2-final-race'); go test -race ./apps/test-service/internal/coverageexec ./apps/test-service/internal/task ./apps/test-service/internal/artifactstore ./apps/test-service/internal/taskstore -count=1
```

```text
ok  unit-test-ide.local/test-service/internal/coverageexec  32.647s
ok  unit-test-ide.local/test-service/internal/task           2.141s
ok  unit-test-ide.local/test-service/internal/artifactstore  1.872s
ok  unit-test-ide.local/test-service/internal/taskstore     80.958s
```

Fresh static and patch gates:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix2-final'); go vet ./apps/test-service/...
git diff --check
```

Output: both exited 0 with no diagnostics.

### Phase, outcome, fault, cancel, replay and ownership evidence

- Configure exit 1 stops at configure and yields
  `infrastructure_failed/instrumentation_failed`; build exit 1 stops at build and
  yields `command_failed/build_failed`; merge exit 1 stops before export and
  yields `infrastructure_failed/merge_failed`.
- Malformed LLVM JSON and a valid export with duplicate physical source identity
  independently exercise parser and normalizer failures. Both stop before the
  report action and yield `normalization_failed`.
- Renderer failure executes the real report action and leaves publish unstarted,
  yielding `report_generation_failed` with no report/public artifact.
- Task timeout terminates blocked configure once and never starts build/test/
  merge/export/report/publish. Child timeout remains separately covered as exact
  partial coverage evidence.
- Before continuation, mutations of workspace generation, catalog revision,
  coverage profile, instrumentation fingerprint, retained binary identity, and
  workspace trust all stop the tree with the phase-appropriate exact unavailable
  reason. Toolchain identity/version mutation remains covered by the round-1
  real harness.
- Every fault asserts one terminal Task/TestRun/CoverageRun aggregate, no report
  ID, no public artifact metadata, no report-available event, no later process,
  and absence of raw native-path/token/profile sentinels from durable events and
  artifacts.
- The filesystem-finalization test creates a real second-file collision after
  the first report file publishes. ArtifactStore removes the first file; Manager
  retries only an unavailable completion on a clean sink; the final artifact
  tree contains no regular file for the CoverageRun.
- The SQLite test injects failure at `Mutation.FinishCoverage`. Before recovery,
  Task and CoverageRun remain nonterminal, the broker has zero TestRun/CoverageRun/
  report/Task terminal events, SQLite has zero CoverageRun artifacts, and the
  CoverageRun artifact directory has zero regular files. `RecoverInterrupted`
  then returns exactly TestRun-finished, CoverageRun-finished, Task-finished and
  stores `interrupted`, `unavailable/service_restarted`, and `interrupted`.
- Post-seal cancellation observes one sealed live manifest before cancel, one
  process termination, exact `cancelled/user_cancelled`, a closed manifest,
  adapter/allocator close count one, removed retained execution root, no later
  phase/public artifact, and unchanged counts after duplicate `Close`.
- Round-1 coordinator and SQLite completion replay tests remain in the full and
  race gates. The new rollback path discards a report-bearing cached completion
  before preparing the unavailable graph, so no stale report IDs/blobs can be
  replayed after persistence failure.

### Files changed in fix round 2

- `apps/test-service/internal/coverageexec/orchestration_faults_windows_test.go`
- `apps/test-service/internal/coverageexec/orchestration_windows_test.go`
- `apps/test-service/internal/coverageexec/coordinator_test.go`
- `apps/test-service/internal/coverageexec/model.go`
- `apps/test-service/internal/coverageexec/coordinator.go`
- `apps/test-service/internal/coverageexec/completion.go`
- `apps/test-service/internal/task/ports.go`
- `apps/test-service/internal/task/manager_artifacts.go`
- `apps/test-service/internal/task/manager_execution.go`
- `apps/test-service/internal/artifactstore/store.go`
- `apps/test-service/internal/artifactstore/task_sink.go`
- `apps/test-service/internal/artifactstore/task_sink_test.go`

### Self-review

- Re-read the round-2 findings against the production diff and the final test
  matrix. All requested failure boundaries are driven through real Manager,
  SQLite and ArtifactStore owners; no test calls `failureReason` directly as its
  orchestration evidence.
- Verified rollback is CoverageRun-only. A review run caught an early version
  incorrectly applying rollback to non-coverage terminal conflicts; the helper
  was scoped back to CoverageRun and the three affected Manager conflict tests
  plus the full task suite passed.
- Verified Manager retains the finalized sink only until `Store.Apply` returns,
  clears it on commit, and rolls it back before tripping storage on failure.
  Broker publication remains after the successful SQLite mutation.
- Verified the finalization-failure retry cannot reuse the frozen available
  completion. `DiscardPreparedCompletion` clears the cached graph under the same
  lock order and pins the failure projection to publish/persistence.
- Verified rollback never follows a replaced path: it revalidates the pinned
  parent and exact regular-file identity and content before removal. A hostile
  replacement is refused rather than followed.
- Verified the real manifest cancellation assertion waits for asynchronous
  process-close/boundary release by state, not by assuming the terminal row and
  cleanup occur simultaneously.
- Confirmed no Go cache, temporary SQLite database, report file, profile,
  profdata, LLVM fixture, or execution-root byproduct is in the worktree diff.

### Deferred ledger status

- **Task 1 — stronger task-layer finished aggregate validation: remains open.**
  This round adds real aggregate and recovery evidence but no new standalone
  stronger Task-layer aggregate validator.
- **Task 1 — direct successful/action-error continuations: remains closed for
  Task 7.** Normal success runs all continuations to publish; renderer failure
  exercises the real action-error path and proves publish is not started.
- **Task 5 — cancellation-specific allocator/root/profile release-once: closed
  by new direct evidence.** Unlike round 1's configure-time cancellation, this
  round cancels only after real profile allocation and manifest sealing, then
  verifies manifest/allocator/adapter/root/process ownership exactly once.

### Concerns and evidence boundary

- The process factory is injectable and drives real Manager orchestration but
  does not launch host-installed LLVM binaries. Concrete host process wiring
  remains outside this hermetic fault matrix.
- `FinalizedArtifactRollback` is an optional sink capability because generic
  Task sinks retain their existing lifecycle. A report-bearing CoverageRun sink
  that omits it fails closed; the production ArtifactStore implements it and has
  compile-time and real-stack coverage.
- No blocking concern remains for Task 8's coordinator contract. The open Task 1
  standalone validator ledger item is unchanged and is not claimed by these
  integration tests.

### Commit

- Fix-round-2 implementation and verification evidence: `c1f912c` —
  `fix: complete coverage execution fault matrix`.
- This commit identifier was recorded in a report-only follow-up commit; that
  follow-up identifier is supplied in the agent handoff because a commit cannot
  contain its own final hash.

## Fix Round 3 — retained rollback identity, real SQLite fault, exact terminal graph, root replacement

### Summary

- Coverage report finalization now retains the original parent-directory and
  file handles plus their identities until the SQLite terminal mutation settles.
  Rollback validates the complete three-file graph before deleting any sibling,
  removes only the original objects, closes every retained capability, and
  caches the first rollback/release result for idempotent duplicate calls.
- The Task Manager explicitly releases retained artifact capabilities only after
  the terminal SQLite commit. A capability-close error no longer prevents
  execution-boundary release or publication of already committed events.
- The former `coordinatorSQLiteStore.terminalApplyFailures` pre-return was
  removed. The real-stack transaction regression installs a SQLite trigger that
  aborts the terminal `coverage_runs` update after Task/step/event/artifact,
  TestRun and CoverageReport writes have already occurred in the same
  `taskstore.Store.Apply` transaction.
- Every phase-failure, Task-timeout, post-seal cancellation and revalidation
  case now reads all three stored aggregates and asserts exact terminal status,
  outcome/reason, timestamp relationships, TestRun incomplete/summary/result
  state, empty unavailable report/artifacts, exact phase cutoff and unique
  terminal broker events.
- A Windows real-stack regression directly replaces the retained task-root
  pathname between configure preparation and its continuation, verifies exact
  `infrastructure_failed` / `unavailable,instrumentation_failed`, and proves
  cleanup neither follows nor deletes the replacement or detached original.

### RED evidence

Same-content replacement exposed the hash-only rollback defect:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix3-red'); go test ./apps/test-service/internal/artifactstore -run TestCoverageArtifactRollbackNeverDeletesSameContentReplacement -count=1 -v
```

Output: exit 1. `RollbackFinalized() error = <nil>, want ErrUnsafePath`; the old
implementation accepted a fresh same-content object as the finalized object.

Replacing the wrapper fault with a genuine SQLite fault initially had no fault
installer, proving the old pre-return mechanism had been removed from the test:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix3-sql-red'); go test ./apps/test-service/internal/coverageexec -run TestCoordinatorTerminalSQLiteFailureKeepsBrokerInvisibleAndRecoversExactly -count=1 -v
```

Output: build failure, `undefined: installLateTerminalSQLiteFault`. The helper
subsequently installed a real database trigger rather than restoring the Store
wrapper fault.

Strengthening the aggregate matrix produced RED against over-strong provisional
timestamp/error assumptions. The stored evidence showed exact domain behavior:
Task infrastructure/command failures retain their closed error code/message,
and an action failure may finish the embedded TestRun immediately before the
Task terminal timestamp. Assertions were corrected to require the exact closed
values and bounded ordering rather than weakening production behavior.

The first direct task-root rename attempt produced a Windows-specific RED:

```text
rename ...\coverage-executions\111... ...\111....detached: Access is denied.
```

Windows correctly pinned the non-empty tree while retained handles were open.
The test-only fault now closes those OS handles without calling owner cleanup,
preserves the original identity snapshot, performs the replacement, and then
lets the production continuation/cleanup paths validate it.

Duplicate unsafe rollback initially hid the first failure:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix3-replay-red'); go test ./apps/test-service/internal/artifactstore -run TestCoverageArtifactRollbackNeverDeletesSameContentReplacement -count=1 -v
```

Output: exit 1, `duplicate RollbackFinalized() error = <nil>, want cached
ErrUnsafePath`.

### GREEN evidence

Identity-bound rollback regression:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix3-replay-green'); go test ./apps/test-service/internal/artifactstore -run TestCoverageArtifactRollbackNeverDeletesSameContentReplacement -count=1 -v
```

Output: exit 0; PASS in `0.03s`, package `0.095s`. The same-content replacement
and every sibling remained, the duplicate call returned the cached unsafe error,
and test cleanup removed the directory, proving handles were closed.

Genuine late SQLite transaction fault:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix3-sql-exact-green'); go test ./apps/test-service/internal/coverageexec -run TestCoordinatorTerminalSQLiteFailureKeepsBrokerInvisibleAndRecoversExactly -count=1 -v
```

Output: exit 0; PASS in `0.60s`, package `0.686s`.

Exact fault/cancel/revalidation matrix:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix3-cancel-exact'); go test ./apps/test-service/internal/coverageexec -run 'TestCoordinatorTaskTimeoutStopsCurrentTreeBeforeLaterPhase|TestCoordinatorCancellationAfterProfileSealingReleasesRealManifestTreeOnce|TestCoordinatorRealManagerTerminalizesExactPhaseFaultMatrix|TestCoordinatorRevalidatesEveryRetainedBoundaryBeforeContinuation' -count=1
```

Output: exit 0; package PASS in `4.790s`.

Direct retained-root replacement:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix3-root-replace4'); go test ./apps/test-service/internal/coverageexec -run TestCoordinatorDirectExecutionRootReplacementFailsClosedWithoutFollowingReplacement -count=1 -v
```

Output: exit 0; PASS in `0.12s`, package `0.224s`.

Fresh affected package gates, run concurrently with independent caches:

```powershell
go test ./internal/artifactstore -count=1
go test ./internal/task -count=1
go test ./internal/taskstore -count=1
go test ./internal/coverageexec -count=1
```

Output: all exit 0: artifactstore `0.957s`, task `0.787s`, taskstore
`11.687s`, coverageexec `11.836s`.

Fresh full service gate:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix3-service-full'); go test ./... -count=1
```

Working directory: `apps/test-service`. Output: exit 0 in `95.5s`; every
test-bearing package passed, including coverageexec `21.385s`, artifactstore
`2.629s`, task `0.874s`, taskstore `18.614s`, runtime `4.196s`, and
processcontrol `35.465s`.

Fresh affected race gate:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix3-race'); go test -race ./internal/coverageexec ./internal/artifactstore ./internal/task ./internal/taskstore -count=1
```

Output: exit 0 in `173.1s`; coverageexec `36.248s`, artifactstore `2.110s`,
task `2.429s`, taskstore `87.093s`.

Fresh static and patch gates:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix3-vet'); go vet ./...
git diff --check
```

Output: both exit 0 with no diagnostics.

Repeated fault/leak/replay/ownership selection:

```powershell
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix3-fault-replay'); go test ./internal/coverageexec -run 'TestCoordinator(RealArtifactFinalizationFailureRollsBackAndTerminalizesUnavailable|TerminalSQLiteFailureKeepsBrokerInvisibleAndRecoversExactly|RealManagerTerminalizesExactPhaseFaultMatrix|TaskTimeoutStopsCurrentTreeBeforeLaterPhase|CancellationAfterProfileSealingReleasesRealManifestTreeOnce|RevalidatesEveryRetainedBoundaryBeforeContinuation|DirectExecutionRootReplacementFailsClosedWithoutFollowingReplacement|CloseCancelsRealActiveManagerProcessBeforeReleasing|DuplicateResumeReturnsTheSingleLiveTask)|TestCompletionCommitsClosedReportSetBeforeReportBearingGraph' -count=3
$env:GOCACHE=(Join-Path $env:TEMP 'unitTest-phase8-task7-fix3-artifact-replay'); go test ./internal/artifactstore -run 'TestCoverageArtifactSinkRollsBackPublishedReportFilesWhenFinalizationFailsPartway|TestCoverageArtifactRollbackNeverDeletesSameContentReplacement' -count=10
```

Output: both exit 0; coverageexec `18.395s`, artifactstore `0.590s`.

### Phase, outcome, transaction, replay and ownership evidence

- Configure/build/merge/parser/normalizer/report failures stop at the expected
  last phase. Stored Task, TestRun and CoverageRun are each terminal once with
  the precise closed outcome; unavailable CoverageRuns have nil summary, blank
  report ID, empty artifact refs, and matching Task/Coverage finish timestamps.
- Configure/revalidation failures store an unstarted errored incomplete TestRun;
  build failure stores blocked incomplete; post-test failures store a started
  passed complete TestRun. Each has the exact `{Iterations:1}` empty summary and
  no results in this empty-catalog harness.
- Task timeout stores `timed_out`, `cancelled/task_timed_out`, and an unstarted
  `timed_out` incomplete TestRun, terminates configure once, and starts no later
  phase. Post-seal cancel stores `cancelled`, `cancelled/user_cancelled`, and a
  started cancelled complete TestRun; manifest/allocator/adapter/root ownership
  remains once-only across duplicate coordinator Close.
- Workspace generation, catalog revision, coverage profile, instrumentation
  fingerprint, retained binary identity and trust loss are revalidated before
  continuation with the precise phase cutoff. The direct root replacement is a
  seventh concrete retained-boundary mutation and leaves both replacement and
  detached-original sentinels intact with no public output.
- The SQLite trigger fires in `finishCoverageRunTx`, after earlier writes in the
  same transaction. Before recovery, Task remains running at publish, CoverageRun
  and TestRun remain queued, the attempted report is not found, artifact metadata
  and files are empty, and the broker has no terminal event. After dropping the
  trigger, `RecoverInterrupted` stores exact interrupted/service-restarted
  timestamps, summary and not-run result with exactly three recovery events.
- Rollback preflights all report objects through retained handles before any
  removal. A same-content replacement therefore cannot cause deletion of either
  that replacement or an already-validated sibling. Partial finalization and
  SQLite failure still remove only original files; duplicate rollback is
  idempotent and returns the frozen first result without reopening paths.

### Files changed in fix round 3

- `apps/test-service/internal/artifactstore/store.go`
- `apps/test-service/internal/artifactstore/task_sink.go`
- `apps/test-service/internal/artifactstore/task_sink_test.go`
- `apps/test-service/internal/coverageexec/coordinator_test.go`
- `apps/test-service/internal/coverageexec/orchestration_faults_windows_test.go`
- `apps/test-service/internal/task/manager_execution.go`
- `apps/test-service/internal/task/ports.go`

### Self-review

- Removed the obsolete path-reopen/hash rollback implementation; production
  CoverageRun rollback now has a single retained-capability path.
- Verified the whole three-file graph is validated before the first deletion and
  each removal rechecks current path identity. Capability close is centralized,
  idempotent, and executed after success, rollback failure, partial finalization,
  and duplicate calls.
- Verified Store.Apply is called by the wrapper before its error is recorded;
  the trigger is persistent SQLite schema state and explicitly dropped only
  after the precommit assertions, before recovery.
- Verified terminal assertions do not equate action completion and Task finish
  when the Manager legitimately records them a fraction apart; they require
  exact non-zero/ordering relationships and exact domain values.
- Verified root replacement happens after preparation/configure execution and
  before the continuation consumes the process result. The test-only handle
  closure is necessary because Windows otherwise prevents the hostile state;
  production cleanup still owns the identity check and refuses both trees.
- Verified no cache, database, report, profile, profdata or execution-root
  byproduct appears in the worktree diff.

### Deferred ledger status

- **Task 1 stronger standalone task-layer finished aggregate validator remains
  open.** Round 3 adds direct exact real-stack assertions but does not introduce
  the separately requested general validator.
- **Task 1 successful/action-error continuation evidence remains closed for Task
  7.** The full real flow reaches publish and renderer failure stops before it.
- **Task 5 cancellation allocator/root/profile release-once remains closed.**
  The post-seal real cancellation still asserts manifest, allocator, adapter,
  root and process ownership once; round 3 strengthens its three-aggregate
  terminal assertions and repeats it in race/replay gates.

### Concerns and evidence boundary

- Windows prevents an external rename while the retained directory/profile
  capabilities are open. The replacement regression therefore uses a test-only
  fault to close OS handles without owner cleanup, then replaces the path while
  retaining the original identity snapshot. This is stronger than a metadata
  mutation for cleanup behavior, but it is not evidence that an unprivileged
  peer can bypass Windows handle pinning.
- The fake process remains hermetic; it exercises the real Manager, SQLite,
  ArtifactStore, retained LLVM adapter/profile handles and process contract, not
  host-installed LLVM executables.
- No blocking concern remains for Task 8. The only deferred item in this scope
  is the pre-existing Task 1 standalone aggregate validator.

### Commit

- Fix-round-3 implementation and tests: `3ea19c2` —
  `fix: bind coverage rollback to retained identities`.
- The report-only follow-up commit identifier is supplied in the handoff.
