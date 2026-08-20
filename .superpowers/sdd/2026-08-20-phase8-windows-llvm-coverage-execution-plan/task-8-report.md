# Phase 8 Task 8 Report

## Status and summary

Implemented production Runtime wiring for the Phase 7 `coverageexec.Coordinator`.
The trusted Runtime now owns a persist-first queue backend and one coverage
executor; Windows dispatches queued work to the real LLVM coordinator path,
while non-Windows dispatches the same durable aggregate to the coordinator's
explicit unsupported completion.

The fixed startup order is now:

1. cleanup process leases;
2. `RecoverInterrupted`;
3. artifact orphan cleanup;
4. resume queued builds;
5. resume queued tests;
6. resume queued coverage in ascending created-time/Task-ID order.

The fixed shutdown order is now:

1. stop coverage admission;
2. close the coverage executor, which cancels and waits for active coverage
   Tasks through the still-live Task Manager;
3. shut down the Task Manager;
4. close test resources, broker, artifact store, SQLite store, instance lock,
   and data-directory guards.

## TDD RED evidence

Baseline command before Task 8 tests:

```powershell
$env:GOENV='off'; $env:GOTOOLCHAIN='local'; $env:GOCACHE=(Join-Path (Get-Location) '.gocache-task8'); & 'D:\Program Files\Go\bin\go.exe' test ./apps/test-service/internal/runtime ./apps/test-service/internal/server -run 'Coverage|Queued|Trusted|Unsupported|Shutdown' -count=1
```

Baseline output: runtime and server passed.

After writing the Task 8 behavior tests and before production implementation,
the same command failed at compile time with the intended missing production
surface:

```text
too many arguments in call to newRuntimeCoverageBackend
undefined: platformCoverageExecutor
undefined: resumeQueuedCoverage
unknown field coverageExecutor in struct literal of type Runtime
FAIL unit-test-ide.local/test-service/internal/runtime [build failed]
```

This RED names the production change directly: the existing backend could only
persist a queued aggregate and had no executor, runtime resume path, or owned
shutdown lifecycle.

## Implementation and ownership

- `queuedCoverageBackend.StartCoverageRun` resolves and persists while coverage
  admission is open, then calls the executor. It reloads Task, CoverageRun and
  TestRun from the canonical repository after every resume attempt. A resume
  error returns that canonical persisted graph with the error; it never returns
  newly generated in-memory relation IDs.
- `coverageBuildPreparer` is the minimal typed adapter required because Go return
  types are invariant: `build.Coordinator.PreparePlan` returns
  `*build.PreparedPlan`, while `coverageexec.BuildPreparer` returns the
  `coverageexec.PreparedBuild` interface. The exact prepared capability is
  forwarded without rebuilding its public tool path.
- `llvmCoverageAdapter.Prepare` passes the exact `toolchain.Instance` received
  from the coordinator's current build/Inspector revalidation directly to
  `coveragellvm.PinToolset`. It then creates retained instrumentation and profile
  capabilities under the coordinator-owned execution root. Persisted public
  provenance is never used to reconstruct executables.
- The Windows prepared adapter delegates only to the existing closed
  `WriteInstrumentation`, `NewProfileAllocator`, `SealProfiles`, and
  `BuildCollectorInvocation` APIs and closes allocator/toolset ownership once.
- Non-Windows Runtime uses `platformCoverageExecutor.Resume` to call
  `coverageexec.Coordinator.FinishUnsupported`. Its guard adapter is
  side-effect-free and is never prepared, so Linux starts no compiler,
  collector, directory allocation, or native process. Task 7's completion owner
  stores Task `infrastructure_failed`, CoverageRun
  `unavailable/instrumentation_failed`, no report and no public artifact.
- Untrusted Runtime constructs no build/test/coverage coordinator or executor.
  It does not enter the LLVM adapter and leaves the base coverage data directory
  empty.
- `coverageexec.Coordinator.Close` remains the active execution owner. Runtime
  closes it before Manager shutdown, preserving Task 7 cancellation, bounded
  terminal wait, retained-root cleanup, replay, security and fault semantics.

## Tests added

- persist-before-resume and canonical reload after resume failure;
- exact queued graph identity validation;
- typed build capability forwarding;
- Windows native versus Linux explicit-unsupported dispatch;
- created-time/ID coverage startup order and non-queued/running skip;
- trusted production dependency composition and strict startup stages;
- untrusted zero executor/native execution side effects;
- admission stop before executor close and executor close before Manager
  shutdown, including duplicate Runtime shutdown.

## GREEN verification

Focused runtime/server gate:

```powershell
go test ./apps/test-service/internal/runtime ./apps/test-service/internal/server -run 'Coverage|Queued|Trusted|Unsupported|Shutdown' -count=1
```

Output: both packages `ok`.

Runtime/server/session full gate:

```powershell
go test ./apps/test-service/internal/runtime ./apps/test-service/internal/server ./apps/test-service/internal/session -count=1
```

Output: all three packages `ok`.

Affected race gate:

```powershell
go test -race ./apps/test-service/internal/runtime ./apps/test-service/internal/coverageexec -run 'Coverage|Queued|Shutdown|Cancel' -count=1
```

Output:

```text
ok unit-test-ide.local/test-service/internal/runtime      5.070s
ok unit-test-ide.local/test-service/internal/coverageexec 5.430s
```

Static gate:

```powershell
go vet ./apps/test-service/internal/runtime ./apps/test-service/internal/coverageexec ./apps/test-service/internal/coveragellvm
```

Output: exit 0 with no diagnostics.

Linux compile-only gate:

```powershell
$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'; go test -c -o "$env:TEMP\unitTest-phase8-task8-runtime-linux.test" ./apps/test-service/internal/runtime
```

Output: exit 0. This is compile/static evidence only and is not a Linux native
coverage PASS.

Full Service gate:

```powershell
go test ./apps/test-service/... -count=1
```

Output: exit 0; every test-bearing Service package passed, including runtime,
coverageexec, coveragellvm, task, taskstore, artifactstore, server and session.

## Documentation

Updated `docs/development.md` in Chinese while retaining English technical
terms. It now states that Windows `clang-cl` can complete the LLVM coverage
chain, Linux GCC/Clang native execution is the next batch, cross-compile is not
native PASS evidence, and GitHub/Gitee are development distribution rather than
product runtime dependencies.

## Concerns and evidence boundary

- Task 8 does not claim a host-installed LLVM smoke. Task 9 owns the real
  `clang-cl/llvm-profdata/llvm-cov` Protocol and artifact vertical slice.
- Linux evidence in this task is explicit unsupported behavior plus CGO-disabled
  compilation. Native GCC/Clang collection remains unimplemented by design.
- The existing Task 7 deferred standalone Task-layer aggregate validator remains
  unchanged. Task 8 does not weaken or claim to close it.

## Commit

The implementation commit identifier is supplied in the Task 8 handoff because
the report cannot contain the hash of its own commit.
