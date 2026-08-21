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

## Fix Round 3

### Review findings and credible RED

Both reviewed Important findings were reproduced with real PowerShell fixture
processes before implementation.

- The initial per-store audit suite ran 41 tests with 27 passing and 14
  failing. Every PersistentStore policy/filter tamper incorrectly reached
  `ready`, and the valid control had no PersistentStore filter trace. This
  proved that the ActiveStore rule object was still being used to endorse both
  stores.
- After adding closed guardian-state and delayed-creator cases, the focused
  suite ran 50 tests with 41 passing and 9 failing. Corrupt canonical-marker
  inputs, an extra leaf, a reparse marker, and a forged `removed` leaf beside a
  live delayed guardian were incorrectly accepted by the old CleanupAll state
  check.

Final-gate preparation then exposed a separate test-helper ordering concern:
on this non-elevated workstation the smoke tried to establish the firewall
guardian before determining that no verified LLVM coverage toolset existed.
The first focused preflight test produced the expected TypeScript RED
(`runAfterVerifiedCoverageToolsetPreflight` was not exported). The parent
approved the minimal test-helper scope: reuse the production discovery and
identity verifier, without changing production coverage behavior.

### Independent ActiveStore and PersistentStore proof

`Assert-RuleInstalled` now obtains the ActiveStore and PersistentStore rule
objects independently. Each object must be unique and must independently match
the exact Name, DisplayName, Enabled, Direction, Action, Profile and Group.
Application, address, port, service, interface and interface-type filters are
also fetched from and checked against each corresponding store object. The
effective firewall-profile query remains explicitly
`Get-NetFirewallProfile -PolicyStore ActiveStore` and accepts only enabled
Domain, Private and Public profiles.

The real PowerShell fixture can tamper every policy/filter field in either
store while leaving the other store valid. All 26 Active/Persistent tamper
combinations now fail before `ready`, converge rule removal, publish only the
canonical removal proof and exit nonzero. The valid fixture records all twelve
store/filter queries and reaches the normal release/removed lifecycle.

### Closed guardian state and convergent CleanupAll

The Node launcher creates and flushes the durable `rule-name` and `owner.pid`
markers before spawning the sole guardian. Guardian state has a closed six-leaf
schema: `rule-name`, `owner.pid`, `guardian.pid`, `release`, `ready` and
`removed`. Every present leaf must be a regular non-reparse file no larger than
256 bytes, decode as strict UTF-8 and match its exact canonical content;
owner/guardian PIDs are strict positive decimal integers. Unknown leaves,
directories, case variants, damaged/oversized markers and reparse points fail
closed.

CleanupAll performs the dedicated group removal and empty ActiveStore plus
PersistentStore audit on every retry before it considers state convergence.
For a canonical guardian directory it writes or validates the canonical
release marker. A live PID is a blocker even when `removed` already exists,
and is trusted only when both its executable is Windows PowerShell and its CIM
command line binds the same Guard action, rule, owner PID and state directory.
A not-yet-scheduled guardian with no PID remains a possible late creator and
therefore remains a blocker. A dead guardian is accepted only with canonical
removed proof. Thus a delayed pre-ready guardian observes release, follows its
no-create/cleanup path and exits before CleanupAll can obtain three stable
audits; a forged removed leaf cannot make CleanupAll return early.

The guardian itself validates the closed state before PID creation, before and
after firewall creation/audit, and before readiness. Ready, release and removed
use distinct canonical values. Removal/query exceptions continue to be caught
and retried to the bounded deadline. The former free-form `failed` leaf was
removed so failure cannot expand the state schema.

### Verified-toolset preflight scope closure

The Windows coverage smoke now builds and runs a small Go preflight helper
before firewall installation, Service start or coverage-native execution. The
helper directly uses production `NewWindowsAdapters` discovery evidence and
`coveragellvm.PinToolset/Verify`, including the retained executable handles,
Windows file identity and SHA-256 validation. It does not inspect only PATH or
file existence and emits one closed no-path JSON availability object.

Three focused branches prove ordering and zero forbidden side effects:
non-required unavailable produces only the exact skip; required unavailable
throws before boundary/Service/native callbacks; verified establishes the
guardian before executing, and a guardian failure cannot invoke execution.
The real smoke additionally requires the Service-discovered toolchain version
to equal the preflight version. Local unavailable remains exactly one SKIP;
required unavailable is an expected test failure, not a SKIP or evidence.

### Fresh GREEN verification

All applicable commands used Node 24.18.0, pnpm 11.4.0 and Go 1.26.6. Go
commands used `GOENV=off`, `GOTOOLCHAIN=local` and a private worktree cache.

- Independent store/state/guardian focused suite: PASS, 50/50.
- Coverage support preflight/JUnit/evidence suite: PASS, 12/12.
- `pnpm --filter @unit-test-ide/service-probe test`: PASS, 90/90.
- `pnpm --filter code-oss-extension test`: PASS, 136/136.
- Existing real Named Pipe Service smoke: PASS, 20/20.
- Coverage Service smoke: honest local SKIP, exactly 1 test / 0 pass / 0 fail /
  1 skip with `SKIP: verified clang-cl coverage toolset is unavailable`;
  firewall state and the final execution report are absent.
- Required `clang-cl` negative control: expected FAIL, exactly 1 test / 0 pass /
  1 fail / 0 skip with
  `required verified clang-cl coverage toolset is unavailable`; no report was
  published.
- Generic Windows native E2E control: PASS for the available MSVC matrix;
  unavailable `clang-cl` remained an honest skip and was not relabelled PASS.
- `go test ./apps/test-service/... -count=1`: PASS, all packages including the
  new preflight command.
- `go test -race ./apps/test-service/... -count=1`: PASS, all packages and no
  race report.
- `pnpm check:protocol-generated` and `pnpm check:coverage-generated`: PASS.
- Root `pnpm build`: PASS.
- `pnpm test:workspace`: PASS, 21/21.
- Root `pnpm test`: PASS, including generators, bundle/workspace checks, all
  package suites, Extension 136/136 and the full Go Service suite.
- `git diff --check`: PASS.

The first sandboxed Go full run could not perform the coverage-bundle
ancestor-identity rename; its approved rerun passed unchanged. Likewise, the
workspace control required read access to installed Windows SDK metadata. The
fixed CMake bundle used the official 4.3.4 archive, matched the repository
manifest SHA-256 exactly, and was prepared through the production bundle
verifier. These were environment/setup boundaries, not relaxed assertions.

Generated Node/Go/CMake caches, fixture binaries, Python cache, `.native-e2e`
artifacts and the stale preflight-era guardian directory were removed. The
coverage execution report remains absent because this host cannot produce a
verified LLVM native PASS. The approved preflight change is isolated to test
orchestration and a test-only Go command; no production Service, resolver,
collector, protocol or Extension runtime behavior was weakened. No push was
performed.

## Fix Round 4

### Review findings and credible RED

The reviewed `e5fced6` baseline was clean and its 50-test real PowerShell
guardian suite passed before new regression coverage was added. Both remaining
Important findings were then reproduced against that implementation.

- A live guardian was paused for three seconds inside rule creation. Replacing
  its canonical `guardian.pid` with `2147483647` and adding a canonical forged
  `removed` marker made the old CleanupAll process win the exit race while the
  creator was still live. The strengthened test reported `cleanup` instead of
  the required `guardian` first exit, proving that marker-selected PID lookup
  could hide a late creator.
- An ordinary file at the state root was silently accepted with exit code 0
  because `Get-ChildItem -Directory` did not enumerate it. Replacing a delayed
  live guardian directory with an ordinary file likewise let the old cleanup
  return before the creator. The companion unknown-directory, root reparse and
  extra-leaf cases preserve the complete fail-closed root/state matrix.
- Pre-install self-PID tampering initially allowed rule creation and successful
  guardian exit. Adding the command-bound nonce contract first produced the
  expected TypeScript interface RED; the real PowerShell nonce/PID tamper cases
  then demonstrated that writable state was not yet bound continuously to the
  creator.

The workstation denied even same-user `Win32_Process` CIM command-line reads,
so a CIM-based global enumerator could not provide the required local or CI
evidence. This was treated as an identity-audit blocker, not bypassed.

### Command-bound guardian identity and continuous self-audit

Every Windows boundary now generates an independent 256-bit nonce. The Node
launcher writes the exact lowercase nonce to the exclusive
`guardian.nonce` marker before spawn and passes the same value on both Guard
and CleanupExact command lines. Guardian state is a closed seven-leaf schema:
`rule-name`, `owner.pid`, `guardian.nonce`, `guardian.pid`, `release`, `ready`
and `removed`.

The guardian binds the strict nonce marker to its command argument and binds
`guardian.pid` to its actual `$PID`. It repeats the complete plain-directory,
regular-leaf, canonical-content audit immediately after publishing PID, before
the only create call, after creation, before ready, on every ready-state wait
poll, before and after stable firewall cleanup, and after publishing removed.
Tampering still enters the bounded Active/Persistent cleanup path, but cannot
publish readiness or a canonical removal proof from invalid state. Real tests
cover PID/nonce tampering before creation and PID/nonce/extra-leaf tampering
after readiness; every case exits nonzero and the creator performs cleanup.

### Marker-independent creator discovery and closed state root

CleanupAll no longer uses `guardian.pid` or `removed` to discover live
creators. On every convergence audit it independently enumerates OS
`powershell.exe` processes. A small in-process C# inspector uses read-only
`OpenProcess`, `QueryFullProcessImageName` and
`NtQueryInformationProcess(ProcessCommandLineInformation)` so executable and
command line remain auditable where CIM is denied. A matching creator must
bind the exact System32 Windows PowerShell executable, exact boundary script,
case-exact `Guard` action, canonical state root, a direct-child state directory,
canonical rule name, positive owner PID and one valid 256-bit nonce. Duplicate
arguments/creators or unauditable identity fail closed. The live command owner,
nonce and process PID are cross-checked against the corresponding strict state
before convergence.

All matching processes receive canonical release when their directory is still
plain and remain blockers until a fresh OS enumeration proves they exited.
Therefore a dead-PID substitution, forged removed proof, missing/replaced
directory or not-yet-scheduled marker writer cannot hide create capability.
Three stable iterations must still prove the firewall group absent from both
ActiveStore and PersistentStore after all matching creators are gone.

The state root now uses `Get-ChildItem -Force` as a closed enumeration and
revalidates the root itself as a plain non-reparse directory. Its only accepted
entries are canonical-name, ordinary non-reparse guardian directories. Any
ordinary file, symlink, junction, other reparse point or unknown entry fails
closed. Each state directory is also revalidated as plain on every marker
audit, so replacing it while a creator is delayed cannot produce early cleanup
success.

### Fresh GREEN verification

Commands used Node 24.19.0 (within the repository's pinned Node 24 range), pnpm
11.4.0 and Go 1.26.6. Go commands used `GOENV=off`, `GOTOOLCHAIN=local` and a
private worktree GOCACHE.

- Final new guardian/root/process focused suite: PASS, 12/12.
- Full real PowerShell guardian suite: PASS, 62/62; the prior 50 tests remain
  intact, including all 26 independent ActiveStore/PersistentStore tamper cases.
- `pnpm --filter @unit-test-ide/service-probe test`: PASS, 102/102.
- `pnpm --filter code-oss-extension test`: PASS, 136/136; its
  preflight/JUnit/evidence subset remains 12/12.
- Real Named Pipe Service vertical slice: PASS, 4/4.
- Coverage Service smoke: honest local SKIP, exactly 1 test / 0 pass / 0 fail /
  1 skip with `SKIP: verified clang-cl coverage toolset is unavailable`; no
  firewall state or final report existed.
- Required `clang-cl` control: expected FAIL, exactly 1 test / 0 pass / 1 fail /
  0 skip with `required verified clang-cl coverage toolset is unavailable`;
  again no firewall state or report existed.
- `go test ./apps/test-service/cmd/coverage-toolset-preflight -count=1`: PASS
  (compile-only package). The current root suite also passed every Go Service
  package. No Go source changed, so the fresh reviewed `e5fced6` full race PASS
  remains applicable.
- `pnpm check:protocol-generated`, `pnpm check:coverage-generated` and root
  `pnpm build`: PASS.
- `pnpm test:workspace`: PASS, 21/21.
- Root `pnpm test`: PASS, including generator/bundle/workspace gates, all
  packages, service-probe 102/102, Extension 136/136 and the complete Go suite.
- `git diff --check`: PASS.

The first Service smoke attempt precisely reported missing local fixture
binaries; rebuilding them with the pinned Go runtime made the real slice pass.
The first root build attempt correctly rejected an ambient pnpm 11.19.0; the
11.4.0 shim plus Node 24 PATH was used for the successful gates. The sandboxed
workspace run was 20/21 only because MSBuild could not read installed Windows
SDK metadata under LocalAppData; the approved read-only rerun was 21/21. These
were environment boundaries, not relaxed assertions.

Generated Go cache, fixture binaries, Python cache, temporary pnpm shim and
`.native-e2e` state were removed. No Go, production coverage execution,
protocol or Extension behavior was changed, and no push was performed.

## Fix Round 5

### Review findings and credible RED

The final review started from clean commit `e509d95`. New real Windows
PowerShell tests first reproduced the remaining cleanup-ordering and command
line findings without changing production behavior.

- CleanupAll failed immediately at `Get-Item` when its state root was missing,
  so it never reached global firewall removal or either policy-store audit.
  Replacing a live installed guardian's StateRoot with an ordinary file likewise
  produced no cleanup trace at all.
- A delayed command-bound guardian kept running when its StateRoot was renamed
  and replaced by a junction. The old CleanupAll rejected the junction and
  returned inside the 750 ms early-return window while the creator was still
  live; because the guardian validated only its child directory, it retained a
  late create path through the junction.
- A legal ordinary `powershell.exe` launched with an unmatched Windows quote
  remained live, but the handwritten regular-expression parser classified its
  command line as non-canonical. CleanupAll then timed out after roughly five
  seconds even though the process was unrelated to the boundary.
- A fixture-preloaded native enumeration failure already demonstrated the
  intended fail-closed direction: cleanup returned nonzero while the global
  remove plus ActiveStore and PersistentStore audits continued. The regression
  is retained to prevent a future swallowed enumeration/query error.

The focused baseline result was four expected failures and one passing
fail-closed enumeration regression. After the implementation, all five cases
passed unchanged.

### Safety-first convergent CleanupAll

CleanupAll now canonicalizes only the StateRoot string before entering a
specialized bounded convergence loop. Every iteration performs the safety work
in this order:

1. enumerate and identify command-bound guardian processes without consulting
   the filesystem, then request release where strict state still permits it;
2. globally remove the fixed firewall rule group and audit both ActiveStore and
   PersistentStore even if enumeration, release or state validation failed;
3. validate or create the state root and audit its complete closed state.

A matching guardian remains a blocker until a fresh native enumeration proves
it has exited. Failure to write release because the root or state directory was
removed, replaced or made a reparse point is recorded as state corruption; it
does not remove the live-process blocker. Enumeration errors likewise prevent
stable success but do not suppress global removal. Only three consecutive
iterations with successful enumeration, no matching creator and both stores
empty can terminate. Durable state corruption is then returned as an aggregated
nonzero error, so CI fails only after the OS firewall state has converged.

A genuinely missing clean StateRoot is created by the script after the first
global removal/audit rather than by the workflow. An ordinary file, junction,
symlink, other reparse point or corrupt closed state is never replaced and
never reported as successful. The Windows CI cleanup step therefore invokes
CleanupAll directly; its former unconditional `CreateDirectory` pre-step was
removed so an invalid root cannot stop the boundary script from running.

The guardian's owned-state check now revalidates the canonical StateRoot itself
as a plain non-reparse directory on every existing PID/nonce/state transition:
after PID publication, before the only create call, after creation, before and
after ready, during every wait poll, and around removal proof. A junction
introduced during the pre-install pause therefore produces no rule creation;
an ordinary-file replacement after readiness makes the guardian perform stable
rule cleanup and exit nonzero.

### Native Windows argv and process enumeration

The regular-expression command-line parser and PowerShell `Get-Process
-ErrorAction SilentlyContinue` enumerator were deleted. The embedded C# helper
now uses `Process.GetProcessesByName("powershell")`, disposes every returned
`Process` object and propagates enumeration or process-query errors. The only
ignored query race is `OpenProcess` returning explicit Windows error 87 for an
already-exited PID; access denial and every other identity failure remain
fatal.

Each inspected process is opened read-only, its exact executable and native
command line are queried, and shell32 `CommandLineToArgvW` supplies Windows argv
semantics. Argument count, raw command length, each argument length, total
characters, NT buffer bounds and the x86/x64 `UNICODE_STRING` pointer are
bounded before exact guardian comparisons. `Process.Dispose`, `FreeHGlobal`,
`LocalFree` and `CloseHandle` all run from `finally` paths; allocation or close
failures are fail-closed. Only the resulting native argv is compared against
the exact System32 Windows PowerShell executable, boundary script, `Guard`
action, canonical StateRoot/direct-child StateDirectory, rule name, owner PID
and 256-bit nonce. The live odd-quote PowerShell regression proves unrelated
legal Windows command lines no longer block cleanup, while the existing install
race and forged-marker tests repeatedly exercise exact guardian recognition on
the x64 host.

### Fresh GREEN verification

Commands used Node 24.19.0, pnpm 11.4.0, Go 1.26.6 and the repository-verified
CMake 4.3.4 Windows x64 bundle. Go commands used `GOENV=off`,
`GOTOOLCHAIN=local` and a private worktree cache.

- New Round 5 real-PowerShell focused suite: PASS, 5/5.
- Full real-PowerShell guardian/root/process suite: PASS, 67/67. All prior 62
  tests remain intact, including the 26 independent ActiveStore/PersistentStore
  tamper cases, PID/nonce tampering, install races and forged removal case.
- `pnpm --filter @unit-test-ide/service-probe test`: PASS, 107/107.
- `pnpm --filter code-oss-extension test`: PASS, 136/136; the focused
  preflight/JUnit/evidence slice remains 12/12.
- Real Named Pipe Service vertical slice: PASS, 4/4.
- Coverage Service smoke: honest local SKIP, exactly 1 test / 0 pass / 0 fail /
  1 skip with `SKIP: verified clang-cl coverage toolset is unavailable`.
- Required `clang-cl` control: expected FAIL, exactly 1 test / 0 pass / 1 fail /
  0 skip with `required verified clang-cl coverage toolset is unavailable`.
- `go test ./apps/test-service/cmd/coverage-toolset-preflight -count=1`: PASS
  (compile-only package). The final root suite also passed every Go Service
  package. No Go source changed, so the fresh reviewed Round 4 full race PASS
  remains applicable.
- `pnpm check:protocol-generated`, `pnpm check:coverage-generated` and root
  `pnpm build`: PASS.
- `pnpm test:workspace`: PASS, 21/21.
- Root `pnpm test`: PASS in 217.6 seconds, including generators, bundle and
  workspace checks, all packages, service-probe 107/107, Extension 136/136 and
  the complete Go Service suite.
- `git diff --check`: PASS.

The first root build correctly rejected ambient pnpm 11.19.0; the pinned 11.4.0
shim made the unchanged gate pass. The first workspace attempt lacked CMake and
used the ambient Go cache; after preparing the fixed bundle and private cache,
the sandboxed result was 20/21 solely because MSBuild could not read installed
Windows SDK metadata. Its approved read-only rerun passed 21/21. The repository
Node downloader hit its five-minute network bound for the official CMake
archive; a resumable `curl` download of the same locked URL was fed through the
unchanged repository SHA-256, archive-entry, installed-file, CMake and CTest
verifier before use.

The private Go cache, fixture binaries, verified CMake runtime, downloaded
archive, temporary pnpm shim, Python cache and smoke state were removed. No Go,
coverage execution, protocol or Extension runtime source changed, and no push
was performed.

## Native WFP migration evidence — 2026-08-21 Task 5

The former PowerShell guard path has now been replaced for new runs by the
native dynamic-session WFP guardian. Required Windows CI runs a privileged Go
lifecycle test and the TypeScript default-sibling sequencing test before the
coverage Service/native smoke. The integration covers unique V4/V6 filters,
blocked outbound connects, normal release, guardian crash, owner termination,
dynamic-object disappearance through a fresh observer, and creation-time PID
reuse rejection. Local WFP access denial is the closed `WFPAccessDenied`
result; required mode fails and never skips.

The current host was not privileged and had no verified LLVM toolset, so its
real Go path skipped with `WFPAccessDenied` and its real TypeScript path skipped
with `ToolchainUnavailable`; their required-mode controls failed as designed.
No native PASS is claimed. Full Go, race, vet, Linux cross-compile, Node build,
generated-file and package gates passed. The exact root/workspace gate remained
environment-limited solely by `spawnSync cmake ENOENT`; package suites were run
and passed separately. Detailed RED/GREEN commands, security review, exact
skip/fail boundaries and handoff concerns are recorded in
`.superpowers/sdd/2026-08-21-windows-wfp-offline-boundary-plan/task-5-report.md`.

### Task 5 review fix round 1

Review follow-up closed the remaining native-boundary lifecycle gaps. Coverage
smoke now stops Service, closes the guardian, removes its fixture and only then
publishes evidence. The TypeScript boundary monitors socket frames and child
exit continuously after `Ready`, rejects pending reads on every terminal socket
event, bounds close/termination, and aborts guarded native work plus Service
cleanup on liveness loss. Go Add/Audit/Ready failures now join every engine
close failure with `SessionCloseFailed` and report fixed cleanup code 3 before
any access-denied classification. Focused regressions, full Service Probe and
Extension suites, full Go/race/vet, Linux cross-compiles, generated checks and
root build passed; the exact root test retained only the previously documented
host `cmake ENOENT` boundary. Detailed RED/GREEN evidence is in the Task 5
report.
