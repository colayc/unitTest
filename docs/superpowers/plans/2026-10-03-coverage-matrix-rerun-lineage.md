# Coverage Matrix Rerun Lineage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the foundation coverage matrix consume the exact backend evidence artifacts produced by its upstream jobs, including when only one upstream job is rerun.

**Architecture:** Each native coverage job exposes the artifact ID returned by its backend evidence upload step as a job output. The matrix job downloads by those three IDs through `needs`, eliminating its dependency on the workflow-wide `github.run_attempt` suffix.

**Tech Stack:** GitHub Actions YAML, Node.js `node:test` workflow contract tests, pnpm.

**Spec:** The failed foundation run `37079005526` and its `coverage-backend-matrix` artifact lineage failure.

## Global Constraints

- Keep all existing artifact names and retention behavior unchanged.
- Do not weaken coverage requirements or allow missing evidence.
- Do not publish a Release or enable signing.
- Preserve pinned GitHub Action SHAs.
- Keep the fix limited to coverage artifact lineage and its contract test.

## Review Focus

- A partial rerun must be able to mix artifacts from different attempts; test that matrix downloads use upstream IDs, not `github.run_attempt`.
- Each backend upload must expose the exact upload action output; test all three jobs.
- The matrix must retain all three backend dependencies and paths; test job ordering and download mapping.
- Artifact names remain attempt-scoped for uniqueness; test that upload names are unchanged.
- Missing upstream output must not silently fall back to a name; test that every matrix download uses `artifact-ids` and has no `name` input.

### Task 1: Add the failing workflow contract

**Files:**
- Modify: `tools/release/producer/workflow-contract.test.mjs`

- [ ] **Step 1: Add assertions for coverage backend artifact outputs and matrix ID downloads**

Assert that `coverage-linux-gcc`, `coverage-linux-clang`, and `coverage-windows-clang-cl` each expose `backend_artifact_id` from an identified upload step, and that `coverage-backend-matrix` maps each download's `artifact-ids` to the corresponding `needs.<job>.outputs.backend_artifact_id` with no `name` input.

- [ ] **Step 2: Run the focused contract test and verify it fails**

Run: `pnpm exec node --test tools/release/producer/workflow-contract.test.mjs`

Expected: FAIL because the current workflow has no backend artifact job outputs and matrix downloads are keyed by `github.run_attempt` names.

### Task 2: Implement artifact-ID lineage in the workflow

**Files:**
- Modify: `.github/workflows/foundation.yml:604-903`

- [ ] **Step 1: Expose upload artifact IDs from each coverage job**

Add `backend_artifact_id` job outputs sourced from identified backend upload steps, without changing their existing attempt-scoped artifact names or paths.

- [ ] **Step 2: Download by upstream artifact IDs in the matrix job**

Replace the three matrix `name: ...-${{ github.run_attempt }}` inputs with `artifact-ids` expressions referencing the matching coverage job outputs. Keep the existing destination paths and validation command unchanged.

- [ ] **Step 3: Run the focused contract test and verify it passes**

Run: `pnpm exec node --test tools/release/producer/workflow-contract.test.mjs`

Expected: PASS.

### Task 3: Verify, commit, and prepare the PR

**Files:**
- Verify: `.github/workflows/foundation.yml`, `tools/release/producer/workflow-contract.test.mjs`

- [ ] **Step 1: Run the targeted release workflow test suite**

Run: `pnpm run test:release-producer`

Expected: PASS with zero failures.

- [ ] **Step 2: Inspect the diff and repository state**

Run: `git diff --check` and `git status --short`.

Expected: only the planned workflow, contract test, and plan file are changed.

- [ ] **Step 3: Commit the fix**

```bash
git add .github/workflows/foundation.yml tools/release/producer/workflow-contract.test.mjs docs/superpowers/plans/2026-10-03-coverage-matrix-rerun-lineage.md
git commit -m "fix: preserve coverage artifact lineage across reruns"
```

- [ ] **Step 4: Push and create the authorized PR**

Push `codex/fix-coverage-matrix-rerun-lineage` to GitHub and Gitee, then create a GitHub PR against `master`; do not merge, publish a Release, or enable signing.
