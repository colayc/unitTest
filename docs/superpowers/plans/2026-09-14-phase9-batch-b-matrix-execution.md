# Phase 9 Batch B Matrix Execution Evidence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Close the five executable Phase 9 matrix gates with fresh, candidate-bound GitHub Actions evidence while leaving performance, upstream, signing, legal, and release work unchanged.

**Architecture:** Extend the existing read-only Phase 9 workflow with three fixed execution jobs: one offline job proving the contract/unit/integration gates, one hosted E2E job, and one native fault-injection job. The existing audit job remains the only network reader and will re-audit a small canonical receipt after the execution run succeeds. The candidate baseline will be changed only through evidence-only files, so the fresh run remains bound to the tested GitHub master commit.

**Tech Stack:** GitHub Actions on `ubuntu-24.04`, Node.js 24.18.0, pnpm 11.4.0, existing repository test scripts, Phase 9 canonical receipt/audit tools.

**Spec:** `docs/superpowers/specs/2026-09-13-phase9-gate-evidence-matrix-design.md`

## Global Constraints

- The only gates in scope are `P9-MATRIX-CONTRACT`, `P9-MATRIX-UNIT`, `P9-MATRIX-INTEGRATION`, `P9-MATRIX-E2E`, and `P9-MATRIX-FAULT-INJECTION`.
- Do not change product runtime, producer/foundation workflows, signing, tags, GitHub Releases, performance gates, or `P9-UPSTREAM-CODEOSS`.
- Keep workflow permissions exactly `actions: read` and `contents: read`; use the already pinned action SHAs and fixed repository coordinates.
- Do not fabricate a receipt: a gate is `PASS` only when the exact run, attempt, head SHA, job conclusion, and required artifact metadata are available to the auditor.
- Keep `P8-SIGN-WINDOWS`, `P8-LEGAL-THIRD-PARTY`, and `P8-DOCS-CLOSEOUT` deferred; `releaseReady` must remain `false`.
- Never force-push, merge, tag, sign, or publish. Remote writes require a separate explicit authorization.

---

### Task 1: Add fixed Phase 9 matrix execution jobs

**Files:**
- Modify: `.github/workflows/phase9-gates.yml`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`

**Interfaces:**
- Consumes: existing pinned checkout/setup steps and repository test scripts.
- Produces: jobs named `phase9-offline`, `phase9-matrix-e2e`, and `phase9-fault-injection`, each with a fixed command and no dynamic inputs.

- [ ] **Step 1: Add failing workflow contract assertions**

Assert the workflow contains the three fixed jobs and exact commands:

```js
assert.match(workflow, /phase9-offline:/u);
assert.match(workflow, /node --test tools\/phase9\/validate\.test\.mjs tools\/phase9\/audit\.test\.mjs/u);
assert.match(workflow, /phase9-matrix-e2e:/u);
assert.match(workflow, /pnpm test:e2e/u);
assert.match(workflow, /phase9-fault-injection:/u);
assert.match(workflow, /pnpm test:e2e:native/u);
```

- [ ] **Step 2: Run the contract test and verify it fails**

Run `pnpm test:workspace`. It must fail because the execution jobs do not yet exist.

- [ ] **Step 3: Add the three jobs**

Use `ubuntu-24.04`, `timeout-minutes: 30`, the existing pinned setup actions, `pnpm install --frozen-lockfile`, and static `run` commands. The native job must invoke the existing Linux offline wrapper before `pnpm test:e2e:native` so it does not silently use network-installed toolchains.

- [ ] **Step 4: Run workflow contract and Phase 9 tests**

Run `pnpm test:workspace` and `pnpm test:phase9-gates`; both must pass without contacting GitHub.

- [ ] **Step 5: Commit**

```powershell
git add -- .github/workflows/phase9-gates.yml tools/workspace-smoke/workspace-smoke.test.mjs
git commit -m "ci: execute phase 9 matrix gates"
```

### Task 2: Run the candidate matrix workflow and capture immutable identities

**Files:**
- No repository files are changed in this task.

**Interfaces:**
- Consumes: the pushed candidate commit and workflow run.
- Produces: one successful workflow run with successful jobs `phase9-offline`, `phase9-matrix-e2e`, and `phase9-fault-injection`, plus its immutable run attempt and head SHA.

- [ ] **Step 1: Verify the candidate commit**

Confirm the GitHub `master` SHA, workflow file SHA, and branch ancestry before dispatch. Use a fixed `gh workflow run` invocation with no user-provided inputs.

- [ ] **Step 2: Wait for completion**

Use the exact returned run identity, then query the run and jobs. Require `status=completed`, `conclusion=success`, `event=workflow_dispatch`, matching `head_sha`, and one successful job for each required job name. If any job fails or is skipped, keep all five gates `MISSING` and stop.

- [ ] **Step 3: Record the run coordinates**

Save the run ID, attempt, candidate SHA, workflow path, event, and successful job names as temporary evidence. Do not infer IDs from a recency list.

### Task 3: Create the candidate receipt and audited matrix

**Files:**
- Create: `docs/superpowers/evidence/phase9/receipts/github-actions-${verifiedRunId}-${verifiedAttempt}.json` (where both values come directly from the completed workflow identity)
- Modify: `docs/superpowers/evidence/phase9/baseline.json`
- Generate: `docs/superpowers/evidence/phase9/gate-matrix.json`
- Generate: `docs/superpowers/evidence/phase9/gate-matrix.md`

**Interfaces:**
- Consumes: immutable run/job data from Task 2 and the existing canonical receipt schema.
- Produces: a candidate-mode recorded matrix with the five matrix gates `PASS`, exactly three approved deferred gates, and all unrelated missing gates unchanged.

- [ ] **Step 1: Write the receipt from verified metadata**

Map only the five in-scope gate IDs. Use `kind: "github-actions"`, the exact 40-character lowercase candidate SHA, canonical decimal run/attempt IDs, workflow path `.github/workflows/phase9-gates.yml`, event `workflow_dispatch`, conclusion `success`, and the three required successful jobs. Do not add an artifact identity unless the run actually exposes one required by a gate.

- [ ] **Step 2: Switch the baseline to candidate mode**

Set `evaluationMode` to `candidate`, set `candidateCommit` to the tested GitHub master SHA, and select the new receipt plus any still-valid historical receipts. Keep the three Phase 8 deferments unchanged.

- [ ] **Step 3: Generate and check the matrix**

Run:

```powershell
node tools/phase9/render.mjs --registry tools/phase9/gates.json --baseline docs/superpowers/evidence/phase9/baseline.json --receipts docs/superpowers/evidence/phase9/receipts --repository-root . --json-out docs/superpowers/evidence/phase9/gate-matrix.json --markdown-out docs/superpowers/evidence/phase9/gate-matrix.md
node tools/phase9/render.mjs --registry tools/phase9/gates.json --baseline docs/superpowers/evidence/phase9/baseline.json --receipts docs/superpowers/evidence/phase9/receipts --repository-root . --json-out docs/superpowers/evidence/phase9/gate-matrix.json --markdown-out docs/superpowers/evidence/phase9/gate-matrix.md --check
```

Require the five in-scope gates to be `PASS`, `counts.deferred=3`, and `releaseReady=false` because performance/upstream/missing gates and Phase 8 deferments remain.

- [ ] **Step 4: Commit evidence-only changes**

```powershell
git add -- docs/superpowers/evidence/phase9
git commit -m "docs: record phase 9 matrix execution evidence"
```

### Task 4: Re-run the read-only audit and report the remaining batches

**Files:**
- No product files are changed.

- [ ] **Step 1: Run the audit workflow on the evidence-only descendant**

Confirm the candidate lineage contains only `docs/superpowers/evidence/phase9/` changes, then dispatch the fixed audit workflow. Require its `audit` job to succeed and its uploaded matrix to agree with the recorded matrix.

- [ ] **Step 2: Inspect the audited matrix**

Download the audit artifact by immutable artifact ID. Assert the five matrix gates remain `PASS`, no gate is unexpectedly `FAILED`, exactly three Phase 8 gates are `DEFERRED`, and `releaseReady=false`.

- [ ] **Step 3: Run local verification**

Run `pnpm test:phase9-gates`, `pnpm test:workspace`, and `pnpm check:phase9-gates`.

- [ ] **Step 4: Report the next independent batch**

List the remaining missing groups explicitly: performance (`P9-PERF-*`, seven gates), upstream Code-OSS (`P9-UPSTREAM-CODEOSS`), and the three deferred Phase 8 gates. Do not mark Phase 9 complete until those groups have their own successful candidate receipts.

## Completion Criteria

- The five matrix execution gates have fresh, independently auditable candidate receipts.
- The audited matrix has no identity mismatch and preserves `releaseReady=false`.
- Performance, upstream, signing, legal, and documentation work remains visibly `MISSING`/`DEFERRED`, never silently promoted.
