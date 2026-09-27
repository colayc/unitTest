# Offline Coverage-Guided Test Generation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a completely offline, self-developed C/C++ unit-test generator that raises function, line, and branch coverage, validates every retained test, previews all changes, and writes only after explicit user confirmation.

**Architecture:** First close the missing Linux Clang/LLVM coverage path, then add Protocol v1.5 contracts over the existing task, event, artifact, and session infrastructure. A product-owned fixed Clang frontend produces a bounded, path-redacted program model; project-owned Go packages classify the safe subset, solve branch inputs, derive assertions, render CppUTest or Unity candidates, and validate them in isolated workspaces against real coverage deltas. Code-OSS presents progress, candidate evidence, diffs, and a digest-bound confirmation step; a separate atomic publisher is the only component permitted to modify the user's workspace.

**Tech Stack:** Go test service; TypeScript protocol models, client, and Code-OSS extension; JSON Schema; Node.js generators and supply-chain checks; fixed Clang/LLVM tools; CMake 4.3.4; CppUTest for C++; Unity for C; GitHub Actions on Windows x64 and Linux x64.

**Spec:** `docs/superpowers/specs/2026-09-27-offline-coverage-guided-test-generation-design.md`

## Global Constraints

- Operate completely offline after checked bundle preparation; never send source, AST, CFG, generated tests, paths, or diagnostics to an external service.
- Do not use cloud or local LLMs, third-party test-generation products, Mock, Stub, CppUMock, or CMock in generated tests.
- Use only the reviewed, immutable, product-owned Clang frontend bundle for analysis. Do not fall back to `PATH`, system Clang, arbitrary compiler plugins, or user-supplied commands.
- Generate only for the approved safe subset: pure functions, in-memory data, and controlled temporary-directory I/O. Reject uncontrolled network, database, hardware, system configuration, time, randomness, processes, privilege, and destructive filesystem behavior.
- Never modify production source. Before acceptance, all candidate source and CMake edits remain under a service-owned staging root.
- Retain a verified candidate only when it has at least one independently derived assertion, compiles, passes, and adds function, line, or branch coverage. A characterization candidate must instead contain an explicit observed-output assertion, compile, pass, add coverage, remain visibly labeled, and receive separate user confirmation. Assertionless probes are not tests.
- All limits are bounded: source/AST size, process time, run time, candidate count, memory, concurrency, output bytes, event count, and artifact count. Cancellation must terminate complete process trees.
- Persist only closed, versioned, path-redacted models and digest-bound artifacts. No absolute host path, secret, environment dump, or raw production source may enter protocol payloads, receipts, or logs.
- Preserve the three deferred Phase 8 release gates. Do not sign, tag, publish a Release, claim legal approval, or mark the product release-ready during Phase 10 implementation.
- Every task below begins with a failing test, ends with focused verification and an independent commit, and receives review before the next dependent task begins.

## Review Focus

Reviewers must treat these five cases as release-blocking, not optional hardening:

1. **Snapshot drift:** source, compile database, target graph, framework, or coverage baseline changes between analysis and acceptance. The run must become stale and publish nothing.
2. **Unsupported syntax:** macros, templates, implicit code, malformed AST output, or unsupported control flow yields a deterministic unsupported diagnostic, never a partial unsafe model or execution.
3. **False progress:** a verified candidate lacking an independent assertion, a characterization candidate lacking an explicit observed-output assertion, or any candidate lacking a coverage delta is rejected and cannot appear in the retained candidate set.
4. **Interruption:** cancellation, crash, timeout, or service restart during compilation, execution, minimization, or publication kills owned processes, resumes or terminalizes deterministically, and leaves no partial workspace edits.
5. **Repeat acceptance:** reruns, user edits, case-ID collisions, or accepting the same candidate set twice never overwrite user content; publication is idempotent or returns a closed conflict diff.

## File and Ownership Map

| Area | Existing ownership to extend | New ownership |
| --- | --- | --- |
| Coverage toolchains | `apps/test-service/internal/toolchain`, `coveragellvm`, `coverageexec`, `coveragecoord` | Linux Clang discovery, four-tool identity, instrumentation |
| Supply chain | `tools/coverage-bundle`, `tools/cmake-bundle`, release staging | `tools/testgen-bundle`, `internal/testgenbundle` |
| Protocol | `packages/protocol-schema`, `packages/protocol-models`, `packages/test-client`, `tools/protocol-gen` | Protocol v1.5 generation contracts |
| Durable execution | `internal/taskstore`, `artifactstore`, `eventstore`, `session`, `runtime` | `testgendomain`, `testgencoord` |
| Analysis/generation | none | `testgenanalysis`, `testgensolver`, `testgenassert`, `testgenrender` |
| Validation/publication | coverage/build/test runtimes and atomic artifact helpers | `testgenvalidate`, `testgenpublish` |
| Code-OSS | commands, trust gate, protocol client, coverage controller/viewer | generation controller, results tree, diff/confirmation UI |
| Evidence and gates | `.github/workflows/foundation.yml`, `tools/phase9`, Phase 9 receipts | Phase 10 jobs, receipts, gate rows, roadmap status |

---

### Task 1: Pin a real four-tool Linux LLVM coverage identity

**Files:**
- Modify: `apps/test-service/internal/toolchain/model.go`
- Modify: `apps/test-service/internal/toolchain/gnu.go`
- Test: `apps/test-service/internal/toolchain/gnu_test.go`
- Create: `apps/test-service/internal/toolchain/model_test.go`
- Modify: `apps/test-service/internal/coveragellvm/toolset_nonwindows.go`
- Test: `apps/test-service/internal/coveragellvm/toolset_nonwindows_test.go`
- Modify: `apps/test-service/internal/runtime/coverage_backend.go`
- Test: `apps/test-service/internal/runtime/coverage_backend_test.go`

**Interfaces:**
- `probeLLVMCoverage(context.Context, Instance) (CoverageCapability, error)` resolves and fingerprints `clang`, `clang++`, `llvm-profdata`, and `llvm-cov` from one installation.
- `LLVMToolsetIdentityForTools(version string, tools []LLVMToolEvidence) (string, error)` produces one canonical, role-bound identity; the existing `LLVMToolsetIdentity` remains a Windows three-tool compatibility wrapper.
- `coveragellvm.PinToolset(toolchain.Instance) (*Toolset, error)` returns distinct C/C++ compilers on Linux and rejects missing, mixed-version, mixed-root, mutable, or synthetic evidence.

- [ ] **Step 1: Write failing tests** for separate `clang`/`clang++`, matching versions and installation roots, executable digests, mixed-tool rejection, absent `clang++`, and refusal of the current synthetic fallback fingerprint.
- [ ] **Step 2: Run focused tests**

    go test ./apps/test-service/internal/toolchain ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/runtime

  Expected: FAIL because Linux LLVM pinning currently returns `ErrUnsupportedPlatform` and runtime permits a synthetic identity.
- [ ] **Step 3: Implement the minimal platform-generic identity and Linux probe**, retaining exact Windows clang-cl semantics and exposing no absolute path in serialized capability data.
- [ ] **Step 4: Remove the synthetic runtime fallback**; missing verified evidence must report a closed unavailable capability.
- [ ] **Step 5: Rerun the focused tests** and require PASS on Windows and Linux-specific tests via injected filesystem/process probes.
- [ ] **Step 6: Commit**

    git add apps/test-service/internal/toolchain apps/test-service/internal/coveragellvm apps/test-service/internal/runtime/coverage_backend.go apps/test-service/internal/runtime/coverage_backend_test.go
    git commit -m "feat: pin linux llvm coverage toolset"

### Task 2: Execute Linux Clang coverage end to end

**Files:**
- Modify: `apps/test-service/internal/coveragellvm/instrumentation.go`
- Test: `apps/test-service/internal/coveragellvm/instrumentation_test.go`
- Create: `apps/test-service/internal/coveragellvm/instrumentation_linux.go`
- Test: `apps/test-service/internal/coveragellvm/instrumentation_linux_test.go`
- Modify: `apps/test-service/internal/coverageexec/coordinator.go`
- Test: `apps/test-service/internal/coverageexec/coordinator_test.go`
- Test: `apps/test-service/internal/coverageexec/orchestration_linux_test.go`
- Modify: `apps/test-service/internal/coveragerun/llvm.go`
- Test: `apps/test-service/internal/coveragerun/llvm_test.go`
- Modify: `apps/test-service/internal/coveragerun/collector.go`
- Test: `apps/test-service/internal/coveragerun/collector_test.go`
- Modify: `apps/test-service/internal/coveragecoord/coordinator.go`
- Test: `apps/test-service/internal/coveragecoord/coordinator_test.go`

**Interfaces:**
- `coveragellvm.PlanInstrumentation(Toolset, BuildRequest) (InstrumentationPlan, error)` selects clang-cl flags on Windows and `-fprofile-instr-generate -fcoverage-mapping` with distinct `clang`/`clang++` on Linux.
- `coverageexec.Coordinator` plans and owns Linux LLVM execution using only pinned tool evidence and a service-owned `LLVM_PROFILE_FILE` pattern.
- `coveragerun.BuildLLVMInvocation(LLVMInputs) (LLVMInvocation, error)` merges profiles with pinned `llvm-profdata` and exports with pinned `llvm-cov`; the collector feeds that output into the existing normalized report pipeline.

- [ ] **Step 1: Add failing unit tests** for C/C++ compiler separation, profile filename collision resistance, zero-profile failure, non-zero test exit, cancellation, bounded stdout/stderr, path redaction, and normalized function/line/branch totals.
- [ ] **Step 2: Run focused tests**

    go test ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/coverageexec ./apps/test-service/internal/coveragerun ./apps/test-service/internal/coveragecoord

  Expected: FAIL because Linux instrumentation and collection are not implemented.
- [ ] **Step 3: Split platform instrumentation** without weakening the existing clang-cl path, then implement Linux execution and collection through existing prepared-process ownership.
- [ ] **Step 4: Add a native fixture assertion** that C and C++ sources produce stable, path-free coverage and that cancellation removes `.profraw` files and terminates descendants.
- [ ] **Step 5: Rerun focused tests** and require PASS.
- [ ] **Step 6: Commit**

    git add apps/test-service/internal/coveragellvm apps/test-service/internal/coverageexec apps/test-service/internal/coveragerun apps/test-service/internal/coveragecoord
    git commit -m "feat: execute linux clang coverage"

### Task 3: Close Phase 10A hosted coverage evidence

**Files:**
- Modify: `tools/service-probe/src/native-build-linux.test.ts`
- Modify: `tools/service-probe/src/native-build-windows.test.ts`
- Modify: `tools/service-probe/src/native-report.ts`
- Create: `tools/service-probe/src/native-report.test.ts`
- Modify: `tools/service-probe/run-tests.mjs`
- Modify: `.github/workflows/foundation.yml`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`
- Modify: `tools/phase9/gates.json`
- Modify: `tools/phase9/validate.test.mjs`
- Create after hosted success: `docs/superpowers/evidence/phase10/receipts/github-actions-${GITHUB_RUN_ID}-coverage-backends.json`

**Interfaces:**
- Linux native evidence must contain real `gcc` and `clang` coverage results; Windows evidence must contain a refreshed real `clang-cl` result bound to one candidate commit.
- The coverage receipt binds workflow/run/job IDs, candidate SHA, runner images, compiler identities, artifact IDs/names/digests, and function/line/branch totals.
- Required coverage backend rows are `linux-gcc`, `linux-clang`, and `windows-clang-cl`; `MISSING` or `SKIPPED` is a failure.

- [ ] **Step 1: Add failing workflow and gate tests** requiring the Linux Clang job, refreshed Windows clang-cl evidence, exact artifact names, immutable action pins, and no post-preparation download.
- [ ] **Step 2: Run static tests**

    node --test tools/workspace-smoke/workspace-smoke.test.mjs tools/phase9/validate.test.mjs
    pnpm --filter @unit-test-ide/service-probe build
    node --test tools/service-probe/dist/native-report.test.js tools/service-probe/dist/native-build-linux.test.js tools/service-probe/dist/native-build-windows.test.js

  Expected: FAIL because Linux Clang remains missing from the evidence catalog.
- [ ] **Step 3: Wire the hosted jobs and closed report producer**; keep administrator/WFP, release, signing, and legal paths unchanged.
- [ ] **Step 4: With explicit user authorization, push the implementation branch and run the workflow.** Download artifacts by immutable ID, verify their digests and candidate SHA, and record one closed receipt only after every required job succeeds.
- [ ] **Step 5: Regenerate and audit the matrix**

    pnpm test:phase9-gates
    pnpm check:phase9-gates
    git diff --check

- [ ] **Step 6: Commit implementation and evidence separately** so hosted observations never rewrite the implementation commit.

### Task 4: Add the fixed offline test-generation Clang bundle

**Files:**
- Create: `tools/testgen-bundle/manifest.schema.json`
- Create: `tools/testgen-bundle/manifest.json`
- Create: `tools/testgen-bundle/manifest.test.mjs`
- Create: `tools/testgen-bundle/prepare.mjs`
- Create: `tools/testgen-bundle/prepare.test.mjs`
- Create: `tools/testgen-bundle/check.mjs`
- Create: `tools/testgen-bundle/check.test.mjs`
- Create: `apps/test-service/internal/testgenbundle/bundle.go`
- Test: `apps/test-service/internal/testgenbundle/bundle_test.go`
- Modify: `package.json`
- Modify: `tools/release/release-config.json`
- Modify: `tools/release/stage.mjs`
- Modify: `tools/release/stage.test.mjs`

**Interfaces:**
- The manifest uses immutable HTTPS archive coordinates, lowercase SHA-256, exact extracted file inventory, license inventory, Clang version, target platform, and resource-directory identity.
- `prepare.mjs` is the only network-capable preparation step; `check.mjs` verifies a prepared cache without network or mutation.
- `testgenbundle.Open(root string) (*Bundle, error)` verifies manifest, executable/resource digests, versions, platform, and licenses before returning `ClangPath()` and `ResourceDir()`.

- [ ] **Step 1: Write failing manifest, preparation, release-staging, and Go consumer tests** for mutable URLs, digest mismatch, archive traversal, symlinks/reparse points, extra/missing files, mixed platforms, missing licenses, executable substitution, and `PATH` fallback.
- [ ] **Step 2: Run focused tests**

    node --test tools/testgen-bundle/manifest.test.mjs tools/testgen-bundle/prepare.test.mjs tools/testgen-bundle/check.test.mjs tools/release/stage.test.mjs
    go test ./apps/test-service/internal/testgenbundle

  Expected: FAIL because the bundle and consumer do not exist.
- [ ] **Step 3: Select and record reviewed immutable Clang coordinates** for Windows x64 and Linux x64, including upstream license texts and exact archive/file digests; do not insert placeholder versions or hashes.
- [ ] **Step 4: Implement preparation, offline checking, Go consumption, and release staging** with atomic cache replacement and canonical manifest hashing.
- [ ] **Step 5: Run focused tests and offline re-check**

    pnpm prepare:testgen-bundle
    pnpm check:testgen-bundle
    go test ./apps/test-service/internal/testgenbundle
    git diff --check

- [ ] **Step 6: Commit**

    git add tools/testgen-bundle apps/test-service/internal/testgenbundle package.json tools/release/release-config.json tools/release/stage.mjs tools/release/stage.test.mjs
    git commit -m "build: pin offline test generation clang"

### Task 5: Define and generate Protocol v1.5 contracts

**Files:**
- Create: `packages/protocol-schema/schema/v1.5/capabilities.schema.json`
- Create: `packages/protocol-schema/schema/v1.5/diagnostic.schema.json`
- Create: `packages/protocol-schema/schema/v1.5/test.schema.json`
- Create: `packages/protocol-schema/schema/v1.5/coverage.schema.json`
- Create: `packages/protocol-schema/schema/v1.5/test-generation.schema.json`
- Create: `packages/protocol-schema/schema/v1.5/task.schema.json`
- Create: `packages/protocol-schema/schema/v1.5/event.schema.json`
- Create: `packages/protocol-schema/schema/v1.5/artifact.schema.json`
- Create: `packages/protocol-schema/schema/v1.5/message.schema.json`
- Create: `packages/protocol-schema/fixtures/v1.5/test-generation-start.valid.json`
- Create: `packages/protocol-schema/fixtures/v1.5/test-generation-run.valid.json`
- Create: `packages/protocol-schema/fixtures/v1.5/test-generation-candidate-set.valid.json`
- Create: `packages/protocol-schema/fixtures/v1.5/test-generation-accept.valid.json`
- Create: `packages/protocol-schema/fixtures/v1.5/test-generation-budget.invalid.json`
- Create: `packages/protocol-schema/fixtures/v1.5/test-generation-path.invalid.json`
- Modify: `packages/protocol-schema/test/schema.test.mjs`
- Modify: `tools/protocol-gen/generate.mjs`
- Create generated: `packages/protocol-models/src/generated/capabilities-v1-5.ts`
- Create generated: `packages/protocol-models/src/generated/diagnostic-v1-5.ts`
- Create generated: `packages/protocol-models/src/generated/test-v1-5.ts`
- Create generated: `packages/protocol-models/src/generated/coverage-v1-5.ts`
- Create generated: `packages/protocol-models/src/generated/test-generation-v1-5.ts`
- Create generated: `packages/protocol-models/src/generated/task-v1-5.ts`
- Create generated: `packages/protocol-models/src/generated/event-v1-5.ts`
- Create generated: `packages/protocol-models/src/generated/artifact-v1-5.ts`
- Create generated: `apps/test-service/internal/protocolmodel/v1_5/capabilities/generated.go`
- Create generated: `apps/test-service/internal/protocolmodel/v1_5/diagnostic/generated.go`
- Create generated: `apps/test-service/internal/protocolmodel/v1_5/test/generated.go`
- Create generated: `apps/test-service/internal/protocolmodel/v1_5/coverage/generated.go`
- Create generated: `apps/test-service/internal/protocolmodel/v1_5/testgeneration/generated.go`
- Create generated: `apps/test-service/internal/protocolmodel/v1_5/task/generated.go`
- Create generated: `apps/test-service/internal/protocolmodel/v1_5/event/generated.go`
- Create generated: `apps/test-service/internal/protocolmodel/v1_5/artifact/generated.go`
- Modify: `packages/protocol-models/src/index.ts`
- Modify: `packages/protocol-models/src/generated-contract.test.ts`
- Modify: `packages/test-client/src/client.ts`
- Modify: `packages/test-client/src/decoders.ts`
- Modify: `packages/test-client/src/envelopes.ts`
- Test: `packages/test-client/src/client.test.ts`

**Interfaces:**
- Add methods `testGeneration/targets/list`, `testGeneration/start`, `testGeneration/runs/get`, `testGeneration/candidates/list`, and `testGeneration/accept`; continue using the generic task cancellation method.
- The client exposes `listTestGenerationTargets`, `startTestGeneration`, `getTestGenerationRun`, `listTestGenerationCandidates`, and `acceptTestGeneration` with v1.5 envelopes.
- Closed enums include scope `symbol|file|target|workspace|coverage-gap`, framework `auto|cpputest|unity`, candidate kind `verified|characterization`, and run states `queued|baseline|analyzing|solving|rendering|validating|minimizing|awaiting_confirmation|accepted|rejected|cancelled|failed`.
- Request fields include coverage targets for functions/lines/branches and budgets for wall time, candidates, memory MiB, and concurrency. Candidate summaries carry code/artifact digests, assertion provenance, baseline/delta coverage, planned file edits, diagnostics, and characterization status, never raw source or absolute paths.

- [ ] **Step 1: Add failing schema and client tests** for every method/envelope, unknown keys, invalid percentages, zero/overflow budgets, illegal state transitions, path-bearing fields, digest format, characterization confirmation, and downgrade from a v1.5-only method.
- [ ] **Step 2: Run focused tests**

    node --test packages/protocol-schema/test/schema.test.mjs
    pnpm --filter @unit-test-ide/protocol-models test
    pnpm --filter @unit-test-ide/test-client test

  Expected: FAIL because Protocol v1.5 is not registered.
- [ ] **Step 3: Extend the generator as the single source of truth**, generate TypeScript and Go models, and keep v1.0-v1.4 byte-stable.
- [ ] **Step 4: Implement the typed client methods and strict decoders**; no `unknown as` escape, permissive fallback, or raw JSON exposure.
- [ ] **Step 5: Verify generated closure**

    pnpm generate:protocol
    pnpm check:protocol-generated
    node --test packages/protocol-schema/test/schema.test.mjs
    pnpm --filter @unit-test-ide/protocol-models test
    pnpm --filter @unit-test-ide/test-client test

- [ ] **Step 6: Commit**

    git add packages/protocol-schema packages/protocol-models packages/test-client tools/protocol-gen apps/test-service/internal/protocolmodel/v1_5
    git commit -m "feat: define protocol v1.5 test generation"

### Task 6: Add the closed generation domain and durable coordinator

**Files:**
- Create: `apps/test-service/internal/testgendomain/model.go`
- Create: `apps/test-service/internal/testgendomain/request.go`
- Test: `apps/test-service/internal/testgendomain/model_test.go`
- Test: `apps/test-service/internal/testgendomain/request_test.go`
- Create: `apps/test-service/internal/testgencoord/coordinator.go`
- Create: `apps/test-service/internal/testgencoord/state.go`
- Test: `apps/test-service/internal/testgencoord/coordinator_test.go`
- Test: `apps/test-service/internal/testgencoord/state_test.go`
- Create: `apps/test-service/internal/taskstore/test_generation.go`
- Test: `apps/test-service/internal/taskstore/test_generation_test.go`

**Interfaces:**
- `testgendomain.ValidateRequest(Request) error` validates scope, framework, targets, budgets, trusted workspace identity, and compile/coverage snapshot digests.
- `testgendomain.Candidate` contains stable case ID, kind, target symbol, assertions, staged source artifact, coverage delta, diagnostics, and planned edits; it contains no host path or executable command.
- `testgencoord.Start(context.Context, Request) (Run, error)`, `Get(runID)`, `ListCandidates(runID)`, `Cancel(taskID)`, and `Resume(taskID)` operate over existing task/event/artifact stores.
- Persisted state records the exact state-machine revision and artifact digests so restart resumes from the last completed stage, not from in-memory objects.

- [ ] **Step 1: Write failing table tests** for all closed enums, invalid budgets/coverage targets, task/run ID mismatch, duplicate case IDs, stale snapshot identities, impossible transitions, cancellation, restart after each stage, and event replay bounds.
- [ ] **Step 2: Run focused tests**

    go test ./apps/test-service/internal/testgendomain ./apps/test-service/internal/testgencoord ./apps/test-service/internal/taskstore

  Expected: FAIL because the domain and coordinator do not exist.
- [ ] **Step 3: Implement immutable domain values and transition validation**; use the existing task store for lifecycle, event store for progress, and artifact store for large results rather than adding a parallel persistence system.
- [ ] **Step 4: Implement restart-safe coordinator checkpoints** with one writer per run, bounded replay, cancellation ownership, and terminal-state idempotency.
- [ ] **Step 5: Run focused tests and the race detector**

    go test -race ./apps/test-service/internal/testgendomain ./apps/test-service/internal/testgencoord ./apps/test-service/internal/taskstore

- [ ] **Step 6: Commit**

    git add apps/test-service/internal/testgendomain apps/test-service/internal/testgencoord apps/test-service/internal/taskstore/test_generation.go apps/test-service/internal/taskstore/test_generation_test.go
    git commit -m "feat: add durable test generation domain"

### Task 7: Parse fixed Clang output into a stable safe program model

**Files:**
- Create: `apps/test-service/internal/testgenanalysis/analyzer.go`
- Create: `apps/test-service/internal/testgenanalysis/clang.go`
- Create: `apps/test-service/internal/testgenanalysis/ast.go`
- Create: `apps/test-service/internal/testgenanalysis/ir.go`
- Create: `apps/test-service/internal/testgenanalysis/safety.go`
- Test: `apps/test-service/internal/testgenanalysis/analyzer_test.go`
- Test: `apps/test-service/internal/testgenanalysis/ast_test.go`
- Test: `apps/test-service/internal/testgenanalysis/safety_test.go`
- Create: `apps/test-service/internal/testgenanalysis/testdata/safe/scalar-branches.c`
- Create: `apps/test-service/internal/testgenanalysis/testdata/safe/aggregate-methods.cpp`
- Create: `apps/test-service/internal/testgenanalysis/testdata/safe/bounded-loop.c`
- Create: `apps/test-service/internal/testgenanalysis/testdata/safe/temp-file.cpp`
- Create: `apps/test-service/internal/testgenanalysis/testdata/unsupported/macro-side-effect.cpp`
- Create: `apps/test-service/internal/testgenanalysis/testdata/unsupported/template-instantiation.cpp`
- Create: `apps/test-service/internal/testgenanalysis/testdata/unsupported/system-api.c`

**Interfaces:**
- `Analyzer.Analyze(context.Context, AnalysisRequest) (Program, error)` invokes only the verified bundle executable with `-Xclang -ast-dump=json -fsyntax-only` plus normalized compile arguments.
- `Program` contains translation units, symbols, types, functions, parameters, branches, predicates, side-effect classifications, source-location digests, and unsupported diagnostics; it contains neither raw source nor absolute paths.
- `SafetyClassifier.Classify(Function) Decision` returns `supported`, `unsupported`, or `requires-confirmation` with a closed reason code; uncertainty is never treated as safe.

- [ ] **Step 1: Write failing golden tests** for C/C++ primitives, enums, bounded arrays, structs/classes, member functions, conditionals, switches, short-circuit logic, loops with explicit bounds, and temp-directory I/O.
- [ ] **Step 2: Add adversarial failing tests** for macros hiding side effects, template instantiations, inline assembly, function pointers, recursion without bound, exceptions across unknown code, volatile/hardware access, network/database/process/system APIs, time/random APIs, malformed/truncated/oversized AST JSON, injected compiler flags, and cancellation.
- [ ] **Step 3: Run focused tests**

    go test ./apps/test-service/internal/testgenanalysis

  Expected: FAIL because no analyzer exists.
- [ ] **Step 4: Implement bounded Clang invocation and streaming JSON decoding**, normalize compile commands against the trusted workspace snapshot, and reject response files, plugin flags, output flags, arbitrary executable selection, and environment injection.
- [ ] **Step 5: Implement stable IR and fail-closed classification**. Store only digest-addressed excerpts required for later rendering and return deterministic unsupported diagnostics for partial or unfamiliar nodes.
- [ ] **Step 6: Rerun focused tests with race detection** and require identical model digests across path, locale, and timestamp changes.
- [ ] **Step 7: Commit**

    git add apps/test-service/internal/testgenanalysis
    git commit -m "feat: analyze safe c and cpp generation targets"

### Task 8: Generate bounded inputs for uncovered branches

**Files:**
- Create: `apps/test-service/internal/testgensolver/solver.go`
- Create: `apps/test-service/internal/testgensolver/domain.go`
- Create: `apps/test-service/internal/testgensolver/predicate.go`
- Create: `apps/test-service/internal/testgensolver/candidates.go`
- Test: `apps/test-service/internal/testgensolver/solver_test.go`
- Test: `apps/test-service/internal/testgensolver/predicate_test.go`
- Test: `apps/test-service/internal/testgensolver/candidates_test.go`

**Interfaces:**
- `Solver.Solve(context.Context, Program, CoverageGap, Budget) ([]InputVector, Diagnostics, error)` targets uncovered function entries and branch outcomes.
- Supported domains are finite booleans/enums, integer boundary partitions, floating-point finite special values, null/non-null pointer choices backed by owned values, bounded strings/arrays, and recursively bounded aggregate fields.
- Candidate identity is a canonical digest of target symbol, branch goal, typed input vector, compile snapshot, analyzer version, and solver version.

- [ ] **Step 1: Write failing tests** for equality/range predicates, nested boolean logic, switches, loop entry/exit boundaries, signed/unsigned overflow edges, NaN/infinity rejection unless explicitly safe, pointer ownership, bounded arrays/strings, aggregate construction, deterministic ordering, deduplication, and unsatisfiable goals.
- [ ] **Step 2: Add budget tests** for wall-clock expiry, candidate cap, memory accounting, cancellation, concurrent target ordering, and no-progress termination.
- [ ] **Step 3: Run focused tests**

    go test ./apps/test-service/internal/testgensolver

  Expected: FAIL because the solver does not exist.
- [ ] **Step 4: Implement project-owned predicate normalization and bounded search** using deterministic partitions and coverage feedback hints; do not add an opaque external solver or random unseeded search.
- [ ] **Step 5: Implement canonical candidate IDs and stable minimization of duplicate input vectors**, preserving reason codes for goals that cannot be solved.
- [ ] **Step 6: Rerun focused tests and fuzz the predicate decoder** for a bounded duration; commit only reproducible seed cases.
- [ ] **Step 7: Commit**

    git add apps/test-service/internal/testgensolver
    git commit -m "feat: solve bounded coverage input candidates"

### Task 9: Derive assertions and render CppUTest or Unity tests

**Files:**
- Create: `apps/test-service/internal/testgenassert/assertions.go`
- Create: `apps/test-service/internal/testgenassert/oracle.go`
- Test: `apps/test-service/internal/testgenassert/assertions_test.go`
- Test: `apps/test-service/internal/testgenassert/oracle_test.go`
- Create: `apps/test-service/internal/testgenrender/renderer.go`
- Create: `apps/test-service/internal/testgenrender/cpputest.go`
- Create: `apps/test-service/internal/testgenrender/unity.go`
- Create: `apps/test-service/internal/testgenrender/cmake.go`
- Test: `apps/test-service/internal/testgenrender/cpputest_test.go`
- Test: `apps/test-service/internal/testgenrender/unity_test.go`
- Test: `apps/test-service/internal/testgenrender/cmake_test.go`
- Create: `apps/test-service/internal/testgenrender/testdata/golden/cpputest.cpp`
- Create: `apps/test-service/internal/testgenrender/testdata/golden/unity.c`
- Create: `apps/test-service/internal/testgenrender/testdata/golden/CMakeLists.txt`

**Interfaces:**
- `testgenassert.Derive(Program, InputVector, Observation) ([]Assertion, CandidateKind, error)` accepts contract-derived, invariant-derived, and explicit error/return-state assertions; observed-output assertions are characterization only.
- Each `Assertion` records a closed provenance kind, target expression digest, expected typed value, comparison rule, and stability evidence.
- `testgenrender.Render(RenderRequest) (StagedEditSet, error)` emits CppUTest for C++, Unity for C, stable case IDs, required includes/fixtures, and a minimal idempotent CMake patch.

- [ ] **Step 1: Write failing assertion tests** that accept explicit return contracts, boolean predicates, enum states, bounded output-buffer effects, and invariants, while rejecting assertionless probes, self-fulfilling assertions, unstable pointer/address values, timestamps, random values, and non-finite comparisons without a declared rule.
- [ ] **Step 2: Write failing renderer golden tests** for CppUTest/Unity syntax, type-correct literals, escaping, array/struct comparisons, floating tolerances, setup/teardown, stable naming, duplicate IDs, and CMake target linkage without Mock/Stub libraries.
- [ ] **Step 3: Run focused tests**

    go test ./apps/test-service/internal/testgenassert ./apps/test-service/internal/testgenrender

  Expected: FAIL because assertion and rendering packages do not exist.
- [ ] **Step 4: Implement the closed assertion hierarchy**; any observation-only oracle is marked `characterization` and cannot be silently promoted to `verified`.
- [ ] **Step 5: Implement framework renderers and structural CMake edits** using parsed target metadata rather than string append. Output only staged files and unified diffs.
- [ ] **Step 6: Rerun focused tests**, compile golden fixtures with the prepared framework/CMake bundles, and scan generated sources to prove no Mock/Stub/CMock/CppUMock use.
- [ ] **Step 7: Commit**

    git add apps/test-service/internal/testgenassert apps/test-service/internal/testgenrender
    git commit -m "feat: render assertion-bearing generated tests"

### Task 10: Validate candidates in isolated real build and coverage loops

**Files:**
- Create: `apps/test-service/internal/testgenvalidate/validator.go`
- Create: `apps/test-service/internal/testgenvalidate/workspace.go`
- Create: `apps/test-service/internal/testgenvalidate/pipeline.go`
- Create: `apps/test-service/internal/testgenvalidate/result.go`
- Test: `apps/test-service/internal/testgenvalidate/validator_test.go`
- Test: `apps/test-service/internal/testgenvalidate/workspace_test.go`
- Test: `apps/test-service/internal/testgenvalidate/pipeline_test.go`
- Create: `apps/test-service/internal/testgenvalidate/testdata/fixture-manifest.json`

**Interfaces:**
- `Validator.Validate(context.Context, ValidationRequest) (ValidationResult, error)` performs exactly: materialize snapshot, apply staged edits, configure, compile, discover, run candidate, run relevant suite, collect coverage, compare baseline, and clean up.
- `ValidationResult` separates compile/test/suite/coverage evidence and returns `retained` only when all required executions pass, at least one requested coverage metric increases, and the declared kind has its required assertion evidence: independent for `verified`, explicit observed-output for `characterization`.
- All processes use existing prepared-process leases, fixed executables, closed arguments, bounded outputs, service-owned temporary roots, and complete process-tree cancellation.

- [ ] **Step 1: Write failing pipeline tests** for every stage, including configure error, compiler error, no discovered test, candidate failure, unrelated regression, crash, hang, cancellation, output flood, missing profile, malformed coverage, no delta, regression in another metric, and successful function/line/branch gains.
- [ ] **Step 2: Add isolation tests** proving the source workspace is read-only, symlinks/reparse points cannot escape, absolute paths are redacted, environment is allowlisted, cleanup survives locked files, and a service restart cannot adopt an unowned process.
- [ ] **Step 3: Run focused tests**

    go test ./apps/test-service/internal/testgenvalidate

  Expected: FAIL because no validator exists.
- [ ] **Step 4: Implement snapshot materialization and staged build execution** by composing existing CMake, framework, test, coverage, task, and artifact services; do not duplicate their command construction.
- [ ] **Step 5: Implement strict retain/reject classification** and persist digest-bound evidence for each stage. Reject a passing test with no coverage delta, no assertion, an unproven verified assertion, or an unlabeled characterization oracle as false progress.
- [ ] **Step 6: Run focused and native fixture tests** on C/Unity and C++/CppUTest with GCC, Linux Clang, MSVC, and clang-cl as available; hosted completion is required later, not replaced by local skips.
- [ ] **Step 7: Commit**

    git add apps/test-service/internal/testgenvalidate
    git commit -m "feat: validate generated tests in isolation"

### Task 11: Enforce budgets, deterministic minimization, and restart semantics

**Files:**
- Create: `apps/test-service/internal/testgencoord/budget.go`
- Create: `apps/test-service/internal/testgencoord/minimize.go`
- Modify: `apps/test-service/internal/testgencoord/coordinator.go`
- Modify: `apps/test-service/internal/testgencoord/state.go`
- Test: `apps/test-service/internal/testgencoord/budget_test.go`
- Test: `apps/test-service/internal/testgencoord/minimize_test.go`
- Modify: `apps/test-service/internal/testgencoord/coordinator_test.go`
- Create: `apps/test-service/internal/testgencoord/fuzz_test.go`

**Interfaces:**
- `BudgetLedger` reserves and releases wall time, candidate slots, memory MiB, process slots, output bytes, event count, and artifact count across all stages.
- `Minimize(baseline Coverage, candidates []ValidatedCandidate) []ValidatedCandidate` deterministically selects the smallest stable set using coverage contribution, assertion quality, candidate kind, complexity, and stable case ID as tie-breakers.
- Resume verifies all input/output digests and replays only incomplete idempotent stages; any workspace, compile, analyzer, framework, or coverage drift marks the run stale.

- [ ] **Step 1: Write failing budget tests** for exact-boundary use, reservation races, leaked reservations, nested process limits, cancellation, timeout precedence, output/event/artifact exhaustion, and terminal error mapping.
- [ ] **Step 2: Write failing minimization tests** for overlapping coverage, equal contributions, characterization demotion, deterministic ties, removal causing lost branch coverage, and repeated runs with reordered candidates.
- [ ] **Step 3: Add restart/drift tests** after every stage, including changed source, compile database, CMake target, framework bundle, analyzer bundle, baseline report, staged artifact, and process-owner identity.
- [ ] **Step 4: Run focused tests**

    go test -race ./apps/test-service/internal/testgencoord

  Expected: FAIL on the new budget, minimization, and stale-run assertions.
- [ ] **Step 5: Implement shared accounting, deterministic set minimization, and digest revalidation**. Never resume external processes or reuse unverifiable staged bytes.
- [ ] **Step 6: Run race tests and bounded fuzzing**, promote every discovered failure to a deterministic regression test, and require stable candidate ordering across 100 repeated test seeds.
- [ ] **Step 7: Commit**

    git add apps/test-service/internal/testgencoord
    git commit -m "feat: bound and minimize test generation runs"

### Task 12: Publish confirmed edits atomically and idempotently

**Files:**
- Create: `apps/test-service/internal/testgenpublish/publisher.go`
- Create: `apps/test-service/internal/testgenpublish/plan.go`
- Create: `apps/test-service/internal/testgenpublish/receipt.go`
- Test: `apps/test-service/internal/testgenpublish/publisher_test.go`
- Test: `apps/test-service/internal/testgenpublish/plan_test.go`
- Create: `apps/test-service/internal/testgenpublish/testdata/CMakeLists.txt`
- Create: `apps/test-service/internal/testgenpublish/testdata/existing_test.cpp`

**Interfaces:**
- `Publisher.Plan(context.Context, CandidateSet) (PublishPlan, error)` returns normalized relative edits, before/after digests, a unified diff artifact, characterization acknowledgements, and one confirmation digest.
- `Publisher.Accept(context.Context, AcceptRequest) (Receipt, error)` requires exact run, candidate-set, snapshot, diff, confirmation, and optional characterization digests; it stages sibling files, rechecks preimages, atomically replaces, and rolls back the complete set on failure.
- `Receipt` records changed relative paths and before/after digests only. The same acceptance is idempotent; a different preimage returns a conflict and never overwrites.

- [ ] **Step 1: Write failing plan tests** for path traversal, absolute/UNC/device paths, case-fold collisions, reserved names, duplicate edits, symlink/reparse escapes, production-source edits, non-test CMake edits, stale preimages, changed generated files, and malformed unified diffs.
- [ ] **Step 2: Write failing atomicity tests** for denial on the first/middle/last file, disk-full simulation, crash journal recovery, cancellation before and during commit, concurrent user edit, duplicate acceptance, case-ID collision, and exact rollback bytes/permissions.
- [ ] **Step 3: Run focused tests**

    go test -race ./apps/test-service/internal/testgenpublish

  Expected: FAIL because the publisher does not exist.
- [ ] **Step 4: Implement closed plan validation and journaled multi-file publication** using existing safe path and atomic artifact primitives. The only allowed destinations are approved generated test files and their required test-target CMake entries.
- [ ] **Step 5: Implement digest-bound idempotency and conflict receipts**; repeat acceptance must return the existing receipt, while user-edited content produces a new preview requirement.
- [ ] **Step 6: Run focused tests plus fault injection** and verify the source tree is byte-identical after every injected failure.
- [ ] **Step 7: Commit**

    git add apps/test-service/internal/testgenpublish
    git commit -m "feat: publish generated tests atomically"

### Task 13: Expose generation through the service session and runtime

**Files:**
- Create: `apps/test-service/internal/runtime/test_generation.go`
- Test: `apps/test-service/internal/runtime/test_generation_test.go`
- Create: `apps/test-service/internal/session/test_generation_routes.go`
- Test: `apps/test-service/internal/session/test_generation_routes_test.go`
- Modify: `apps/test-service/internal/session/session.go`
- Modify: `apps/test-service/internal/session/session_test.go`
- Modify: `apps/test-service/internal/runtime/runtime.go`
- Modify: `apps/test-service/internal/runtime/runtime_test.go`
- Modify: `apps/test-service/cmd/unit-test-service/main.go`
- Test: `apps/test-service/cmd/unit-test-service/main_test.go`

**Interfaces:**
- Runtime methods mirror Protocol v1.5: `ListTestGenerationTargets`, `StartTestGeneration`, `GetTestGenerationRun`, `ListTestGenerationCandidates`, and `AcceptTestGeneration`.
- Session routes negotiate v1.5, enforce workspace trust and capability checks, map domain errors to closed protocol diagnostics, and stream progress through existing task events.
- `AcceptTestGeneration` is the sole route that may invoke the publisher and requires the current preview confirmation digest; start/get/list routes are read-only with respect to the user workspace.

- [ ] **Step 1: Write failing route tests** for exact method names, v1.4 rejection, malformed envelopes, untrusted workspace, missing capability, unknown run, foreign-session run, cancellation, stale preview, missing characterization acknowledgement, and error redaction.
- [ ] **Step 2: Write failing runtime integration tests** with fake analyzer/solver/renderer/validator/publisher components to prove stage order, events, artifact ownership, restart, terminalization, and publish-only-on-accept.
- [ ] **Step 3: Run focused tests**

    go test ./apps/test-service/internal/runtime ./apps/test-service/internal/session ./apps/test-service/cmd/unit-test-service

  Expected: FAIL because the v1.5 runtime/routes are absent.
- [ ] **Step 4: Compose the existing and new services** behind narrow interfaces; construction must fail closed when the test-generation bundle or required coverage backend is unavailable.
- [ ] **Step 5: Register strict v1.5 handlers and capability advertisement** while preserving v1.0-v1.4 behavior byte-for-byte.
- [ ] **Step 6: Run focused tests and the Go race suite**

    go test -race ./apps/test-service/internal/runtime ./apps/test-service/internal/session
    go test ./apps/test-service/...

- [ ] **Step 7: Commit**

    git add apps/test-service/internal/runtime apps/test-service/internal/session apps/test-service/cmd/unit-test-service/main.go
    git commit -m "feat: expose test generation service routes"

### Task 14: Add Code-OSS generation, review, and confirmation UX

**Files:**
- Create: `apps/code-oss-extension/src/test-generation-controller.ts`
- Create: `apps/code-oss-extension/test/test-generation-controller.test.ts`
- Create: `apps/code-oss-extension/src/test-generation-results.ts`
- Create: `apps/code-oss-extension/test/test-generation-results.test.ts`
- Create: `apps/code-oss-extension/src/test-generation-diff.ts`
- Create: `apps/code-oss-extension/test/test-generation-diff.test.ts`
- Modify: `apps/code-oss-extension/src/commands.ts`
- Create: `apps/code-oss-extension/test/test-generation-commands.test.ts`
- Modify: `apps/code-oss-extension/src/contracts.ts`
- Modify: `apps/code-oss-extension/src/protocol-client.ts`
- Modify: `apps/code-oss-extension/src/trust-gate.ts`
- Modify: `apps/code-oss-extension/src/extension.ts`
- Modify: `apps/code-oss-extension/src/extension-entry.cts`
- Modify: `apps/code-oss-extension/package.json`

**Interfaces:**
- Commands: `unitTestIde.generateTests`, `unitTestIde.generateTestsForSymbol`, `unitTestIde.generateTestsForFile`, `unitTestIde.generateTestsForTarget`, `unitTestIde.generateTestsForCoverageGap`, `unitTestIde.reviewGeneratedTests`, `unitTestIde.acceptGeneratedTests`, and `unitTestIde.cancelTestGeneration`.
- `TestGenerationController` owns one active selection/run, restores durable runs, polls/replays bounded events, and never treats a locally cached preview as authority.
- Results show baseline/after/delta for function/line/branch coverage, assertion provenance, candidate kind, diagnostics, resource use, and planned edits. Acceptance opens the exact service diff, requires a fresh explicit confirmation, and separately confirms characterization candidates.
- The extension's default `test` script includes every new compiled generation test so root `pnpm test` cannot omit them.

- [ ] **Step 1: Write failing controller tests** for all scopes, configurable targets/budgets, capability/version gating, trust loss, reconnect, cancellation, stale run, multiple workspaces, event deduplication, and no implicit acceptance.
- [ ] **Step 2: Write failing results/diff tests** for verified versus characterization labels, assertion provenance, coverage deltas, unsupported reasons, path redaction, stale diff digest, changed editor content, duplicate confirmation, and service-side conflict refresh.
- [ ] **Step 3: Write failing command/manifest tests** proving every command is contributed and registered once, context menus use valid resource/symbol/coverage contexts, and no command shells out or edits files directly.
- [ ] **Step 4: Run focused tests**

    pnpm --filter code-oss-extension test

  Expected: FAIL because generation UI modules and commands do not exist.
- [ ] **Step 5: Implement controller and views** by reusing protocol client, task progress, trust gate, output redaction, coverage rendering, and VS Code diff APIs. Keep source bytes in editor/service artifacts, not telemetry or extension logs.
- [ ] **Step 6: Implement confirmation flow** that fetches a fresh plan, displays all edits, binds the accepted digest, and handles conflicts by returning to preview rather than retrying writes.
- [ ] **Step 7: Run extension build/tests and package-manifest checks**

    pnpm --filter code-oss-extension build
    pnpm --filter code-oss-extension test
    node --test tools/workspace-smoke/workspace-smoke.test.mjs

- [ ] **Step 8: Commit**

    git add apps/code-oss-extension
    git commit -m "feat: add offline test generation ux"

### Task 15: Prove native, fault, security, and performance behavior

**Files:**
- Create: `tools/service-probe/src/test-generation-e2e.ts`
- Create: `tools/service-probe/src/test-generation-e2e.test.ts`
- Create: `tools/service-probe/fixtures/test-generation/cpputest/CMakeLists.txt`
- Create: `tools/service-probe/fixtures/test-generation/cpputest/unit-test-ide.json`
- Create: `tools/service-probe/fixtures/test-generation/cpputest/include/classifier.hpp`
- Create: `tools/service-probe/fixtures/test-generation/cpputest/src/classifier.cpp`
- Create: `tools/service-probe/fixtures/test-generation/unity/CMakeLists.txt`
- Create: `tools/service-probe/fixtures/test-generation/unity/unit-test-ide.json`
- Create: `tools/service-probe/fixtures/test-generation/unity/include/classifier.h`
- Create: `tools/service-probe/fixtures/test-generation/unity/src/classifier.c`
- Create: `tools/phase10/test-generation-report.schema.json`
- Create: `tools/phase10/test-generation-report.mjs`
- Test: `tools/phase10/test-generation-report.test.mjs`
- Create: `tools/phase10/mutation.test.mjs`
- Create: `tools/phase10/performance.test.mjs`
- Modify: `tools/service-probe/src/native-run.ts`
- Modify: `tools/service-probe/src/native-report.ts`
- Modify: `tools/service-probe/run-tests.mjs`
- Modify: `.github/workflows/foundation.yml`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`
- Modify: `package.json`

**Interfaces:**
- Native matrix is Windows `msvc,clang-cl` and Linux `gcc,clang`, each running one CppUTest and one Unity fixture through the service protocol with the real fixed analyzer bundle.
- Every matrix block records baseline/final function, line, and branch coverage; generated/retained/rejected counts; exact assertion-bearing tests; cancellation/fault outcomes; input/output artifact digests; and path-free tool identities.
- Performance gates use committed fixture sizes and deterministic budgets; mutation gates require generated retained tests to kill the committed target mutations rather than merely execute changed lines.
- The service-probe test registry and root package scripts include the new E2E, report, mutation, and performance suites; the default repository test cannot omit Phase 10.

- [ ] **Step 1: Add failing closed-report tests** for all four toolchains, both frameworks, exact scenario IDs, candidate SHA, analyzer/framework/CMake/coverage identities, no skip, no absolute paths, and artifact digest uniqueness.
- [ ] **Step 2: Add failing E2E scenarios** for function/class/file/target/workspace/coverage-gap scopes; verified and characterization paths; no-safe-target; unsatisfied branch; compile failure; suite regression; no coverage delta; cancellation at each stage; service restart; stale snapshot; conflict; repeat acceptance; and one-click full test/coverage refresh.
- [ ] **Step 3: Add failing abuse tests** for malicious compile databases, response files, compiler plugins, path traversal, symlink/reparse escape, source/output flood, zip bomb bundle, process escape, environment-secret echo, hostile AST JSON, and workspace changes during acceptance.
- [ ] **Step 4: Run static/fake tests**

    pnpm --filter @unit-test-ide/service-probe test -- test-generation-e2e.test.ts
    node --test tools/phase10/test-generation-report.test.mjs tools/phase10/mutation.test.mjs tools/phase10/performance.test.mjs tools/workspace-smoke/workspace-smoke.test.mjs

  Expected: FAIL until producer, schemas, and workflow jobs exist.
- [ ] **Step 5: Implement the native producer and closed aggregator**, then wire independent Windows/Linux jobs using pinned actions, fixed runners, prepared bundles, no post-preparation network, error-on-missing artifacts, and 14-day retention.
- [ ] **Step 6: With explicit user authorization, push and run hosted native jobs.** Require all four toolchains, both frameworks, coverage increases, mutation kills, fault cleanup, and performance budgets; a skipped or missing required block is failure.
- [ ] **Step 7: Run complete local verification**

    pnpm check:protocol-generated
    pnpm check:coverage-generated
    pnpm check:testgen-bundle
    pnpm build
    pnpm test
    pnpm test:go:race
    pnpm test:e2e
    git diff --check

- [ ] **Step 8: Commit implementation, hosted receipts, and regenerated evidence as separate commits.**

### Task 16: Close Phase 10 gates and update release documentation

**Files:**
- Modify: `tools/phase9/gates.json`
- Modify: `tools/phase9/validate.test.mjs`
- Modify: `docs/superpowers/evidence/phase9/baseline.json`
- Modify: `docs/superpowers/evidence/phase9/gate-matrix.json`
- Modify: `docs/superpowers/evidence/phase9/gate-matrix.md`
- Create: `docs/superpowers/evidence/phase10/receipts/github-actions-${GITHUB_RUN_ID}-test-generation.json`
- Create: `docs/test-generation.md`
- Modify: `docs/native-e2e.md`
- Modify: `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`
- Modify: `docs/superpowers/plans/2026-09-27-offline-coverage-guided-test-generation.md`

**Interfaces:**
- Phase 10 gate rows cover Linux LLVM coverage, bundle provenance/licenses, Protocol v1.5, analysis safety, formal assertion/delta retention, atomic acceptance, four-toolchain native matrix, fault/security cases, mutation effectiveness, and performance limits.
- The receipt binds the merged candidate SHA, immutable workflow/jobs/artifacts, report digests, exact gate IDs, and reviewed bundle/license inventory.
- Final matrix remains `releaseReady=false` with exactly the previously deferred Windows signing, third-party license/legal approval, and final release qualification gates still deferred.

- [ ] **Step 1: Add failing gate tests** for every Phase 10 row, receipt/artifact lineage, merged-SHA binding, stale evidence, duplicate artifact identity, and any accidental PASS of the three deferred Phase 8 gates.
- [ ] **Step 2: Audit hosted evidence manually and mechanically**: verify candidate SHA, job conclusions, runner/tool versions, bundle identities, report closure, all coverage deltas, mutation/performance results, absence of paths/secrets, and license inventory.
- [ ] **Step 3: Record the immutable receipt and regenerate the gate matrix**

    pnpm test:phase9-gates
    pnpm check:phase9-gates

- [ ] **Step 4: Document operation and limits**: supported safe subset, unsupported reason codes, scopes, coverage targets/budgets, characterization confirmation, preview/accept conflicts, cancellation/recovery, offline preparation, and one-click run/coverage workflow.
- [ ] **Step 5: Update the roadmap truthfully**: Phase 10 complete only when all required rows pass; leave signing, legal approval, and final release qualification as the subsequent release phase.
- [ ] **Step 6: Run the final verification from a clean checkout**

    pnpm install --frozen-lockfile --offline
    pnpm verify
    git status --short

  Expected: all required tests and gates PASS; worktree clean; no signing, tag, Release, or legal approval claim.
- [ ] **Step 7: Commit documentation/evidence only**

    git add tools/phase9 docs/superpowers/evidence docs/test-generation.md docs/native-e2e.md docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md docs/superpowers/plans/2026-09-27-offline-coverage-guided-test-generation.md
    git commit -m "docs: close phase10 test generation evidence"

## Execution Boundaries

- Implementation proceeds strictly in task order. Tasks 1-4 are Phase 10A prerequisites; Tasks 5-13 are service foundation; Task 14 is Code-OSS UX; Tasks 15-16 are formal validation and evidence.
- Each task is reviewed and its focused tests are green before the next dependent task starts. Do not combine task commits or carry known failures forward.
- Pushing branches, dispatching hosted workflows, creating or merging pull requests, synchronizing Gitee, changing branch protection, and any release/signing action require separate explicit user authorization at the point of action.
- Formal Windows signing and final third-party license/legal approval begin only after Phase 10 is merged, fully regressed, and the user explicitly starts the final release phase.
