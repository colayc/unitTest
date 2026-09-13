# Phase 9 Gate Evidence Matrix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a fail-closed Phase 9 gate catalog, durable evidence receipts, deterministic offline matrix, and read-only GitHub Actions audit without changing product, packaging, signing, or release behavior.

**Architecture:** A canonical closed JSON registry defines authoritative requirements and stable gate IDs. Canonical baseline and receipt documents feed an offline validator and renderer; a separate auditor validates fixed GitHub API snapshots, while a read-only workflow performs the network fetch and uploads the audited matrix. The baseline explicitly distinguishes `historical` evidence from the current `candidate`: historical PASS rows never make a release ready. Batch A may complete with honest `MISSING` and exactly three approved `DEFERRED` gates, but `releaseReady` remains false until every missing, failed, and deferred gate is closed on a candidate-mode baseline.

**Tech Stack:** Node.js 24.18.0 ESM, pnpm 11.4.0, Ajv 8.20.0 with JSON Schema 2020-12, Node `node:test`, Git, GitHub CLI/API, GitHub Actions on `ubuntu-24.04`.

## Global Constraints

- Implement against the confirmed design `docs/superpowers/specs/2026-09-13-phase9-gate-evidence-matrix-design.md` and keep its four-state semantics exact.
- Do not modify product runtime, protocol, Service, Code-OSS Extension, `foundation.yml`, `release-inputs.yml`, signing, tags, or GitHub Release behavior.
- The only allowed deferred gates are `P8-SIGN-WINDOWS`, `P8-LEGAL-THIRD-PARTY`, and `P8-DOCS-CLOSEOUT`; tests must pin this exact sorted set.
- `catalogComplete=true` means complete requirement mapping, not release readiness. `releaseReady=true` requires `evaluationMode=candidate`, zero `MISSING`, zero `FAILED`, and zero `DEFERRED`.
- Every automated `PASS` binds one exact 40-character lowercase candidate commit and a canonical GitHub run attempt, job set, artifact IDs, and lowercase SHA-256 digests.
- JSON inputs are canonical UTF-8, newline-terminated, closed objects with bounded bytes. Canonical round-trip mismatch rejects duplicate keys, alternate key order, or non-canonical whitespace before semantic evaluation.
- No command string from JSON may be executed. Network access belongs only to the Phase 9 GitHub Actions audit step; unit tests and root `pnpm verify` remain offline.
- GitHub workflow permissions are exactly `actions: read` and `contents: read`; it reads no secrets and accepts no dynamic repository, workflow, run ID, or command input.
- Current evidence is the unsigned producer run `34728889872` and foundation run `34731651809`, both attempt `1` on `1e20a5ceee370e9823f9fe05f40980f893cd9ed3`. It proves unsigned qualification only.
- Preserve the user-owned untracked `.merge-stash-20260903/` directory and all unrelated worktree content.
- Do not push to GitHub or Gitee, create a PR, merge, tag, sign, or publish until the user gives the corresponding explicit authorization.

---

## File Structure

### New implementation files

- `tools/phase9/canonical-json.mjs` — bounded canonical JSON reader/writer and stable Phase 9 error constructor.
- `tools/phase9/gates.schema.json` — closed JSON Schema for registry, baseline, GitHub Actions receipt, manual receipt, matrix, and audit snapshot shapes.
- `tools/phase9/gates.json` — authoritative source list and stable gate definitions.
- `tools/phase9/validate.mjs` — semantic validation, source coverage, deferred boundary, candidate binding, status evaluation, and CLI.
- `tools/phase9/render.mjs` — deterministic JSON/Markdown generation and byte-level `--check` mode.
- `tools/phase9/audit.mjs` — offline validation of GitHub API snapshots against receipts and audited matrix generation.
- `tools/phase9/validate.test.mjs` — canonical parsing, registry, receipt, lineage, status, and renderer tests.
- `tools/phase9/audit.test.mjs` — offline GitHub run/job/artifact identity and expiry tests.
- `docs/superpowers/evidence/phase9/baseline.json` — evaluated candidate and selected receipt IDs.
- `docs/superpowers/evidence/phase9/receipts/github-actions-34728889872-1.json` — producer evidence receipt.
- `docs/superpowers/evidence/phase9/receipts/github-actions-34731651809-1.json` — unsigned foundation evidence receipt.
- `docs/superpowers/evidence/phase9/gate-matrix.json` — generated machine-readable recorded matrix.
- `docs/superpowers/evidence/phase9/gate-matrix.md` — generated human-readable recorded matrix.
- `.github/workflows/phase9-gates.yml` — read-only online audit workflow.

### Modified files

- `package.json` — add `test:phase9-gates` and `check:phase9-gates`, then include them in `test` and `verify`.
- `tools/workspace-smoke/workspace-smoke.test.mjs` — pin workflow permissions, triggers, action SHAs, secret absence, static commands, artifact upload, and the exact three deferred gates.

## Stable Interfaces

All later tasks use these exact exports:

```js
// tools/phase9/canonical-json.mjs
export class Phase9GateError extends Error {}
export function phase9Failure(code, message, cause)
export function encodeCanonicalJson(value)
export async function readCanonicalJson(path, { label, maxBytes })
export async function writeCanonicalJson(path, value)

// tools/phase9/validate.mjs
export const ALLOWED_DEFERRED_GATE_IDS
export const EVIDENCE_ONLY_PATHS
export function validateRegistry(value)
export function validateBaseline(value)
export function validateReceipt(value)
export function validateCandidateChanges({ candidateCommit, currentCommit, changedPaths })
export function evaluateRecordedMatrix({ registry, baseline, receipts, currentCommit, changedPaths })
export async function loadPhase9Inputs({ registryPath, baselinePath, receiptsDirectory })

// tools/phase9/render.mjs
export function renderMatrixJson(matrix)
export function renderMatrixMarkdown(matrix)
export async function writeMatrixOutputs({ matrix, jsonPath, markdownPath, check })

// tools/phase9/audit.mjs
export function auditGithubReceipt({ receipt, runSnapshot, jobSnapshot, artifactSnapshot })
export function evaluateAuditedMatrix({ recordedMatrix, receipts, snapshotsByRunId })
```

The CLI contracts are:

```text
node tools/phase9/validate.mjs --registry <file> --baseline <file> --receipts <directory> --repository-root <directory> --out <file> --requests-out <file>
node tools/phase9/render.mjs --registry <file> --baseline <file> --receipts <directory> --repository-root <directory> --json-out <file> --markdown-out <file> [--check]
node tools/phase9/audit.mjs --recorded-matrix <file> --receipts <directory> --snapshots <directory> --json-out <file> --markdown-out <file>
```

---

### Task 1: Canonical bounded JSON and closed schemas

**Files:**
- Create: `tools/phase9/canonical-json.mjs`
- Create: `tools/phase9/gates.schema.json`
- Create: `tools/phase9/validate.test.mjs`

**Interfaces:**
- Produces: `Phase9GateError`, `phase9Failure`, `encodeCanonicalJson`, `readCanonicalJson`, and `writeCanonicalJson`.
- Produces: Schema `$defs` named `registry`, `baseline`, `githubActionsReceipt`, `manualApprovalReceipt`, `matrix`, `runSnapshot`, `jobSnapshot`, and `artifactSnapshot`.
- Consumes: Node.js built-ins only plus the repository's existing Ajv dependency in later semantic validation.

- [ ] **Step 1: Write failing canonical JSON tests**

Create tests that build temporary files and assert exact behavior:

```js
import assert from "node:assert/strict";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  encodeCanonicalJson,
  readCanonicalJson,
} from "./canonical-json.mjs";

test("canonical JSON recursively sorts object keys and ends with one newline", () => {
  assert.equal(
    encodeCanonicalJson({ z: 1, a: { y: 2, x: 3 }, list: [{ b: 2, a: 1 }] }),
    '{\n  "a": {\n    "x": 3,\n    "y": 2\n  },\n  "list": [\n    {\n      "a": 1,\n      "b": 2\n    }\n  ],\n  "z": 1\n}\n',
  );
});

test("reader rejects duplicate keys through canonical round-trip", async () => {
  const root = await mkdtemp(join(tmpdir(), "phase9-json-"));
  const path = join(root, "duplicate.json");
  await writeFile(path, '{"gate":"a","gate":"b"}\n');
  await assert.rejects(
    readCanonicalJson(path, { label: "receipt", maxBytes: 1024 }),
    /PHASE9_GATE_SCHEMA_INVALID: receipt is not canonical JSON/u,
  );
});
```

Add cases for invalid UTF-8, empty input, non-canonical whitespace/key order, a 1,025-byte file with `maxBytes: 1024`, arrays, `null`, and an unsafe top-level primitive.

- [ ] **Step 2: Run the focused test and prove it fails**

Run:

```powershell
node --test tools/phase9/validate.test.mjs
```

Expected: FAIL with `ERR_MODULE_NOT_FOUND` for `tools/phase9/canonical-json.mjs`.

- [ ] **Step 3: Implement the canonical JSON module**

Implement recursive canonicalization and byte comparison with these exact bounds and errors:

```js
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname } from "node:path";

export class Phase9GateError extends Error {
  constructor(code, message, cause) {
    super(`${code}: ${message}`, cause ? { cause } : undefined);
    this.code = code;
  }
}

export function phase9Failure(code, message, cause) {
  return new Phase9GateError(code, message, cause);
}

function canonicalValue(value) {
  if (Array.isArray(value)) return value.map(canonicalValue);
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(
      Object.keys(value).sort((left, right) => left.localeCompare(right, "en"))
        .map((key) => [key, canonicalValue(value[key])]),
    );
  }
  return value;
}

export function encodeCanonicalJson(value) {
  return `${JSON.stringify(canonicalValue(value), null, 2)}\n`;
}

export async function readCanonicalJson(path, { label, maxBytes }) {
  const bytes = await readFile(path);
  if (bytes.length === 0 || bytes.length > maxBytes) {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", `${label} byte length is invalid`);
  }
  let text;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch (error) {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", `${label} is not UTF-8`, error);
  }
  let value;
  try {
    value = JSON.parse(text);
  } catch (error) {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", `${label} is not JSON`, error);
  }
  if (value === null || typeof value !== "object" || encodeCanonicalJson(value) !== text) {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", `${label} is not canonical JSON`);
  }
  return value;
}

export async function writeCanonicalJson(path, value) {
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, encodeCanonicalJson(value), { encoding: "utf8", flag: "w" });
}
```

- [ ] **Step 4: Define the closed JSON Schema**

Create a draft 2020-12 schema with `additionalProperties: false` on every object. Use these exact common patterns:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://unit-test-ide.invalid/schemas/phase9-gates-v1.json",
  "$defs": {
    "commit": { "type": "string", "pattern": "^[0-9a-f]{40}$" },
    "digest": { "type": "string", "pattern": "^[0-9a-f]{64}$" },
    "decimalId": { "type": "string", "pattern": "^[1-9][0-9]*$" },
    "utcIso": { "type": "string", "format": "date-time", "pattern": "Z$" },
    "status": { "enum": ["PASS", "MISSING", "FAILED", "DEFERRED"] },
    "conclusion": { "enum": ["success", "failure", "cancelled", "timed_out", "action_required", "neutral", "skipped"] }
  }
}
```

Define the complete required properties described in the design for registry, sources, gates, verification, deferment, baseline, both receipt kinds, jobs, artifacts, recorded matrix, and normalized run/job/artifact snapshot objects. The baseline requires `evaluationMode` with enum `historical|candidate`; the matrix persists the same mode plus `currentCommit`. Use `oneOf` for `github-actions` versus `manual-approval` receipts and require `schemaVersion: { "const": 1 }` in every repository-controlled persisted root document. Raw ephemeral GitHub API snapshots do not gain synthetic fields: parse them with strict UTF-8 and byte bounds, select only allowlisted fields, then validate the normalized internal objects against the snapshot `$defs`.

- [ ] **Step 5: Run tests and validate the schema itself**

Run:

```powershell
node --test tools/phase9/validate.test.mjs
node -e "const s=require('./tools/phase9/gates.schema.json'); if(s['$schema']!=='https://json-schema.org/draft/2020-12/schema') process.exit(1)"
```

Expected: all canonical JSON tests pass and the schema command exits `0`.

- [ ] **Step 6: Commit Task 1**

```powershell
git add -- tools/phase9/canonical-json.mjs tools/phase9/gates.schema.json tools/phase9/validate.test.mjs
git commit -m "feat: add canonical phase 9 evidence schemas"
```

---

### Task 2: Registry semantics and fail-closed state evaluation

**Files:**
- Create: `tools/phase9/validate.mjs`
- Modify: `tools/phase9/validate.test.mjs`

**Interfaces:**
- Consumes: canonical JSON functions and `gates.schema.json` from Task 1.
- Produces: the `validateRegistry`, `validateBaseline`, `validateReceipt`, `validateCandidateChanges`, `evaluateRecordedMatrix`, and `loadPhase9Inputs` exports.
- Produces: CLI request JSON containing sorted unique canonical run IDs for the online workflow.

- [ ] **Step 1: Write failing exact-deferment and source-coverage tests**

Pin the exact list and verify that data cannot widen it:

```js
import {
  ALLOWED_DEFERRED_GATE_IDS,
  evaluateRecordedMatrix,
  validateRegistry,
} from "./validate.mjs";

test("only the three approved Phase 8 gates may be deferred", () => {
  assert.deepEqual(ALLOWED_DEFERRED_GATE_IDS, [
    "P8-DOCS-CLOSEOUT",
    "P8-LEGAL-THIRD-PARTY",
    "P8-SIGN-WINDOWS",
  ]);
  const registry = validRegistry();
  registry.gates.push(deferredGate("P9-PERF-MEMORY"));
  assert.throws(() => validateRegistry(registry), /PHASE9_DEFERRED_NOT_ALLOWED/u);
});

test("every declared source section must be referenced", () => {
  const registry = validRegistry();
  registry.gates[0].requirementRefs = [];
  assert.throws(() => validateRegistry(registry), /PHASE9_GATE_MISSING: source section/u);
});
```

Add cases for duplicate source path/section, duplicate gate ID, unknown source, missing commands/jobs/artifacts policy, non-canonical sorting, unsafe paths, and a deferred gate without exact `reason` and `resumeCondition` strings.

- [ ] **Step 2: Write failing status evaluation tests**

Use fixtures with one required gate and the three deferred gates. Assert:

```js
test("matrix reports missing evidence without blocking catalog completion", () => {
  const matrix = evaluateRecordedMatrix({
    registry: validRegistry(),
    baseline: validBaseline(),
    receipts: [],
    currentCommit: candidateCommit,
    changedPaths: [],
  });
  assert.equal(matrix.catalogComplete, true);
  assert.equal(matrix.releaseReady, false);
  assert.equal(matrix.counts.missing, 1);
  assert.equal(matrix.counts.deferred, 3);
  assert.equal(matrix.gates.find(({ id }) => id === "P9-MATRIX-UNIT").status, "MISSING");
});
```

Add cases for valid recorded `PASS`, explicit failed run conclusion producing `FAILED`, missing required job/artifact producing `MISSING`, conflicting receipts producing `PHASE9_EVIDENCE_CONFLICT`, and `releaseReady` becoming true only in a synthetic registry with no missing, failed, or deferred gates.

Add two baseline-mode cases: a `historical` baseline may preserve exact receipt-backed historical `PASS` rows but must keep `releaseReady=false`; a `candidate` baseline may become release-ready only when candidate lineage is valid and every gate passes.

- [ ] **Step 3: Run focused tests and prove they fail**

```powershell
node --test tools/phase9/validate.test.mjs
```

Expected: FAIL because `tools/phase9/validate.mjs` does not exist.

- [ ] **Step 4: Implement Ajv and semantic validation**

Compile the root schema once with strict Ajv and formats:

```js
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

export const ALLOWED_DEFERRED_GATE_IDS = Object.freeze([
  "P8-DOCS-CLOSEOUT",
  "P8-LEGAL-THIRD-PARTY",
  "P8-SIGN-WINDOWS",
]);

export const EVIDENCE_ONLY_PATHS = Object.freeze([
  "docs/superpowers/evidence/phase9/",
]);

const ajv = new Ajv2020({ allErrors: true, strict: true });
addFormats(ajv);
```

Use exact-key semantic checks after Schema validation. Require `registry.allowedDeferredGateIds` to equal `ALLOWED_DEFERRED_GATE_IDS`, require deferred IDs to have `disposition=deferred`, require every other gate to have `disposition=required`, and require every declared source section to have at least one reference.

- [ ] **Step 5: Implement deterministic state evaluation**

Resolve receipts by `gateIds`, reject more than one selected receipt for the same gate, and return gates sorted with `localeCompare(..., "en")`. Use these outcome rules in order:

```js
function recordedStatus(gate, receipt) {
  if (gate.disposition === "deferred") return "DEFERRED";
  if (receipt === undefined) return "MISSING";
  if (receipt.evidence.kind === "github-actions" && receipt.evidence.conclusion !== "success") return "FAILED";
  if (!evidenceSatisfiesVerification(gate.verification, receipt)) return "MISSING";
  return "PASS";
}
```

Compute counts from the sorted gate array. Set `releaseReady` only when `baseline.evaluationMode === "candidate"` and every gate is `PASS`; never special-case `catalogComplete` as release readiness. Persist `evaluationMode`, `candidateCommit`, and `currentCommit` in the matrix.

- [ ] **Step 6: Implement the offline CLI parser**

Accept each named flag exactly once, reject unknown flags and missing values without echoing unsafe input. Bounds are:

```js
const MAX_REGISTRY_BYTES = 1024 * 1024;
const MAX_BASELINE_BYTES = 64 * 1024;
const MAX_RECEIPT_BYTES = 256 * 1024;
const MAX_RECEIPTS = 256;
```

Write the recorded matrix with `writeCanonicalJson`. Write request JSON as `{ schemaVersion: 1, runIds: [...] }`, sorted numerically with `BigInt` comparison and rendered as strings.

- [ ] **Step 7: Run focused tests**

```powershell
node --test tools/phase9/validate.test.mjs
```

Expected: all Task 1–2 tests pass.

- [ ] **Step 8: Commit Task 2**

```powershell
git add -- tools/phase9/validate.mjs tools/phase9/validate.test.mjs
git commit -m "feat: validate phase 9 gate states"
```

---

### Task 3: Candidate lineage and evidence-only descendants

**Files:**
- Modify: `tools/phase9/validate.mjs`
- Modify: `tools/phase9/validate.test.mjs`
- Modify: `tools/phase9/gates.schema.json`

**Interfaces:**
- Consumes: `baseline.evaluationMode`, `candidateCommit`, `currentCommit`, and a normalized repository-relative `changedPaths` array.
- Produces: `validateCandidateChanges`, which returns `exact` or `evidence-only-descendant` for a valid candidate-mode lineage and throws `PHASE9_EVIDENCE_UNTRUSTED` otherwise.

- [ ] **Step 1: Write failing lineage tests using a temporary Git repository**

Create a temporary repository with three commits: product candidate, evidence-only descendant, and product-changing descendant. Invoke Git with `execFile`, never with a shell. Assert:

```js
test("candidate evidence survives only an evidence-only descendant", async () => {
  const fixture = await createGitLineageFixture();
  assert.doesNotThrow(() => validateCandidateChanges({
    candidateCommit: fixture.candidate,
    currentCommit: fixture.evidenceCommit,
    changedPaths: ["docs/superpowers/evidence/phase9/receipts/run.json"],
  }));
  assert.throws(() => validateCandidateChanges({
    candidateCommit: fixture.candidate,
    currentCommit: fixture.productCommit,
    changedPaths: ["apps/test-service/internal/task/manager.go"],
  }), /PHASE9_EVIDENCE_UNTRUSTED/u);
});
```

Add rejection for unrelated commits, absolute/backslash paths, an exact prefix alias such as `docs/superpowers/evidence/phase9-evil/x`, and changes to `.github/workflows/foundation.yml`.

- [ ] **Step 2: Run and prove the lineage tests fail**

```powershell
node --test --test-name-pattern "candidate" tools/phase9/validate.test.mjs
```

Expected: FAIL because lineage validation is incomplete.

- [ ] **Step 3: Implement Git metadata collection without shell execution**

Use `execFile("git", [...])` with fixed argument positions:

```js
async function repositoryState(repositoryRoot, candidateCommit) {
  const currentCommit = (await execFileText("git", ["-C", repositoryRoot, "rev-parse", "HEAD"])).trim();
  await execFileText("git", ["-C", repositoryRoot, "merge-base", "--is-ancestor", candidateCommit, currentCommit]);
  const changedPaths = (await execFileText("git", [
    "-C", repositoryRoot,
    "diff", "--name-only", "--diff-filter=ACDMRTUXB",
    `${candidateCommit}..${currentCommit}`,
  ])).split(/\r?\n/u).filter(Boolean);
  return { changedPaths, currentCommit };
}
```

Treat a nonzero ancestry result, malformed Git output, or any non-evidence path as `PHASE9_EVIDENCE_UNTRUSTED`. Error text includes only the normalized repository-relative path, not the repository root.

- [ ] **Step 4: Connect lineage to matrix evaluation**

For `evaluationMode=historical`, require the recorded candidate to be an ancestor of current HEAD, preserve exact receipt-backed rows as historical conclusions, label the matrix with its root `evaluationMode=historical`, and force `releaseReady=false` regardless of counts. The existing root mode is the historical label; do not add a per-row scope field.

For `evaluationMode=candidate`, allow recorded `PASS` rows only when current HEAD equals `baseline.candidateCommit` or every changed path is evidence-only. `validateCandidateChanges` throws on unrelated history or tested-content changes; `evaluateRecordedMatrix` catches only that stable lineage error and changes receipt-backed would-be `PASS` rows to `FAILED` with reason `candidate-descendant-changed-tested-content`. Add optional closed `reason` to the schema's `matrixGate` object for this stable diagnostic. Do not catch schema, registry, conflict, or Git execution errors.

- [ ] **Step 5: Run all validator tests**

```powershell
node --test tools/phase9/validate.test.mjs
```

Expected: all tests pass without network access.

- [ ] **Step 6: Commit Task 3**

```powershell
git add -- tools/phase9/validate.mjs tools/phase9/validate.test.mjs
git commit -m "feat: bind phase 9 evidence to candidates"
```

---

### Task 4: Deterministic JSON and Markdown matrix rendering

**Files:**
- Create: `tools/phase9/render.mjs`
- Modify: `tools/phase9/validate.test.mjs`

**Interfaces:**
- Consumes: the validated recorded matrix from `evaluateRecordedMatrix`.
- Produces: `renderMatrixJson`, `renderMatrixMarkdown`, and `writeMatrixOutputs`.

- [ ] **Step 1: Write failing renderer golden tests**

Use a four-gate matrix with one status each. Assert exact bytes and ordering:

```js
test("renderer orders gates and emits honest summary", () => {
  const markdown = renderMatrixMarkdown(matrixFixture({
    gates: [failedGate, deferredGate, passGate, missingGate],
  }));
  assert.match(markdown, /Catalog complete: `true`/u);
  assert.match(markdown, /Release ready: `false`/u);
  assert.ok(markdown.indexOf("P8-DOCS-CLOSEOUT") < markdown.indexOf("P9-MATRIX-UNIT"));
  assert.match(markdown, /\| PASS \| 1 \|/u);
  assert.match(markdown, /\| MISSING \| 1 \|/u);
  assert.match(markdown, /\| FAILED \| 1 \|/u);
  assert.match(markdown, /\| DEFERRED \| 1 \|/u);
});
```

Add a `--check` test that changes one byte in an existing Markdown output and expects `PHASE9_MATRIX_DRIFT` without overwriting the file.

- [ ] **Step 2: Run and prove renderer tests fail**

```powershell
node --test --test-name-pattern "renderer|drift" tools/phase9/validate.test.mjs
```

Expected: FAIL because `render.mjs` does not exist.

- [ ] **Step 3: Implement exact Markdown layout**

Generate these sections in order:

```markdown
# Phase 9 Gate Matrix

- Candidate commit: `<sha>`
- Recorded by commit: `<sha>`
- Evaluation mode: `historical|candidate`
- Catalog complete: `true|false`
- Release ready: `true|false`

## Status summary

| Status | Count |
|---|---:|
| PASS | n |
| MISSING | n |
| FAILED | n |
| DEFERRED | n |

## Gates

| Gate | Phase | Category | Status | Evidence | Reason |
|---|---:|---|---|---|---|
```

Escape Markdown table pipes and line breaks. Evidence cells contain only receipt IDs and public run/artifact coordinates, never local paths. Do not include a render timestamp.

- [ ] **Step 4: Implement write and check modes**

In write mode create parent directories and write canonical JSON plus Markdown. In check mode read both files and compare exact UTF-8 bytes; throw `PHASE9_MATRIX_DRIFT` without modifying either file.

- [ ] **Step 5: Run renderer and validator tests**

```powershell
node --test tools/phase9/validate.test.mjs
```

Expected: all tests pass.

- [ ] **Step 6: Commit Task 4**

```powershell
git add -- tools/phase9/render.mjs tools/phase9/validate.test.mjs
git commit -m "feat: render deterministic phase 9 matrices"
```

---

### Task 5: Author the Phase 1–4 gate catalog

**Files:**
- Create: `tools/phase9/gates.json`
- Modify: `tools/phase9/validate.test.mjs`

**Interfaces:**
- Consumes: the registry schema and semantics from Tasks 1–2.
- Produces: authoritative source entries and required gates for Phases 1–4.

- [ ] **Step 1: Add the exact Phase 1–4 source documents**

Add these source paths with their acceptance/security/test headings exactly as they appear in each file:

```text
docs/superpowers/specs/2026-07-21-secure-token-file-preparation-design.md
docs/superpowers/specs/2026-07-22-task-engine-persistence-design.md
docs/superpowers/specs/2026-07-26-workspace-cmake-toolchains-design.md
docs/superpowers/specs/2026-07-27-prepared-process-lease-ownership-design.md
docs/superpowers/specs/2026-07-27-publisher-failure-task-ownership-design.md
docs/superpowers/specs/2026-07-28-close-before-terminalization-design.md
docs/superpowers/specs/2026-07-30-test-framework-discovery-execution-design.md
docs/superpowers/specs/2026-09-03-native-diagnostic-uri-design.md
```

Explicitly exclude `docs/superpowers/specs/2026-07-22-markdown-chinese-localization-design.md`: it governs a completed documentation-only localization migration, declares product architecture and behavior out of scope, and therefore contributes no product-release gate. Pin this exclusion in the inventory test so a future scope change is reviewed rather than silently ignored.

The validator test must read every listed file and prove each declared heading exists verbatim.

- [ ] **Step 2: Add the exact Phase 1–4 gate IDs**

Use this sorted inventory; each gate is `required` and maps to at least one source section:

```text
P1-IPC-PER-USER-AUTH
P1-PROTOCOL-NO-SHELL
P1-PROTOCOL-VERSION-COMPAT
P1-TOKEN-FILE-SECURE
P2-ARTIFACT-ATOMIC-CLEANUP
P2-EVENT-REPLAY-PERSISTENCE
P2-FAILURE-OWNERSHIP
P2-PROCESS-TREE-TERMINATION
P2-TASK-CANCEL-TIMEOUT
P3-CMAKE-CONFIGURE-BUILD
P3-DIAGNOSTIC-URI
P3-TOOLCHAIN-LINUX-CLANG
P3-TOOLCHAIN-LINUX-GCC
P3-TOOLCHAIN-WINDOWS-CLANGCL
P3-TOOLCHAIN-WINDOWS-MSVC
P3-WORKSPACE-TRUST-PATHS
P4-CPPUTEST-CPPUMOCK
P4-DISCOVERY-CTEST
P4-RECOVERY-AND-10000-BACKEND
P4-SELECTION-AND-RERUN
P4-UNITY-CMOCK
```

Verification commands must reference existing static commands such as `pnpm verify`, `pnpm test:e2e`, `pnpm test:e2e:native`, and focused `go test` package invocations. Do not invent a command that does not run in the repository.

- [ ] **Step 3: Write catalog coverage tests before accepting the data**

Assert the exact sorted gate subset and source paths, then temporarily omit one source reference and prove `PHASE9_GATE_MISSING` occurs. Also assert commands are display-only strings and none contain a newline, NUL, backtick, `$(`, `&&`, `||`, `;`, or redirection character.

- [ ] **Step 4: Canonicalize and validate the catalog**

Run the canonical writer once through a short ESM invocation, then validate:

```powershell
node --input-type=module -e "import {readFile} from 'node:fs/promises'; import {writeCanonicalJson} from './tools/phase9/canonical-json.mjs'; const p='tools/phase9/gates.json'; await writeCanonicalJson(p, JSON.parse(await readFile(p,'utf8')));"
node --test tools/phase9/validate.test.mjs
```

Expected: all tests pass and `gates.json` is canonical.

- [ ] **Step 5: Commit Task 5**

```powershell
git add -- tools/phase9/gates.json tools/phase9/validate.test.mjs
git commit -m "feat: catalog phase 1 through 4 gates"
```

---

### Task 6: Complete the Phase 5–9 gate catalog and deferred boundary

**Files:**
- Modify: `tools/phase9/gates.json`
- Modify: `tools/phase9/validate.test.mjs`

**Interfaces:**
- Consumes: the Phase 1–4 catalog from Task 5.
- Produces: full Phase 1–9 source coverage and the exact three deferred Phase 8 gates.

- [ ] **Step 1: Add the remaining authoritative source documents**

Add these exact paths plus the roadmap Phase 5–9 sections:

```text
docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md
docs/superpowers/specs/2026-08-03-coverage-report-pipeline-design.md
docs/superpowers/specs/2026-08-05-typescript-client-v1-4-coverage-design.md
docs/superpowers/specs/2026-08-16-phase6-code-oss-extension-design.md
docs/superpowers/specs/2026-08-18-phase6b-testing-api-design.md
docs/superpowers/specs/2026-08-20-phase8-windows-llvm-coverage-execution-design.md
docs/superpowers/specs/2026-08-21-windows-wfp-offline-boundary-design.md
docs/superpowers/specs/2026-08-27-code-oss-runtime-packaging-design.md
docs/superpowers/specs/2026-08-28-release-input-attempt-artifact-identity-design.md
docs/superpowers/specs/2026-08-28-trusted-code-oss-release-input-production-design.md
docs/superpowers/specs/2026-08-31-formal-packaging-blockers-design.md
docs/superpowers/specs/2026-09-01-formal-packaging-followup-design.md
docs/superpowers/specs/2026-09-03-code-oss-cli-smoke-handshake-design.md
docs/superpowers/specs/2026-09-06-phase7-linux-gcc-coverage-execution-design.md
docs/superpowers/specs/2026-09-11-qualified-release-manifest-collision-design.md
docs/superpowers/specs/2026-09-11-windows-llvm-coverage-regression-design.md
docs/superpowers/specs/2026-09-13-phase9-gate-evidence-matrix-design.md
```

- [ ] **Step 2: Add the exact Phase 5–8 gate IDs**

```text
P5-COVERAGE-FAULT-MAPPING
P5-COVERAGE-REPORTS
P5-LINUX-CLANG-COVERAGE
P5-LINUX-GCC-COVERAGE
P5-PROTOCOL-V14-COMPAT
P5-WINDOWS-LLVM-COVERAGE
P6-BRANDING-AND-BUILTIN-REGISTRATION
P6-CODEOSS-HOST-SMOKE
P6-SERVICE-LIFECYCLE
P6-TESTING-API
P6-TESTING-API-10000-ITEMS
P6-WORKSPACE-TRUST-GATE
P7-COVERAGE-UI-AND-SOURCE-DECORATION
P7-HISTORY-AND-ARTIFACT-BROWSER
P7-LINUX-GCC-OFFLINE
P7-MAIN-USER-JOURNEY
P7-MOCK-CONFIGURATION-UX
P7-WINDOWS-WFP-OFFLINE
P8-INSTALL-LIFECYCLE-LINUX
P8-INSTALL-LIFECYCLE-WINDOWS
P8-LICENSE-AUDIT
P8-LINUX-APPIMAGE-PACKAGE
P8-QUALIFICATION-UNSIGNED
P8-RUNTIME-PRODUCER-PROVENANCE
P8-WINDOWS-MSIX-PACKAGE
```

Add the three deferred IDs separately with exact reasons and resume conditions from the design.

- [ ] **Step 3: Add the exact Phase 9 gate IDs**

```text
P9-MATRIX-CONTRACT
P9-MATRIX-E2E
P9-MATRIX-FAULT-INJECTION
P9-MATRIX-INTEGRATION
P9-MATRIX-UNIT
P9-PERF-CANCEL
P9-PERF-DISCOVERY-10000
P9-PERF-FILTER
P9-PERF-MEMORY
P9-PERF-REPORT
P9-PERF-STARTUP
P9-PERF-HARDWARE-BASELINE
P9-UPSTREAM-CODEOSS
```

These Phase 9 gates begin as required gates without receipts, so the generated status is `MISSING`. Do not mark future work `DEFERRED`.

- [ ] **Step 4: Add an exact registry inventory test**

Build one sorted constant containing all gate IDs from Tasks 5–6 and assert deep equality with `gates.json`. Assert exactly three `disposition=deferred`, every other gate is required, every source section is referenced, and every gate has at least one requirement reference plus one nonempty verification channel.

- [ ] **Step 5: Validate the full catalog**

```powershell
node --test tools/phase9/validate.test.mjs
```

Expected: catalog coverage and all semantic tests pass; future gates appear as `MISSING`, not deferred.

- [ ] **Step 6: Commit Task 6**

```powershell
git add -- tools/phase9/gates.json tools/phase9/validate.test.mjs
git commit -m "feat: complete phase 9 gate catalog"
```

---

### Task 7: Baseline, durable receipts, and recorded matrix

**Files:**
- Create: `docs/superpowers/evidence/phase9/baseline.json`
- Create: `docs/superpowers/evidence/phase9/receipts/github-actions-34728889872-1.json`
- Create: `docs/superpowers/evidence/phase9/receipts/github-actions-34731651809-1.json`
- Create: `docs/superpowers/evidence/phase9/gate-matrix.json`
- Create: `docs/superpowers/evidence/phase9/gate-matrix.md`
- Modify: `tools/phase9/validate.test.mjs`

**Interfaces:**
- Consumes: validated registry, baseline, receipt, and renderer APIs.
- Produces: an initial honest matrix for candidate `1e20a5ceee370e9823f9fe05f40980f893cd9ed3`.

- [ ] **Step 1: Recheck current run and artifact identities before writing receipts**

Run these read-only commands:

```powershell
gh run view 34728889872 --repo colayc/unitTest --json databaseId,attempt,headSha,event,conclusion,workflowName,url
gh api repos/colayc/unitTest/actions/runs/34728889872/artifacts --jq '.artifacts[] | {id,name,digest,expired}'
gh run view 34731651809 --repo colayc/unitTest --json databaseId,attempt,headSha,event,conclusion,workflowName,url,jobs
gh api repos/colayc/unitTest/actions/runs/34731651809/artifacts --jq '.artifacts[] | {id,name,digest,expired}'
```

Expected: both runs remain `success`, attempt `1`, head SHA `1e20a5ceee370e9823f9fe05f40980f893cd9ed3`. If an artifact is already absent or expired, record the run only as historical context and leave its gates `MISSING`; do not fabricate `expired=false` or dispatch a replacement without confirming the dispatch remains within the user's authorization.

- [ ] **Step 2: Write the producer receipt from the already observed immutable values**

The receipt must include all four producer artifacts exactly:

```text
10310125633 code-oss-windows-x64-1 633300fc1c8aa1398adbf0ab830c8f1972a60fe506fb17ac0f14c4e37a5ecb0b
10309416757 release-input-provenance-1 45979b3ef5ae575a79b50fddcd06d5cd67cecc582731be2c8d8d44330cef4241
10309375277 appimagetool-linux-x64-1 8d7d80c08fbd120feb2f90c87a53d51f724966608e9049330f641afa9b4485d3
10308294547 code-oss-linux-x64-1 dcad449b10fb9304218e1f4c2c9c4b307ec450a5691b33bd561ee290032e8e81
```

Map it only to `P8-RUNTIME-PRODUCER-PROVENANCE` and record workflow path `.github/workflows/release-inputs.yml`, event `workflow_dispatch`, conclusion `success`, and capture observation `2026-09-13T10:07:11.621Z` only if Step 1 confirms the same unexpired identities.

- [ ] **Step 3: Write the foundation receipt**

Require all nine successful jobs:

```text
coverage-linux-gcc
install-smoke-linux
install-smoke-windows
package-linux
package-windows
release-qualification
verify-linux
verify-release-input-run
verify-windows
```

Include the ten artifact identities observed in run `34731651809`, including:

```text
10310420278 native-toolchain-linux-1 70e8d30ec50429c5a5a783b959a61ce608eac94486f2de367c7fd21c2aef9f27
10310365352 coverage-bundle-linux-1 ec2ec63fbe8f16892cd03cd59f5e8769dc7d43a6330f2482965171adb81824f3
10310246247 qualified-release-0.1.0-1 628a9a2ff2372f60435c7eac051182cd05432fcd887bdc0813befae0bc81cf79
10310171601 coverage-bundle-windows-1 292c4e9a2810941823fe14e4358241de92fe596654bfaf59c37eb3c5f72e0cae
10310027267 install-smoke-windows-1 7987c46fa6262e83b7f3f8df094f217d0bcaee6056c8e7675f94e6de8be1eb27
10309967342 release-input-linux-0.1.0-1 b41cf848c685842e2963ecec1e38f000d880359c73a905392b349be85fe596ac
10309967175 release-input-windows-0.1.0-1 76bf3f7a2fa1ea62b9f508cb213ff4a4f532f97f7a8264146f30e701bd078973
10309881602 linux-gcc-coverage-report-1 28a0a172af8a519baab7e973a6150c75a9d43d17455d8b8ff623090ff2a680c0
10309453360 release-qualification-1 c08b2db08ced3723f092a2663027811846f9cecffae0c5515cb4f67f7c6034e2
10309453290 install-smoke-linux-1 d41206510e598acd9526065b11cf6a335b9b204308019092da185ca4fbb13aff
```

Map the receipt only to gates actually proven by this run:

```text
P5-LINUX-GCC-COVERAGE
P7-LINUX-GCC-OFFLINE
P7-WINDOWS-WFP-OFFLINE
P8-INSTALL-LIFECYCLE-LINUX
P8-INSTALL-LIFECYCLE-WINDOWS
P8-LICENSE-AUDIT
P8-LINUX-APPIMAGE-PACKAGE
P8-QUALIFICATION-UNSIGNED
P8-WINDOWS-MSIX-PACKAGE
```

Do not map this unsigned receipt to `P8-SIGN-WINDOWS`.

- [ ] **Step 4: Write the historical baseline and generate the initial matrix**

Set `evaluationMode` to `historical`, set `candidateCommit` to `1e20a5ceee370e9823f9fe05f40980f893cd9ed3`, and select the two receipt IDs. This records exact prior evidence but cannot make the Batch A implementation commit release-ready. Generate outputs:

```powershell
node tools/phase9/render.mjs `
  --registry tools/phase9/gates.json `
  --baseline docs/superpowers/evidence/phase9/baseline.json `
  --receipts docs/superpowers/evidence/phase9/receipts `
  --repository-root . `
  --json-out docs/superpowers/evidence/phase9/gate-matrix.json `
  --markdown-out docs/superpowers/evidence/phase9/gate-matrix.md
```

Expected: `evaluationMode=historical`, `catalogComplete=true`, `releaseReady=false`, exactly three deferred gates, exact prior proven gates recorded as historical `PASS`, and unproven Phase 9 gates recorded as `MISSING`.

- [ ] **Step 5: Test the checked-in real registry and receipts**

Add a test that loads the repository files, evaluates them, and asserts:

```js
assert.equal(matrix.catalogComplete, true);
assert.equal(matrix.releaseReady, false);
assert.equal(matrix.evaluationMode, "historical");
assert.equal(matrix.counts.deferred, 3);
assert.equal(matrix.gates.find(({ id }) => id === "P8-SIGN-WINDOWS").status, "DEFERRED");
assert.equal(matrix.gates.find(({ id }) => id === "P9-PERF-MEMORY").status, "MISSING");
```

- [ ] **Step 6: Run the recorded matrix checks**

```powershell
node --test tools/phase9/validate.test.mjs
node tools/phase9/render.mjs `
  --registry tools/phase9/gates.json `
  --baseline docs/superpowers/evidence/phase9/baseline.json `
  --receipts docs/superpowers/evidence/phase9/receipts `
  --repository-root . `
  --json-out docs/superpowers/evidence/phase9/gate-matrix.json `
  --markdown-out docs/superpowers/evidence/phase9/gate-matrix.md `
  --check
```

Expected: all tests pass and no output drift exists.

- [ ] **Step 7: Commit Task 7**

```powershell
git add -- docs/superpowers/evidence/phase9 tools/phase9/validate.test.mjs
git commit -m "docs: record initial phase 9 evidence"
```

---

### Task 8: Offline GitHub Actions evidence auditor

**Files:**
- Create: `tools/phase9/audit.mjs`
- Create: `tools/phase9/audit.test.mjs`
- Modify: `tools/phase9/gates.schema.json`

**Interfaces:**
- Consumes: canonical receipts, recorded matrix, and saved GitHub run/job/artifact API snapshots.
- Produces: `auditGithubReceipt` and `evaluateAuditedMatrix` with no network or shell calls.

- [ ] **Step 1: Write a complete successful synthetic audit test**

Construct a receipt plus separate run, job, and artifact snapshots with exact run ID, attempt, repository, workflow path, event, head SHA, success conclusion, required jobs, and artifacts. Assert `auditGithubReceipt(...).status === "PASS"`.

The run snapshot fixture must contain only the fields the auditor accepts:

```js
{
  id: 34731651809,
  run_attempt: 1,
  event: "workflow_dispatch",
  head_sha: candidateCommit,
  status: "completed",
  conclusion: "success",
  path: ".github/workflows/foundation.yml",
  repository: { full_name: "colayc/unitTest" },
}
```

The job snapshot fixture is separate and mirrors the fixed `/jobs` endpoint:

```js
{
  total_count: 1,
  jobs: [
    { id: 9001, run_id: 34731651809, name: "verify-linux", status: "completed", conclusion: "success" },
  ],
}
```

- [ ] **Step 2: Write failing mismatch and expiry tests**

Table-drive mutations for repository, path, run ID, attempt, event, head SHA, status, conclusion, missing/duplicate job, job run ID, artifact ID/name/digest/run ID, uppercase digest, and unexpected artifact. Prove irrelevant raw API fields are ignored and cannot override any allowlisted trusted field after normalization.

Test expiry rules explicitly:

```js
test("expired artifact remains historical only when exact metadata still exists", () => {
  const result = auditGithubReceipt({
    receipt: validReceipt(),
    runSnapshot: validRunSnapshot(),
    jobSnapshot: validJobSnapshot(),
    artifactSnapshot: validArtifactSnapshot({ expired: true }),
  });
  assert.equal(result.status, "PASS");
  assert.equal(result.artifactAvailability, "expired");
  assert.equal(result.releaseUsable, false);
});
```

Missing expired artifact metadata must produce `PHASE9_EVIDENCE_UNTRUSTED`, not historical PASS.

- [ ] **Step 3: Run and prove auditor tests fail**

```powershell
node --test tools/phase9/audit.test.mjs
```

Expected: FAIL because `audit.mjs` does not exist.

- [ ] **Step 4: Implement snapshot normalization and exact matching**

Normalize GitHub's numeric JSON IDs to canonical decimal strings without accepting floats or unsafe integers. Select only the documented trusted fields from each raw API payload, then compare every selected identity field exactly; irrelevant API fields cannot affect the result. Never accept repository or workflow coordinates from CLI flags.

Use the stable failure form:

```js
function untrusted(receiptId, reason) {
  throw phase9Failure(
    "PHASE9_EVIDENCE_UNTRUSTED",
    `${receiptId}: ${reason}`,
  );
}
```

- [ ] **Step 5: Implement audited matrix evaluation and CLI**

Start from the recorded matrix. Replace a recorded `PASS` with `FAILED` if its selected receipt cannot be audited. Preserve `MISSING` and allowed `DEFERRED`. Recompute counts and force `releaseReady=false` unless `evaluationMode=candidate`, every audited gate is `PASS`, and every required artifact is currently available.

Write both audited JSON and Markdown. The CLI reads at most 256 snapshot directories, 2 MiB per run snapshot, 8 MiB per job snapshot, and 8 MiB per artifact snapshot.

- [ ] **Step 6: Run both test suites**

```powershell
node --test tools/phase9/validate.test.mjs tools/phase9/audit.test.mjs
```

Expected: all validator and auditor tests pass offline.

- [ ] **Step 7: Commit Task 8**

```powershell
git add -- tools/phase9/audit.mjs tools/phase9/audit.test.mjs tools/phase9/gates.schema.json
git commit -m "feat: audit phase 9 github evidence"
```

---

### Task 9: Read-only GitHub workflow and root verification integration

**Files:**
- Create: `.github/workflows/phase9-gates.yml`
- Modify: `package.json`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`

**Interfaces:**
- Consumes: validator, renderer, auditor, baseline, and receipts from Tasks 1–8.
- Produces: `phase9-gate-audit-${{ github.run_attempt }}` artifact and root scripts `test:phase9-gates` / `check:phase9-gates`.

- [ ] **Step 1: Write failing workflow contract tests**

Add a workspace-smoke test that reads `.github/workflows/phase9-gates.yml` and asserts:

```js
assert.match(workflow, /permissions:\r?\n\s+actions: read\r?\n\s+contents: read/u);
assert.doesNotMatch(workflow, /secrets\./u);
assert.doesNotMatch(workflow, /repository_dispatch|workflow_call/u);
assert.doesNotMatch(workflow, /inputs:/u);
for (const pin of [
  "actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803",
  "actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38",
  "pnpm/action-setup@f40ffcd9367d9f12939873eb1018b921a783ffaa",
  "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
]) assert.match(workflow, new RegExp(pin.replaceAll("/", "\\/")));
```

Also assert fixed repository `colayc/unitTest`, canonical run-ID validation before `gh api`, fixed endpoint shapes, `if-no-files-found: error`, finite retention, and no `continue-on-error`.

- [ ] **Step 2: Run the contract test and prove it fails**

```powershell
pnpm test:workspace
```

Expected: FAIL because `.github/workflows/phase9-gates.yml` is missing.

- [ ] **Step 3: Create the workflow with pinned actions**

Use this exact trigger, permission, runner, and setup skeleton:

```yaml
name: Phase 9 gate evidence audit

on:
  pull_request:
  push:
    branches: [master]
  workflow_dispatch:

permissions:
  actions: read
  contents: read

jobs:
  audit:
    runs-on: ubuntu-24.04
    timeout-minutes: 15
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
```

The offline validation step writes `.superpowers/phase9/requests.json` before any API access. The fetch step reads only its validated `runIds`, rechecks `^[1-9][0-9]*$`, sets `umask 077`, and writes fixed files:

```bash
gh api "repos/colayc/unitTest/actions/runs/$run_id" > ".superpowers/phase9/snapshots/$run_id/run.json"
gh api "repos/colayc/unitTest/actions/runs/$run_id/jobs?per_page=100" > ".superpowers/phase9/snapshots/$run_id/jobs.json"
gh api "repos/colayc/unitTest/actions/runs/$run_id/artifacts?per_page=100" > ".superpowers/phase9/snapshots/$run_id/artifacts.json"
```

Do not use `eval`, `sh -c`, interpolated repository names, or commands from JSON.

- [ ] **Step 4: Add audit and artifact upload steps**

Run `audit.mjs` against the snapshots. Upload `.superpowers/phase9/audit/phase9-gate-matrix.json` and `.md` with the pinned upload action, name `phase9-gate-audit-${{ github.run_attempt }}`, `if: always()`, `if-no-files-found: error`, and `retention-days: 14`. The audit command's exit code must remain the job conclusion.

- [ ] **Step 5: Add root scripts without changing existing command semantics**

Add:

```json
"check:phase9-gates": "node tools/phase9/render.mjs --registry tools/phase9/gates.json --baseline docs/superpowers/evidence/phase9/baseline.json --receipts docs/superpowers/evidence/phase9/receipts --repository-root . --json-out docs/superpowers/evidence/phase9/gate-matrix.json --markdown-out docs/superpowers/evidence/phase9/gate-matrix.md --check",
"test:phase9-gates": "node --test tools/phase9/validate.test.mjs tools/phase9/audit.test.mjs"
```

Place `pnpm check:phase9-gates` after generated protocol/coverage checks in `verify`. Place `pnpm run test:phase9-gates` before `pnpm run test:workspace` in `test`.

- [ ] **Step 6: Run focused integration checks**

```powershell
pnpm test:phase9-gates
pnpm test:workspace
pnpm check:phase9-gates
```

Expected: all tests pass, generated files have no drift, and no command accesses GitHub.

- [ ] **Step 7: Commit Task 9**

```powershell
git add -- .github/workflows/phase9-gates.yml package.json tools/workspace-smoke/workspace-smoke.test.mjs
git commit -m "ci: audit phase 9 gate evidence"
```

---

### Task 10: Full verification, review, and authorized remote acceptance

**Files:**
- Modify only if a verified in-scope defect is found: files introduced or listed in Tasks 1–9.
- Do not modify: `.github/workflows/foundation.yml`, `.github/workflows/release-inputs.yml`, product runtime, signing, tags, or publication files.

**Interfaces:**
- Consumes: the complete Batch A candidate.
- Produces: locally verified branch, independent review result, and—only after authorization—GitHub/Gitee branch plus a GitHub PR and online audit evidence.

- [ ] **Step 1: Run whitespace, diff-scope, and secret scans**

```powershell
git diff --check github/master...HEAD
git diff --name-only github/master...HEAD
rg -n -i "BEGIN .*PRIVATE KEY|RELEASE_SIGNING_PFX|ghp_|github_pat_|password\s*[:=]|token\s*[:=]" tools/phase9 docs/superpowers/evidence/phase9 .github/workflows/phase9-gates.yml
```

Expected: no whitespace errors; changed paths are limited to the design, plan, Phase 9 implementation/evidence, root scripts, workspace-smoke test, and new workflow; secret scan finds no credential value.

- [ ] **Step 2: Run the full pinned local verification**

Use Node `24.18.0`, pnpm `11.4.0`, Go `1.26.6`, and the repository's prepared bundles, then run:

```powershell
pnpm verify
```

Expected: exit `0`, including `test:phase9-gates`, matrix drift check, workspace smoke, all package tests, Go tests/race tests, and E2E. If a platform-specific native test is explicitly skipped because its documented runtime is absent, record the exact skip; do not call it PASS.

- [ ] **Step 3: Perform independent design and code review**

Review `github/master...HEAD` against the confirmed design. Require explicit findings on:

- exact three-gate deferment boundary;
- canonical duplicate-key rejection and size bounds;
- no execution of registry commands;
- candidate/receipt binding and honest expiry semantics;
- GitHub repository/workflow/run/job/artifact identity checks;
- deterministic rendering and `catalogComplete`/`releaseReady` separation;
- read-only workflow permissions and zero secret references;
- no change to producer, foundation, signing, tags, or Release behavior.

Fix only confirmed in-scope defects, rerun Steps 1–2, and create a focused fix commit.

- [ ] **Step 4: Stop for explicit push and PR authorization**

Report the branch name, commit range, test results, review result, current matrix counts, and the fact that Phase 8 has exactly three deferred gates. Ask for explicit authorization before pushing to GitHub/Gitee or creating a PR.

- [ ] **Step 5: After authorization, push both remotes and create the GitHub PR**

Use normal non-force pushes:

```powershell
git push github HEAD:codex/phase9-gate-evidence-matrix
git push origin HEAD:codex/phase9-gate-evidence-matrix
gh pr create `
  --repo colayc/unitTest `
  --base master `
  --head codex/phase9-gate-evidence-matrix `
  --title "feat: add phase 9 gate evidence matrix" `
  --body "Add the fail-closed Phase 9 Batch A gate catalog, canonical evidence receipts, deterministic matrix generation, and read-only GitHub evidence audit. Exactly three approved Phase 8 gates remain DEFERRED. This does not change product runtime, producer, foundation, signing, tags, or release publication."
```

Expected: both branch pushes succeed and one GitHub PR URL is returned. Do not merge.

- [ ] **Step 6: Wait for PR checks and inspect the online audit artifact**

Require the Phase 9 workflow job plus existing required checks to succeed. Query the run and artifact through GitHub CLI, then download `phase9-gate-audit-1` into a fresh temporary directory. Assert audited JSON has `catalogComplete=true`, `releaseReady=false`, exactly three deferred gates, and no identity mismatch.

- [ ] **Step 7: Stop for merge authorization**

Report PR number, run ID/URL, audit artifact ID/digest, job conclusions, current `PASS/MISSING/FAILED/DEFERRED` counts, and all remaining Phase 9 batches. Do not merge, sync master, dispatch a replacement producer/foundation, sign, tag, or publish without the next explicit authorization.

---

## Plan Completion Checks

Before calling Batch A implemented, verify all of the following:

- [ ] Every file and interface named in the design has an implementation task.
- [ ] The exact three deferred IDs are pinned in data, implementation, unit tests, generated output, and workflow contract tests.
- [ ] Every source section is mapped and every gate has a verification channel.
- [ ] Offline commands never call the network and never execute registry command strings.
- [ ] The online workflow uses only fixed repository/API coordinates and read-only permissions.
- [ ] Recorded and audited matrices remain distinct, deterministic, and honest about artifact expiry.
- [ ] Existing unsigned evidence never proves formal signing or legal approval.
- [ ] Full local verification and independent review pass before any remote push.
- [ ] GitHub/Gitee pushes, PR creation, merge, signing, tag, and Release boundaries each retain explicit authorization gates.
