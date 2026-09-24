# Phase 9 Batch C Upstream Code-OSS Regression Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an independently auditable hosted `P9-UPSTREAM-CODEOSS` regression job and record a fresh candidate receipt without promoting any performance, signing, legal, or release gate.

**Architecture:** Extend the existing read-only `phase9-gates.yml` workflow with one fixed Ubuntu job that runs the repository's complete workspace verification command. Because adding a workflow job changes tested content, rerun the complete Batch B matrix at the new candidate commit and replace the old candidate receipt with one receipt covering the five matrix gates plus the upstream gate.

**Tech Stack:** GitHub Actions on `ubuntu-24.04`, pinned checkout/setup actions already used by the workflow, Node.js 24.18.0, pnpm 11.4.0, Node test runner, canonical Phase 9 receipt and matrix tooling.

**Spec:** `docs/superpowers/specs/2026-09-13-phase9-gate-evidence-matrix-design.md` §13 and `tools/phase9/gates.json` gate `P9-UPSTREAM-CODEOSS`.

## Global Constraints

- Keep workflow permissions read-only: `actions: read` and `contents: read`; add no secrets, inputs, network credentials, PR, merge, tag, Release, or signing behavior.
- Keep the exact upstream command `pnpm test:workspace` and job name `phase9-upstream-codeoss`.
- Use only pinned action SHAs and `ubuntu-24.04`, matching the existing Phase 9 jobs.
- Do not modify product runtime, producer/foundation workflows, signing behavior, legal records, or performance thresholds.
- Candidate evidence is valid only for the exact tested commit and an evidence-only descendant under `docs/superpowers/evidence/phase9/`.
- Preserve exactly the three approved Phase 8 deferred gates and keep `releaseReady=false`.

---

### Task 1: Add the upstream workflow contract

**Files:**
- Modify: `.github/workflows/phase9-gates.yml`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`
- Test: `tools/workspace-smoke/workspace-smoke.test.mjs`

**Interfaces:**
- Consumes: the existing pinned checkout, pnpm, and Node setup pattern in `phase9-offline`.
- Produces: a job named `phase9-upstream-codeoss` whose only verification command is `pnpm test:workspace`, plus job-local contract assertions that reject command or job swaps.

- [ ] **Step 1: Add a failing job-local contract assertion**

  Extend the workflow contract test to extract the `phase9-upstream-codeoss` job and assert that its run-step inventory contains exactly `pnpm test:workspace`; assert the job uses `ubuntu-24.04`, `timeout-minutes: 30`, and the pinned checkout/setup actions already required by the Phase 9 workflow.

- [ ] **Step 2: Run the focused contract test**

  Run `node --test tools/workspace-smoke/workspace-smoke.test.mjs`. Expected result: FAIL because the workflow has no `phase9-upstream-codeoss` job yet.

- [ ] **Step 3: Add the fixed upstream job**

  Add the job after `phase9-fault-injection`:

  ```yaml
  phase9-upstream-codeoss:
    runs-on: ubuntu-24.04
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803 # v6
        with:
          fetch-depth: 0
          persist-credentials: false
      - uses: pnpm/action-setup@f40ffcd9367d9f12939873eb1018b921a783ffaa # v4
      - uses: actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38 # v6
        with:
          node-version: 24.18.0
          cache: pnpm
      - run: pnpm install --frozen-lockfile
      - run: pnpm test:workspace
  ```

- [ ] **Step 4: Run focused and full local checks**

  Run:

  ```powershell
  node --test tools/workspace-smoke/workspace-smoke.test.mjs
  node --test tools/phase9/validate.test.mjs tools/phase9/audit.test.mjs
  ```

  Expected: the workflow contract and all 98 Phase 9 tests pass.

- [ ] **Step 5: Commit the workflow change**

  ```powershell
  git add -- .github/workflows/phase9-gates.yml tools/workspace-smoke/workspace-smoke.test.mjs
  git commit -m "ci: add phase9 upstream code-oss regression"
  ```

### Task 2: Execute the complete candidate workflow

**Files:**
- No repository files are changed.

**Interfaces:**
- Consumes: the exact Task 1 commit pushed to the authorized remotes.
- Produces: one successful workflow run with `audit`, `phase9-offline`, `phase9-matrix-e2e`, `phase9-fault-injection`, and `phase9-upstream-codeoss` all successful on the exact candidate SHA.

- [ ] **Step 1: Push only after explicit remote authorization**

  Verify the local commit, branch, and clean tracked worktree. Push the branch normally to GitHub and Gitee only after the user explicitly authorizes that remote action; do not create a PR or merge.

- [ ] **Step 2: Dispatch and inspect the exact run**

  Dispatch `phase9-gates.yml` with `--ref codex/phase9-batch-c-matrix-execution` (or the explicitly authorized branch), then require `status=completed`, `conclusion=success`, `event=workflow_dispatch`, matching `head_sha`, and all five execution/audit jobs successful.

- [ ] **Step 3: Preserve fail-closed behavior**

  If any job fails, is skipped, or has a mismatched identity, do not create a receipt; leave all new upstream/performance rows `MISSING` and report the exact failed job.

### Task 3: Replace the candidate receipt and matrix

**Files:**
- Create: `docs/superpowers/evidence/phase9/receipts/github-actions-${verifiedRunId}-${verifiedAttempt}.json`
- Modify: `docs/superpowers/evidence/phase9/baseline.json`
- Generate: `docs/superpowers/evidence/phase9/gate-matrix.json`
- Generate: `docs/superpowers/evidence/phase9/gate-matrix.md`

**Interfaces:**
- Consumes: immutable run/job metadata from Task 2 and the existing canonical receipt schema.
- Produces: a candidate-mode receipt covering `P9-MATRIX-CONTRACT`, `P9-MATRIX-E2E`, `P9-MATRIX-FAULT-INJECTION`, `P9-MATRIX-INTEGRATION`, `P9-MATRIX-UNIT`, and `P9-UPSTREAM-CODEOSS`.

- [ ] **Step 1: Write the exact receipt**

  Include the exact 40-character candidate SHA, run ID/attempt, workflow path, `workflow_dispatch`, successful job names, and no artifacts because these six gate verifications declare no required artifact.

- [ ] **Step 2: Replace the old candidate baseline**

  Set `evaluationMode` to `candidate`, bind `candidateCommit` to the new Task 1 commit, and select only the new receipt. Do not retain the old candidate receipt because its candidate SHA predates the workflow change.

- [ ] **Step 3: Render and verify**

  Run the canonical renderer and `--check`. Require six Phase 9 PASS rows, zero FAILED rows, exactly three approved Phase 8 DEFERRED rows, performance rows still MISSING, and `releaseReady=false`.

- [ ] **Step 4: Commit evidence-only changes**

  ```powershell
  git add -- docs/superpowers/evidence/phase9
  git commit -m "evidence: record phase9 upstream regression"
  ```

### Task 4: Audit the evidence-only descendant

**Files:**
- No product files are changed.

**Interfaces:**
- Consumes: the Task 3 evidence-only descendant and the read-only audit workflow.
- Produces: an immutable audit artifact whose matrix agrees with the six PASS rows and preserves all remaining MISSING/DEFERRED rows.

- [ ] **Step 1: Dispatch the audit workflow on the evidence-only descendant**

  Verify `git diff --name-only <candidate>..HEAD` contains only `docs/superpowers/evidence/phase9/`, then dispatch `phase9-gates.yml` and require the `audit` job plus all execution jobs to succeed.

- [ ] **Step 2: Inspect the immutable audit artifact**

  Download the artifact by ID, verify the candidate SHA, six upstream/matrix PASS rows, zero FAILED rows, three DEFERRED rows, performance rows MISSING, and `releaseReady=false`. Record the artifact ID and digest in the SDD ledger.

- [ ] **Step 3: Run local verification**

  Run the Phase 9 tests, workspace verification with the repository's bundled CMake path, the renderer `--check`, and `git diff --check`.

## Completion Criteria

- `P9-UPSTREAM-CODEOSS` has a fresh candidate-bound receipt and independently audited PASS.
- The five existing Batch B gates remain PASS after the candidate refresh.
- All seven performance gates remain MISSING and exactly three Phase 8 gates remain DEFERRED.
- `releaseReady=false`; no Release, signing, PR, or merge action occurs.
