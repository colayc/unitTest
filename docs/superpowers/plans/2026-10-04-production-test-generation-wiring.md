# Production Test Generation Wiring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the shipped Code-OSS product expose a complete, offline Protocol v1.6 test-generation workflow that produces maintainable C/C++ tests, executes them, and reports file/function coverage without manual bundle copying or system-tool fallback.

**Architecture:** Keep generation inside the existing Go `unit-test-service`. The built-in extension resolves one release-root contract and passes explicit verified bundle roots; runtime composes the existing analysis, solver, renderer, validator, publisher, managed registry, and coverage-detail components behind one atomic readiness gate. The same production path is exercised by release-shaped smoke tests and the Windows/Linux native matrix.

**Tech Stack:** Go 1.26.6, TypeScript 6, Node.js 24.18+, pnpm 11.4.0, Code-OSS extension APIs, local JSON-line protocol v1.5/v1.6, SQLite, CMake, Clang/LLVM, GCC/MSVC/clang-cl, Unity, CppUTest.

**Spec:** `docs/superpowers/specs/2026-10-04-production-test-generation-wiring-design.md`

## Global Constraints

- The product remains Code-OSS plus the built-in extension, the existing local `unit-test-service`, fixed tool bundles, and license files; do not add another service process.
- Generation is completely offline after bundle preparation. Do not add a cloud API, local LLM, telemetry payload, or network fallback.
- Do not generate or use Mock, Stub, CMock, or CppUMock.
- Production mode may execute only manifest-verified product tools and trusted workspace compiler/test targets; never search `PATH` for a replacement.
- The extension submits only service-issued IDs and explicit user decisions; the Go service remains authoritative for paths, snapshots, coverage, candidates, and writes.
- Production source is read-only. Before explicit acceptance, all generated source and CMake edits remain in a service-owned staging root.
- Generation capability is atomic: advertise compatible v1.5 and full v1.6 together only when every production and managed dependency is ready.
- Preserve v1.0-v1.5 wire semantics and the existing 1 MiB/2 MiB protocol limits; large v1.6 data remains paginated.
- Windows signing, final third-party license/legal approval, and GitHub Release publication remain deferred and must not be enabled by this plan.

## Review Focus

- A packaged extension installed under an unexpected, symlinked, or escaped layout must fail before spawning the service; Task 2 pins this with layout tests.
- A bundle file replaced between startup verification and execution must invalidate readiness or the run; Tasks 3 and 6 pin identity revalidation.
- Workspace generation, compile inputs, or coverage report changing during generation must make the preview stale and write zero bytes; Tasks 5 and 6 test every boundary.
- A user-edited managed block must produce a three-way conflict while bytes outside managed blocks remain exact; Task 7 tests this end to end.
- An unsupported compiler/framework or missing product bundle must return a bounded stable unavailable reason and must never fall back to `PATH`; Tasks 3, 5, and 8 test this behavior.

---

### Task 1: Close the release-root resource inventory

**Files:**
- Modify: `tools/release/release-config.json`
- Modify: `tools/release/stage.mjs`
- Test: `tools/release/stage.test.mjs`
- Modify: `tools/release/producer/source-manifest.mjs`
- Test: `tools/release/producer/source-manifest.test.mjs`
- Test: `tools/release/producer/workflow-contract.test.mjs`
- Modify: `.github/workflows/release-inputs.yml`

**Interfaces:**
- Consumes: existing prepared CMake, coverage, testgen, and reviewed framework inputs.
- Produces: one release root containing `service/unit-test-service[.exe]`, `bundles/cmake`, `bundles/coverage`, `bundles/testgen`, and `bundles/framework`; all entries are bound by `release-manifest.json`.

- [ ] **Step 1: Write failing staging tests**

Add tests named `stageRelease binds every production generation resource under one root` and `stageRelease rejects a missing or extra generation resource before publication`. Assert the exact relative paths, license inventory, artifact kinds, and manifest digests; assert staging publishes no final root on failure.

- [ ] **Step 2: Run the release tests and verify the new assertions fail**

Run: `node --test tools/release/stage.test.mjs tools/release/producer/source-manifest.test.mjs tools/release/producer/workflow-contract.test.mjs`

Expected: FAIL because the release contract does not yet bind every production generation resource.

- [ ] **Step 3: Implement the closed resource inventory**

Add `frameworkBundlePath: "bundles/framework"`, required `frameworkRoot`, and CLI option `--framework-root`. Update `stageRelease(input)` to validate, copy, license-audit, classify, and manifest the reviewed Unity/CppUTest inputs without changing the existing `app/`, `service/`, or `bundles/` root relationship. Update producer source and workflow contracts to transport the same bytes.

- [ ] **Step 4: Run focused release tests**

Run: `node --test tools/release/stage.test.mjs tools/release/producer/source-manifest.test.mjs tools/release/producer/workflow-contract.test.mjs`

Expected: PASS with no signing or Release publication.

- [ ] **Step 5: Commit**

```bash
git add tools/release .github/workflows/release-inputs.yml
git commit -m "build: bind generation resources into release root"
```

### Task 2: Resolve the installed product layout in the built-in extension

**Files:**
- Create: `apps/code-oss-extension/src/service-layout.ts`
- Test: `apps/code-oss-extension/test/service-layout.test.ts`
- Modify: `apps/code-oss-extension/src/extension.ts`
- Modify: `apps/code-oss-extension/src/service-manager.ts`
- Test: `apps/code-oss-extension/test/extension.test.ts`
- Test: `apps/code-oss-extension/test/service-manager.test.ts`
- Modify: `apps/code-oss-extension/package.json`

**Interfaces:**
- Produces: `resolveProductLayout(extensionPath: string, platform: NodeJS.Platform, developmentMode: boolean): ProductLayout`.
- Produces: `ProductLayout` with absolute `serviceExecutable`, `cmakeBundleRoot`, `coverageBundleRoot`, `testgenBundleRoot`, and `frameworkBundleRoot`.
- Consumes in `ServiceManagerOptions`: `layout: ProductLayout`; service launch emits explicit `--cmake-bundle-root`, `--coverage-bundle-root`, `--testgen-bundle-root`, and `--framework-bundle-root` arguments.

- [ ] **Step 1: Write failing layout and launch tests**

Cover the exact staged path `app/extensions/unit-test-ide`, Windows and Linux service names, paths containing spaces, development overrides, unexpected depth, missing components, symlink/junction/reparse escape, UNC/device paths, and arguments containing no duplicates. Assert invalid layouts fail before `spawnService`.

- [ ] **Step 2: Run focused extension tests and verify failure**

Run: `pnpm --filter code-oss-extension build && node --test apps/code-oss-extension/dist/test/service-layout.test.js apps/code-oss-extension/dist/test/service-manager.test.js apps/code-oss-extension/dist/test/extension.test.js`

Expected: FAIL because `ProductLayout` and the bundle arguments do not exist.

- [ ] **Step 3: Implement layout resolution and service launch**

Keep production resolution derived only from the canonical extension installation root and the fixed release-root relationship. Preserve the existing approved development-only executable override, but require explicit development bundle roots and reject partial overrides.

- [ ] **Step 4: Run the focused extension suite**

Run: `pnpm --filter code-oss-extension test`

Expected: all extension tests PASS, and all sensitive paths remain redacted from startup errors.

- [ ] **Step 5: Commit**

```bash
git add apps/code-oss-extension
git commit -m "fix: resolve packaged generation service resources"
```

### Task 3: Verify explicit bundle roots in the service runtime

**Files:**
- Modify: `apps/test-service/cmd/unit-test-service/main.go`
- Test: `apps/test-service/cmd/unit-test-service/main_test.go`
- Create: `apps/test-service/internal/runtime/product_bundles.go`
- Test: `apps/test-service/internal/runtime/product_bundles_test.go`
- Modify: `apps/test-service/internal/runtime/runtime.go`
- Modify: `apps/test-service/internal/runtime/coverage_execution.go`
- Test: `apps/test-service/internal/runtime/runtime_test.go`
- Test: `apps/test-service/internal/runtime/coverage_execution_test.go`

**Interfaces:**
- Produces CLI flags: `--coverage-bundle-root <absolute>`, `--testgen-bundle-root <absolute>`, and `--framework-bundle-root <absolute>`, alongside the existing `--cmake-bundle-root`.
- Produces: `ProductBundleRoots{CMake, Coverage, Testgen, Framework string}` in `runtime.Config`.
- Produces: `openProductBundles(ProductBundleRoots, platform string) (*ProductBundles, error)`; `ProductBundles` exposes only verified/pinned handles, never raw fallback commands.

- [ ] **Step 1: Write failing CLI and bundle-boundary tests**

Test missing roots, relative roots, root aliases, paths outside the common product root, symlink/reparse components, wrong platform, missing license/READY files, manifest mismatch, extra files, and a post-open replacement attempt. Assert no coverage or generation backend is constructed and errors use fixed categories without raw paths.

- [ ] **Step 2: Run focused Go tests and verify failure**

Run: `go test ./apps/test-service/cmd/unit-test-service ./apps/test-service/internal/runtime -run 'ProductBundle|ExplicitBundle|CoverageBundleRoot' -count=1`

Expected: FAIL because coverage still derives a service-relative directory and no explicit testgen root is consumed.

- [ ] **Step 3: Implement root parsing, anchoring, and verification**

Use the existing `testgenbundle.Open`, CMake resolver verification, and coverage bundle verifier. Remove service-directory bundle inference from production. Keep dependency seams for tests, but do not permit production fallback to `PATH` or current working directory.

- [ ] **Step 4: Run focused tests and race checks**

Run: `go test -race ./apps/test-service/cmd/unit-test-service ./apps/test-service/internal/runtime`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/test-service/cmd/unit-test-service apps/test-service/internal/runtime
git commit -m "feat: verify explicit production generation bundles"
```

### Task 4: Add an atomic production generation composition

**Files:**
- Create: `apps/test-service/internal/runtime/production_generation.go`
- Test: `apps/test-service/internal/runtime/production_generation_test.go`
- Modify: `apps/test-service/internal/runtime/runtime.go`
- Modify: `apps/test-service/internal/runtime/test_generation.go`
- Modify: `apps/test-service/internal/server/service.go`
- Test: `apps/test-service/internal/session/session_test.go`
- Test: `apps/test-service/internal/server/service_test.go`

**Interfaces:**
- Produces: `ProductionGenerationConfig` containing the verified bundles, store, runtime authorities, publisher, driver, selected-output validator, managed baseline/receipt providers, and managed stores.
- Produces: `newProductionGenerationBackend(ProductionGenerationConfig) (session.GenerationBackend, error)`.
- Changes `Runtime.GenerationBackend()` to return the complete managed provider only when `TestGenerationReady()` and `ManagedTestsReady()` are both true.

- [ ] **Step 1: Write failing atomic-readiness tests**

Use a table that removes each dependency one at a time: trusted workspace, coverage backend, current coverage index, every verified bundle, store migration, driver, publisher, validator, baseline, receipts, managed registry, and review store. Assert v1.5 and v1.6 generation methods are both absent for every incomplete row and both present only for the complete row.

- [ ] **Step 2: Run runtime/session tests and verify failure**

Run: `go test ./apps/test-service/internal/runtime ./apps/test-service/internal/session ./apps/test-service/internal/server -run 'ProductionGeneration|GenerationCapability|ManagedReady' -count=1`

Expected: FAIL because production does not construct a managed provider and the base service reports managed readiness as false.

- [ ] **Step 3: Implement internal composition**

Construct the existing base `generationService`, then `ManagedRuntimeProvider`, and store only the completed provider on `Runtime`. Retain explicit fake factories for unit tests; production `main` must select the verified production factory.

- [ ] **Step 4: Run focused race tests**

Run: `go test -race ./apps/test-service/internal/runtime ./apps/test-service/internal/session ./apps/test-service/internal/server`

Expected: PASS with unchanged v1.0-v1.5 non-generation behavior.

- [ ] **Step 5: Commit**

```bash
git add apps/test-service/internal/runtime apps/test-service/internal/session apps/test-service/internal/server
git commit -m "feat: compose atomic production generation backend"
```

### Task 5: Implement deterministic production target resolution and candidate generation

**Files:**
- Create: `apps/test-service/internal/runtime/production_generation_driver.go`
- Test: `apps/test-service/internal/runtime/production_generation_driver_test.go`
- Create: `apps/test-service/internal/runtime/production_generation_pipeline.go`
- Test: `apps/test-service/internal/runtime/production_generation_pipeline_test.go`
- Modify: `apps/test-service/internal/runtime/production_generation.go`

**Interfaces:**
- Produces: `productionGenerationDriver`, implementing `GenerationDriver` and `ManagedRuntimeDriver`.
- Produces internal immutable `generationTarget` bound to project ID, workspace generation, source digest, compile-input digest, target/toolchain/framework identity, coverage report ID, file/function/gap IDs, and language.
- Uses existing `testgenanalysis.Analyzer`, `testgensolver.Solver`, `testgenassert.Derive`, and `testgenrender.Render`/`RenderManagedFile`.

- [ ] **Step 1: Write failing target-resolution tests**

Cover file, function, and coverage-gap IDs; C versus C++; Unity versus CppUTest; ambiguous symbols; templates/macros; unsafe effects; unsupported framework/compiler; stale workspace generation; stale coverage report; changed compile database; and malicious response/plugin arguments. Assert unsupported inputs return closed reason codes and start no process.

- [ ] **Step 2: Write failing deterministic pipeline tests**

For safe branch fixtures, assert exact stage order, stable candidate IDs, stable generated bytes, no duplicate scenarios, bounded solver inputs, descriptive Arrange–Act–Assert output, and absence of case-insensitive `mock`, `stub`, `cmock`, and `cppumock` tokens.

- [ ] **Step 3: Run the new tests and verify failure**

Run: `go test ./apps/test-service/internal/runtime -run 'ProductionTarget|ProductionGenerationPipeline' -count=1`

Expected: FAIL because no production driver exists.

- [ ] **Step 4: Implement resolution and generation stages**

Map existing coordinator states to one product-owned action each. Re-read all authoritative identities before each stage, enforce `testgencoord.Budget`, and persist only closed models/digests/artifacts through the existing store.

- [ ] **Step 5: Run focused tests**

Run: `go test -race ./apps/test-service/internal/runtime ./apps/test-service/internal/testgenanalysis ./apps/test-service/internal/testgensolver ./apps/test-service/internal/testgenassert ./apps/test-service/internal/testgenrender`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add apps/test-service/internal/runtime
git commit -m "feat: generate deterministic production test candidates"
```

### Task 6: Execute real isolated validation and close v1.5 publication

**Files:**
- Create: `apps/test-service/internal/runtime/production_generation_validation.go`
- Test: `apps/test-service/internal/runtime/production_generation_validation_test.go`
- Modify: `apps/test-service/internal/runtime/production_generation_driver.go`
- Modify: `apps/test-service/internal/runtime/production_generation_pipeline.go`
- Test: `apps/test-service/internal/runtime/test_generation_test.go`
- Test: `apps/test-service/internal/testgenvalidate/validator_test.go`

**Interfaces:**
- Produces: a trusted `testgenvalidate.StageExecutor` that prepares only fixed configure, compile/link, candidate-test, regression-test, and coverage plans.
- Produces: durable candidate evidence binding assertion proof, source snapshot, compile inputs, product bundles, process owner, baseline coverage, after coverage, and per-target delta.
- `ValidateCandidate` replays evidence checks; `CandidateSet` returns only retained candidates to the existing atomic `testgenpublish.Publisher`.

- [ ] **Step 1: Write failing real-loop and fault tests**

Use safe C/Unity and C++/CppUTest fixtures. Assert configure, compile/link, candidate run, full regression, coverage collection, and delta verification occur in order. Inject timeout, cancellation, child escape, output flood, bundle replacement, source/compile/baseline drift, assertion mismatch, flaky output, coverage regression, zero delta, cleanup failure, and publisher conflict; assert zero workspace writes before acceptance.

- [ ] **Step 2: Run focused validation tests and verify failure**

Run: `go test ./apps/test-service/internal/runtime ./apps/test-service/internal/testgenvalidate ./apps/test-service/internal/testgenpublish -run 'ProductionValidation|Generation|Candidate' -count=1`

Expected: FAIL because production has no real trusted stage executor.

- [ ] **Step 3: Implement the isolated validation adapter**

Reuse existing build/test/coverage planners and `processcontrol`; never create argv from IPC fields. Require at least one target function/line/branch improvement, no metric regression, and successful existing-suite regression before retaining a candidate.

- [ ] **Step 4: Prove v1.5 end-to-end locally**

Run: `go test -race ./apps/test-service/internal/runtime ./apps/test-service/internal/session ./apps/test-service/internal/testgenvalidate ./apps/test-service/internal/testgenpublish`

Expected: PASS; an explicit accept is the sole operation that changes the fixture workspace.

- [ ] **Step 5: Commit**

```bash
git add apps/test-service/internal/runtime apps/test-service/internal/testgenvalidate apps/test-service/internal/testgenpublish
git commit -m "feat: validate and publish generated tests offline"
```

### Task 7: Complete managed v1.6 generation and maintenance

**Files:**
- Create: `apps/test-service/internal/runtime/production_managed_generation.go`
- Test: `apps/test-service/internal/runtime/production_managed_generation_test.go`
- Modify: `apps/test-service/internal/runtime/managed_runtime_backend.go`
- Modify: `apps/test-service/internal/runtime/managed_runtime_reads.go`
- Test: `apps/test-service/internal/runtime/managed_runtime_backend_test.go`
- Modify: `apps/test-service/internal/taskstore/managed_reviews.go`
- Test: `apps/test-service/internal/taskstore/managed_reviews_test.go`
- Modify: `apps/test-service/internal/taskstore/managed_tests.go`
- Test: `apps/test-service/internal/taskstore/managed_tests_test.go`
- Modify: `apps/test-service/internal/testgenpublish/managed.go`
- Test: `apps/test-service/internal/testgenpublish/managed_test_test.go`

**Interfaces:**
- `productionGenerationDriver` implements `PrepareManaged` and `ManagedCandidateSet` from Task 5.
- Produces production implementations of `managedBaselineProvider`, `managedReceiptProvider`, and `managedValidationProvider` using the current coverage index and Task 6 evidence.
- Uses the existing store as `managedReviewStore`; uses the existing publisher for three-way plan, publication, receipts, and recovery.
- Extends `ResolveManagedStart` and `ListManagedTargets` to resolve service-issued file IDs and function IDs as well as coverage-gap IDs from the same current `CoverageDetailIndex`; no client path or symbol text becomes authoritative.

- [ ] **Step 1: Write failing managed lifecycle tests**

Cover first generation, identical regeneration, source change to `stale`, removed symbol to `orphaned`, user edit to `conflicted`, invalid markers to `invalid`, exact preservation outside managed blocks, all three explicit conflict decisions, stale review digest, interrupted publication, recovery, and duplicate acceptance. Assert no automatic delete and no automatic overwrite.

- [ ] **Step 2: Run managed tests and verify failure**

Run: `go test ./apps/test-service/internal/runtime ./apps/test-service/internal/taskstore ./apps/test-service/internal/testgenpublish -run 'ProductionManaged|ManagedReview|ManagedTest' -count=1`

Expected: FAIL because production does not build a ready managed provider.

- [ ] **Step 3: Implement managed adapters and evidence checks**

Bind every review to owner, project, workspace generation, report, toolchain, source, accepted/current/generated block digests, and Task 6 selected-output receipt. Keep `ManagedTestsReady()` false until recovery and all providers succeed.

- [ ] **Step 4: Run managed race and protocol suites**

Run: `go test -race ./apps/test-service/internal/runtime ./apps/test-service/internal/session ./apps/test-service/internal/taskstore ./apps/test-service/internal/testgenpublish`

Expected: PASS, including paginated file/function coverage and managed records.

- [ ] **Step 5: Commit**

```bash
git add apps/test-service/internal/runtime apps/test-service/internal/taskstore apps/test-service/internal/testgenpublish
git commit -m "feat: enable managed test generation in production"
```

### Task 8: Prove the release-shaped Code-OSS workflow

**Files:**
- Modify: `apps/code-oss-extension/test/extension-host-smoke.mjs`
- Modify: `apps/code-oss-extension/test/extension-host-smoke-support.test.mjs`
- Create: `apps/code-oss-extension/test/test-generation-service-smoke.test.ts`
- Modify: `apps/code-oss-extension/package.json`
- Modify: `tools/release/stage.test.mjs`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`

**Interfaces:**
- Consumes: the exact staged root from Task 1, extension product layout from Task 2, and production backend from Tasks 3-7.
- Produces: one release-shaped smoke receipt proving startup, v1.6 negotiation, file/function/gap generation, preview, explicit accept, one-click run, and updated file/function coverage.

- [ ] **Step 1: Write failing packaged smoke tests**

Start from a copied release-shaped root with repository `node_modules` hidden and system CMake/Clang removed from `PATH`. Assert no manual bundle copy, no development override, `Unit Test IDE` output registration, v1.6 capability, generated managed file, successful test run, updated function coverage, restart persistence, and immediate fail-closed behavior on trust revocation or missing bundle.

- [ ] **Step 2: Run the packaged smoke and verify failure**

Run: `pnpm --filter code-oss-extension build && node --test apps/code-oss-extension/test/extension-host-smoke-support.test.mjs apps/code-oss-extension/dist/test/test-generation-service-smoke.test.js tools/release/stage.test.mjs tools/workspace-smoke/workspace-smoke.test.mjs`

Expected: FAIL until the packaged product reaches the production backend without manual environment repair.

- [ ] **Step 3: Complete extension integration and bounded diagnostics**

Expose stable unavailable reasons in the existing output channel and command UI without raw paths. Do not add a client-side generation fallback or write path.

- [ ] **Step 4: Run Go, extension, and packaged smoke suites**

Run: `go test ./apps/test-service/...`

Run: `pnpm --filter code-oss-extension test`

Run: `node --test tools/release/stage.test.mjs tools/workspace-smoke/workspace-smoke.test.mjs`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/code-oss-extension tools/release/stage.test.mjs tools/workspace-smoke/workspace-smoke.test.mjs
git commit -m "test: prove packaged test generation workflow"
```

### Task 9: Close native matrix, evidence, and Phase 10 status

**Files:**
- Create: `tools/service-probe/src/test-generation-e2e.ts`
- Test: `tools/service-probe/src/test-generation-e2e.test.ts`
- Modify: `tools/service-probe/src/native-run.ts`
- Modify: `tools/service-probe/src/native-report.ts`
- Modify: `tools/service-probe/run-tests.mjs`
- Modify: `tools/phase10/test-generation-report.mjs`
- Test: `tools/phase10/test-generation-report.test.mjs`
- Modify: `tools/phase10/mutation.test.mjs`
- Modify: `tools/phase10/performance.test.mjs`
- Modify: `.github/workflows/foundation.yml`
- Modify: `docs/test-generation.md`
- Modify: `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`

**Interfaces:**
- Produces eight required blocks: Windows MSVC/clang-cl and Linux GCC/Clang, each with Unity and CppUTest.
- Produces closed path-free receipts for generation, execution, file/function coverage delta, mutation effectiveness, fault/security behavior, and performance.
- Updates status only from receipts bound to the exact candidate commit and immutable job/artifact identities.

- [ ] **Step 1: Write failing report and workflow-contract tests**

Require all eight blocks and reject skipped, duplicate, stale-SHA, wrong-bundle, zero-delta, regressed coverage, leaked path/secret, mock/stub token, missing mutation, missing fault, and over-budget performance records. Require offline enforcement after bundle preparation.

- [ ] **Step 2: Run local contract tests and verify failure**

Run: `pnpm --filter @unit-test-ide/service-probe test`

Run: `node --test tools/phase10/test-generation-report.test.mjs tools/phase10/mutation.test.mjs tools/phase10/performance.test.mjs tools/workspace-smoke/workspace-smoke.test.mjs`

Expected: FAIL because the production generation matrix is not yet required by the workflow.

- [ ] **Step 3: Implement the native probe and closed aggregator**

Drive the real service protocol, never a direct helper-only shortcut. Record only fixed IDs, counts, durations, tool/bundle digests, coverage totals/deltas, and bounded reason codes.

- [ ] **Step 4: Run the complete local verification set**

Run: `go test -race ./apps/test-service/...`

Run: `pnpm test`

Run: `pnpm build`

Expected: PASS locally; privileged/native hosted blocks may be produced only by their required runners and must not be synthesized.

- [ ] **Step 5: Run hosted Phase 10 and audit immutable evidence**

Trigger the dedicated Phase 10/foundation workflow on the feature branch. Verify candidate SHA, every job conclusion, tool versions, bundle identities, all eight blocks, report digests, artifact IDs, retention, and absence of secrets/paths. Do not publish a Release and do not enable signing.

- [ ] **Step 6: Update documentation from verified evidence only**

Document the exact user workflow, supported subset, fail-closed reasons, maintainability rules, and per-file/per-function coverage. Mark production generation complete only if all required receipts pass; otherwise retain the precise open gate.

- [ ] **Step 7: Commit**

```bash
git add tools/service-probe tools/phase10 .github/workflows/foundation.yml docs/test-generation.md docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md
git commit -m "evidence: close production test generation gates"
```

## Final Branch Verification

- [ ] Run `git diff --check` and verify the worktree is clean except for intentional commits.
- [ ] Run `go test -race ./apps/test-service/...`.
- [ ] Run `pnpm test` and `pnpm build` with Node.js 24.18+ and pnpm 11.4.0.
- [ ] Run release staging tests and a release-shaped extension/service smoke with system tool fallbacks unavailable.
- [ ] Review every required hosted receipt against the exact branch head.
- [ ] Request whole-branch code review before any merge.
- [ ] Keep GitHub Release publication, Windows signing, and final license/legal approval disabled.
