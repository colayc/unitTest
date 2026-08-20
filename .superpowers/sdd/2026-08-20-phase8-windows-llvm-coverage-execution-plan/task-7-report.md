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
