# Phase 8 Task 9 Report

## Status and summary

Implemented the required Windows LLVM coverage execution gate without changing
or weakening production coverage behavior. The new smoke builds the current Go
Service, starts a trusted Windows Named Pipe session, and drives the real
TypeScript Protocol Client v1.4 through workspace inspection, generated profile
selection, base build, discovery, catalog, coverage start, bounded completion
polling, report retrieval, and artifact chunk retrieval.

The deterministic CMake fixture has two CppUTest-compatible cases. One covers a
selected branch. The other executes instrumented code and then emits a real
assertion failure before returning exit code 1, so profile data can be flushed.
The success contract is therefore deliberately CoverageRun `available` plus its
associated TestRun `failed`, not a passing test run.

## TDD RED and local toolset boundary

The smoke test was written and compiled before the tracked fixture existed. Its
first execution produced one failing test, zero passing tests and zero skipped
tests with the intended feature-level assertion:

```text
the tracked deterministic coverage fixture must exist
false !== true
```

After adding the fixture, the first integration execution exposed that this
worktree had no verified CMake bundle and therefore could not form a verified
`clang-cl` coverage toolset. The final local run is intentionally not a PASS:

```text
tests 1
pass 0
fail 0
skipped 1
SKIP: verified clang-cl coverage toolset is unavailable
```

When `UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS=clang-cl` is present, that same
absence throws and fails the Windows CI job. No evidence report is published on
the local SKIP path.

## End-to-end assertions

- The Service is built from the repository with Go 1.26.6, `GOENV=off`,
  `GOTOOLCHAIN=local`, a private GOCACHE and `-trimpath`.
- The session is trusted, uses the production ServiceManager and real Named
  Pipe transport, and negotiates the production Protocol v1.4 client.
- Workspace generation, generated `clang-cl`/Ninja build profile, coverage
  profile, discovery Task, catalog and revision are all obtained from the real
  Service; no fake protocol responses or fixture-side production hooks are
  used.
- Coverage completion is bounded. The run must be `available` with no reason,
  while the associated completed TestRun must be `failed`, complete and exactly
  2 total / 1 passed / 1 failed.
- Lines, branches and functions are nonzero. Branch coverage must be partial,
  proving both a covered and an uncovered branch outcome.
- All artifact pages and all chunks are read with the real Client. Coverage
  JSON, JUnit XML and coverage HTML must have three distinct artifact IDs, and
  every byte count, SHA-256 and kind is checked against public metadata.
- Coverage JSON v1 is decoded by `@unit-test-ide/coverage-models`; only
  `src/math.cpp` is included. JUnit is structurally parsed as 2 tests and 1
  failure. HTML is opened through the real Extension viewer adapter and must
  contain a no-network CSP with no remote URL.
- The real CoverageController refresh path publishes the finished report.
- Provenance is exactly `windows/x64/clang-cl/llvm-cov`; compiler, driver and
  collector versions are nonempty, equal and match the selected toolchain.
  Compiler/driver/collector provenance contains neither `path` nor
  `executable`.

## Security and evidence boundary

The wire capture records both outbound and inbound Protocol bytes for the
coverage exchange. A hostile environment value and LLVM raw-profile value are
injected into the Service process. Protocol bytes, every report artifact and
the evidence bytes are rejected if they contain the token, hostile value,
workspace/data/tool paths, a Windows absolute path, `LLVM_PROFILE_FILE`,
`.profraw` or `.profdata`.

Successful execution writes exactly one newline-terminated strict JSON object
to `.native-e2e/artifacts/windows/coverage-execution-report.json`. The closed
shape contains only schema version, platform, architecture, tool names and
versions, public outcomes, integer summary metrics, artifact kind/size/digest
and duration. It contains no runtime IDs or native paths. The file is created
with exclusive temporary-file semantics, flushed, and atomically renamed only
after the sensitive-byte gate passes.

The evidence file is a CI/runtime artifact and is not tracked. GitHub Actions
and uploaded artifacts are development evidence only. GitHub and Gitee remain
source-hosting, collaboration and development-distribution channels, never a
production coverage runtime dependency.

## CI and documentation

The Windows job now prepares the fixed CMake bundle and builds the Service
before running `test:coverage-service-smoke` with required family `clang-cl`.
The report upload runs with `if-no-files-found: error`. Linux does not run this
native coverage smoke or upload its report; it retains the existing real Unix
Socket Service smoke.

Chinese development, roadmap and design documents now state the exact
PASS/SKIP/evidence boundary, the failed-TestRun/available-CoverageRun contract,
the three verified artifacts, the Windows-only required gate, the unimplemented
Linux native coverage boundary, and the non-runtime role of GitHub/Gitee.

## GREEN verification

Pinned runtimes were Node 24.18.0, pnpm 11.4.0 and Go 1.26.6. The root build's
first environment invocation correctly failed because its nested script found
pnpm 11.19.0 on PATH. After pinning both PATH and Corepack's existing 11.4.0
cache, the unchanged root `pnpm build` command passed.

- `go test ./apps/test-service/... -count=1`: PASS, all Service packages.
- `go test -race ./apps/test-service/internal/coverageexec ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/runtime -count=1`:
  PASS.
- `pnpm --filter code-oss-extension test`: PASS, 124/124.
- Existing real `service-smoke.test.js`: PASS, 4/4.
- Coverage command/controller/source/viewer/Protocol Client tests: PASS, 17/17.
- `pnpm --filter code-oss-extension test:coverage-service-smoke`: SKIP, exactly
  1 test / 0 pass / 0 fail / 1 skip with the required message; this is not a
  Windows native PASS.
- The tracked fixture compiled with `-Wall -Wextra -Werror`; list mode exposed
  both cases and verbose execution produced 1 pass, 1 assertion failure and
  process exit 1 as designed.
- `pnpm check:protocol-generated`: PASS.
- `pnpm check:coverage-generated`: PASS.
- `pnpm build`: PASS with pinned Node/pnpm and local Go toolchain/cache.
- `git diff --check`: PASS.

## Concerns

- This workstation cannot supply native PASS evidence because its verified
  CMake/LLVM capability is unavailable. The precise single SKIP, zero PASS and
  absent evidence report are the required honest local result. Windows CI owns
  the required real `clang-cl/llvm-profdata/llvm-cov` PASS and report.
- Linux native GCC/Clang coverage remains outside this task. The Linux Unix
  Socket Service smoke is not relabelled as coverage evidence.
- No production Service, coordinator, resolver, collector or Extension runtime
  behavior was changed to accommodate the smoke.
- No push was performed.

## Commit

The implementation commit identifier is supplied in the Task 9 handoff because
this report cannot contain the hash of its own commit.
