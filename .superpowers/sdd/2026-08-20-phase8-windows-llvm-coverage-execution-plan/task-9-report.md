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

## Fix Round 1

### Review findings and RED evidence

The four reviewed Important findings were reproduced before their fixes:

- The offline-boundary test first failed to compile because the audited Windows
  OS-boundary API did not exist. A later real Windows PowerShell control exposed
  a second fail-open defect: an unavailable firewall query was silently treated
  as an absent rule (4 tests passed, 1 failed with `Missing expected rejection`).
- The strict JUnit and PASS-only evidence tests first failed with `TS2307`
  because their support module did not exist. After the tokenizer was present,
  an entity-encoded non-whitespace document tail still produced a focused RED
  until text outside the one root was checked in its raw XML form.
- Workspace focused tests failed because `.go-version` still contained
  `1.26.5` and the workflow had neither the required offline cleanup ordering
  nor a success-only coverage-evidence upload.
- The fixture control reported line 63, which mapped to `return true;` inside
  the helper instead of the failed assertion call.

### Offline execution boundary

Before the real Service starts, the smoke now installs both the existing Node
HTTP(S) guard and a random unique Windows Firewall all-program outbound
`Block/Any` rule for the isolated-runner test window. The Service, CMake,
Ninja, compiler/linker, fixture test, `llvm-profdata` and `llvm-cov` therefore
share one OS-enforced boundary instead of relying on a Node monkeypatch.

Installation is fail-closed and auditable. A detached PowerShell watchdog must
write an exclusive PID-specific readiness marker before rule installation.
The installed rule is then checked in ActiveStore for identity, enabled state,
outbound/block/Any-profile policy, all enabled firewall profiles, and
unrestricted application, package, service, address, port and interface
filters. Firewall-query permission failures propagate; they are never treated
as an empty policy store. Normal teardown removes and re-audits both stores,
the PID watchdog repeatedly removes the exact random rule after an abnormal
Node exit, and the workflow has an `always()` cleanup/audit by the dedicated
rule group. Failure to start the watchdog or create, audit or revoke the rule
fails the required gate.

The local host has a medium-integrity token and cannot enumerate Firewall
policy. The real control now verifies that `AuditRemoved` rejects this state
instead of claiming a clean audit. Because this worktree also lacks the
verified CMake/LLVM bundle, the coverage smoke returns the exact single SKIP
before any native execution or OS-rule installation. Required CI does not have
that escape: missing toolchain or firewall authority is a failure.

### Strict JUnit and PASS-only evidence

The regex/tag-stack JUnit check was replaced by a bounded, fatal-UTF-8,
character-by-character XML tokenizer and closed JUnit parser. It validates the
exact declaration, quoted and non-duplicate attributes, legal builtin and
numeric entities, XML characters, matching nesting, exactly one root, exact
testsuite/testcase/outcome schemas and structural counts. Unknown/bare/
unterminated entities, bad numeric entities, unquoted/duplicate/extra/missing
attributes, mismatched nesting, extra roots, DOCTYPE/ENTITY, comments, CDATA
and extra processing instructions are rejected.

At test entry, any stale final report is removed. Assertions, artifact decode,
the real Extension viewer adapter and CoverageController disposal all finish
before teardown. Service shutdown, fixture removal and offline-boundary
revocation are attempted in order; every teardown must succeed before
publication. Evidence is written exclusively to a random temporary file,
flushed, read back and checked for exact canonical strict-JSON bytes, with
atomic rename as the final fallible operation. Injected post-write readback
corruption and injected Service teardown failure both leave no final report or
temporary evidence. The workflow uploads coverage execution evidence only on
`success()` and retains `if-no-files-found: error`.

### Pin and fixture correction

The live repository pin, README, current development plan, workspace assertion
and both CI jobs now agree on Go 1.26.6. Repository-wide search leaves only
dated historical plans and recorded Go 1.26.5 command evidence; those are
historical facts rather than current pin claims and were not rewritten.

The fixture passes `__LINE__` from the failed `expect_equal` call. A fresh
compiled control now emits `test/math_test.cpp:77`, and source line 77 is the
actual `return expect_equal(1, instrumented, __LINE__);` call, so unrelated
helper-line drift cannot make the diagnostic point at successful helper code.

### Fresh GREEN verification

All commands used Node 24.18.0, pnpm 11.4.0, Go 1.26.6, `GOENV=off`,
`GOTOOLCHAIN=local` and a worktree-local GOCACHE where Go was involved.

- `pnpm install --offline --frozen-lockfile`: PASS.
- Offline-boundary focused suite: PASS, 5/5, including the real unprivileged
  fail-closed Windows control.
- Strict JUnit/evidence focused suite: PASS, 9/9.
- `pnpm --filter @unit-test-ide/service-probe test`: PASS, 45/45.
- `pnpm --filter code-oss-extension test`: PASS, 133/133.
- Existing real Named Pipe Service smoke: PASS, 4/4.
- Coverage Service smoke: honest local SKIP, exactly 1 test / 0 pass / 0 fail /
  1 skip with `SKIP: verified clang-cl coverage toolset is unavailable`; the
  final coverage execution report is absent.
- `go test ./apps/test-service/... -count=1`: PASS, all Service packages.
- `go test -race ./apps/test-service/... -count=1`: PASS, all Service packages,
  no race report.
- `pnpm check:protocol-generated` and `pnpm check:coverage-generated`: PASS.
- `pnpm build`: PASS.
- Root `pnpm test`: PASS through coverage/CMake bundle generators, workspace
  smoke, every package suite and the full Go Service suite.
- `pnpm test:workspace`: PASS, 21/21. The first sandboxed attempt correctly
  exposed blocked Visual Studio SDK metadata; the unchanged gate passed after
  granting the test read access and using the installed CMake 3.31.6-msvc6.
- Fixture diagnostic location control: PASS; process exit 1 and the emitted
  line maps to the assertion call.
- `git diff --check`: PASS.

Generated Go cache, temporary fixture binaries, Service build outputs and test
cache artifacts were removed. No production coverage behavior was weakened,
no out-of-scope production fix was required, and no push was performed.

## Fix Round 2

### Review findings and credible RED

Both remaining Important findings were reproduced before implementation.

- Six new real PowerShell fixture cases failed because the old script exposed
  only separate `Watch`/`Install` actions. It rejected both `Guard` and
  `CleanupAll`, produced neither ready nor removed state, and could not satisfy
  the owner-death or cleanup-retry contracts. The existing real unprivileged
  Windows control continued to reject an unavailable firewall audit.
- The TypeScript lifecycle tests then failed to compile because there was no
  guardian handle or state-root API. The Hosted-CI focused test also failed
  because the workflow still contained one inline removal/audit pass instead
  of the shared convergent cleanup action.

This RED specifically captured the reviewed race: a detached watcher could be
ready before a separate installer started, so the installer could outlive Node
and create a rule after cleanup had already observed no rule. It also captured
that `Get-NetFirewallProfile` without `-PolicyStore` does not audit the
effective ActiveStore profiles.

### Single-creator guardian and ActiveStore audit

The separate installer and watchdog were replaced by one detached,
long-lived PowerShell guardian. The guardian writes its PID state first,
captures the owner process, proves the exact rule absent in ActiveStore and
PersistentStore, and is itself the sole caller of `New-NetFirewallRule`. It
then audits the effective rule, every application/address/port/service/
interface filter, and the persistent identity. Firewall profiles are queried
only with `Get-NetFirewallProfile -PolicyStore ActiveStore`; the closed set
must be exactly Domain, Private and Public, with every profile enabled. An
exclusive ready marker is written only after the complete audit.

After the one create call returns, the guardian has no create path. It only
waits for owner exit or an exclusive release marker. Its finally path catches
each removal and query failure, resets the stability count and retries within
a bounded deadline. Only three consecutive empty Active/Persistent audits
produce the removed marker and successful process exit. Owner death during a
delayed install therefore lets that same installer finish and immediately
converge cleanup without ever publishing ready.

Node now waits for guardian ready before returning the boundary. Normal close
writes release and waits for both removed and a zero guardian exit before it
restores the HTTP guard. If readiness or release is abnormal, recovery first
requests release and waits; on timeout it terminates the sole creator and must
observe its exit before launching an independent exact cleanup. When removal
cannot be proven, the Node guard remains installed and the gate fails closed.

### CI convergence and regression coverage

The coverage smoke places guardian state under the fixed ignored root
`.native-e2e/runtime/windows-firewall-guardians`. The Windows `always()` step
now calls the same script's `CleanupAll` action. It repeatedly signals every
known guardian, removes the dedicated firewall group, audits ActiveStore and
PersistentStore, and refuses success while an unarmed/live guardian remains.
Query/removal faults reset convergence and a permission failure or timeout is
fatal. Since ready guardians cannot create again and pre-ready guardians have
a state directory before spawn, successful CI convergence has no late creator.

The real PowerShell fixture and TypeScript tests cover:

- ready only after exact ActiveStore profiles and all closed filters;
- missing, extra and disabled profile sets fail after confirmed cleanup;
- owner death while the sole installer is delayed, followed by cleanup whose
  first two removals throw and whose third succeeds;
- removal and query exceptions being retried, permanent audit failure timing
  out, and `CleanupAll` waiting for a concurrently finishing guardian;
- normal explicit close waiting for removal, readiness failure waiting for
  recovery, abnormal close recovery, and HTTP remaining blocked whenever
  cleanup is not proven;
- a static ban on default profile queries, the real unprivileged fail-closed
  firewall control, and the workflow's shared CleanupAll/state-root contract.

Chinese development, native-E2E, security, roadmap and design documentation
now describes this exact single-creator lifecycle and the ActiveStore profile
semantics. The Protocol vertical slice, strict JUnit parser, PASS-only atomic
evidence, Go 1.26.6 pin, local single-SKIP and required-family failure behavior
from Fix Round 1 remain unchanged.

### Fresh GREEN verification

All commands used Node 24.18.0, pnpm 11.4.0 and Go 1.26.6. Go commands used
`GOENV=off`, `GOTOOLCHAIN=local` and a worktree-local GOCACHE.

- `pnpm install --offline --frozen-lockfile`: PASS with pnpm 11.4.0.
- Guardian/profile/cleanup focused suite: PASS, 15/15, including the real
  unprivileged Windows control.
- `pnpm --filter @unit-test-ide/service-probe test`: PASS, 55/55.
- `pnpm --filter code-oss-extension test`: PASS, 133/133.
- Existing real Named Pipe Service smoke: PASS, 4/4.
- Coverage Service smoke: honest local SKIP, exactly 1 test / 0 pass / 0 fail /
  1 skip with `SKIP: verified clang-cl coverage toolset is unavailable`; the
  final coverage execution report remained absent.
- Required `clang-cl` control with the same missing verified toolset: expected
  FAIL, exactly 1 test / 0 pass / 1 fail / 0 skip with
  `required verified clang-cl coverage toolset is unavailable`.
- `go test ./apps/test-service/... -count=1`: PASS, all Service packages.
- `go test -race ./apps/test-service/... -count=1`: PASS, all Service packages,
  no race report.
- `pnpm check:protocol-generated` and `pnpm check:coverage-generated`: PASS.
- Root `pnpm build`: PASS.
- Root `pnpm test`: PASS after supplying the installed Visual Studio CMake to
  PATH and allowing MSBuild read-only access to its LocalAppData Windows SDK
  metadata; no network access was used. The two earlier attempts precisely
  reported missing CMake on PATH and sandbox-denied SDK metadata respectively.
- Standalone `pnpm test:workspace`: PASS, 21/21 under the same local CMake/SDK
  read boundary.
- `git diff --check`: PASS.

Generated GOCACHE, Python cache, Service build output and `.native-e2e`
runtime/artifact state were removed. No production Service or coverage
execution behavior was weakened, no out-of-scope production fix was required,
and no push was performed.
