# Phase 9 Batch F2 Framework Matrix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Consume the committed Phase 9 Batch F1 inputs to produce candidate-bound, hosted four-toolchain evidence for CppUTest/CppUMock and Unity/CMock without changing release, signing, or legal state.

**Architecture:** Extend the existing native Service E2E harness so each supported runner executes the real committed fixtures through the Service protocol, never by invoking framework binaries directly from a client-controlled command. A closed report builder will bind the candidate commit, F1 manifest/tree/CMock provenance digests, toolchain identity, stable test IDs, and the complete 17-scenario contract; the existing P4 matrix validator will consume one Windows and one Linux report.

**Tech Stack:** Go test-service and framework adapters, TypeScript service-probe E2E harness, Node.js canonical JSON/report validation, CMake 4.3.4 bundle, GitHub Actions pinned hosted runners.

**Spec:** `docs/superpowers/specs/2026-09-16-phase9-batch-f1-framework-inputs-design.md` (F2 handoff contract) and `docs/superpowers/specs/2026-07-30-test-framework-discovery-execution-design.md` (native adapter and scenario requirements).

## Global Constraints

- Consume only the committed F1 schema-v2 lock, prepared source identity, real fixtures, generated CMock outputs, and `cmock-generation.json`; F2 must not download dependencies, run Ruby/CMock, or rewrite F1 provenance.
- Execute exactly four hosted toolchains: Windows `msvc`, `clang-cl`; Linux `gcc`, `clang`; a missing required compiler is a failure, never a skip.
- Produce exactly 17 ordered scenarios per framework: `all`, `assertion-failure`, `cancel`, `crash`, `discovery`, `failed-rerun`, `filter`, `malformed-output`, `mock-failure`, `opaque-fallback`, `reconnect-replay`, `repeat`, `service-restart`, `single`, `skip`, `stale-catalog`, `timeout`.
- Keep reports path-free and closed; every scenario binds candidate commit, platform, toolchain family, catalog revision, source artifact digest, source-location digest, executable digest, and a unique result artifact digest.
- Preserve the existing three Phase 8 deferred gates and `releaseReady=false`; do not sign, tag, publish a Release, or obtain legal approval.
- Hosted actions use the repository's existing full commit pins, `ubuntu-24.04`/`windows-2022`, pnpm `11.4.0`, and Node `24.18.0`.

---

### Task 1: Lock the F2 report and scenario contracts with failing tests

**Files:**
- Create: `tools/service-probe/src/native-framework-report.ts`
- Test: `tools/service-probe/src/native-framework-report.test.ts`
- Modify: `tools/phase9/p4-report.schema.json`
- Test: `tools/phase9/p4-report.test.mjs`

**Interfaces:**
- `buildFrameworkPlatformReport(input: FrameworkPlatformReportInput): FrameworkPlatformReport` returns one closed, canonical report for `linux` or `win32`.
- `validateFrameworkScenarioSet(value: unknown): asserts value is readonly FrameworkScenarioEvidence[]` enforces the exact 17 IDs and order.
- `FrameworkScenarioEvidence` carries `id`, `candidateCommit`, `platform`, `toolchainFamily`, `frameworkId`, `catalogRevision`, `sourceArtifactSha256`, `sourceLocationDigest`, `executableArtifactSha256`, `resultArtifactSha256`, `observedOutcome`, and `classification`.

- [ ] **Step 1: Write failing tests** for missing report fields, duplicate/reordered scenario IDs, path-bearing strings, wrong F1 digests, duplicate result digests, and a report containing fewer than both platform toolchains.
- [ ] **Step 2: Run the tests**

    pnpm --filter @unit-test-ide/service-probe test -- native-framework-report.test.ts
    node --test tools/phase9/p4-report.test.mjs

  Expected: FAIL because the report builder and closed schema extensions do not exist.
- [ ] **Step 3: Implement the minimal closed report model** using the existing canonical JSON and P4 constants; reject absolute paths, extra keys, unsafe IDs, mutable provenance, and non-unique result digests.
- [ ] **Step 4: Run the focused tests** and require PASS.
- [ ] **Step 5: Commit**

    git add tools/service-probe/src/native-framework-report.ts tools/service-probe/src/native-framework-report.test.ts tools/phase9/p4-report.schema.json tools/phase9/p4-report.test.mjs
    git commit -m "test: lock phase9 framework matrix report contract"

### Task 2: Add a Service-driven framework scenario runner

**Files:**
- Create: `tools/service-probe/src/native-framework-matrix.ts`
- Test: `tools/service-probe/src/native-framework-matrix.test.ts`
- Modify: `tools/service-probe/src/native-build.ts`
- Modify: `tools/service-probe/src/native-run.ts`

**Interfaces:**
- `runFrameworkMatrix(options: FrameworkMatrixOptions): Promise<FrameworkMatrixResult>` consumes an F1 fixture workspace and a running `TaskServiceFixture`, returning the ordered 17 scenario records for one framework/toolchain.
- `runFrameworkPlatform(options: FrameworkPlatformOptions): Promise<FrameworkPlatformReport>` runs both frameworks for one platform/toolchain set and writes `framework-report.json` atomically below `.native-e2e/artifacts/linux` or `.native-e2e/artifacts/windows`.
- The runner uses `inspectWorkspace`, `discover`, `run`, `cancelTask`, `reconnect`, and `getTask`; it never accepts a shell command or launches CMock/Ruby/Docker.

- [ ] **Step 1: Write failing tests** with a fake protocol client proving all 17 scenario IDs are emitted in order, each task is selected from the catalog, cancellation/reconnect/service restart use bounded deadlines, and arbitrary command arguments are rejected.
- [ ] **Step 2: Run the tests**

    pnpm --filter @unit-test-ide/service-probe test -- native-framework-matrix.test.ts

  Expected: FAIL because the runner is absent.
- [ ] **Step 3: Implement bounded scenario execution** by reusing the existing native task helpers and fixture workspaces; map CppUTest and Unity adapter outcomes to the exact P4 classifications and retain raw output only behind validated evidence digests.
- [ ] **Step 4: Integrate the runner** after each toolchain workspace is launched, while preserving the existing CMake/toolchain report and cleanup paths.
- [ ] **Step 5: Run focused TypeScript tests** and require PASS.
- [ ] **Step 6: Commit**

    git add tools/service-probe/src/native-framework-matrix.ts tools/service-probe/src/native-framework-matrix.test.ts tools/service-probe/src/native-build.ts tools/service-probe/src/native-run.ts
    git commit -m "test: run framework matrix through service"

### Task 3: Bind F1 fixture identity and stable cross-platform digests

**Files:**
- Modify: `tools/service-probe/src/native-framework-matrix.ts`
- Test: `tools/service-probe/src/native-framework-matrix.test.ts`
- Modify: `tools/framework-bundle/consume.mjs`
- Test: `tools/framework-bundle/consume.test.mjs`

**Interfaces:**
- `loadF1FrameworkIdentity(repositoryRoot: string): Promise<F1FrameworkIdentity>` reads only committed manifest, fixture metadata, generated outputs, and checked cache; it returns manifest SHA-256, framework tree SHA-256, CMock provenance SHA-256, and fixture source/executable digests.
- `stableFrameworkIdDigest(frameworkId: string, catalog: Catalog, provenance: F1FrameworkIdentity): string` is deterministic across platform, compiler, build directory, timestamps, and array indexes.

- [ ] **Step 1: Add failing tests** for manifest/tree/CMock drift, mixed-case path ordering, timestamp-only changes, platform/compiler substitution, and missing generated output.
- [ ] **Step 2: Run the tests**

    node --test tools/framework-bundle/consume.test.mjs tools/service-probe/src/native-framework-matrix.test.ts

  Expected: FAIL on the new identity and digest assertions.
- [ ] **Step 3: Implement read-only F1 identity loading** by calling the existing framework-bundle validators; reject any cache or fixture that is not byte-identical to the committed lock.
- [ ] **Step 4: Bind stable IDs and result artifacts** to canonical catalog/source/provenance bytes, never to absolute paths, compiler versions, timestamps, or ordinal array positions.
- [ ] **Step 5: Run focused tests and commit**

    git add tools/service-probe/src/native-framework-matrix.ts tools/service-probe/src/native-framework-matrix.test.ts tools/framework-bundle/consume.mjs tools/framework-bundle/consume.test.mjs
    git commit -m "test: bind framework evidence to f1 identities"

### Task 4: Exercise real CppUTest/CppUMock and Unity/CMock fixtures locally

**Files:**
- Modify: `tools/service-probe/src/native-build.ts`
- Modify: `tools/service-probe/src/native-report.ts`
- Test: `tools/service-probe/src/native-build-windows.test.ts`
- Test: `tools/service-probe/src/native-build-linux.test.ts`
- Modify: `docs/native-e2e.md`

**Interfaces:**
- Windows runs `msvc,clang-cl`; Linux runs `gcc,clang`; each family executes both committed fixtures through Service and writes one platform `framework-report.json`.
- A successful report contains 2 toolchains × 2 frameworks × 17 scenarios; any missing compiler, fixture, scenario, provenance field, or report file fails the job.

- [ ] **Step 1: Add red integration assertions** that the native platform report is required, contains both frameworks, and has 17 scenarios per framework/toolchain.
- [ ] **Step 2: Run the tests**

    pnpm --filter @unit-test-ide/service-probe test -- native-build-windows.test.ts native-build-linux.test.ts

  Expected: FAIL because the native build currently writes only `toolchain-report.json`.
- [ ] **Step 3: Wire real fixture execution** into the existing native matrix after F1 `check:framework-bundle`; keep all process trees, deadlines, workspace cleanup, and redaction rules intact.
- [ ] **Step 4: On Windows, build and run both toolchains**

    go -C apps/test-service build -trimpath -o ../../build/unity-runner-generator.exe ./cmd/unity-runner-generator
    pnpm prepare:framework-bundle
    $cmake = (node tools/cmake-bundle/prepare.mjs | ConvertFrom-Json).executable
    $generator = (Resolve-Path build\\unity-runner-generator.exe).Path
    pnpm verify:framework-fixtures -- --cmake $cmake --generator $generator --toolchains msvc,clang-cl --frameworks cpputest,unity
    pnpm test:e2e:native -- --platform win32

  Expected: both Windows toolchains produce the full 34-scenario framework matrix; no skip is accepted.
- [ ] **Step 5: Run focused tests and commit**

    git add tools/service-probe/src/native-build.ts tools/service-probe/src/native-report.ts tools/service-probe/src/native-build-windows.test.ts tools/service-probe/src/native-build-linux.test.ts docs/native-e2e.md
    git commit -m "test: execute real framework fixtures in native matrix"

### Task 5: Produce and validate the cross-platform P4 matrix artifact

**Files:**
- Modify: `tools/phase9/p4-report.mjs`
- Test: `tools/phase9/p4-report.test.mjs`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`
- Modify: `package.json`

**Interfaces:**
- `pnpm test:native-framework-matrix` runs the closed producer/validator suite.
- The trusted runtime producer publishes only `.native-e2e/framework-runtime/{windows|linux}.json` and the owned `.native-e2e/framework-work/{windows|linux}/{toolchain}/{cpputest|unity}/{service,workspace}` trees. Runtime inputs are not downloaded platform reports.
- Required native execution consumes those fixed runtime outputs and writes `.native-e2e/artifacts/{windows|linux}/framework-report.json`. The aggregator downloads the two reports into `.native-e2e/framework-inputs/{windows|linux}`; these are aggregation input directories, not runtime producer outputs.
- `node tools/phase9/p4-report.mjs --windows .native-e2e/framework-inputs/windows/framework-report.json --linux .native-e2e/framework-inputs/linux/framework-report.json --candidate \"$GITHUB_SHA\" --out .superpowers/phase9/p4/native-framework-matrix-report.json` remains the sole matrix aggregation command over the downloaded reports.

- [ ] **Step 1: Add failing contract tests** for exact artifact names, candidate SHA binding, two platform reports, eight toolchain/framework blocks, 136 scenario records, and refusal of unsigned or path-bearing substitutions.
- [ ] **Step 2: Run the contract tests**

    node --test tools/phase9/p4-report.test.mjs tools/workspace-smoke/workspace-smoke.test.mjs

  Expected: FAIL until the producer output and package script are wired.
- [ ] **Step 3: Implement the closed artifact command and package script**; preserve the existing P4 schema and make matrix output deterministic and path-free.
- [ ] **Step 4: Run focused validation**

    pnpm test:native-framework-matrix
    pnpm check:phase9-gates
    git diff --check

- [ ] **Step 5: Commit**

    git add tools/phase9/p4-report.mjs tools/phase9/p4-report.test.mjs tools/workspace-smoke/workspace-smoke.test.mjs package.json
    git commit -m "test: validate phase9 framework matrix artifact"

### Task 6: Wire the hosted four-toolchain workflow and artifact contract

**Files:**
- Modify: `.github/workflows/foundation.yml`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`
- Modify: `docs/native-e2e.md`

**Interfaces:**
- Independent `verify-framework-windows` on fixed `windows-2022` prepares the trusted Windows runtime and publishes `native-framework-windows` only after required `msvc` and `clang-cl` execution completes. The existing `verify-windows` and `verify-windows-wfp` administrator/WFP paths are unchanged and are not this producer.
- `verify-linux` prepares the trusted Linux runtime and runs required `gcc` and `clang` execution through the existing offline wrapper after dependency preparation; it publishes `native-framework-linux` only after both complete.
- `verify-framework-matrix` depends on exactly `verify-framework-windows` and `verify-linux`, downloads both exact artifacts, runs the fixed P4 aggregator, and uploads `native-framework-matrix-report` with retention 14 days and error-on-missing behavior.

- [x] **Step 1: Extend failing workflow contract tests** to require fixed runners, `UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED`, no mutable actions or secrets, exact artifact paths, and `if-no-files-found: error`.
- [x] **Step 2: Run the workflow contract tests** and observe failure for the missing F2 producer invocation.
- [x] **Step 3: Wire the independent `verify-framework-windows` and offline `verify-linux` producer paths** to prepare the fixed runtime and call the Service-driven framework matrix after F1 bundle validation; do not add administrator/WFP privileges, release inputs, signing, or network downloads after namespace entry.
- [x] **Step 4: Run local static checks**

    node --test tools/workspace-smoke/workspace-smoke.test.mjs tools/linux-offline/run.test.mjs
    git diff --check

- [x] **Step 5: Commit the static workflow wiring** (completed by the trusted-runtime follow-up, `5213943` and hardening `28ee186`; no hosted execution is implied).

    git add .github/workflows/foundation.yml tools/workspace-smoke/workspace-smoke.test.mjs docs/native-e2e.md
    git commit -m "ci: run phase9 framework matrix on four toolchains"

### Task 7: Run hosted evidence, audit the receipt, and update the candidate matrix

**Files:**
- Create: `docs/superpowers/evidence/phase9/receipts/github-actions-${GITHUB_RUN_ID}-f2-framework.json`
- Modify: `docs/superpowers/evidence/phase9/baseline.json`
- Modify: `docs/superpowers/evidence/phase9/gate-matrix.json`
- Modify: `docs/superpowers/evidence/phase9/gate-matrix.md`
- Modify: `docs/superpowers/plans/2026-09-16-phase9-batch-f2-framework-matrix.md`

**Interfaces:**
- The receipt binds one successful producer attempt, candidate SHA, Windows/Linux job IDs, exact artifact IDs/names/digests, and all five P4 gate IDs.
- Matrix regeneration must yield zero `FAILED`, all F2-proven P4 rows `PASS`, exactly the three approved Phase 8 `DEFERRED` rows, and `releaseReady=false`.

- [ ] **Step 1: After explicit user authorization, push only the F2 branch and dispatch the foundation workflow**; stop if any required job is skipped or fails.
- [ ] **Step 2: Download artifacts by immutable ID** and verify candidate SHA, F1 manifest/tree/CMock digests, 136 scenario records, exact toolchain set, and absence of local paths/secrets.
- [ ] **Step 3: Record the closed receipt and run `node tools/phase9/audit.mjs` plus `pnpm check:phase9-gates`.**
- [ ] **Step 4: Commit evidence only**; do not tag, release, sign, merge remotely, or alter Phase 8 deferred rows.

### Task 8: Complete verification and handoff

**Files:**
- Modify: `docs/superpowers/plans/2026-09-16-phase9-batch-f2-framework-matrix.md`
- Modify: ignored `.superpowers/sdd/2026-09-16-phase9-batch-f2-framework-matrix/progress.md`

- [x] **Step 1: Run the complete local gate** (2026-09-17 trusted-runtime follow-up; local verification only).

    pnpm verify
    git diff --check

- [ ] **Step 2: Confirm the matrix** has all intended P4 `PASS` rows, no `FAILED` rows, three Phase 8 `DEFERRED` rows, and `releaseReady=false`.
- [ ] **Step 3: Record the four-toolchain hosted run IDs, attempts, artifact digests, and any environment-only skips; Linux native evidence must be reported as PASS only when hosted jobs actually pass.
- [ ] **Step 4: Obtain final code review approval and stop before release operations.

## Trusted-runtime follow-up and hosted handoff (2026-09-17)

- [x] Trusted runtime producer implementation is locally complete under `2026-09-17-phase9-f2-trusted-runtime-producer.md`: closed manifests, owned staging, Service-derived compiler/catalog/executable identity, audited benchmark input, coordinated atomic publication, and required runtime consumption.
- [x] Static hosted workflow wiring is locally complete: independent `verify-framework-windows`, offline `verify-linux`, and the exact two-artifact P4 aggregator contract. These checks do not execute the hosted four-toolchain matrix.
- [x] Final local verification passed with bundled Node 24.19.0 and pinned pnpm 11.4.0: all four focused producer suites, workflow/offline checks, full service-probe, framework-bundle, P4 matrix, workspace, Phase 9 consistency, and `pnpm verify` (including Go race checks and 20/20 Service E2E tests). Only existing platform-dependent/opt-in skips were accepted; generated tracked files and recorded evidence are unchanged. The ignored Task 7 report records the corrected environment, separately repaired stale script assertion, and initial CppUTest timeout followed by unchanged successful reruns.
- [ ] Hosted `msvc`, `clang-cl`, `gcc`, and `clang` execution and candidate-bound 136-scenario evidence remain incomplete; remote execution requires separate user authorization/actions.
- [ ] Immutable hosted artifact audit, receipt creation, and gate-matrix update/promotion remain incomplete. Task 7 and Task 8's hosted evidence/approval steps above remain open; local success is not hosted evidence.
- [ ] Push, PR creation, merge, signing, Release publication, and legal approval remain incomplete and require separate authorization/user actions. None is authorized by this local handoff.

The recorded Phase 9 evidence is unchanged, including the three Phase 8 deferred gates and `releaseReady=false`. A passing local `check:phase9-gates` confirms consistency of that existing record, not P4 acceptance or release readiness.

## Self-review

- Every F2 handoff requirement is mapped: four hosted toolchains (Tasks 4 and 6), 17 scenarios per framework (Tasks 1–4), platform reports and cross-platform stable digests (Tasks 1, 3, and 5), workflow artifacts and online audit (Tasks 6–7), and candidate-bound receipts (Task 7).
- No task permits dependency downloads, CMock generation, signing, legal approval, Release publication, or mutation of F1 provenance.
- All commands use the repository's existing package scripts, pinned hosted runners, and current report validator interfaces; no new shell-input surface is introduced.
