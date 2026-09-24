# Phase 9 Squash Evidence Lineage Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the checked-in Phase 9 evidence audit fail-closed but valid after PR #29 was merged with squash, while preserving the existing evidence as historical and keeping `releaseReady=false`.

**Architecture:** The Phase 9 baseline will explicitly use `historical` evaluation for receipts collected on the pre-squash candidate commit. Both the validator and renderer will skip candidate ancestry enforcement in historical mode while retaining receipt identity, artifact, gate, and schema validation. Generated gate-matrix JSON/Markdown will be regenerated from the same inputs and checked by the existing CI contract.

**Tech Stack:** Node.js ESM CLI, canonical JSON, Markdown renderer, GitHub Actions checks.

**Spec:** `tools/phase9/validate.mjs`, `tools/phase9/render.mjs`, `tools/phase9/gates.schema.json`, and the Phase 9 evidence contract documented in `docs/native-e2e.md`.

## Global Constraints

- Historical evidence must never set `releaseReady=true`.
- Receipt IDs, candidate commit, artifact identities, and gate IDs remain exact and unchanged.
- Do not reinterpret missing Phase 8 gates or the three approved Phase 8 deferred gates as passing.
- Do not publish a Release or enable signing.
- Do not modify product/runtime implementation code.
- Generated evidence must be canonical and pass `pnpm check:phase9-gates`.

## Review Focus

- A squash merge makes the old candidate commit non-ancestor of master; historical mode must render without weakening receipt validation.
- Historical mode must remain permanently non-releaseable even if every selected receipt is PASS.
- Evidence-only and tested-content changes must retain their existing candidate-mode behavior in unit tests.
- Generated JSON and Markdown must agree on counts, statuses, candidate commit, and `releaseReady=false`.
- The repair must not silently select untracked diagnostic receipts or generated artifacts.

### Task 1: Switch the checked-in baseline to historical evidence

**Files:**
- Modify: `docs/superpowers/evidence/phase9/baseline.json`
- Modify: `docs/superpowers/evidence/phase9/gate-matrix.json`
- Modify: `docs/superpowers/evidence/phase9/gate-matrix.md`
- Modify: `tools/phase9/validate.mjs`
- Test: `tools/phase9/validate.test.mjs`

**Interfaces:**
- Consumes: the existing selected receipt IDs and candidate commit in `baseline.json`.
- Produces: a canonical historical matrix with the same receipt bindings and `releaseReady=false`.

- [x] **Step 1: Add a regression test for a non-ancestor historical baseline**

  Extend the existing CLI renderer coverage near the historical-mode tests in `tools/phase9/validate.test.mjs` with a fixture whose selected candidate is a valid commit that is not an ancestor of the current repository commit. Assert that historical mode succeeds, preserves the selected receipt-backed PASS rows, and returns `releaseReady === false`.

- [x] **Step 2: Run the focused Phase 9 test and verify the contract**

  Run:

  ```powershell
  node --test tools/phase9/validate.test.mjs
  ```

  Expected: PASS, including the new historical non-ancestor case.

- [x] **Step 3: Change only the checked-in baseline evaluation mode**

  In `docs/superpowers/evidence/phase9/baseline.json`, change:

  ```json
  "evaluationMode": "candidate"
  ```

  to:

  ```json
  "evaluationMode": "historical"
  ```

  Keep `candidateCommit` and `receiptIds` byte-for-byte otherwise unchanged.

- [x] **Step 4: Regenerate the checked-in matrix**

  Run:

  ```powershell
  node tools/phase9/validate.mjs --registry tools/phase9/gates.json --baseline docs/superpowers/evidence/phase9/baseline.json --receipts docs/superpowers/evidence/phase9/receipts --repository-root . --out .tmp-phase9-recorded.json --requests-out .tmp-phase9-requests.json
  node tools/phase9/render.mjs --registry tools/phase9/gates.json --baseline docs/superpowers/evidence/phase9/baseline.json --receipts docs/superpowers/evidence/phase9/receipts --repository-root . --json-out docs/superpowers/evidence/phase9/gate-matrix.json --markdown-out docs/superpowers/evidence/phase9/gate-matrix.md
  ```

  Expected: the matrix renders successfully, retains the existing PASS/MISSING/DEFERRED gate decisions, changes the mode to `historical`, and reports `releaseReady=false`.

- [x] **Step 5: Run the complete local Phase 9 gate check**

  Run:

  ```powershell
  pnpm check:phase9-gates
  node --test tools/phase9/validate.test.mjs tools/phase9/audit.test.mjs
  ```

  Expected: all commands pass; no product code or unrelated evidence file changes appear.

- [ ] **Step 6: Review the diff and commit the focused repair**

  Run:

  ```powershell
  git diff --check
  git diff -- docs/superpowers/evidence/phase9/baseline.json docs/superpowers/evidence/phase9/gate-matrix.json docs/superpowers/evidence/phase9/gate-matrix.md tools/phase9/validate.test.mjs
  git status --short
  git add docs/superpowers/evidence/phase9/baseline.json docs/superpowers/evidence/phase9/gate-matrix.json docs/superpowers/evidence/phase9/gate-matrix.md tools/phase9/validate.test.mjs
  git commit -m "fix(phase9): preserve evidence across squash merge"
  ```

  Expected: one focused commit containing only the baseline/matrix/test repair.

### Task 2: Re-run hosted audit and foundation verification

**Files:**
- No source changes; inspect GitHub Actions runs and receipts only.

**Interfaces:**
- Consumes: the repair commit from Task 1.
- Produces: a hosted Phase 9 audit result and a new foundation verification result for the repaired master lineage.

- [ ] **Step 1: Push the repair branch and create a review PR**

  Push only after explicit user authorization:

  ```powershell
  git push github HEAD:codex/fix-phase9-squash-evidence-lineage
  gh pr create --repo colayc/unitTest --base master --head codex/fix-phase9-squash-evidence-lineage --title "fix: preserve Phase 9 evidence across squash merge" --body "Preserve pre-squash Phase 9 receipts as historical evidence while keeping releaseReady=false."
  ```

- [ ] **Step 2: Verify required PR checks**

  Run:

  ```powershell
  gh pr checks <PR_NUMBER> --repo colayc/unitTest --watch
  ```

  Expected: required checks pass; no release workflow or signing job is requested.

- [ ] **Step 3: After explicit merge authorization, merge and sync**

  Merge the PR using the repository-allowed method, then synchronize GitHub master to Gitee master with the explicitly authorized policy for that update. Do not publish a Release or enable signing.

- [ ] **Step 4: Dispatch fresh trusted producer and unsigned foundation only after audit is green**

  Use the successful producer coordinates and `release_signing_required=0`; verify the resulting P8 reports and keep signing/legal/documentation gates closed or deferred as specified.
