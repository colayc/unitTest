# Phase 9 Batch D Performance Baseline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce a deterministic, hardware-qualified Phase 9 performance baseline for discovery, filtering, cancellation, memory, startup, and report generation, with candidate-bound GitHub Actions evidence and no release promotion.

**Architecture:** Add a repository-owned Node benchmark runner that exercises the existing extension/service contracts with synthetic, bounded fixtures and emits a small redacted JSON report. Add one fixed, read-only GitHub Actions job that runs the benchmark repeatedly on `ubuntu-24.04`, checks sample stability and correctness invariants, and uploads the report under the exact `phase9-performance-baseline` artifact name. Record a candidate-bound receipt and regenerate the Phase 9 matrix; the performance batch must never alter producer/foundation behavior or mark release readiness while Phase 8 remains deferred.

**Tech Stack:** TypeScript/Node test harness, existing pnpm workspace scripts, Node `perf_hooks`, GitHub Actions, Phase 9 registry/receipt/audit tooling.

**Spec:** `docs/superpowers/specs/2026-09-13-phase9-gate-evidence-matrix-design.md`, Phase 9 section of `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`

## Global Constraints

- Keep the Phase 9 workflow read-only: `permissions: actions: read` and `contents: read`; do not use `secrets.*`, dynamic repository/run inputs, or shell command inputs.
- Pin every third-party action to the already-reviewed full commit SHA and keep the runner fixed at `ubuntu-24.04`.
- Do not modify product runtime, release-input producer, unsigned foundation, signing, legal evidence, tags, Releases, or branch protection.
- Benchmark fixtures are synthetic and bounded; no workspace paths, usernames, tokens, or machine-specific absolute paths may enter the report.
- A baseline is a measurement record, not an absolute speed promise: use five measured samples after one warm-up, report median/p95/min/max, and require finite values, bounded sample count, stable correctness counts, and coefficient of variation at or below 20% for each timed scenario.
- `releaseReady` must remain `false`; all three approved Phase 8 deferred gates remain `DEFERRED`.

---

### Task 1: Add the deterministic performance harness and contract tests

**Files:**
- Create: `tools/phase9/performance.mjs`
- Create: `tools/phase9/performance.test.mjs`
- Modify: `package.json`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`

**Interfaces:**
- `node tools/phase9/performance.mjs --out <path>` writes one strict JSON object containing `schemaVersion`, `candidateCommit`, `runtime`, `hardware`, and six scenario records (`discovery-10000`, `filter`, `cancel`, `memory`, `startup`, `report`).
- Each scenario record contains `sampleCount: 5`, `warmupCount: 1`, `samplesMs` or `samplesBytes`, `median`, `p95`, `min`, `max`, `coefficientOfVariation`, and a correctness summary. The runner exits non-zero on malformed output, unstable samples, or incorrect counts.
- `pnpm test:phase9:performance` runs `node --test tools/phase9/performance.test.mjs`.

- [ ] **Step 1: Write failing contract tests**

  Cover exact schema/field closure, six scenario IDs, five measured samples plus one warm-up, no absolute paths/secrets, bounded synthetic input sizes, correctness counts, and rejection of unstable or non-finite samples.

- [ ] **Step 2: Run the focused tests to verify they fail**

  Run: `node --test tools/phase9/performance.test.mjs`

  Expected: FAIL because the runner and contract fixtures do not exist.

- [ ] **Step 3: Implement the minimal runner**

  Reuse the existing `TestingApiAdapter` benchmark fixture for discovery and identity; add in-memory filter, cancellation, report serialization, startup, and RSS measurements using only synthetic objects. Use `performance.now()` and `process.memoryUsage().rss`; never read arbitrary files or execute a command from input. Derive `candidateCommit` from `git rev-parse HEAD` only when the command is run in CI, and redact the value to the canonical 40-hex form.

- [ ] **Step 4: Add the package script and static workflow contract assertions**

  Add `test:phase9:performance` and extend workspace smoke checks so the future job must invoke exactly `node tools/phase9/performance.mjs --out .superpowers/phase9/performance/baseline.json`, upload `phase9-performance-baseline`, and keep the fixed runner/action pins.

- [ ] **Step 5: Run focused verification and commit**

  Run: `pnpm test:phase9:performance` and `node --test tools/workspace-smoke/workspace-smoke.test.mjs`.

  Commit: `git add tools/phase9/performance.mjs tools/phase9/performance.test.mjs package.json tools/workspace-smoke/workspace-smoke.test.mjs && git commit -m "test: add phase9 performance baseline harness"`

### Task 2: Add the hosted performance job and artifact contract

**Files:**
- Modify: `.github/workflows/phase9-gates.yml`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`

**Interfaces:**
- New fixed job ID: `phase9-performance`.
- Job output artifact: `phase9-performance-baseline`, path `.superpowers/phase9/performance/baseline.json`, `if-no-files-found: error`, retention 14 days.

- [ ] **Step 1: Extend the failing static contract tests**

  Require the job to install with the pinned setup actions, run `pnpm test:phase9:performance`, run the canonical performance command, and upload the exact artifact name/path without secrets or dynamic inputs.

- [ ] **Step 2: Run the contract tests to verify the new job is absent**

  Run: `node --test tools/workspace-smoke/workspace-smoke.test.mjs`

  Expected: FAIL on the missing `phase9-performance` job.

- [ ] **Step 3: Implement the read-only job**

  Use the same checkout/pnpm/setup-node pins as existing Phase 9 jobs, create `.superpowers/phase9/performance`, run the package test and benchmark command, then upload the exact JSON artifact. Do not add `GH_TOKEN`, API calls, or release logic.

- [ ] **Step 4: Run local static and focused verification**

  Run: `node --test tools/workspace-smoke/workspace-smoke.test.mjs tools/phase9/performance.test.mjs` and `git diff --check`.

- [ ] **Step 5: Commit the workflow change**

  Commit: `git add .github/workflows/phase9-gates.yml tools/workspace-smoke/workspace-smoke.test.mjs && git commit -m "ci: run phase9 performance baseline"`

### Task 3: Generate candidate-bound receipt and matrix evidence

**Files:**
- Create: `docs/superpowers/evidence/phase9/receipts/github-actions-<run>-performance.json`
- Modify: `docs/superpowers/evidence/phase9/baseline.json`
- Modify: `docs/superpowers/evidence/phase9/gate-matrix.json`
- Modify: `docs/superpowers/evidence/phase9/gate-matrix.md`
- Modify: `docs/superpowers/plans/2026-09-14-phase9-batch-d-performance.md`

**Interfaces:**
- Receipt selects exactly the successful `phase9-performance` job and binds its `runId`, `runAttempt`, `headSha`, candidate commit, and artifact ID/name/digest.
- Receipt gate IDs are exactly `P9-PERF-CANCEL`, `P9-PERF-DISCOVERY-10000`, `P9-PERF-FILTER`, `P9-PERF-HARDWARE-BASELINE`, `P9-PERF-MEMORY`, `P9-PERF-REPORT`, and `P9-PERF-STARTUP`.

- [ ] **Step 1: Dispatch the workflow on the implementation commit**

  Push only the explicitly authorized performance branch after local verification, dispatch `Phase 9 gate evidence audit`, and wait for all jobs. If the performance job or audit fails, do not create a receipt.

- [ ] **Step 2: Download and verify the artifact**

  Verify the artifact is not expired, has the exact name, has the expected SHA-256 digest, contains the exact candidate commit, six scenario records, seven gate mappings, five measured samples plus one warm-up, and no sensitive strings.

- [ ] **Step 3: Record the receipt and render the matrix**

  Set `evaluationMode` to `candidate`, select only the new performance receipt, run `node tools/phase9/render.mjs ... --check`, and require seven new `PASS` rows, zero `FAILED`, three `DEFERRED`, and `releaseReady=false`.

- [ ] **Step 4: Commit evidence-only changes**

  Commit: `git add docs/superpowers/evidence/phase9 docs/superpowers/plans/2026-09-14-phase9-batch-d-performance.md && git commit -m "evidence: record phase9 performance baseline"`

### Task 4: Final audit and handoff

**Files:**
- Modify: `docs/superpowers/plans/2026-09-14-phase9-batch-d-performance.md`
- Modify: ignored `.superpowers/sdd/2026-09-14-phase9-batch-d-performance/progress.md`

- [ ] **Step 1: Run the complete local verification set**

  Run: `pnpm test:phase9:performance`, `node --test tools/phase9/validate.test.mjs tools/phase9/audit.test.mjs`, `node --test tools/workspace-smoke/workspace-smoke.test.mjs`, `node tools/phase9/render.mjs --registry tools/phase9/gates.json --baseline docs/superpowers/evidence/phase9/baseline.json --receipts docs/superpowers/evidence/phase9/receipts --out-json docs/superpowers/evidence/phase9/gate-matrix.json --out-markdown docs/superpowers/evidence/phase9/gate-matrix.md --check`, and `git diff --check`.

- [ ] **Step 2: Inspect the hosted audit artifact**

  Confirm the seven performance gates are `PASS`, all seven performance IDs map to the same successful run/attempt/artifact, all seven `P9-PERF-*` rows have no missing evidence, the three Phase 8 rows remain `DEFERRED`, and `releaseReady=false`.

- [ ] **Step 3: Update the plan ledger and final review**

  Record run ID, attempt, artifact ID/digest, candidate SHA, matrix counts, and any stability observations. A final reviewer must report Spec PASS, Quality APPROVED, and no P0–P2 findings before the branch is offered for push.

- [ ] **Step 4: Stop before release operations**

  Do not create a PR, merge, tag, GitHub Release, signature, or legal approval as part of this plan. The next handoff is a user authorization decision for push/PR only.

## Plan self-review

- All seven `P9-PERF-*` gates are mapped to Tasks 1–3.
- The hardware gate has an explicit artifact identity and digest check.
- The plan keeps the existing evidence-audit fail-closed behavior and does not convert deferred Phase 8 gates into passes.
- No absolute, machine-speed-dependent threshold is introduced; repeatability and correctness are enforced instead.
