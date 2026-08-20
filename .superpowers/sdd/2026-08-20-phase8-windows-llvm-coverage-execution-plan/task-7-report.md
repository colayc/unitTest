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
